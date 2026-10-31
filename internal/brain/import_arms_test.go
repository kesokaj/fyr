package brain

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// The arms of Import, classify, readDocs and readMemoryStore no other test
// reaches, pinned before the functions were split
// (PLAN-2026-10-27-funclen, Phase 2, D13).

// Import's target: --global, a directory outside a checkout, a symlinked
// .agents and a docs root that is not there are refused before anything is
// read.
func TestImportRefusesItsTarget(t *testing.T) {
	home := t.TempDir()
	o := opts(docsFixture(t), home)
	o.Global = true
	if _, err := Import(o, "", true); !IsUsage(err) {
		t.Errorf("--global: %v, want a usage error", err)
	}
	if _, err := Import(opts(t.TempDir(), home), "", true); !IsRefusal(err) || !strings.Contains(err.Error(), "not a git checkout") {
		t.Errorf("outside a checkout: %v", err)
	}
	dir := repo(t)
	os.Symlink(t.TempDir(), filepath.Join(dir, ".agents"))
	if _, err := Import(opts(dir, home), "", true); !IsRefusal(err) || !strings.Contains(err.Error(), "is a symlink") {
		t.Errorf("a symlinked .agents: %v", err)
	}
	if _, err := Import(opts(docsFixture(t), home), "nope", true); !IsRefusal(err) || !strings.Contains(err.Error(), "nope") {
		t.Errorf("a docs root that is not there: %v", err)
	}
}

// A record the load would refuse fails the import before anything is
// written: a graduated bug whose Prevention section and graduation note
// together pass the body cap.
func TestImportRefusesARecordTheLoadWouldRefuse(t *testing.T) {
	dir := repo(t)
	head := "# BUG-005: big\n\n## Prevention\n"
	bug := head + strings.Repeat("a", maxBody-len(head)-10) + "\n"
	files := map[string]string{
		".agents/docs/bugs/BUG-005-big.md": bug,
		".agents/docs/bugs/INDEX.md":       "# Bugs\n\n## Graduated\n\n| ID | Area | Graduated to |\n|---|---|---|\n| BUG-005 | a | " + strings.Repeat("t", 200) + " |\n",
	}
	for rel, text := range files {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(text), 0o644)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "big bug")
	if _, err := Import(opts(dir, t.TempDir()), "", true); !IsRefusal(err) || !strings.Contains(err.Error(), "(BUG-005)") {
		t.Fatalf("want the over-cap bug refused by name, got %v", err)
	}
}

func classified(t *testing.T, files map[string]string) map[string]candidate {
	t.Helper()
	var docs []docFile
	for rel, text := range files {
		docs = append(docs, docFile{rel: rel, path: ".agents/docs/" + rel, text: text})
	}
	cands, _, err := classify(docs, "2026-10-27")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]candidate{}
	for _, c := range cands {
		out[c.rec.ID] = c
	}
	return out
}

// A file with no heading is titled by its name; a bug takes its status and
// prevention from INDEX.md when its own file does not say, and its ID when
// its heading holds nothing else; a testing matrix is a guide.
func TestClassifyArms(t *testing.T) {
	got := classified(t, map[string]string{
		"plans/2026-01-01-untitled.md": "- [ ] left\n",
		"bugs/BUG-010-from-index.md":   "# BUG-010: index status\n\nsymptom\n",
		"bugs/BUG-011-bare.md":         "# BUG-011\n\nsymptom\n",
		"bugs/INDEX.md": "# Bugs\n\n| ID | Date | Area | Status | Symptom | Prevention |\n|---|---|---|---|---|---|\n" +
			"| BUG-010 | d | a | open | s | p |\n| BUG-011 | d | a | fixed | s | the index rule |\n",
		"testing/search-matrix.md": "# Search matrix\n\nrows\n",
	})
	if c := got["PLAN-2026-01-01-untitled"]; c.rec.Title != "2026-01-01-untitled" {
		t.Errorf("a plan with no heading: title %q", c.rec.Title)
	}
	if c := got["BUG-010"]; c.rec.Status != "open" {
		t.Errorf("BUG-010 takes INDEX.md's status: %q", c.rec.Status)
	}
	if c := got["BUG-011"]; c.rec.Title != "BUG-011" || c.rec.Body != "# BUG-011: BUG-011\n\n## Prevention\nthe index rule" {
		t.Errorf("BUG-011: title %q body %q", c.rec.Title, c.rec.Body)
	}
	if c := got["GUIDE-search-matrix"]; c.rec.Kind != "guide" || c.rec.Status != "current" || !c.root {
		t.Errorf("a testing matrix is a current guide kept as a root: %+v", c)
	}
}

