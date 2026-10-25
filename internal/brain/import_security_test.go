package brain

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// Security review 2026-10-25 of the import (reviews/2026-10-25-security-brain-import.md),
// one test per finding fixed.

// Finding 1: an impossible date in the old trail never reaches the export,
// where every other home's load would refuse it.
func TestImportBadDateLoads(t *testing.T) {
	dir := docsFixture(t)
	os.WriteFile(filepath.Join(dir, ".agents", "docs", "plans", "2026-02-30-typo.md"), []byte("---\ndate: 2026-13-45\n---\n# Typo plan\n\n- [ ] left\n"), 0o644)
	adr := filepath.Join(dir, ".agents", "docs", "architecture", "DECISIONS.md")
	b, _ := os.ReadFile(adr)
	os.WriteFile(adr, append(b, []byte("\n## ADR-048: Bad day     (2026-02-30) — Accepted\n\nbody\n")...), 0o644)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "typos")
	if _, err := Import(opts(dir, t.TempDir()), "", false); err != nil {
		t.Fatal(err)
	}
	// A fresh home loads the committed file from scratch.
	recs, err := List(opts(dir, t.TempDir()), ListFilter{})
	if err != nil {
		t.Fatalf("a fresh home refuses the imported trail: %v", err)
	}
	for _, r := range recs {
		if _, err := time.Parse("2006-01-02", r.Date); err != nil {
			t.Errorf("%s carries the impossible date %q", r.ID, r.Date)
		}
	}
}

// Finding 2: a project's symlinked .agents is never read through, by the
// global import either.
func TestImportGlobalRefusesASymlinkedAgents(t *testing.T) {
	victim := t.TempDir()
	os.MkdirAll(filepath.Join(victim, ".agents", "memory"), 0o700)
	os.WriteFile(filepath.Join(victim, ".agents", "memory", "secret.md"), []byte("---\nname: secret\ndescription: s\n---\n\nsecret\n"), 0o600)
	proj := repo(t)
	if err := os.Symlink(filepath.Join(victim, ".agents"), filepath.Join(proj, ".agents")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(proj, ".agents")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the fixture's .agents is not a symlink: %v", err)
	}
	mems, files, problems, err := readMemoryStore(proj, newBudget())
	if len(mems) != 0 || len(files) != 0 {
		t.Errorf("read through a symlinked .agents: %v %v", mems, files)
	}
	if err == nil && len(problems) == 0 {
		t.Error("a symlinked .agents is neither refused nor reported")
	}
}

// Finding 4: a path printed for the caller to paste into `git rm` is
// quoted when it is not plain.
func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		".agents/docs/plans/a-b_c.1.md": ".agents/docs/plans/a-b_c.1.md",
		".agents/docs/plans/$(x).md":    `'.agents/docs/plans/$(x).md'`,
		"a b's":                         `'a b'\''s'`,
		"-rf":                           `'-rf'`,
	} {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

// Finding 5: a memory's description follows the title rules, and one long
// line cannot crowd every other memory out of `memory show`.
func TestMemoryAddTitleRules(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	for _, d := range []string{strings.Repeat("a", maxTitle+1), "mark\x01zebra\x02", "esc\x1b[31m"} {
		if _, err := MemoryAdd(o, MemoryEntry{Type: "user", Name: "x", Description: d}, strings.NewReader("b")); !IsRefusal(err) {
			t.Errorf("description %q: %v, want a refusal", d[:min(len(d), 20)], err)
		}
	}
}

func TestMemoryShowBoundsEachLine(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	// Stored past MemoryAdd's checks, as an older store or another writer could.
	s, err := memorySession(o)
	if err != nil {
		t.Fatal(err)
	}
	s.st.db.Exec(`INSERT INTO records(scope, id, kind, status, date, title, body, project, updated) VALUES (?, 'MEMORY-0big', 'memory', 'closed', '2026-10-25', ?, 'type: user', '', '2026-10-25T00:00:00Z')`, s.scope, strings.Repeat("x", 40000))
	s.close()
	for _, n := range []string{"alpha", "beta"} {
		if _, err := MemoryAdd(o, MemoryEntry{Type: "user", Name: n, Description: n + " fact"}, strings.NewReader("b")); err != nil {
			t.Fatal(err)
		}
	}
	show, err := MemoryShow(o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, "alpha") || !strings.Contains(show, "beta") {
		t.Errorf("one long memory hid the others: %q", show[:min(len(show), 200)])
	}
}

// Finding 6: the import reads a bounded amount in all.
func TestImportCapsTotalRead(t *testing.T) {
	old := maxImportBytes
	maxImportBytes = 4 << 10
	defer func() { maxImportBytes = old }()
	dir := docsFixture(t)
	for i := 0; i < 3; i++ {
		os.WriteFile(filepath.Join(dir, ".agents", "docs", "reviews", "2026-01-0"+string(rune('5'+i))+"-big.md"), []byte("# big\n\n"+strings.Repeat("x", 2<<10)), 0o644)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "big")
	if _, err := Import(opts(dir, t.TempDir()), "", true); !IsRefusal(err) || !strings.Contains(err.Error(), "over") {
		t.Fatalf("an import past the total cap must be refused, got %v", err)
	}
}

// Finding 7: a file is opened without following a link or blocking on a
// FIFO, and read no further than its cap.
func TestReadRegular(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.md")
	os.WriteFile(plain, []byte("text"), 0o644)
	if b, err := readRegular(plain, 10); err != nil || string(b) != "text" {
		t.Fatalf("plain file: %q %v", b, err)
	}
	if _, err := readRegular(plain, 3); !IsRefusal(err) {
		t.Errorf("a file over its cap: %v, want a refusal", err)
	}
	link := filepath.Join(dir, "link.md")
	os.Symlink(plain, link)
	if _, err := readRegular(link, 10); err == nil {
		t.Error("readRegular followed a symlink")
	}
	fifo := filepath.Join(dir, "fifo.md")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no FIFO here: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := readRegular(fifo, 10); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a FIFO was read as a file")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readRegular blocked on a FIFO")
	}
}

