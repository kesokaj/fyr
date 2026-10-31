package trail

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Dirty reports whether root's work tree differs from HEAD, for a caller
// that refuses or labels a dirty tree. It answers without `git status`,
// which runs the clean filters a repository's own config defines (BUG-164;
// SD38 of the 2026-10-27 audit): a regular file is compared by
// `hash-object --no-filters`, a symlink by its target, and an exec-bit change
// or a submodule's state is not a change. A staged new file or an untracked,
// unignored one is. A file committed through a filter (LFS, crlf) reads dirty,
// the safe direction for a caller that refuses. An error means git could not
// answer (plan 2026-10-27-harness-audit-fix D6, D7). The checks are made at
// one moment: a concurrent writer in the checkout can change a file between
// the os.Root Lstat and git's read of it, which static repository content
// cannot do (accepted, security review of the fix).
//
// root must be the checkout's top level: the HEAD listing is full-tree while
// the os.Root reads relative to root, so a subdirectory would read every
// file as deleted (ledger item 57).
func Dirty(root string) (bool, error) {
	return dirtyWhere(root, func(string) bool { return true })
}

// DirtyUnder is Dirty over the paths under the named top-level directories
// only, by each of the same reads: eval's harness key looks at what `fyr
// install` installs, not at the trail a run's own testing record changes
// (PLAN.md item 53).
func DirtyUnder(root string, dirs []string) (bool, error) {
	return dirtyWhere(root, func(p string) bool {
		for _, d := range dirs {
			if p == d || strings.HasPrefix(p, d+"/") {
				return true
			}
		}
		return false
	})
}

// dirtyWhere is Dirty over the repository paths keep accepts.
func dirtyWhere(root string, keep func(string) bool) (bool, error) {
	if err := isTopLevel(root); err != nil {
		return false, err
	}
	if !Readable(root) {
		return false, errPartialClone
	}
	tree, err := Command(root, "ls-tree", "-r", "-z", "--full-tree", "HEAD").Output()
	if err != nil {
		return false, fmt.Errorf("git ls-tree: %w", err)
	}
	fsRoot, err := os.OpenRoot(root)
	if err != nil {
		return false, err
	}
	defer fsRoot.Close()
	inHead, paths, blobs, dirty, err := headEntries(root, fsRoot, tree, keep)
	if err != nil || dirty {
		return dirty, err
	}
	differ, err := hashesDiffer(root, paths, blobs)
	if err != nil || len(differ) > 0 {
		return len(differ) > 0, err
	}
	if dirty, err := indexDiffers(root, inHead, keep); err != nil || dirty {
		return dirty, err
	}
	untracked, err := Command(root, "ls-files", "-z", "-o", "--exclude-standard").Output()
	if err != nil {
		return false, fmt.Errorf("git ls-files: %w", err)
	}
	for _, p := range strings.Split(string(untracked), "\x00") {
		if p != "" && keep(p) {
			return true, nil
		}
	}
	return false, nil
}

