package search

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// entry is one path of the current view.
type entry struct {
	path, oid, state string
}

// git runs in dir through trail.Command, the hardened builder: the checkout's
// own config is untrusted (no lock, no fsmonitor, no signature program, no
// lazy fetch; BUG-164, BUG-165).
func git(dir string, stdin []byte, args ...string) ([]byte, error) {
	cmd := trail.Command(dir, args...)
	// Git's messages in its C locale: dropReason matches "not a git
	// repository", which a translated catalogue would reword.
	cmd.Env = append(cmd.Env, "LC_ALL=C", "LANGUAGE=")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// checkoutRoot returns the top of the git work tree holding dir.
func checkoutRoot(dir string) (string, error) {
	out, err := git(dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", errors.New("no work tree")
	}
	return root, nil
}

// errUnsafePartialClone is a partial clone on a git that ignores
// GIT_NO_LAZY_FETCH: the search refuses it.
var errUnsafePartialClone = errors.New("search: a partial clone on a git older than 2.44, which could fetch through the clone's own config")

// lazyFetchSafe reports whether this git honours GIT_NO_LAZY_FETCH; a
// variable so a test can stand in for an older git.
var lazyFetchSafe = trail.LazyFetchSafe

// isNoIndexMarker reports a file that takes its directory out of the index;
// the names live in trail, which fyr check reads too (plan fyr-check D20).
func isNoIndexMarker(p string) bool { return trail.IsNoIndexMarker(p) }

// gitView lists the checkout's current files: the index entries of
// `git ls-files -s`, each marked modified when its bytes are not the index's
// blob, plus the untracked files of `git ls-files --others` (blob IDs
// computed from their bytes; no `git status`, BUG-165), minus deleted paths,
// symlinks, submodules, secret-shaped names and every path under a
// directory that holds a noindex marker file. A file's content is read
// again when its blob is indexed: a file edited and restored between the two
// reads is indexed with the edit until `index --rebuild` (a known limit; the
// operator's choice at the security review's cap, plan D35).
// The second result is the directories a marker takes out.
func gitView(root string) ([]entry, []string, error) {
	// Every read goes through an os.Root: a path that leaves the checkout
	// through `..` or a symlinked directory is refused by the standard
	// library, whatever git lists (security check of the v1.13.0 gate fixes).
	// A git older than 2.44 ignores GIT_NO_LAZY_FETCH, and a partial clone
	// could then fetch a missing object through its own core.sshCommand.
	if trail.PartialClone(root) && !lazyFetchSafe() {
		return nil, nil, fmt.Errorf("%w: %s; update git, or search a full clone", errUnsafePartialClone, root)
	}
	fsRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, err
	}
	defer fsRoot.Close()
	out, err := git(root, nil, "ls-files", "-s", "-z")
	if err != nil {
		return nil, nil, err
	}
	order, view, markers := trackedView(out)
	dropGoneMarkers(fsRoot, markers)
	refreshTracked(root, fsRoot, order, view)
	out, err = git(root, nil, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, nil, err
	}
	order = addUntracked(fsRoot, out, order, view, markers)
	marked := markedDirs(markers)
	return keptEntries(order, view, marked), leftOutDirs(marked), nil
}

// indexed reports a path the view may hold. The brain's export is searched as
// records from brain.db (internal/brain): indexed as a file too, every record
// would print twice (brain plan Phase 2).
func indexed(p string) bool { return p != trailExport }

// trackedView reads `git ls-files -s -z`: the index entries, in index order,
// and the noindex markers.
func trackedView(out []byte) ([]string, map[string]entry, map[string]bool) {
	var order []string
	view := map[string]entry{}
	markers := map[string]bool{}
	for _, rec := range strings.Split(string(out), "\x00") {
		meta, p, ok := strings.Cut(rec, "\t")
		if ok && isNoIndexMarker(p) {
			markers[p] = true
		}
		if !ok || !indexed(p) {
			continue
		}
		f := strings.Fields(meta) // mode oid stage
		if len(f) != 3 || f[0] == "120000" || f[0] == "160000" {
			continue
		}
		if _, dup := view[p]; dup { // an unmerged path: its bytes decide below
			continue
		}
		view[p] = entry{path: p, oid: f[1], state: "tracked"}
		order = append(order, p)
	}
	return order, view, markers
}

// dropGoneMarkers forgets a tracked marker deleted from the work tree, so
// it no longer counts. refreshTracked would only drop it from the view.
func dropGoneMarkers(fsRoot *os.Root, markers map[string]bool) {
	for p := range markers {
		if _, err := fsRoot.Lstat(filepath.FromSlash(p)); err != nil {
			delete(markers, p)
		}
	}
}

