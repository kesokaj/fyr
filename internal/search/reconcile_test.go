package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReconcileBranchSwitch(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	gitT(t, root, "switch", "-q", "-c", "feature")
	writeFile(t, root, "notes.md", "# Notes\n\nThe zebrafish decision lives here; see BUG-777.\n")
	commitAll(t, root, "feature")
	if out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"zebrafish"}}); !strings.Contains(out, "notes.md:3") {
		t.Fatalf("on feature, want notes.md:3:\n%s", out)
	}
	db := openDB(t, root)
	chunks := count(t, db, `SELECT count(*) FROM chunks`)

	gitT(t, root, "switch", "-q", "main")
	for _, o := range []Options{
		{Dir: root, Home: home, Words: []string{"zebrafish"}},
		{Dir: root, Home: home, Refs: "BUG-777"},
	} {
		out := mustSearch(t, o)
		if strings.Contains(out, "notes.md") {
			t.Errorf("on main, %+v answered from the other branch:\n%s", o, out)
		}
	}
	gitT(t, root, "switch", "-q", "feature")
	if out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"zebrafish"}}); !strings.Contains(out, "notes.md:3") {
		t.Fatalf("back on feature, want notes.md:3:\n%s", out)
	}
	if got := count(t, db, `SELECT count(*) FROM chunks`); got != chunks {
		t.Errorf("switching back re-chunked: %d chunks, want %d", got, chunks)
	}
}

func TestReconcileDirtyAndUntracked(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	writeFile(t, root, "README.md", "# demo\n\nNow mentions a quokka.\n")
	writeFile(t, root, "new/idea.go", "package idea\n\n// capybara lives here\n")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"quokka"}})
	if !strings.Contains(out, "README.md:3") {
		t.Errorf("uncommitted edit not found:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"capybara"}})
	if !strings.Contains(out, "new/idea.go:3  code") {
		t.Errorf("untracked file not found:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Refs: "new/idea.go"})
	if !strings.HasPrefix(out, "new/idea.go  untracked") {
		t.Errorf("want untracked state first:\n%s", out)
	}
}

func TestReconcileDeletedFileLeaves(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "gone.md", "a narwhal was here\n")
	commitAll(t, root, "add")
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"narwhal"}})
	os.Remove(filepath.Join(root, "gone.md"))
	if out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"narwhal"}}); strings.Contains(out, "gone.md") {
		t.Errorf("deleted file still answered:\n%s", out)
	}
}

func TestReconcileStagedRename(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "old.md", "the axolotl note\n")
	commitAll(t, root, "add")
	gitT(t, root, "mv", "old.md", "new.md")
	// Renamed and then edited: status reports "RM", and only the new path's
	// working copy holds the new word.
	writeFile(t, root, "new.md", "the axolotl note, now with a salamander\n")
	if st := gitT(t, root, "status", "--porcelain=v1"); !strings.HasPrefix(st, "RM ") {
		t.Fatalf("fixture: want an RM status line, got %q", st)
	}
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"salamander"}})
	if !strings.Contains(out, "new.md:1") || strings.Contains(out, "old.md") {
		t.Errorf("after a staged rename and an edit want new.md only:\n%s", out)
	}
}

func TestReconcileSkipsBinaryLargeSymlinkSubmodule(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "bin.dat", "okapi\x00binary")
	writeFile(t, root, "big.txt", "okapi "+strings.Repeat("x", maxFileBytes))
	writeFile(t, root, "target.md", "okapi in the target\n")
	if err := os.Symlink("target.md", filepath.Join(root, "link.md")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "add")
	gitT(t, root, "update-index", "--add", "--cacheinfo", "160000,1111111111111111111111111111111111111111,sub")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"okapi"}})
	for _, bad := range []string{"bin.dat", "big.txt", "link.md", "sub"} {
		if strings.Contains(out, bad+":") {
			t.Errorf("%s was indexed:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "target.md:1") {
		t.Errorf("the regular file is missing:\n%s", out)
	}
}

func TestReconcileGC(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "a.md", "old gazelle\n")
	writeFile(t, root, "b.md", "old ibex\n")
	commitAll(t, root, "add")
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"gazelle"}})
	db := openDB(t, root)
	oidA := strings.TrimSpace(gitT(t, root, "rev-parse", "HEAD:a.md"))
	oidB := strings.TrimSpace(gitT(t, root, "rev-parse", "HEAD:b.md"))
	writeFile(t, root, "a.md", "new gazelle\n")
	writeFile(t, root, "b.md", "new ibex\n")
	commitAll(t, root, "change")
	now := time.Now()
	day := int64(24 * 60 * 60)
	for oid, age := range map[string]int64{oidA: 15, oidB: 13} {
		if _, err := db.Exec(`UPDATE blobs SET last_seen = ? WHERE oid = ?`, now.Unix()-age*day, oid); err != nil {
			t.Fatal(err)
		}
	}
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"gazelle"}})
	if n := count(t, db, `SELECT count(*) FROM blobs WHERE oid = ?`, oidA); n != 0 {
		t.Errorf("blob unseen 15 days survived GC")
	}
	if n := count(t, db, `SELECT count(*) FROM chunks WHERE oid = ?`, oidA); n != 0 {
		t.Errorf("chunks of a collected blob survived")
	}
	if n := count(t, db, `SELECT count(*) FROM blobs WHERE oid = ?`, oidB); n != 1 {
		t.Errorf("blob seen 13 days ago was collected")
	}
}

func TestFTSFollowsGC(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "a.md", "unique wombatword\n")
	commitAll(t, root, "add")
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"wombatword"}})
	db := openDB(t, root)
	oid := strings.TrimSpace(gitT(t, root, "rev-parse", "HEAD:a.md"))
	os.Remove(filepath.Join(root, "a.md"))
	commitAll(t, root, "rm")
	db.Exec(`UPDATE blobs SET last_seen = 0 WHERE oid = ?`, oid)
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	if n := count(t, db, `SELECT count(*) FROM chunks_fts WHERE chunks_fts MATCH 'wombatword'`); n != 0 {
		t.Errorf("full-text index kept %d rows of a collected blob", n)
	}
	if _, err := db.Exec(`INSERT INTO chunks_fts(chunks_fts) VALUES ('integrity-check')`); err != nil {
		t.Errorf("integrity-check after GC: %v", err)
	}
}

func TestSameContentTwoPaths(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "one/x.md", "shared tapir text\n")
	writeFile(t, root, "two/x.md", "shared tapir text\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"tapir"}})
	if !strings.Contains(out, "one/x.md:1") || !strings.Contains(out, "two/x.md:1") {
		t.Errorf("want hits at both paths:\n%s", out)
	}
	db := openDB(t, root)
	oid := strings.TrimSpace(gitT(t, root, "rev-parse", "HEAD:one/x.md"))
	if n := count(t, db, `SELECT count(*) FROM chunks WHERE oid = ?`, oid); n != 1 {
		t.Errorf("one blob at two paths chunked %d times", n)
	}
}

func TestOpenRebuildsCorruptAndOldSchema(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, ".agents/scratch/search.db", "this is not a database, just text that is long enough to be read as a header")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	if !strings.Contains(out, "was unreadable (or an older schema); rebuilt") || !strings.Contains(out, "README.md") {
		t.Errorf("corrupt file not rebuilt:\n%s", out)
	}
	db := openDB(t, root)
	if _, err := db.Exec(`UPDATE meta SET value = '1' WHERE key = 'schema'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	if !strings.Contains(out, "rebuilt") || !strings.Contains(out, "README.md") {
		t.Errorf("schema 1 not rebuilt:\n%s", out)
	}
}
