// Package trail answers when a project's trail records changed, from git:
// the nightly dreamer's watermark and the metrics store's events both need
// it, and neither imports the other (plan 2026-10-08-metrics-store D31). It
// imports no SQLite driver.
//
// Every git call here runs in a project root taken from the search index or
// a transcript, so the root's own config and attributes are untrusted. The
// calls are chosen so none of them starts a program the repository defines:
// `log` with signatures off, `ls-tree`, `hash-object --no-filters`,
// `rev-parse` and `config --get` read objects, files and config only;
// `status`, which runs clean filters, is never called (BUG-164).
package trail

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NoIndexMarkers are the file names that take a directory out of the search
// index and out of fyr check's docs set. The second is the marker's name from
// before the rename, honoured the same way: it is a privacy control the
// operator placed, and nothing renames their files (plan fyr D12, F8).
var NoIndexMarkers = []string{".fyr-noindex", ".sh-agent-noindex"}

// IsNoIndexMarker reports whether the slash-separated path p names one of
// NoIndexMarkers.
func IsNoIndexMarker(p string) bool {
	b := path.Base(p)
	for _, m := range NoIndexMarkers {
		if b == m {
			return true
		}
	}
	return false
}

// Dates is, for every tracked file under docs, the time of its newest
// commit (paths relative to root, slash-separated), and the set of those
// tracked files whose working copy differs from HEAD. Both are empty when
// git is absent, root is no checkout, or root is a partial clone this
// machine's git cannot read safely (Readable): every file then keeps its
// mtime.
//
// A working copy is compared by its raw bytes (`hash-object --no-filters`)
// with HEAD's blob, so no clean filter runs: a file the repository filters
// reads as modified and keeps its mtime, the safe direction.
func Dates(root, docs string) (dates map[string]time.Time, modified map[string]bool) {
	dates, modified = map[string]time.Time{}, map[string]bool{}
	out, err := Log(root, "--format=%ct", "--name-only", "--no-renames", "--", docs)
	if err != nil {
		return dates, modified
	}
	var t time.Time
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		if secs, err := strconv.ParseInt(line, 10, 64); err == nil {
			t = time.Unix(secs, 0)
			continue
		}
		if _, seen := dates[line]; !seen && !t.IsZero() {
			dates[line] = t
		}
	}
	diff, ok := changed(root, docs)
	for p := range dates {
		// Unreadable working state: every record keeps its mtime.
		if !ok || diff[p] {
			modified[p] = true
		}
	}
	return dates, modified
}

// changed lists the tracked files under docs whose working copy is not
// HEAD's blob byte for byte, or is gone; ok is false when git could not
// answer, and the caller then treats every record as changed.
func changed(root, docs string) (out map[string]bool, ok bool) {
	out = map[string]bool{}
	if !Readable(root) {
		return nil, false
	}
	tree, err := Command(root, "ls-tree", "-r", "-z", "--full-tree", "HEAD", "--", docs).Output()
	if err != nil {
		return nil, false
	}
	fsRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, false
	}
	defer fsRoot.Close()
	var paths, blobs []string
	for _, rec := range strings.Split(string(tree), "\x00") {
		meta, path, cut := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !cut || len(f) != 3 || f[1] != "blob" {
			continue
		}
		// Through an os.Root: a path whose directory became a symlink to the
		// outside is refused there, so nothing outside the checkout is hashed
		// (security review of v2.6.0, part 2).
		info, err := fsRoot.Lstat(filepath.FromSlash(path))
		if err != nil || !info.Mode().IsRegular() {
			out[path] = true // removed, outside the checkout, or not a regular file
			continue
		}
		paths, blobs = append(paths, path), append(blobs, f[2])
	}
	if len(paths) == 0 {
		return out, true
	}
	cmd := Command(root, "hash-object", "--no-filters", "--stdin-paths")
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	hashed, err := cmd.Output()
	got := strings.Fields(string(bytes.TrimSpace(hashed)))
	if err != nil || len(got) != len(paths) {
		return nil, false
	}
	for i, p := range paths {
		if got[i] != blobs[i] {
			out[p] = true
		}
	}
	return out, true
}