// headEntries reads ls-tree's output: every HEAD path with its "<mode>
// <blob>", the regular files left to hash, and dirty when a symlink, a
// missing path or a type change already decides it. A path keep refuses is
// left out.
func headEntries(root string, fsRoot *os.Root, tree []byte, keep func(string) bool) (inHead map[string]string, paths, blobs []string, dirty bool, err error) {
	inHead = map[string]string{}
	for _, rec := range strings.Split(string(tree), "\x00") {
		meta, p, cut := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !cut || len(f) != 3 || !keep(p) {
			continue
		}
		inHead[p] = f[0] + " " + f[2]
		if f[0] == "160000" {
			continue // a submodule's state is not this tree's
		}
		// Through the os.Root: a path whose directory became a symlink to
		// the outside is refused, so nothing outside the checkout is read.
		info, err := fsRoot.Lstat(filepath.FromSlash(p))
		if err != nil {
			return nil, nil, nil, true, nil // removed, or no longer reachable inside root
		}
		if f[0] == "120000" {
			same, err := linkMatches(root, p, f[2], info)
			if err != nil || !same {
				return nil, nil, nil, true, err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return nil, nil, nil, true, nil
		}
		paths, blobs = append(paths, p), append(blobs, f[2])
	}
	return inHead, paths, blobs, false, nil
}

// indexDiffers compares the index with HEAD by mode and blob: a staged
// change whose work tree was restored, a staged new file, a conflict, or a
// staged removal, which no other read sees when the file is ignored
// (security review of the 2026-10-27 fix, finding 3 and round 2). Plumbing
// only, no filter runs. An index path keep refuses is left out, as
// headEntries leaves it out of inHead.
func indexDiffers(root string, inHead map[string]string, keep func(string) bool) (bool, error) {
	index, err := Command(root, "ls-files", "-z", "-s").Output()
	if err != nil {
		return false, fmt.Errorf("git ls-files: %w", err)
	}
	indexed := 0
	for _, rec := range strings.Split(string(index), "\x00") {
		meta, p, cut := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !cut || len(f) != 3 || !keep(p) {
			continue
		}
		if f[2] != "0" || inHead[p] != f[0]+" "+f[1] {
			return true, nil
		}
		indexed++
	}
	return indexed != len(inHead), nil
}

// linkMatches reports whether the tracked symlink p still points where the
// committed blob says. info is p's Lstat, already taken through an os.Root.
func linkMatches(root, p, blob string, info os.FileInfo) (bool, error) {
	if info.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}
	target, err := os.Readlink(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return false, nil
	}
	// A link target is at most PATH_MAX; a larger committed blob is not
	// read into memory and cannot match (security review of the fix).
	size, err := Command(root, "cat-file", "-s", blob).Output()
	if err != nil {
		return false, fmt.Errorf("git cat-file: %w", err)
	}
	if n, err := strconv.Atoi(strings.TrimSpace(string(size))); err != nil || n > 4096 {
		return false, nil
	}
	want, err := Command(root, "cat-file", "blob", blob).Output()
	if err != nil {
		return false, fmt.Errorf("git cat-file: %w", err)
	}
	return string(want) == target, nil
}

// errUnhashable is a tracked name hash-object's --stdin-paths cannot take:
// a newline or CR splits it, and a leading '"' is C-unquoted into another
// path, whose hash would then stand for this one (the v3.1.0 gate's
// security review).
var errUnhashable = errors.New(`a tracked name holds a newline or opens with '"': git cannot hash it by path`)

// hashesDiffer hashes paths, relative to root, with `hash-object
// --no-filters` (no clean filter runs) and returns those whose hash is not
// the matching entry of blobs. An error means git could not answer.
func hashesDiffer(root string, paths, blobs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(paths) == 0 {
		return out, nil
	}
	for _, p := range paths {
		if strings.ContainsAny(p, "\n\r") || strings.HasPrefix(p, `"`) {
			return nil, errUnhashable
		}
	}
	cmd := Command(root, "hash-object", "--no-filters", "--stdin-paths")
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	hashed, err := cmd.Output()
	got := strings.Fields(string(bytes.TrimSpace(hashed)))
	if err != nil {
		return nil, fmt.Errorf("git hash-object: %w", err)
	}
	if len(got) != len(paths) {
		return nil, fmt.Errorf("git hash-object: %d hashes for %d paths", len(got), len(paths))
	}
	for i, p := range paths {
		if got[i] != blobs[i] {
			out[p] = true
		}
	}
	return out, nil
}

// isTopLevel refuses a root that is not its checkout's top level, comparing
// both paths with their links resolved, as git reports the top level.
func isTopLevel(root string) error {
	_, top, err := Roots(root)
	if err != nil {
		return err
	}
	real, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if r, err := filepath.EvalSymlinks(real); err == nil {
		real = r
	}
	if t, err := filepath.EvalSymlinks(top); err == nil {
		top = t
	}
	if real != top {
		return fmt.Errorf("trail: Dirty: %s is not the checkout's top level", root)
	}
	return nil
}
