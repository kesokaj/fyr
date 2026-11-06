package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// resolve returns p with every symlink resolved, for a path that may not
// exist yet: the nearest existing ancestor is resolved and the rest joined
// on, so a refused root is judged without creating it (preamble :41-43).
func resolve(p string) (string, error) {
	p = filepath.Clean(p)
	var rest []string
	for cur := p; ; cur = filepath.Dir(cur) {
		if _, err := os.Stat(cur); err == nil {
			r, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			for i := len(rest) - 1; i >= 0; i-- {
				r = filepath.Join(r, rest[i])
			}
			return r, nil
		}
		if filepath.Dir(cur) == cur {
			return "", fmt.Errorf("no part of %s exists", p)
		}
		rest = append(rest, filepath.Base(cur))
	}
}

// nearestExisting is the deepest ancestor of p, p included, that exists.
func nearestExisting(p string) string {
	for cur := filepath.Clean(p); ; cur = filepath.Dir(cur) {
		if _, err := os.Stat(cur); err == nil || filepath.Dir(cur) == cur {
			return cur
		}
	}
}

// within reports whether p is root or lies under it; both resolved.
func within(p, root string) bool {
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}

// checkRoots is safety check a (preamble :31-50): leftover processes are
// sent SIGTERM under these roots and the sandbox is written there, so each
// must be absolute and, resolved, neither /, nor the real home or a parent
// of it, nor the install source's tree or a parent of it. The sandbox home
// may not even literally be $HOME.
func checkRoots(realHome, source string, roots ...string) error {
	rh, err := resolve(realHome)
	if err != nil {
		return fmt.Errorf("the real home %s: %v", realHome, err)
	}
	var src string
	if source != "" {
		if src, err = resolve(source); err != nil {
			return fmt.Errorf("the install source %s: %v", source, err)
		}
	}
	for _, r := range roots {
		if !filepath.IsAbs(r) {
			return fmt.Errorf("%s is not an absolute path", r)
		}
		if filepath.Clean(r) == filepath.Clean(realHome) {
			return fmt.Errorf("%s is the real home; refusing", r)
		}
		res, err := resolve(r)
		if err != nil {
			return err
		}
		switch {
		case res == string(filepath.Separator):
			return fmt.Errorf("%s resolves to /; refusing", r)
		case within(rh, res):
			return fmt.Errorf("%s is the real home or a parent of it; refusing", r)
		case src != "" && within(src, res):
			return fmt.Errorf("%s is the install source %s or a parent of it; refusing", r, source)
		}
	}
	return nil
}

// errInsideCheckout is safety check b's refusal (BUG-106): an ancestor
// AGENTS.md would load into the run, and git from the sandbox would reach
// another repository.
var errInsideCheckout = errors.New("inside a git checkout")

// outsideCheckout is safety check b (preamble :54-58): git run in dir must
// find no repository. It fails closed: only git's discovery failure, "not a
// git repository (or any of the parent directories)" in the C locale so the
// words are git's English, counts as outside; a .git file whose gitdir is
// gone (a stale worktree) says only "not a git repository: <path>" and is
// inside, since the CLI still loads that checkout's AGENTS.md; a
// repository git refuses for its owner, a directory it cannot enter or any
// other error is inside. Discovery crosses mounts, because the CLI's loading
// of an ancestor AGENTS.md does (BUG-106).
func outsideCheckout(dir string) error {
	// trail.Command for its hardened options (SD43 of the 2026-10-27 audit);
	// the env below replaces its own, which is stricter (plan D13).
	cmd := trail.Command(dir, "rev-parse", "--git-dir")
	env := []string{"LC_ALL=C", "GIT_DISCOVERY_ACROSS_FILESYSTEM=1"}
	for _, kv := range WithoutGit(os.Environ()) {
		if k, _, _ := strings.Cut(kv, "="); k != "LC_ALL" && k != "LANGUAGE" {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "not a git repository (or any of the parent directories)") {
		return nil
	}
	return fmt.Errorf("%s is %w", dir, errInsideCheckout)
}

// WithoutGit drops every GIT_* variable: an inherited GIT_DIR or
// GIT_WORK_TREE would point git at another repository (run.sh:26).
func WithoutGit(env []string) []string {
	var out []string
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); !strings.HasPrefix(k, "GIT_") {
			out = append(out, kv)
		}
	}
	return out
}