// A name that makes no ID, and two names that make one, are refused.
func TestClassifyRefusals(t *testing.T) {
	for name, files := range map[string][]docFile{
		"no ID":        {{rel: "plans/!!!.md", path: "d/plans/!!!.md", text: "# x\n"}},
		"one ID":       {{rel: "plans/Same.md", path: "d/plans/Same.md", text: "# a\n"}, {rel: "plans/same.md", path: "d/plans/same.md", text: "# b\n"}},
		"distinct IDs": {{rel: "architecture/DECISIONS.md", path: "d/a/D.md", text: "## ADR-001: a (2026-01-01) — Accepted\n\nx\n"}, {rel: "bugs/BUG-001-x.md", path: "d/b.md", text: "# x\n"}},
	} {
		_, _, err := classify(files, "2026-10-27")
		switch name {
		case "no ID":
			if !IsRefusal(err) || !strings.Contains(err.Error(), `"PLAN-" is not a valid plan id`) {
				t.Errorf("%s: %v", name, err)
			}
		case "one ID":
			if !IsRefusal(err) || !strings.Contains(err.Error(), "d/plans/Same.md and d/plans/same.md both import as PLAN-same") {
				t.Errorf("%s: %v", name, err)
			}
		default:
			if err != nil {
				t.Errorf("%s: distinct IDs are not a collision: %v", name, err)
			}
		}
	}
}

// readDocs refuses a tracked record over the body cap, and returns an
// error it cannot read past as it is.
func TestReadDocsArms(t *testing.T) {
	dir := repo(t)
	root := filepath.Join(dir, ".agents", "docs")
	os.MkdirAll(filepath.Join(root, "plans"), 0o755)
	big := filepath.Join(root, "plans", "big.md")
	os.WriteFile(big, []byte(strings.Repeat("a", maxBody+1)), 0o644)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "big")
	if _, _, err := readDocs(dir, root, newBudget()); !IsRefusal(err) || !strings.Contains(err.Error(), "a record body is at most 1 MiB") {
		t.Errorf("a record over the cap: %v", err)
	}
	if os.Getuid() == 0 {
		t.Skip("root reads a 0000 file")
	}
	os.WriteFile(big, []byte("# small\n"), 0o644)
	os.Chmod(big, 0o000)
	if _, _, err := readDocs(dir, root, newBudget()); err == nil || IsRefusal(err) || !os.IsPermission(err) {
		t.Errorf("an unreadable record: %v, want the permission error as it is", err)
	}
}

// readMemoryStore refuses a store that is not a directory or cannot be
// listed; a file it cannot take is a problem; the read cap ends it.
func TestReadMemoryStoreArms(t *testing.T) {
	dir := repo(t)
	store := filepath.Join(dir, ".agents", "memory")
	os.MkdirAll(filepath.Dir(store), 0o700)
	os.WriteFile(store, []byte("x"), 0o600)
	if _, _, _, err := readMemoryStore(dir, newBudget()); !IsRefusal(err) || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("a store that is a file: %v", err)
	}
	os.Remove(store)
	os.MkdirAll(filepath.Join(store, "sub"), 0o700)
	for name, text := range map[string]string{
		"notes.txt":     "x",
		"big.md":        strings.Repeat("a", maxBody+1),
		"Bad Name.md":   "---\ndescription: d\n---\n\nbody\n",
		"empty-body.md": "---\ndescription: d\n---\n\n",
		"keep-x.md":     "---\ndescription: keep x\n---\n\nThe fact.\n",
	} {
		os.WriteFile(filepath.Join(store, name), []byte(text), 0o600)
	}
	recs, files, problems, err := readMemoryStore(dir, newBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].ID != "MEMORY-keep-x" || len(files) != 1 {
		t.Errorf("want keep-x alone taken: %+v %q", recs, files)
	}
	want := []string{
		".agents/memory/Bad Name.md: not a memory the store takes",
		".agents/memory/big.md: ",
		".agents/memory/empty-body.md: not a memory the store takes",
		".agents/memory/notes.txt: not a memory file",
		".agents/memory/sub: not a memory file",
	}
	if len(problems) != len(want) {
		t.Fatalf("problems %q", problems)
	}
	for i, w := range want {
		if !strings.HasPrefix(problems[i], w) {
			t.Errorf("problem %d is %q, want it to start %q", i, problems[i], w)
		}
	}
	if _, _, _, err := readMemoryStore(dir, &budget{bytes: 1 << 30, files: 0}); !IsRefusal(err) || !strings.Contains(err.Error(), "the import is over") {
		t.Errorf("the read cap: %v", err)
	}
	if os.Getuid() == 0 {
		t.Skip("root lists a 0000 directory")
	}
	os.Chmod(store, 0o000)
	defer os.Chmod(store, 0o700)
	if _, _, _, err := readMemoryStore(dir, newBudget()); err == nil || !os.IsPermission(err) {
		t.Errorf("a store that cannot be listed: %v", err)
	}
}

