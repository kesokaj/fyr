package brain

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// The arms of ImportGlobal no other test reaches, pinned before the
// function was split (PLAN-2026-10-27-funclen, Phase 2, D13).

func globalOpts(home string, t *testing.T) Options {
	return Options{Home: home, Dir: t.TempDir(), Global: true, Now: func() time.Time { return fixed }}
}

func hasProblem(res *ImportResult, part string) bool {
	for _, p := range res.Problems {
		if strings.Contains(p, part) {
			return true
		}
	}
	return false
}

// A log that is not a file, a ts that is not RFC 3339, and a feedback line
// with no corrected text but a user quote.
func TestImportGlobalLogArms(t *testing.T) {
	home := t.TempDir()
	conf := filepath.Join(home, harness.StateDir)
	os.MkdirAll(filepath.Join(conf, "retros.jsonl"), 0o700)
	os.WriteFile(filepath.Join(conf, "feedback.jsonl"), []byte(
		`{"ts":"2026-10-25T13:40:00Z","user_quote":"say it in tables"}`+"\n"+
			`{"ts":"yesterday","corrected":"x"}`+"\n"), 0o600)
	res, err := ImportGlobal(globalOpts(home, t), true)
	if err != nil {
		t.Fatal(err)
	}
	if !hasProblem(res, "retros.jsonl: ") || !hasProblem(res, "not imported") {
		t.Errorf("a log that is not a file is a problem: %q", res.Problems)
	}
	if !hasProblem(res, `feedback.jsonl line 2: ts "yesterday" is not RFC 3339; not imported`) {
		t.Errorf("a ts that is not RFC 3339 is a problem: %q", res.Problems)
	}
	if len(res.Records) != 1 || res.Records[0].rec.Title != "say it in tables" {
		t.Errorf("a feedback line without corrected is titled by its user quote: %+v", res.Records)
	}
	if len(res.Sources) != 0 {
		t.Errorf("logs with a problem are never listed for removal: %q", res.Sources)
	}
}

// A proposal with no body, a SKILL.md and a file with no heading, one over
// the body cap, and one whose name makes no ID.
func TestImportGlobalProposalArms(t *testing.T) {
	home := t.TempDir()
	props := filepath.Join(home, harness.StateDir, "proposals")
	os.MkdirAll(filepath.Join(props, "tidy"), 0o700)
	for name, text := range map[string]string{
		"empty.md":      "---\ndate: 2026-10-01\n---\n\n",
		"tidy/SKILL.md": "a skill draft with no heading\n",
		"plain.md":      "no heading here either\n",
		"big.md":        strings.Repeat("a", maxBody+1),
		"!!!.md":        "# Bangs\n\nbody\n",
	} {
		os.WriteFile(filepath.Join(props, name), []byte(text), 0o600)
	}
	res, err := ImportGlobal(globalOpts(home, t), true)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]string{}
	for _, r := range res.Records {
		titles[r.ID] = r.rec.Title
	}
	if len(titles) != 2 || titles["PROPOSAL-tidy-skill"] != "proposal tidy" || titles["PROPOSAL-plain"] != "proposal plain" {
		t.Errorf("want SKILL.md titled by its directory, plain.md by its name, empty.md skipped: %v", titles)
	}
	if !hasProblem(res, "proposals/big.md: ") {
		t.Errorf("a proposal over the body cap is a problem: %q", res.Problems)
	}
	if !hasProblem(res, "proposals/!!!.md: ") || len(res.Problems) != 2 {
		t.Errorf("a proposal whose name makes no ID is a problem, and nothing else is: %q", res.Problems)
	}
	if strings.Join(res.Sources, " ") != "proposals/plain.md proposals/tidy/SKILL.md" {
		t.Errorf("only the proposals taken are listed for removal: %q", res.Sources)
	}
}

// The read cap stops the walk: the proposal past it is a problem and the
// rest are not read.
func TestImportGlobalProposalsStopAtTheCap(t *testing.T) {
	old := maxImportFiles
	maxImportFiles = 1
	defer func() { maxImportFiles = old }()
	home := t.TempDir()
	props := filepath.Join(home, harness.StateDir, "proposals")
	os.MkdirAll(props, 0o700)
	for _, n := range []string{"a", "b", "c"} {
		os.WriteFile(filepath.Join(props, n+".md"), []byte("# "+n+"\n\nbody\n"), 0o600)
	}
	res, err := ImportGlobal(globalOpts(home, t), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != 1 || len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "proposals/b.md: the import is over") {
		t.Errorf("want a.md taken, b.md refused and c.md never read: %+v %q", res.Records, res.Problems)
	}
}

