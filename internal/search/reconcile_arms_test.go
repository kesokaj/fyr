package search

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The arms of applyLocked and gitView that their splits move into helpers,
// pinned before the splits (PLAN-2026-10-27-funclen Phase 4, D13).

// refuse makes every statement of op on table fail, through the database's
// own schema: a trigger, not a seam in the code (D12).
func refuse(t *testing.T, st *store, op, table string) {
	t.Helper()
	if _, err := st.db.Exec(`CREATE TRIGGER refuse_` + strings.ToLower(op) + `_` + table + ` BEFORE ` + op + ` ON ` + table +
		` BEGIN SELECT RAISE(ABORT, 'refused ` + op + ` ` + table + `'); END`); err != nil {
		t.Fatal(err)
	}
}

// armStore is an empty index, and a directory holding a.md and b.md.
func armStore(t *testing.T) (*store, string) {
	t.Helper()
	st, err := openStore(filepath.Join(t.TempDir(), "search.db"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "# alpha\n\nThe alpha note, BUG-001 and docs/x.md.\n")
	writeFile(t, dir, "b.md", "# beta\n\nThe beta note.\n")
	return st, dir
}

// armEntry is the view entry of name in dir, its blob ID from its bytes.
func armEntry(t *testing.T, dir, name, state string) entry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return entry{path: name, oid: blobID(data), state: state}
}

// Every failing write of applyLocked fails the apply, whichever statement
// it is: the blob and files passes, the last-seen update and the touched
// mark.
func TestApplyLockedWriteErrors(t *testing.T) {
	for _, c := range []struct {
		name, op, table string
		before          bool // apply a.md once before the trigger
		view            func(dir string) []entry
	}{
		{"blob insert", "INSERT", "blobs", false, nil},
		{"chunk insert", "INSERT", "chunks", false, nil},
		{"file insert", "INSERT", "files", false, nil},
		{"file delete", "DELETE", "files", true, func(string) []entry { return nil }},
		{"file state update", "UPDATE", "files", true, nil},
		{"last-seen update", "UPDATE", "blobs", false, nil},
		{"touched mark", "INSERT", "meta", false, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			st, dir := armStore(t)
			src := source{project: "p", dir: dir, view: []entry{armEntry(t, dir, "a.md", "tracked")}}
			if c.before {
				if _, err := st.apply(src); err != nil {
					t.Fatal(err)
				}
				// The same a.md in another state, plus a new b.md: the new path
				// makes the apply take its lock, and a.md's row is updated.
				src.view = []entry{armEntry(t, dir, "a.md", "modified"), armEntry(t, dir, "b.md", "tracked")}
			}
			if c.view != nil {
				src.view = c.view(dir)
			}
			refuse(t, st, c.op, c.table)
			if _, err := st.apply(src); err == nil || !strings.Contains(err.Error(), "refused "+c.op+" "+c.table) {
				t.Fatalf("apply: %v, want the refused %s on %s", err, c.op, c.table)
			}
		})
	}
}

// A project directory that cannot be opened fails the apply before any
// blob is read.
func TestApplyLockedRootError(t *testing.T) {
	st, dir := armStore(t)
	src := source{project: "p", dir: filepath.Join(dir, "gone"), view: []entry{armEntry(t, dir, "a.md", "tracked")}}
	if _, err := st.apply(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("apply from a missing directory: %v, want not-exist", err)
	}
	if n := countStore(t, st, `SELECT count(*) FROM blobs`); n != 0 {
		t.Errorf("%d blobs kept after a failed apply, want 0", n)
	}
}