// watched are the real home's paths check c fingerprints: fyr's state
// directory less its sandboxDirs, eval/, work/, sandbox/, dev/ and auto/,
// but with work/'s three fixed files, agy-linux, config.json and
// antigravity-oauth-token (fingerprintWorkDir, ADR-503); the old state
// directory; and the CLI's installed customizations and settings, which a
// grant written there would outlive the run in. The CLI's own token and its
// conversations are not watched: a sandboxed session refreshes the one
// through its link, and the operator's own sessions write the other.
var watched = []string{harness.StateDir, harness.OldStateDir, ".gemini/config", ".gemini/antigravity-cli/settings.json"}

// sandboxDirs are the state directory's children check c skips: Root's
// verbs, holding the sandboxes and the work runs' locks and pid files
// (eval/, work/, sandbox/: D8, PLAN-2026-10-29-workers D10 and D9 of
// PLAN-2026-11-02-1806-fyr-speed), dev homes (dev/) and auto's run data
// (auto/: the fyr auto plan's D6). Matched as a whole top-level name, so
// auto.json, beside auto/, is not one of them and stays watched.
var sandboxDirs = []string{"eval", "work", "sandbox", "dev", "auto"}

// storeFile, storeNames, storeTemps and atomicTemp are the state
// directory's top-level entries the operator's own sessions, the metrics
// ingest and the dream write, which Config.SkipStores leaves out (D45 of PLAN-2026-10-29-workers,
// as D64 and D70 amend it, and ADR-506 for eval, where Config.BrainWatch
// still watches brain.db by mode and per-scope digest): a worker or eval
// trial that runs beside those writers would otherwise fail check c falsely.
// Each is a writer's exact
// name, never a bare prefix, so a file added later under a similar name is
// still watched (the Phase 3 security review, finding 4). state.json is not
// one of them: only an install writes it, and the lead installs nothing while
// workers run.
var (
	// the three stores and their SQLite companions
	storeFile = regexp.MustCompile(`^(brain|search|metrics)\.db(-wal|-shm|-journal)?$`)
	// the dream's log, lock and state, and the directories it writes
	storeNames = []string{"dream.log", "dream.lock", "dream-state.json", "dream-failed", "dream-work", "proposals"}
	// os.MkdirTemp's and WriteAtomic's names: a writer's prefix, then digits
	storeTemps = regexp.MustCompile(`^(dream-agy-home-|dream-agy-|dream-projects-|dream-metrics-|dream-brain-|metrics-agy-)[0-9]+$`)
	atomicTemp = regexp.MustCompile(`^\.fyr-[0-9]+$`)
)

// storeEntry reports whether rel, relative to the state directory, is one
// of the entries SkipStores leaves out: top-level names only, and
// WriteAtomic's temporary names as files only.
func storeEntry(rel string, dir bool) bool {
	if strings.ContainsRune(rel, filepath.Separator) {
		return false
	}
	return storeFile.MatchString(rel) || slices.Contains(storeNames, rel) || storeTemps.MatchString(rel) || (!dir && atomicTemp.MatchString(rel))
}

// fingerprintWith is safety check c (preamble :85-117, BUG-107, 123, 158):
// every entry under the watched paths, as path relative to the home → type,
// mode, size and a SHA-256 of a file's content or a link's target. A link to
// a regular file is read through; no link is walked. A path that does not
// exist is no entry (BUG-158). skipStores leaves out every storeEntry, and
// with it a non-nil brainWatch still watches brain.db by mode and per-scope
// digest (fingerprintBrainFile, ADR-506).
func fingerprintWith(realHome string, skipStores bool, brainWatch func(string) (string, bool)) map[string]string {
	fp := map[string]string{}
	for _, dir := range watched {
		name := filepath.FromSlash(dir)
		top := filepath.Join(realHome, name)
		root := top
		if r, err := filepath.EvalSymlinks(top); err == nil {
			root = r // the directory itself may be a link (a dotfiles setup)
		} else if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			rel, _ := filepath.Rel(root, p)
			key := filepath.Join(name, rel)
			if err != nil {
				fp[key] = "unreadable: " + err.Error()
				return nil
			}
			if dir == harness.StateDir && d.IsDir() && slices.Contains(sandboxDirs, rel) {
				if rel == "work" {
					fingerprintWorkDir(p, name, fp)
				}
				return filepath.SkipDir
			}
			if dir == harness.StateDir && skipStores && storeEntry(rel, d.IsDir()) {
				if brainWatch != nil && strings.HasPrefix(rel, "brain.db") {
					fingerprintBrainFile(p, d, rel, key, brainWatch, fp)
				}
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			fp[key] = entryPrint(p, d)
			return nil
		})
	}
	return fp
}