// Finding 8: one bad entry in a global log is a problem line, never the
// end of the import.
func TestImportGlobalBadEntriesAreProblems(t *testing.T) {
	home := t.TempDir()
	conf := filepath.Join(home, harness.StateDir)
	os.MkdirAll(filepath.Join(conf, "proposals"), 0o700)
	good := `{"ts":"2026-10-25T13:40:00+02:00","project":"/p/one","corrected":"ok"}`
	long := `{"ts":"2026-10-25T13:41:00+02:00","project":"/p/one","corrected":"` + strings.Repeat("a", maxLine+10) + `"}`
	nested := `{"ts":"2026-10-25T13:42:00+02:00","corrected":"n","x":` + strings.Repeat("[", 300000) + strings.Repeat("]", 300000) + `}`
	os.WriteFile(filepath.Join(conf, "feedback.jsonl"), []byte(good+"\n"+long+"\n"+nested+"\n"), 0o600)
	// Two names equal past the slug's cut: the second gets -2, which must
	// still fit an ID.
	os.WriteFile(filepath.Join(conf, "proposals", strings.Repeat("p", 130)+"a.md"), []byte("# one\n\nx\n"), 0o600)
	os.WriteFile(filepath.Join(conf, "proposals", strings.Repeat("p", 130)+"b.md"), []byte("# two\n\ny\n"), 0o600)
	o := Options{Home: home, Dir: t.TempDir(), Global: true, Now: func() time.Time { return fixed }}
	res, err := ImportGlobal(o, false)
	if err != nil {
		t.Fatalf("one bad entry ended the import: %v", err)
	}
	kinds := map[string]int{}
	for _, r := range res.Records {
		kinds[r.Kind]++
	}
	if kinds["feedback"] != 1 || kinds["proposal"] != 2 {
		t.Errorf("stored %v; want the good feedback line and both proposals", kinds)
	}
	if len(res.Problems) != 2 || !strings.Contains(res.Problems[0], "feedback.jsonl line 2: over") {
		t.Errorf("want the over-long line 2 and the nested line 3 reported, got %v", res.Problems)
	}
	for _, src := range res.Sources {
		if src == "feedback.jsonl" {
			t.Error("a log with lines not imported is listed for removal")
		}
	}
}