// refreshTracked compares each tracked file of order by its own bytes with
// the index's blob, marking it modified, and drops one that is gone, no
// longer a regular file, too large or unreadable. No `git status`: it runs
// the clean filters and the fsmonitor hook the checkout's own
// .gitattributes and config name (BUG-165). A file the checkout filters
// reads as modified and is indexed from its bytes.
//
// The index's own stat data spares the hash of a file git would also call
// unchanged: same size, same mtime to the nanosecond, and not racily clean
// (written within the second the index was). Reading it runs nothing.
func refreshTracked(root string, fsRoot *os.Root, order []string, view map[string]entry) {
	stats, indexTime := indexStats(root)
	for _, p := range append([]string(nil), order...) {
		// A plain stat for the comparison (an os.Root walk per file cost
		// more than the hash it saves); anything read still goes through
		// fsRoot, which refuses a path that leaves the checkout.
		if st, ok := stats[p]; ok {
			if fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p))); err == nil && fi.Mode().IsRegular() &&
				fi.Size() == st.size && fi.ModTime().Unix() == st.sec && int64(fi.ModTime().Nanosecond()) == st.nsec &&
				st.sec < indexTime.Unix() {
				continue // unchanged: the index's blob stands
			}
		}
		fi, err := fsRoot.Lstat(filepath.FromSlash(p))
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFileBytes {
			delete(view, p) // deleted, no longer a regular file, or too large
			continue
		}
		// The blob ID comes from the file's own bytes: `git hash-object
		// --stdin-paths` strips a trailing CR and unquotes a leading '"'
		// in a name, binding an ID to another file (security review,
		// finding 3).
		data, err := readAll(fsRoot, filepath.FromSlash(p))
		if err != nil {
			delete(view, p)
			continue
		}
		if oid := blobID(data); oid != view[p].oid {
			view[p] = entry{path: p, oid: oid, state: "modified"}
		}
	}
}

// addUntracked adds the untracked files `git ls-files --others` listed in
// out to the view, blob IDs computed from their bytes, and returns order
// with the new paths. Untracked files come from ls-files, which reads
// ignore rules only.
func addUntracked(fsRoot *os.Root, out []byte, order []string, view map[string]entry, markers map[string]bool) []string {
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		fi, err := fsRoot.Lstat(filepath.FromSlash(p))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if isNoIndexMarker(p) {
			markers[p] = true // a marker not yet added counts at once
		}
		if !indexed(p) || fi.Size() > maxFileBytes {
			continue
		}
		data, err := readAll(fsRoot, filepath.FromSlash(p))
		if err != nil {
			continue
		}
		if _, known := view[p]; !known {
			order = append(order, p)
		}
		view[p] = entry{path: p, oid: blobID(data), state: "untracked"}
	}
	return order
}

// markedDirs is the directory of each marker, with its trailing slash, ""
// for the root. A directory holding a noindex marker is data the project
// carries, not its own text: nothing below it is indexed. The marker counts
// in every state git reports it in (committed, staged, not yet added): the
// first search after the marker is written must already leave the
// directory out.
func markedDirs(markers map[string]bool) []string {
	var marked []string
	for p := range markers {
		dir := path.Dir(p) + "/"
		if dir == "./" {
			dir = "" // a marker at the root: nothing of the checkout is indexed
		}
		marked = append(marked, dir)
	}
	return marked
}

// keptEntries is the view in order, less secret-shaped names and every
// path under a marked directory.
func keptEntries(order []string, view map[string]entry, marked []string) []entry {
	noIndex := func(p string) bool {
		for _, dir := range marked {
			if strings.HasPrefix(p, dir) {
				return true
			}
		}
		return false
	}
	entries := make([]entry, 0, len(view))
	for _, p := range order {
		if harness.SecretShaped(path.Base(p)) {
			continue // never indexed, even untracked (security review, finding 6)
		}
		if noIndex(p) {
			continue
		}
		if e, ok := view[p]; ok {
			entries = append(entries, e)
			delete(view, p) // an unmerged path appears once
		}
	}
	return entries
}

// leftOutDirs is the marked directories as they go back to the caller:
// sorted, the root as "./". The search header names them, so an empty
// answer is not read as "nothing there".
func leftOutDirs(marked []string) []string {
	leftOut := make([]string, 0, len(marked))
	for _, dir := range marked {
		if dir == "" {
			dir = "./"
		}
		leftOut = append(leftOut, dir)
	}
	sort.Strings(leftOut)
	return leftOut
}

// blobID is the git blob ID of data under SHA-1 ("blob <len>\x00" + data).
// It only has to name content consistently inside this index; a checkout in
// SHA-256 object format gets SHA-1 IDs for its modified files, which never
// collide with its index's IDs.
func blobID(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
