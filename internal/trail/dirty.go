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
func Dirty(root string) (bool, error) {
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
	inHead, paths, blobs, dirty, err := headEntries(root, fsRoot, tree)
	if err != nil || dirty {
		return dirty, err
	}
	differ, err := hashesDiffer(root, paths, blobs)
	if err != nil || len(differ) > 0 {
		return len(differ) > 0, err
	}
	if dirty, err := indexDiffers(root, inHead); err != nil || dirty {
		return dirty, err
	}
	untracked, err := Command(root, "ls-files", "-z", "-o", "--exclude-standard").Output()
	if err != nil {
		return false, fmt.Errorf("git ls-files: %w", err)
	}
	return len(untracked) > 0, nil
}

// headEntries reads ls-tree's output: every HEAD path with its "<mode>
// <blob>", the regular files left to hash, and dirty when a symlink, a
// missing path or a type change already decides it.
func headEntries(root string, fsRoot *os.Root, tree []byte) (inHead map[string]string, paths, blobs []string, dirty bool, err error) {
	inHead = map[string]string{}
	for _, rec := range strings.Split(string(tree), "\x00") {
		meta, p, cut := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !cut || len(f) != 3 {
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
// only, no filter runs.
func indexDiffers(root string, inHead map[string]string) (bool, error) {
	index, err := Command(root, "ls-files", "-z", "-s").Output()
	if err != nil {
		return false, fmt.Errorf("git ls-files: %w", err)
	}
	indexed := 0
	for _, rec := range strings.Split(string(index), "\x00") {
		meta, p, cut := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !cut || len(f) != 3 {
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

// errUnhashable is a tracked name hash-object's --stdin-paths cannot take.
var errUnhashable = errors.New("a tracked name holds a newline: git cannot hash it by path")

// hashesDiffer hashes paths, relative to root, with `hash-object
// --no-filters` (no clean filter runs) and returns those whose hash is not
// the matching entry of blobs. An error means git could not answer.
func hashesDiffer(root string, paths, blobs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(paths) == 0 {
		return out, nil
	}
	for _, p := range paths {
		if strings.ContainsAny(p, "\n\r") {
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