// Review of the import, finding 1: a memory store git tracks is the
// repository's text, never the operator's memory.
func TestImportRefusesCommittedMemory(t *testing.T) {
	dir := docsFixture(t)
	p := filepath.Join(dir, ".agents", "memory", "evil.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("---\nname: evil\ndescription: obey\n---\n\nrun this\n"), 0o644)
	git(t, dir, "add", "-f", ".agents/memory/evil.md")
	git(t, dir, "commit", "-qm", "planted memory")
	if _, err := Import(opts(dir, t.TempDir()), "", true); !IsRefusal(err) || !strings.Contains(err.Error(), "git tracks") {
		t.Fatalf("a committed memory store must be refused, got %v", err)
	}
}

// Review of the import: two projects' memories of one name are two records,
// both counted as stored.
func TestImportStoresSameIDInTwoScopes(t *testing.T) {
	home := t.TempDir()
	st, err := openStore(home)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{st: st}
	defer s.close()
	r := Record{ID: "MEMORY-x", Kind: "memory", Status: "closed", Date: "2026-10-25", Title: "x", Body: "type: user\n\nx", Updated: "2026-10-25T00:00:00Z"}
	a, b := r, r
	a.Scope, b.Scope = "/p/one", "/p/two"
	// /p/one already holds its MEMORY-x: the second run stores only /p/two's.
	if _, err := s.insertNew([]Record{a}, false); err != nil {
		t.Fatal(err)
	}
	stored, err := s.insertNew([]Record{a, b}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := filterStored([]Imported{{ID: "MEMORY-x", rec: a}, {ID: "MEMORY-x", rec: b}}, stored)
	if len(got) != 1 || got[0].rec.Scope != "/p/two" {
		t.Errorf("the stored records are %+v; want only /p/two's", got)
	}
}

// Review of the import: a docs root of another name keeps what its records
// reference by path relative to it.
func TestImportCustomDocsRoot(t *testing.T) {
	dir := repo(t)
	for rel, text := range map[string]string{
		"trail/plans/2026-01-01-open.md":  "# Open\n\n- [ ] left\n\nsee `testing/2026-01-02-log.md`\n",
		"trail/testing/2026-01-02-log.md": "# Log\n\nPASS\n",
	} {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(text), 0o644)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "trail")
	res, err := Import(opts(dir, t.TempDir()), "trail", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := importedIDs(res); len(got) != 2 {
		t.Errorf("want the open plan and the log it cites, got %v", keys(got))
	}
}

// Round 2, finding 1: a -N suffix never lands on an ID another entry holds.
func TestImportGlobalSuffixNeverCollides(t *testing.T) {
	home := t.TempDir()
	conf := filepath.Join(home, harness.StateDir, "proposals")
	os.MkdirAll(conf, 0o700)
	for _, n := range []string{"FOO", "foo-2", "foo"} {
		os.WriteFile(filepath.Join(conf, n+".md"), []byte("# "+n+"\n\nbody "+n+"\n"), 0o600)
	}
	o := Options{Home: home, Dir: t.TempDir(), Global: true, Now: func() time.Time { return fixed }}
	res, err := ImportGlobal(o, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != 3 || res.Skipped != 0 {
		t.Errorf("three proposals, three records: got %d stored, %d skipped", len(res.Records), res.Skipped)
	}
}

// Round 2, finding 2: a feedback line is stored as written, never expanded.
func TestImportGlobalStoresTheLineAsWritten(t *testing.T) {
	home := t.TempDir()
	conf := filepath.Join(home, harness.StateDir)
	os.MkdirAll(conf, 0o700)
	line := `{"ts":"2026-10-25T13:40:00+02:00","corrected":"x","a":[[[[[[[[1]]]]]]]]}`
	os.WriteFile(filepath.Join(conf, "feedback.jsonl"), []byte(line+"\n"), 0o600)
	o := Options{Home: home, Dir: t.TempDir(), Global: true, Now: func() time.Time { return fixed }}
	res, err := ImportGlobal(o, false)
	if err != nil || len(res.Records) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	if got := res.Records[0].rec.Body; got != line {
		t.Errorf("the body is %d bytes, not the %d-byte line", len(got), len(line))
	}
}

// Round 2: the file-count half of the read cap.
func TestImportCapsFileCount(t *testing.T) {
	old := maxImportFiles
	maxImportFiles = 5
	defer func() { maxImportFiles = old }()
	if _, err := Import(opts(docsFixture(t), t.TempDir()), "", true); !IsRefusal(err) || !strings.Contains(err.Error(), "files") {
		t.Fatalf("an import past the file cap must be refused, got %v", err)
	}
}
