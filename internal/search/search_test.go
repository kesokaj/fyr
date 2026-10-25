package search

import (
	"bytes"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newRepo makes a git checkout whose .gitignore ignores .agents/scratch/*,
// as /init-project writes it, with one commit.
func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.email", "test@example.invalid")
	gitT(t, root, "config", "user.name", "test")
	gitT(t, root, "config", "commit.gpgsign", "false")
	writeFile(t, root, ".gitignore", ".agents/scratch/*\n")
	writeFile(t, root, "README.md", "# demo\n\nA small demo project.\n")
	commitAll(t, root, "init")
	return root
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, root, msg string) {
	t.Helper()
	gitT(t, root, "add", "-A")
	gitT(t, root, "commit", "-q", "-m", msg)
}

func runSearch(t *testing.T, o Options) (string, error) {
	t.Helper()
	if o.Home == "" {
		o.Home = t.TempDir()
	}
	var out bytes.Buffer
	err := Search(o, &out)
	return out.String(), err
}

func mustSearch(t *testing.T, o Options) string {
	t.Helper()
	out, err := runSearch(t, o)
	if err != nil {
		t.Fatalf("search %+v: %v\n%s", o, err, out)
	}
	return out
}

func openDB(t *testing.T, root string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+projectDB(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReconcileBranchSwitch(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	gitT(t, root, "switch", "-q", "-c", "feature")
	writeFile(t, root, "notes.md", "# Notes\n\nThe zebrafish decision lives here; see BUG-777.\n")
	writeFile(t, root, ".agents/docs/bugs/BUG-777-zebra.md", "---\nid: BUG-777\nstatus: open\ndate: 2026-09-29\n---\n# BUG-777: zebra\n")
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
		{Dir: root, Home: home, Open: true},
	} {
		out := mustSearch(t, o)
		if strings.Contains(out, "notes.md") || strings.Contains(out, "BUG-777-zebra") {
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
	if _, err := db.Exec(`UPDATE meta SET value = '2' WHERE key = 'schema'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	if !strings.Contains(out, "rebuilt") || !strings.Contains(out, "README.md") {
		t.Errorf("schema 2 not rebuilt:\n%s", out)
	}
}

func TestChunkMarkdownHeadingsAndWindows(t *testing.T) {
	text := "# Top\nintro\n```sh\n# not a heading\n```\n## Second\n" + strings.Repeat("line\n", 85)
	cs := chunkText("x.md", text)
	if len(cs) != 4 {
		t.Fatalf("want 4 chunks (Top, Second split into 3), got %d: %+v", len(cs), cs)
	}
	if cs[0].heading != "Top" || cs[0].line != 1 || cs[1].heading != "Second" || cs[1].line != 6 || cs[2].line != 46 || cs[3].line != 86 {
		t.Errorf("unexpected chunks: %+v", cs)
	}
	go_ := chunkText("x.go", strings.Repeat("x\n", 41))
	if len(go_) != 2 || go_[1].line != 41 || go_[0].heading != "" {
		t.Errorf("code windows: %+v", go_)
	}
}

func TestRecordsBugADRPlan(t *testing.T) {
	// The head of a real bug file as the old docs trail held it (BUG-049,
	// before the brain migration): the file parser still reads an
	// unmigrated project's trail (brain plan D16).
	real := "---\nowner: bugs\ndate: 2026-09-16\nid: BUG-049\nstatus: fixed\narea: internal/harness (dirty check)\n---\n" +
		"# BUG-049: the BUG-046 dirty-checkout guard trusted git's configurable default\n\n## Symptom\nThe dirty-checkout guard used a bare `git\nstatus --porcelain`.\n"
	rs := parseRecords("bugs/BUG-049-dirty-check-untracked-files-config.md", real)
	if len(rs) != 1 || rs[0].id != "BUG-049" || rs[0].status == "" || rs[0].date == "" || rs[0].title == "" {
		t.Errorf("real bug record: %+v", rs)
	}
	adr := "# Decisions\n\n## ADR-040: An unsigned zip needs `--unverified`     (2026-09-28) — Superseded by ADR-041\n\ntext\n\n" +
		"## ADR-042: Dream records join     (2026-09-29) — Accepted; superseded in part by ADR-044 (no line)\n\n" +
		"## ADR-044: A dream record is not indexed     (2026-09-29) — Accepted, amends ADR-042\n"
	rs = parseRecords("architecture/DECISIONS.md", adr)
	if len(rs) != 3 || rs[0].status != "Superseded by ADR-041" || rs[1].status != "Accepted; superseded in part by ADR-044 (no line)" || rs[2].status != "Accepted, amends ADR-042" || rs[0].line != 3 {
		t.Errorf("ADR records: %+v", rs)
	}
	plan := "---\ndate: 2026-09-29\n---\n# Plan — x\n\n```markdown\n- [ ] template box\n```\n- [x] done\n- [ ] first\n- [ ] second\n"
	rs = parseRecords("plans/x.md", plan)
	if len(rs) != 1 || rs[0].status != "open: 2 unticked" || rs[0].line != 10 || rs[0].date != "2026-09-29" {
		t.Errorf("plan record: %+v", rs)
	}
	rs = parseRecords("plans/y.md", "# Plan\n```\n- [ ] only in a fence\n```\n")
	if len(rs) != 1 || rs[0].status != "closed" {
		t.Errorf("fenced box counted: %+v", rs)
	}
}

func TestRefsIDsAndPaths(t *testing.T) {
	rs := parseRefs("see BUG-049 and ADR-033a in `internal/harness/repo.go:12-30`.\nalso ./cmd/x/main.go, plans/x.md and a/internal/y.go; not BUG-49\n")
	want := map[string]int{"BUG-049": 1, "ADR-033a": 1, "internal/harness/repo.go": 1, "cmd/x/main.go": 2, "plans/x.md": 2, "internal/y.go": 2}
	got := map[string]int{}
	for _, r := range rs {
		got[r.target] = r.line
	}
	for k, line := range want {
		if got[k] != line {
			t.Errorf("ref %s: line %d, want %d (all: %v)", k, got[k], line, got)
		}
	}
	if _, ok := got["BUG-49"]; ok {
		t.Errorf("BUG-49 is not an ID")
	}
}

func TestSearchRanksAndCaps(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	var many strings.Builder
	for i := 0; i < 6; i++ {
		many.WriteString("## part\n")
		many.WriteString(strings.Repeat("lynx lynx lynx\n", 3))
	}
	writeFile(t, root, "many.md", many.String())
	writeFile(t, root, "once.md", "a lynx once\n")
	writeFile(t, root, ".agents/docs/bugs/BUG-001-lynx.md", "---\nid: BUG-001\nstatus: fixed\ndate: 2026-09-01\n---\n# BUG-001: lynx\n\nthe lynx escaped\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lynx"}})
	if n := strings.Count(out, "many.md:"); n != perFileCap {
		t.Errorf("many.md printed %d times, want the cap %d:\n%s", n, perFileCap, out)
	}
	if !strings.Contains(out, ".agents/docs/bugs/BUG-001-lynx.md:6  bug BUG-001 fixed  2026-09-01") {
		t.Errorf("bug hit lacks its record line:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lynx"}, Kind: "bug"})
	if strings.Contains(out, "many.md") || !strings.Contains(out, "BUG-001") {
		t.Errorf("--kind bug:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lynx"}, Limit: 1})
	if !strings.HasPrefix(out, "search: 1 hits") {
		t.Errorf("--limit 1:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lynx", "nonexistentword"}})
	if !strings.Contains(out, ", 0 with every word") || !strings.Contains(out, "once.md") {
		t.Errorf("OR fallback:\n%s", out)
	}
	// Chunks with every word come first; the rest of the slots are filled
	// with chunks holding any of them.
	writeFile(t, root, "both.md", "a lynx beside a heron\n")
	commitAll(t, root, "both")
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lynx", "heron"}})
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], ", 1 with every word") || !strings.HasPrefix(lines[1], "both.md:1") || !strings.Contains(out, "once.md") {
		t.Errorf("AND first, then OR:\n%s", out)
	}
}

func TestSearchOperatorCharactersAreLiteral(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "x.md", "BUG-049 and a.go and NOT a keyword\n")
	commitAll(t, root, "add")
	for _, w := range [][]string{{"BUG-049"}, {"a.go"}, {"NOT"}, {`"`}, {"(", "BUG-049"}, {"*"}, {"col:on"}} {
		if _, err := runSearch(t, Options{Dir: root, Home: home, Words: w}); err != nil {
			t.Errorf("words %q: %v", w, err)
		}
	}
	if out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"BUG-049"}}); !strings.Contains(out, "x.md:1") {
		t.Errorf("BUG-049 not found:\n%s", out)
	}
}

func TestRefs(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, ".agents/docs/architecture/DECISIONS.md", "# Decisions\n\n## ADR-040: Old     (2026-09-28) — Superseded by ADR-041\n\n## ADR-041: New     (2026-09-28) — Accepted\n\nsupersedes ADR-040\n")
	writeFile(t, root, ".agents/docs/plans/p.md", "---\ndate: 2026-09-20\n---\n# P\n\ntouches `internal/pkg/z.go` and plans/q.md\n")
	writeFile(t, root, ".agents/docs/plans/q.md", "# Q\n")
	writeFile(t, root, "internal/pkg/z.go", "package pkg // ADR-040\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Refs: "ADR-040"})
	if !strings.HasPrefix(out, "ADR-040  .agents/docs/architecture/DECISIONS.md:3  Superseded by ADR-041  2026-09-28  Old") {
		t.Errorf("ADR-040 status line:\n%s", out)
	}
	if !strings.Contains(out, "internal/pkg/z.go:1  code") || !strings.Contains(out, "DECISIONS.md:7  adr ADR-041 Accepted") {
		t.Errorf("ADR-040 references:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Refs: "internal/pkg/z.go"})
	if !strings.HasPrefix(out, "internal/pkg/z.go  tracked") || !strings.Contains(out, "plans/p.md:6  plan closed  2026-09-20") {
		t.Errorf("path refs:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Refs: ".agents/docs/plans/q.md"})
	if !strings.Contains(out, "plans/p.md:6") {
		t.Errorf("docs-relative ref not matched:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Refs: "internal/"})
	if !strings.HasPrefix(out, "internal/  directory") || !strings.Contains(out, "plans/p.md:6") {
		t.Errorf("directory refs:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Refs: "z.go"})
	if !strings.HasPrefix(out, "z.go  absent\nhint: did you mean --refs internal/pkg/z.go\n") {
		t.Errorf("bare file name, want a hint:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Refs: "ADR-999"})
	if !strings.HasPrefix(out, "ADR-999  no record") {
		t.Errorf("unknown ID:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Refs: "ADR-040", Kind: "code"})
	if strings.Contains(out, "DECISIONS.md:7") || !strings.Contains(out, "z.go") {
		t.Errorf("--kind code on refs:\n%s", out)
	}
}

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

// parseRefs skips lines that cannot hold a reference before it runs its
// regular expressions; the result must be what the expressions alone give.
func TestRefsPrefilterIsEquivalent(t *testing.T) {
	text := strings.Join([]string{
		"only an id: ADR-033a",
		"only a bug: BUG-049.",
		"a path with a slash: internal/harness/repo.go:12",
		"a bare name: main.go and notes.md",
		"prose with a full stop. And another one. No reference here",
		"a dotted word like e.g. or v1.2.3 but no extension",
		"mixed BUG-001 in cmd/x/y.ts, and go.mod; also z.tf",
		"a directory only: skills/feature/",
		"",
	}, "\n")
	// One bare name per accepted extension, no slash on the line.
	for _, e := range refExtensions {
		text += "\nsee name_" + e + "." + e + " here"
	}
	var want []ref
	for i, l := range splitLines(text) {
		seen := map[string]bool{}
		add := func(x string) {
			if x != "" && !seen[x] {
				seen[x] = true
				want = append(want, ref{line: i + 1, target: x})
			}
		}
		for _, m := range idToken.FindAllString(l, -1) {
			add(m)
		}
		for _, m := range pathToken.FindAllString(l, -1) {
			if x := normalizeRef(m); strings.ContainsAny(x, "/.") && !idToken.MatchString(x) {
				add(x)
			}
		}
	}
	got := parseRefs(text)
	if len(want) < 10+len(refExtensions) {
		t.Fatalf("fixture too thin: %d references", len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("prefiltered parseRefs differs:\n got %v\nwant %v", got, want)
	}
}

// TestNoDuplicateHitAcrossRounds: a chunk whose matched term is not on its
// first line was printed twice, once by the every-word round (its line
// shifted to the term) and once by the any-word round (the chunk's first
// line), and counted twice against --limit and the per-file cap (audit of
// 2026-09-30, SD2).
func TestNoDuplicateHitAcrossRounds(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "notes.md", "# notes\nline two\nline three\nline four\nzebra quokka here now\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"zebra", "quokka"}})
	if n := strings.Count(out, "notes.md:"); n != 1 {
		t.Errorf("notes.md printed %d times, want 1:\n%s", n, out)
	}
	if !strings.HasPrefix(out, "search: 1 hits") || !strings.Contains(out, ", 1 with every word") {
		t.Errorf("header counts the duplicate:\n%s", out)
	}
}

// A directory holding a `.fyr-noindex` file is left out of the index,
// files below it included, tracked or not: vendored data a project
// carries (the harness's Google skills catalog) would otherwise crowd the
// project's own files out of the hits (recall 0.633 -> 0.596 on the fixture,
// 2026-10-01). Files beside the directory stay in.
func TestNoIndexMarkerSkipsTheDirectory(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "docs/own.md", "the ocelot lives here\n")
	writeFile(t, root, "vendor/data/.fyr-noindex", "")
	writeFile(t, root, "vendor/data/a/entry.md", "an ocelot in vendored data\n")
	writeFile(t, root, "vendor/beside.md", "an ocelot beside the marked directory\n")
	commitAll(t, root, "add")
	writeFile(t, root, "vendor/data/untracked.md", "an untracked ocelot in vendored data\n")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	for _, want := range []string{"docs/own.md:", "vendor/beside.md:"} {
		if !strings.Contains(out, want) {
			t.Errorf("a file outside the marked directory is missing (%s):\n%s", want, out)
		}
	}
	// A hit line starts with its path; the header names the directory itself.
	if strings.Contains(out, "\nvendor/data/") {
		t.Errorf("the marked directory is in the index:\n%s", out)
	}
	// A marker that is not committed yet counts too, and a reconcile drops
	// what was indexed before it appeared.
	writeFile(t, root, "docs/.fyr-noindex", "")
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if strings.Contains(out, "docs/own.md") || !strings.Contains(out, "vendor/beside.md:") {
		t.Errorf("an uncommitted marker did not take docs/ out of the index:\n%s", out)
	}
	// A marker at the root of the checkout is a directory like any other:
	// nothing of the project is indexed.
	writeFile(t, root, ".fyr-noindex", "")
	var buf bytes.Buffer
	Search(Options{Dir: root, Home: home, Words: []string{"ocelot"}}, &buf) // no hit is an error here; the output is what matters
	if strings.Contains(buf.String(), "vendor/beside.md") || strings.Contains(buf.String(), "docs/own.md") {
		t.Errorf("a root marker left files in the index:\n%s", buf.String())
	}
	// A marker above the prefix a caller asks for counts as well: the global
	// index reads only the docs root, and a checkout marked at its root has
	// no docs trail to offer either (code review round 2).
	entries, _, err := gitView(root, "vendor/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a root marker left %d entries in a prefixed view, first %s", len(entries), entries[0].path)
	}
}

// TestNoIndexMarkerDeletedCountsNoMore: a tracked marker removed from the
// work tree, before the removal is committed, already gives its directory
// back to the index (code review round 3: the branch had no test).
func TestNoIndexMarkerDeletedCountsNoMore(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "docs/own.md", "the ocelot lives here\n")
	writeFile(t, root, "vendor/data/.fyr-noindex", "")
	writeFile(t, root, "vendor/data/entry.md", "an ocelot in vendored data\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if strings.Contains(out, "\nvendor/data/") {
		t.Fatalf("the marked directory is in the index before the marker is removed:\n%s", out)
	}
	if err := os.Remove(filepath.Join(root, "vendor/data/.fyr-noindex")); err != nil {
		t.Fatal(err)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if !strings.Contains(out, "vendor/data/entry.md:") {
		t.Errorf("a marker deleted from the work tree still hides its directory:\n%s", out)
	}
}

// TestHeaderNamesLeftOutDirectories: a marker hides a directory, and the
// header of a words search and of --open says which, so an empty answer is
// not read as "nothing there" (the v2.1.0 gate's L6). The global header
// covers many projects and stays as it was.
func TestHeaderNamesLeftOutDirectories(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "docs/own.md", "the ocelot lives here\n")
	writeFile(t, root, "vendor/beside.md", "an ocelot beside\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if strings.Contains(out, "left out") {
		t.Errorf("no marker, yet the header names one:\n%s", out)
	}
	writeFile(t, root, "vendor/data/.fyr-noindex", "")
	writeFile(t, root, "vendor/data/entry.md", "an ocelot in vendored data\n")
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if !strings.Contains(out, "; left out by a noindex marker: vendor/data/)") {
		t.Errorf("one marker: the header does not name it:\n%s", out)
	}
	writeFile(t, root, "attic/.fyr-noindex", "")
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if !strings.Contains(out, "; left out by a noindex marker: attic/, vendor/data/)") {
		t.Errorf("two markers: want them sorted in the header:\n%s", out)
	}
	var buf bytes.Buffer
	Search(Options{Dir: root, Home: home, Open: true}, &buf)
	if !strings.Contains(buf.String(), "left out by a noindex marker: attic/, vendor/data/") {
		t.Errorf("--open: the header does not name the markers:\n%s", buf.String())
	}
	buf.Reset()
	Search(Options{Dir: root, Home: home, Global: true, Words: []string{"ocelot"}}, &buf)
	if strings.Contains(buf.String(), "left out") {
		t.Errorf("--global: the header names one project's markers:\n%s", buf.String())
	}
	// A directory name is the project's own text: control characters in it
	// must not reach the terminal or forge a line of output (code review of
	// v2.1.0..a62eb9d).
	writeFile(t, root, "ev\x1bil\nfake-hit.md:1/.fyr-noindex", "")
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if strings.Contains(out, "\x1b") || strings.Contains(out, "\nfake-hit.md:1/") || !strings.Contains(out, "ev?il?fake-hit.md:1/") {
		t.Errorf("a marked directory's name reached the header raw:\n%q", out)
	}
	if err := os.RemoveAll(filepath.Join(root, "ev\x1bil\nfake-hit.md:1")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, ".fyr-noindex", "")
	buf.Reset()
	Search(Options{Dir: root, Home: home, Words: []string{"ocelot"}}, &buf) // no hit is an error here; the header is what matters
	if !strings.Contains(buf.String(), "left out by a noindex marker: ./, attic/, vendor/data/") {
		t.Errorf("a root marker: want ./ first in the header:\n%s", buf.String())
	}
}

// TestPrintableDropsLineAndDirectionControls: printable is the one filter
// between project text and the terminal (or a model reading the output). It
// replaces control characters, and also the Unicode line and paragraph
// separators and the bidirectional controls, which can forge a line or
// reorder what is shown (security review of a62eb9d..768efce), and, by
// Unicode property and not by a list, every format character, variation
// selector and other default-ignorable character, which hide text from the
// eye while a model still reads it (BUG-152).
func TestPrintableDropsLineAndDirectionControls(t *testing.T) {
	for in, want := range map[string]string{
		"plain/dir/":          "plain/dir/",
		"a\x1bb\nc":           "a?b?c",
		"a\u2028b\u2029c":     "a?b?c",
		"a\u202eb\u202cc":     "a?b?c",
		"a\u2066b\u2069c":     "a?b?c",
		"a\u200eb\u200fc":     "a?b?c",
		"åäö — ünïcode stays": "åäö — ünïcode stays",
		// The whole format class, not a list: tag characters spell text no
		// terminal shows, and the Arabic letter mark reorders its neighbours.
		"a\U000e0041\U000e0042b": "a??b",
		"a\u200bb\u2060c":        "a?b?c",
		"a\u061cb":               "a?b",
		"\ufeffstart":            "?start",
		"soft\u00adhyphen":       "soft?hyphen",
		// Default-ignorable characters outside the format class: variation
		// selectors, the combining grapheme joiner, the Hangul fillers. An
		// ordinary combining accent stays.
		"a\ufe0fb":        "a?b",
		"a\U000e0101b":    "a?b",
		"a\u034fb":        "a?b",
		"a\u3164b\u115fc": "a?b?c",
		"e\u0301":         "e\u0301",
	} {
		if got := printable(in); got != want {
			t.Errorf("printable(%q) = %q, want %q", in, got, want)
		}
	}
}

// The marker's old name, from before the rename, still takes its directory
// out (plan fyr F8, D12): it is a privacy control the operator placed, and
// nothing renames the operator's files, so a directory holding only
// `.sh-agent-noindex` stays out of every index the dream reads.
func TestOldNoIndexMarkerStillCounts(t *testing.T) {
	if oldNoIndexMarker != ".sh-agent-noindex" || noIndexMarker != ".fyr-noindex" {
		t.Fatalf("markers %q and %q, want .fyr-noindex and .sh-agent-noindex", noIndexMarker, oldNoIndexMarker)
	}
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "docs/own.md", "the ocelot lives here\n")
	writeFile(t, root, "vendor/old/.sh-agent-noindex", "")
	writeFile(t, root, "vendor/old/entry.md", "an ocelot under the old marker\n")
	writeFile(t, root, "vendor/new/.fyr-noindex", "")
	writeFile(t, root, "vendor/new/entry.md", "an ocelot under the new marker\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if !strings.Contains(out, "docs/own.md:") {
		t.Errorf("a file outside both marked directories is missing:\n%s", out)
	}
	if strings.Contains(out, "\nvendor/old/") || strings.Contains(out, "\nvendor/new/") {
		t.Errorf("a marked directory is in the index:\n%s", out)
	}
	if !strings.Contains(out, "; left out by a noindex marker: vendor/new/, vendor/old/)") {
		t.Errorf("the header does not name both directories:\n%s", out)
	}
}

// The brain's export is searched as records (internal/brain), never as a
// file: indexing it too printed every record twice (brain plan Phase 2).
func TestTrailExportIsNotAFile(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, ".agents/trail.jsonl", `{"date":"2026-10-25","id":"BUG-001","kind":"bug","status":"open","title":"zebrafinch","updated":"2026-10-25T00:00:00Z","body":"zebrafinch"}`+"\n")
	writeFile(t, root, "notes.md", "zebrafinch\n")
	commitAll(t, root, "trail")
	out := mustSearch(t, Options{Dir: root, Words: []string{"zebrafinch"}})
	if strings.Contains(out, "trail.jsonl") || !strings.Contains(out, "notes.md") {
		t.Errorf("trail.jsonl is listed as a file hit:\n%s", out)
	}
	// Untracked, it is skipped too.
	root = newRepo(t)
	writeFile(t, root, ".agents/trail.jsonl", "zebrafinch\n")
	if out := mustSearch(t, Options{Dir: root, Words: []string{"zebrafinch"}}); strings.Contains(out, "trail.jsonl") {
		t.Errorf("an untracked trail.jsonl is listed:\n%s", out)
	}
}