// fingerprintWorkDir watches the fixed files inside ~/.config/fyr/work while
// skipping worker sandboxes, locks and pid directories under work/ (ADR-503):
// agy-linux and config.json are fingerprinted in full (type, mode, size and
// SHA-256), while antigravity-oauth-token is fingerprinted by type and mode
// only so an in-place OAuth token refresh during a run passes check c while
// creation, deletion, chmod or symlink replacement fails check c.
func fingerprintWorkDir(workDir, stateName string, fp map[string]string) {
	for _, file := range []string{"agy-linux", "config.json", "antigravity-oauth-token"} {
		full := filepath.Join(workDir, file)
		info, err := os.Lstat(full)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		key := filepath.Join(stateName, "work", file)
		if err != nil {
			fp[key] = "unreadable: " + err.Error()
			continue
		}
		if file == "antigravity-oauth-token" && info.Mode().IsRegular() {
			fp[key] = fmt.Sprintf("%s %v", info.Mode().Type(), info.Mode().Perm())
			continue
		}
		fp[key] = entryPrint(full, fs.FileInfoToDirEntry(info))
	}
}

func entryPrint(p string, d fs.DirEntry) string {
	info, err := d.Info()
	if err != nil {
		return "unreadable: " + err.Error()
	}
	desc := fmt.Sprintf("%s %v", info.Mode().Type(), info.Mode().Perm())
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, _ := os.Readlink(p)
		desc += " -> " + target
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			desc += fmt.Sprintf(" size %d sha256 %s", st.Size(), fileSum(p))
		}
	case info.Mode().IsRegular():
		desc += fmt.Sprintf(" size %d sha256 %s", info.Size(), fileSum(p))
	}
	return desc
}

func fileSum(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return "unreadable"
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unreadable"
	}
	return hex.EncodeToString(h.Sum(nil))
}

// changedPaths lists every relative path whose fingerprint differs, sorted:
// paths only, never content. First-time creation of WorkTokenRel as a regular
// 0600 file by SyncWorkToken does not count as a change, whereas deletion,
// chmod or non-regular replacement does.
func changedPaths(before, after map[string]string) []string {
	var out []string
	tokKey := filepath.FromSlash(WorkTokenRel)
	tokMode := fmt.Sprintf("%s %v", fs.FileMode(0).Type(), fs.FileMode(0o600))
	for k, v := range before {
		if after[k] != v {
			out = append(out, k)
		}
	}
	for k, v := range after {
		if _, ok := before[k]; !ok {
			if k == tokKey && v == tokMode {
				continue
			}
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// fingerprintBrainFile watches ~/.config/fyr/brain.db by its file mode and
// brainWatch's per-scope digest (ADR-506, BUG-504), falling back to entryPrint
// when brain.db is not a readable SQLite store. Regular -wal, -shm and
// -journal companions are skipped; non-regular ones are fingerprinted.
func fingerprintBrainFile(p string, d fs.DirEntry, rel, key string, brainWatch func(string) (string, bool), fp map[string]string) {
	if rel != "brain.db" {
		if !d.Type().IsRegular() {
			fp[key] = entryPrint(p, d)
		}
		return
	}
	if d.Type().IsRegular() {
		if info, err := d.Info(); err == nil {
			if digest, ok := brainWatch(p); ok {
				fp[key] = fmt.Sprintf("%s %v scope-sha256 %s", info.Mode().Type(), info.Mode().Perm(), digest)
				return
			}
		}
	}
	fp[key] = entryPrint(p, d)
}