// DocsRoot is the project's docs root relative to root: `.agents/docs` when
// it is a directory, else `docs`, else "" (one resolver, written three times
// until the project review of 2026-10-09; search adds its own default).
func DocsRoot(root string) string {
	for _, d := range []string{".agents/docs", "docs"} {
		if fi, err := os.Stat(filepath.Join(root, d)); err == nil && fi.IsDir() {
			return d
		}
	}
	return ""
}

// Command is git in root with the hardened global options: no index lock,
// no fsmonitor hook (security review rounds 2 and 3, 2026-10-01), no
// signature check, which makes `git log` start the repository's gpg.program
// (BUG-164), and GIT_NO_LAZY_FETCH, so a partial clone fails instead of
// fetching through its own core.sshCommand (security review 2026-10-08,
// round 2) on a git of 2.44 or later; an older git ignores the variable,
// which is why a partial clone is read only on a newer git (Readable). Every git
// call into a project repository (dream, metrics, search, memory) is built
// here (BUG-165); internal/harness runs git only in the harness's own
// checkout, which is the operator's.
func Command(root string, args ...string) *exec.Cmd {
	full := append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "log.showSignature=false", "-C", root}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1")
	return cmd
}

// Log runs `git log` in root with the hardened options, --no-show-signature,
// and --no-textconv, so a content diff (-G, -S, -p) never starts a textconv
// program that the checkout's own .gitattributes picks (security review
// 2026-10-25, finding 1), and returns its stdout. git log runs an external
// diff only when asked with --ext-diff, which no caller passes; a caller's
// own --textconv or --ext-diff would undo the hardening.
func Log(root string, args ...string) ([]byte, error) {
	if !Readable(root) {
		return nil, errPartialClone
	}
	return Command(root, append([]string{"log", "--no-show-signature", "--no-textconv"}, args...)...).Output()
}

var errPartialClone = errors.New("a partial clone is not read on a git older than 2.44: git could fetch through its own config")

// Readable reports whether root may be read at all: any checkout that is not
// a partial clone, and a partial clone only when this machine's git honours
// GIT_NO_LAZY_FETCH, so a missing object fails instead of being fetched. It
// is the policy search applies (search.md § Freshness). Until audit
// 2026-10-25 SD17 every partial clone was refused here, so the dreamer dated
// such a clone's whole trail by mtime, the SD4 case.
func Readable(root string) bool {
	return !PartialClone(root) || lazyFetchSafe()
}

// lazyFetchSafe is LazyFetchSafe, a variable so a test can take either side.
var lazyFetchSafe = LazyFetchSafe

// PartialClone reports whether root is a partial clone: extensions.partialClone
// set, or any remote marked promisor. Such a repository is not read on a
// git older than 2.44 (Readable): that git ignores GIT_NO_LAZY_FETCH, and a
// missing object would be fetched through the repository's own
// core.sshCommand (security review 2026-10-08, round 3). Reading config
// starts no program.
func PartialClone(root string) bool {
	out, _ := Command(root, "config", "--get", "extensions.partialClone").Output()
	if strings.TrimSpace(string(out)) != "" {
		return true
	}
	// A promisor remote lazy-fetches on its own, with no extension set
	// (release review of v2.6.0). --type=bool normalises true/yes/on/1.
	// --null: each entry is "key\nvalue\x00", so a remote name holding a space
	// or a tab cannot shift the value (release review of v2.6.0, round 2).
	out, _ = Command(root, "config", "--null", "--type=bool", "--get-regexp", `^remote\..*\.promisor$`).Output()
	for _, entry := range strings.Split(string(out), "\x00") {
		if _, value, ok := strings.Cut(entry, "\n"); ok && value == "true" {
			return true
		}
	}
	return false
}

// Toplevel is the git toplevel of dir, or dir itself (cleaned) when it is no
// checkout: a session started in a subdirectory belongs to the project.
//
// The answer must be dir or a parent of it: a repository's core.worktree can
// name any directory, which would file the session under another project
// (security review 2026-10-08, round 3).
func Toplevel(dir string) string {
	clean := filepath.Clean(dir)
	out, err := Command(dir, "rev-parse", "--show-toplevel").Output()
	top := strings.TrimSpace(string(out))
	if err != nil || top == "" {
		return clean
	}
	real := clean
	if r, err := filepath.EvalSymlinks(clean); err == nil {
		real = r
	}
	if rel, err := filepath.Rel(top, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return clean
	}
	return top
}

