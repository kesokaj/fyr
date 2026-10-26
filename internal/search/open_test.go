package search

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestOpen(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, ".agents/docs/bugs/BUG-001-a.md", "---\nid: BUG-001\nstatus: open\ndate: 2026-09-02\n---\n# BUG-001: a\n")
	writeFile(t, root, ".agents/docs/bugs/BUG-002-b.md", "---\nid: BUG-002\nstatus: fixed\ndate: 2026-09-03\n---\n# BUG-002: b\n")
	writeFile(t, root, ".agents/docs/plans/open.md", "---\ndate: 2026-09-04\n---\n# Open\n- [ ] left\n")
	writeFile(t, root, ".agents/docs/plans/fenced.md", "# Fenced\n```\n- [ ] template\n```\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Open: true})
	if !strings.Contains(out, "BUG-001") || strings.Contains(out, "BUG-002") || !strings.Contains(out, "plans/open.md:5") || strings.Contains(out, "fenced.md") {
		t.Errorf("--open:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Open: true, Kind: "plan"})
	if strings.Contains(out, "BUG-001") || !strings.Contains(out, "open.md") {
		t.Errorf("--open --kind plan:\n%s", out)
	}
}

func TestRefuseOutsideCheckout(t *testing.T) {
	_, err := runSearch(t, Options{Dir: t.TempDir(), Words: []string{"x"}})
	if err == nil || err.Error() != "search: not inside a git checkout; use grep" {
		t.Errorf("got %v", err)
	}
}

func TestRefuseNotIgnored(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, ".gitignore", "")
	commitAll(t, root, "unignore")
	_, err := runSearch(t, Options{Dir: root, Words: []string{"demo"}})
	if err == nil || !strings.Contains(err.Error(), "is not gitignored") {
		t.Errorf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
		t.Errorf("a refused run created .agents: %v", err)
	}
}

func TestRefuseSymlinkedScratch(t *testing.T) {
	root := newRepo(t)
	elsewhere := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".agents"), 0o755)
	if err := os.Symlink(elsewhere, filepath.Join(root, ".agents", "scratch")); err != nil {
		t.Fatal(err)
	}
	_, err := runSearch(t, Options{Dir: root, Words: []string{"demo"}})
	if err == nil || !strings.Contains(err.Error(), ".agents/scratch is a symlink") {
		t.Errorf("got %v", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("wrote through the symlink: %v", entries)
	}
}

func TestBusyRefusesNotStale(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	old := busyTimeoutMS
	busyTimeoutMS = 200
	t.Cleanup(func() { busyTimeoutMS = old })
	holder := openDB(t, root)
	holder.SetMaxOpenConns(1)
	if _, err := holder.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	// The condition must hold: a change the next search has to write.
	writeFile(t, root, "README.md", "# demo\n\nfresh pangolin\n")
	out, err := runSearch(t, Options{Dir: root, Home: home, Words: []string{"pangolin"}})
	if err == nil || err.Error() != errBusy.Error() {
		t.Fatalf("want errBusy, got %v\n%s", err, out)
	}
	if strings.Contains(out, "README.md") {
		t.Errorf("a busy search printed an answer:\n%s", out)
	}
	holder.Exec(`ROLLBACK`)
}

// TestHelperIndex is not a test: TestConcurrentReconcile runs this binary as
// a subprocess with SEARCH_HELPER_DIR set.
func TestHelperIndex(t *testing.T) {
	dir := os.Getenv("SEARCH_HELPER_DIR")
	if dir == "" {
		t.Skip("helper process")
	}
	var out bytes.Buffer
	if err := Index(Options{Dir: dir, Home: os.Getenv("SEARCH_HELPER_HOME")}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

func TestConcurrentReconcile(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	for i := 0; i < 200; i++ {
		writeFile(t, root, filepath.Join("docs", "n", "f"+strconv.Itoa(i)+".md"), "# note\n\nbody "+strconv.Itoa(i)+"\n")
	}
	commitAll(t, root, "many")
	var cmds []*exec.Cmd
	var outs []*bytes.Buffer
	for i := 0; i < 2; i++ {
		c := exec.Command(os.Args[0], "-test.run=^TestHelperIndex$", "-test.v")
		c.Env = append(os.Environ(), "SEARCH_HELPER_DIR="+root, "SEARCH_HELPER_HOME="+home)
		b := &bytes.Buffer{}
		c.Stdout, c.Stderr = b, b
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		cmds, outs = append(cmds, c), append(outs, b)
	}
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Errorf("process %d: %v\n%s", i, err, outs[i])
		}
	}
	db := openDB(t, root)
	if n := count(t, db, `SELECT count(*) FROM files`); n != 202 {
		t.Errorf("one index with 202 files wanted, got %d", n)
	}
}

func TestArgumentErrors(t *testing.T) {
	root := newRepo(t)
	for _, o := range []Options{
		{Dir: root, Words: []string{"x"}, Limit: 51},
		{Dir: root, Words: []string{"x"}, Limit: -1},
		{Dir: root, Words: []string{"x"}, Kind: "nope"},
		{Dir: root, Words: []string{"x"}, Refs: "BUG-001"},
		{Dir: root, Refs: "BUG-001", Open: true},
		{Dir: root},
	} {
		if _, err := runSearch(t, o); err == nil || !strings.Contains(err.Error(), "see fyr help") {
			t.Errorf("%+v: got %v", o, err)
		}
	}
}