// A store that cannot be opened, or is busy, ends a project import with its
// error, and no record is reported stored. A busy write waits out the
// driver's 10 s busy timeout, which no test variable shortens.
func TestImportStoreErrors(t *testing.T) {
	dir, home := docsFixture(t), t.TempDir()
	os.MkdirAll(filepath.Join(home, filepath.Dir(harness.StateDir)), 0o700)
	os.WriteFile(filepath.Join(home, harness.StateDir), []byte("a file"), 0o600)
	if res, err := Import(opts(dir, home), "", false); err == nil || res != nil {
		t.Errorf("a config dir that is a file must fail the store's open: %+v %v", res, err)
	}

	home = t.TempDir()
	st, err := openStore(home)
	if err != nil {
		t.Fatal(err)
	}
	st.close()
	holder, err := sql.Open("sqlite", "file:"+Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	holder.SetMaxOpenConns(1)
	if _, err := holder.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer holder.Exec(`ROLLBACK`)
	if res, err := Import(opts(dir, home), "", false); err == nil || res != nil {
		t.Errorf("a busy store must fail the import: %+v %v", res, err)
	}
}

// Review of funclen Phase 2, finding 1: a dirty check that cannot run fails
// the import closed, with its error and no result, in an unborn repository
// and over a corrupt index alike.
func TestImportFailsClosedWhenTheDirtyCheckFails(t *testing.T) {
	unborn := repo(t)
	p := filepath.Join(unborn, ".agents", "docs", "plans", "p.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("# P\n\n- [ ] x\n"), 0o644)
	corrupt := docsFixture(t)
	os.WriteFile(filepath.Join(corrupt, ".git", "index"), []byte("garbage"), 0o644)
	for name, dir := range map[string]string{"unborn": unborn, "corrupt index": corrupt} {
		res, err := Import(opts(dir, t.TempDir()), "", false)
		if err == nil || IsRefusal(err) || res != nil || !strings.Contains(err.Error(), "checking for uncommitted changes") {
			t.Errorf("%s: %+v %v, want the dirty check's error and no result", name, res, err)
		}
	}
}

// Review of funclen Phase 2, finding 2: a tracked-files check that cannot
// run refuses the memory store, never reads it as untracked.
func TestReadMemoryStoreFailsClosedOnACorruptIndex(t *testing.T) {
	dir := repo(t)
	store := filepath.Join(dir, ".agents", "memory")
	os.MkdirAll(store, 0o700)
	os.WriteFile(filepath.Join(store, "keep-x.md"), []byte("---\ndescription: d\n---\n\nThe fact.\n"), 0o600)
	os.WriteFile(filepath.Join(dir, ".git", "index"), []byte("garbage"), 0o644)
	recs, files, problems, err := readMemoryStore(dir, newBudget())
	if err == nil || !strings.Contains(err.Error(), "git ls-files .agents/memory") || recs != nil || files != nil || problems != nil {
		t.Errorf("%v %q %q %v, want the ls-files error and nothing read", recs, files, problems, err)
	}
}

// The old store's git call runs no project hook: a core.fsmonitor hook that
// writes a marker fires for a plain `git status` (the control) and not for
// the import's read (v2.0.0 gate, security code slice; it moved here with
// ADR-087, when this became the store's one reader).
func TestStoreRunsNoProjectHook(t *testing.T) {
	root := repo(t)
	os.MkdirAll(filepath.Join(root, ".agents", "memory"), 0o700)
	marker := filepath.Join(t.TempDir(), "fired")
	hook := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch \""+marker+"\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "config", "core.fsmonitor", hook)
	exec.Command("git", "-C", root, "status", "--porcelain").Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("control: this git did not run the core.fsmonitor hook")
	}
	os.Remove(marker)
	if _, _, _, err := readMemoryStore(root, newBudget()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the old store's read ran the project's fsmonitor hook")
	}
}