// ErrNotCheckout is dir outside any git work tree, or in a bare repository.
var ErrNotCheckout = errors.New("not a git checkout")

// Roots returns, for the checkout that holds dir, the main worktree's top
// level (main), so every worktree of a repository shares one memory store and
// one brain scope (D26 of the memory plan, ADR-063), and this checkout's own
// top level (top), where its committed files are. They differ only in a
// linked worktree.
//
// In the main worktree that is git's own toplevel, never the common dir's
// parent: in a submodule (.git/modules/x) or a --separate-git-dir checkout
// the common dir lies outside the work tree (audit 2026-10-25, SD16). The
// toplevel must be dir or a parent of it, because a repository's
// core.worktree can name any directory (Toplevel's rule). A linked
// worktree's main root is the common dir's parent. A bare repository has no
// toplevel and is refused. memory.Root and internal/brain both resolve
// through here.
func Roots(dir string) (main, top string, err error) {
	out, err := Command(dir, "rev-parse", "--path-format=absolute", "--git-common-dir", "--absolute-git-dir", "--show-toplevel").Output()
	if err != nil {
		return "", "", fmt.Errorf("%w: %s", ErrNotCheckout, dir)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 || lines[0] == "" || lines[1] == "" || lines[2] == "" {
		return "", "", fmt.Errorf("%w: %s", ErrNotCheckout, dir)
	}
	common, gitDir, top := filepath.Clean(lines[0]), filepath.Clean(lines[1]), filepath.Clean(lines[2])
	real := filepath.Clean(dir)
	if r, err := filepath.EvalSymlinks(real); err == nil {
		real = r
	}
	if rel, err := filepath.Rel(top, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("git names %s as the work tree of %s, which is not a parent of it", top, dir)
	}
	if gitDir != common {
		// A linked worktree counts only when the common dir registers it:
		// its git dir is <common>/worktrees/<name>, whose gitdir file names
		// this work tree's .git. A planted git dir with a commondir file
		// would otherwise borrow another project's store (audit-fix
		// checkpoint security review, finding 1).
		if !sameDir(filepath.Dir(gitDir), filepath.Join(common, "worktrees")) {
			return "", "", fmt.Errorf("%s is not a worktree the repository at %s registers", top, common)
		}
		back, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
		// git 2.48's worktree.useRelativePaths writes the path relative to
		// the git dir; resolved against fyr's cwd it named nothing.
		named := strings.TrimSpace(string(back))
		if !filepath.IsAbs(named) {
			named = filepath.Join(gitDir, named)
		}
		if err != nil || !sameDir(filepath.Dir(named), top) {
			return "", "", fmt.Errorf("%s is not a worktree the repository at %s registers", top, common)
		}
		return filepath.Dir(common), top, nil
	}
	return top, top, nil
}

// sameDir compares two paths after resolving symlinks, so a temp directory
// reached through /tmp and its real path match.
func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = filepath.Clean(a)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = filepath.Clean(b)
	}
	return ra == rb
}

// LazyFetchSafe reports whether this machine's git honours GIT_NO_LAZY_FETCH
// (git 2.44 and later). It asks `git version` once, outside any repository,
// so no repository's config is read.
func LazyFetchSafe() bool {
	lazyOnce.Do(func() {
		cmd := exec.Command("git", "version")
		cmd.Dir = os.TempDir()
		out, err := cmd.Output()
		lazySafe = err == nil && versionAtLeast(string(out), 2, 44)
	})
	return lazySafe
}

var (
	lazyOnce sync.Once
	lazySafe bool
)

// versionAtLeast reads "git version 2.47.3" (or "2.39.5 (Apple Git-154)")
// and reports whether it is at least major.minor; unreadable is false.
func versionAtLeast(out string, major, minor int) bool {
	f := strings.Fields(out)
	if len(f) < 3 {
		return false
	}
	parts := strings.SplitN(f[2], ".", 3)
	if len(parts) < 2 {
		return false
	}
	a, e1 := strconv.Atoi(parts[0])
	b, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil {
		return false
	}
	return a > major || a == major && b >= minor
}