// An index whose refs table has lost its columns cannot prepare its
// inserter: the apply fails at the first blob that has text. The table
// keeps oid, so gc's deletes still run and only the inserter can fail.
func TestApplyLockedInserterError(t *testing.T) {
	st, dir := armStore(t)
	if _, err := st.db.Exec(`DROP TABLE refs; CREATE TABLE refs(oid TEXT)`); err != nil {
		t.Fatal(err)
	}
	src := source{project: "p", dir: dir, view: []entry{armEntry(t, dir, "a.md", "tracked")}}
	if _, err := st.apply(src); err == nil || !strings.Contains(err.Error(), "refs has no column") {
		t.Fatalf("apply without a refs table: %v, want the inserter's error", err)
	}
}

func countStore(t *testing.T, st *store, q string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// viewOf is gitView's entry for p, and whether it has one.
func viewOf(t *testing.T, root, p string) (entry, int) {
	t.Helper()
	view, _, err := gitView(root)
	if err != nil {
		t.Fatal(err)
	}
	var got entry
	n := 0
	for _, e := range view {
		if e.path == p {
			got, n = e, n+1
		}
	}
	return got, n
}

// An unmerged path is listed once, by its work-tree bytes against its
// first stage: a work tree holding "theirs" is modified, not their blob.
func TestGitViewUnmergedPath(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "a.md", "base\n")
	commitAll(t, root, "base")
	gitT(t, root, "checkout", "-q", "-b", "other")
	writeFile(t, root, "a.md", "theirs\n")
	commitAll(t, root, "theirs")
	gitT(t, root, "checkout", "-q", "main")
	writeFile(t, root, "a.md", "ours\n")
	commitAll(t, root, "ours")
	if _, err := git(root, nil, "merge", "-q", "other"); err == nil {
		t.Fatal("the merge did not conflict")
	}
	if out := gitT(t, root, "ls-files", "-u", "a.md"); strings.Count(out, "\n") != 3 {
		t.Fatalf("a.md is not unmerged in three stages:\n%s", out)
	}
	writeFile(t, root, "a.md", "theirs\n")
	got, n := viewOf(t, root, "a.md")
	if n != 1 || got.state != "modified" || got.oid != blobID([]byte("theirs\n")) {
		t.Errorf("unmerged a.md: %d entries, %+v; want one, modified, with the work tree's blob", n, got)
	}
}

// A file that cannot be read is left out of the view, tracked or not.
func TestGitViewUnreadableFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	root := newRepo(t)
	writeFile(t, root, "tracked.md", "one\n")
	commitAll(t, root, "add")
	// Another length, so the index's stat data cannot vouch for it.
	writeFile(t, root, "tracked.md", "one, edited\n")
	writeFile(t, root, "untracked.md", "two\n")
	for _, p := range []string{"tracked.md", "untracked.md"} {
		f := filepath.Join(root, p)
		if err := os.Chmod(f, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(f, 0o644) })
	}
	for _, p := range []string{"tracked.md", "untracked.md"} {
		if got, n := viewOf(t, root, p); n != 0 {
			t.Errorf("unreadable %s is in the view: %+v", p, got)
		}
	}
	if _, n := viewOf(t, root, "README.md"); n != 1 {
		t.Error("README.md left the view too")
	}
}

// A tracked file whose size and mtime are what the index recorded, and
// older than the index, is taken as the index's blob without a read: git's
// own rule, so new bytes of the same length behind an old mtime are not
// seen (pinned with a fixed mtime; the suite hit this arm only by timing).
func TestGitViewTrustsIndexStat(t *testing.T) {
	root := newRepo(t)
	f := filepath.Join(root, "same.md")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	writeFile(t, root, "same.md", "aaaa\n")
	if err := os.Chtimes(f, old, old); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "add")
	writeFile(t, root, "same.md", "bbbb\n")
	if err := os.Chtimes(f, old, old); err != nil {
		t.Fatal(err)
	}
	if got, n := viewOf(t, root, "same.md"); n != 1 || got.state != "tracked" || got.oid != blobID([]byte("aaaa\n")) {
		t.Errorf("same.md: %d entries, %+v; want the index's blob, tracked", n, got)
	}
}
