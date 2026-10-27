package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
	for _, kv := range withoutGit(os.Environ()) {
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

// withoutGit drops every GIT_* variable: an inherited GIT_DIR or
// GIT_WORK_TREE would point git at another repository (run.sh:26).
func withoutGit(env []string) []string {
	var out []string
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); !strings.HasPrefix(k, "GIT_") {
			out = append(out, kv)
		}
	}
	return out
}

// watched are the real home's paths check c fingerprints: fyr's state (its
// eval/ directory left out, every row writes there, D8), the old state
// directory, and the CLI's installed customizations and settings, which a
// grant written there would outlive the run in. The token and the CLI's
// conversations are not watched: a sandboxed session refreshes the one
// through its link, and the operator's own sessions write the other.
var watched = []string{harness.StateDir, harness.OldStateDir, ".gemini/config", ".gemini/antigravity-cli/settings.json"}

// fingerprint is safety check c (preamble :85-117, BUG-107, 123, 158): every
// entry under the watched paths, as path relative to the home → type, mode,
// size and a SHA-256 of a file's content or a link's target. A link to a
// regular file is read through; no link is walked. A path that does not
// exist is no entry (BUG-158).
func fingerprint(realHome string) map[string]string {
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
			if dir == harness.StateDir && rel == "eval" && d.IsDir() {
				return filepath.SkipDir
			}
			fp[key] = entryPrint(p, d)
			return nil
		})
	}
	return fp
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
// paths only, never content.
func changedPaths(before, after map[string]string) []string {
	var out []string
	for k, v := range before {
		if after[k] != v {
			out = append(out, k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