// writeProjects makes the global index's project list: a search.db with
// the projects table ListProjects reads.
func writeProjects(t *testing.T, home string, roots ...string) {
	t.Helper()
	p := filepath.Join(home, harness.StateDir, "search.db")
	os.MkdirAll(filepath.Dir(p), 0o700)
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE projects(root TEXT, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range roots {
		if _, err := db.Exec(`INSERT INTO projects VALUES (?, ?)`, r, filepath.Base(r)); err != nil {
			t.Fatal(err)
		}
	}
}

// Every indexed project's memory store is read when the project is
// registered: a root that is no longer a checkout is passed over, a store
// the checks refuse and an unregistered project are problems, and a memory
// is a record of its project's scope. A dry run stores nothing.
func TestImportGlobalReadsEveryProjectsMemory(t *testing.T) {
	home := t.TempDir()
	good, bad := repo(t), repo(t)
	os.MkdirAll(filepath.Join(good, ".agents", "memory"), 0o700)
	os.WriteFile(filepath.Join(good, ".agents", "memory", "keep-x.md"), []byte("---\nname: keep-x\ndescription: keep x\n---\n\nThe fact.\n"), 0o600)
	os.MkdirAll(filepath.Join(bad, ".agents"), 0o700)
	os.WriteFile(filepath.Join(bad, ".agents", "memory"), []byte("not a store"), 0o600)
	stranger := repo(t) // indexed but never registered (ADR-068)
	os.MkdirAll(filepath.Join(stranger, ".agents", "memory"), 0o700)
	os.WriteFile(filepath.Join(stranger, ".agents", "memory", "keep-y.md"), []byte("---\nname: keep-y\ndescription: keep y\n---\n\nNot mine.\n"), 0o600)
	markRegistered(t, home, good)
	markRegistered(t, home, bad)
	writeProjects(t, home, filepath.Join(t.TempDir(), "gone"), good, bad, stranger)
	res, err := ImportGlobal(globalOpts(home, t), true)
	if err != nil {
		t.Fatal(err)
	}
	scope, _, err := trail.Roots(good)
	if err != nil {
		t.Fatal(err)
	}
	badScope, _, _ := trail.Roots(bad)
	want := filepath.Join(scope, ".agents", "memory", "keep-x.md")
	if len(res.Records) != 1 || res.Records[0].ID != "MEMORY-keep-x" || res.Records[0].Path != want || res.Records[0].rec.Scope != scope {
		t.Errorf("want MEMORY-keep-x from %s in scope %s: %+v", want, scope, res.Records)
	}
	if len(res.Memory) != 1 || res.Memory[0] != want {
		t.Errorf("the memory file is listed for removal by its full path: %q", res.Memory)
	}
	if len(res.Problems) != 2 || !strings.HasPrefix(res.Problems[0], badScope+": ") || !strings.Contains(res.Problems[0], "not a directory") {
		t.Errorf("a refused store is a problem naming its project: %q", res.Problems)
	}
	strangerScope, _, _ := trail.Roots(stranger)
	if want := strangerScope + ": not registered (fyr index --register); its memory store is not imported"; len(res.Problems) != 2 || res.Problems[1] != want {
		t.Errorf("an unregistered project's store is a problem: %q, want %q", res.Problems, want)
	}
	if _, err := os.Lstat(Path(home)); !os.IsNotExist(err) {
		t.Errorf("a dry run created the brain store: %v", err)
	}
}

// The global index's project list that cannot be read is a problem, never
// the end of the import.
func TestImportGlobalUnreadableProjectList(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, harness.StateDir, "search.db")
	os.MkdirAll(filepath.Dir(p), 0o700)
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE other(x)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	res, err := ImportGlobal(globalOpts(home, t), true)
	if err != nil {
		t.Fatal(err)
	}
	if !hasProblem(res, "the global index's project list: ") {
		t.Errorf("want the project list's error as a problem: %q", res.Problems)
	}
}

// A store that cannot be opened, or is busy, ends the import with its error,
// and no record is reported stored. A busy write waits out the driver's
// 10 s busy timeout, which no test variable shortens.
func TestImportGlobalStoreErrors(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, filepath.Dir(harness.StateDir)), 0o700)
	os.WriteFile(filepath.Join(home, harness.StateDir), []byte("a file"), 0o600)
	if res, err := ImportGlobal(globalOpts(home, t), false); err == nil || res != nil {
		t.Errorf("a config dir that is a file must fail the store's open: %+v %v", res, err)
	}

	home = t.TempDir()
	props := filepath.Join(home, harness.StateDir, "proposals")
	os.MkdirAll(props, 0o700)
	os.WriteFile(filepath.Join(props, "a.md"), []byte("# a\n\nbody\n"), 0o600)
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
	if res, err := ImportGlobal(globalOpts(home, t), false); err == nil || res != nil {
		t.Errorf("a busy store must fail the import: %+v %v", res, err)
	}
}
