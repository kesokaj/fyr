package brain

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

var fixed = time.Date(2026, 10, 25, 14, 30, 0, 0, time.UTC)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo is a fresh git checkout with no trail at all: a migrated project.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	return dir
}

// opts is a project-scope call in dir against a scratch home, never the
// real one (R6).
func opts(dir, home string) Options {
	return Options{Home: home, Dir: dir, Now: func() time.Time { return fixed }}
}

func add(t *testing.T, o Options, kind, title, body string, extra ...string) string {
	t.Helper()
	in := AddInput{Kind: kind, Title: title, Body: strings.NewReader(body)}
	for i := 0; i+1 < len(extra); i += 2 {
		switch extra[i] {
		case "status":
			in.Status = extra[i+1]
		case "id":
			in.ID = extra[i+1]
		case "project":
			in.Project = extra[i+1]
		}
	}
	id, err := Add(o, in)
	if err != nil {
		t.Fatalf("add %s %q: %v", kind, title, err)
	}
	return id
}

func trailFile(dir string) string { return filepath.Join(dir, ".agents", "trail.jsonl") }

func trailText(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(trailFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func str(s string) *string { return &s }

// The store is created under the home's ~/.config/fyr, private: the
// directory 0700, the database 0600, with the records, FTS and meta tables.
func TestSchemaAndModes(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	add(t, opts(dir, home), "learning", "first", "body")
	p := Path(home)
	if p != filepath.Join(home, harness.StateDir, "brain.db") {
		t.Fatalf("Path = %s", p)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("brain.db mode %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("state dir mode %v, want 0700", di.Mode().Perm())
	}
	db, err := sql.Open("sqlite", "file:"+p+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"records", "records_fts", "meta"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = ?`, name).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s: n=%d err=%v", name, n, err)
		}
	}
	var cols []string
	rows, err := db.Query(`SELECT name FROM pragma_table_info('records')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c string
		rows.Scan(&c)
		cols = append(cols, c)
	}
	rows.Close()
	for _, want := range []string{"scope", "id", "kind", "status", "date", "title", "body", "project", "updated"} {
		if !strings.Contains(" "+strings.Join(cols, " ")+" ", " "+want+" ") {
			t.Errorf("records lacks column %s: %v", want, cols)
		}
	}
}

// A store a newer binary wrote is refused, untouched.
func TestNewerSchemaRefused(t *testing.T) {
	home := t.TempDir()
	p := Path(home)
	os.MkdirAll(filepath.Dir(p), 0o700)
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err = List(Options{Home: home, Global: true}, ListFilter{})
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("a newer schema: %v, want a refusal naming it", err)
	}
}

// add prints the ID, get returns header and body, list names it, and edit
// without a body keeps the body (D9) while edit with one replaces it.
func TestAddGetEditList(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	id := add(t, o, "bug", "trailer slipped into a commit", "Symptom: a trailer.\n\nRule: check the message.\n")
	if id != "BUG-001" {
		t.Fatalf("first bug id %q, want BUG-001", id)
	}
	recs, missing, err := Get(o, []string{id})
	if err != nil || len(missing) != 0 || len(recs) != 1 {
		t.Fatalf("get: %v %v %v", recs, missing, err)
	}
	r := recs[0]
	if r.Kind != "bug" || r.Status != "open" || r.Date != "2026-10-25" || r.Title != "trailer slipped into a commit" {
		t.Errorf("record = %+v", r)
	}
	if r.Body != "Symptom: a trailer.\n\nRule: check the message." {
		t.Errorf("body = %q", r.Body)
	}
	if err := Edit(o, EditInput{ID: id, Status: str("fixed"), Title: str("trailer in a commit")}); err != nil {
		t.Fatal(err)
	}
	recs, _, _ = Get(o, []string{id})
	if recs[0].Status != "fixed" || recs[0].Title != "trailer in a commit" || recs[0].Body != r.Body {
		t.Errorf("edit without --body changed the body or missed metadata: %+v", recs[0])
	}
	if err := Edit(o, EditInput{ID: id, Body: strings.NewReader("Rule: new.\n")}); err != nil {
		t.Fatal(err)
	}
	recs, _, _ = Get(o, []string{id})
	if recs[0].Body != "Rule: new." {
		t.Errorf("edit --body: %q", recs[0].Body)
	}
	list, err := List(o, ListFilter{})
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("list: %v %v", list, err)
	}
	if got := FormatLine(list[0], false); got != "BUG-001  bug  fixed  2026-10-25  trailer in a commit" {
		t.Errorf("list line %q", got)
	}
	_, missing, err = Get(o, []string{"BUG-404"})
	if !IsRefusal(err) || len(missing) != 1 {
		t.Errorf("get of a missing id: %v %v, want a refusal naming it", missing, err)
	}
	if err := Edit(o, EditInput{ID: "BUG-404", Status: str("fixed")}); !IsRefusal(err) {
		t.Errorf("edit of a missing id: %v, want a refusal", err)
	}
}

// An empty body is refused (D9), a body over 1 MiB is refused, and one of
// exactly 1 MiB is taken.
func TestBodyLimits(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	for name, body := range map[string]string{"empty": "", "blank": " \n\t\n", "over 1 MiB": strings.Repeat("a", 1<<20+1)} {
		if _, err := Add(o, AddInput{Kind: "learning", Title: "t", Body: strings.NewReader(body)}); !IsRefusal(err) {
			t.Errorf("%s body: %v, want a refusal", name, err)
		}
	}
	if _, err := Add(o, AddInput{Kind: "learning", Title: "t", Body: strings.NewReader(strings.Repeat("a", 1<<20))}); err != nil {
		t.Errorf("a body of exactly 1 MiB: %v", err)
	}
	if _, err := Add(o, AddInput{Kind: "learning", Title: "t", Body: strings.NewReader("a\x00b")}); !IsRefusal(err) {
		t.Errorf("a NUL in the body: %v, want a refusal", err)
	}
	id := add(t, o, "learning", "keep", "kept")
	if err := Edit(o, EditInput{ID: id, Body: strings.NewReader("\n")}); !IsRefusal(err) {
		t.Errorf("edit --body with an empty body: %v, want a refusal", err)
	}
}

// feedback and proposal are global only (D10, R8): refused in a project,
// taken with --global, and never in the export.
func TestGlobalOnlyKinds(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	for _, k := range []string{"feedback", "proposal"} {
		if _, err := Add(o, AddInput{Kind: k, Title: "t", Body: strings.NewReader("b")}); !IsRefusal(err) {
			t.Errorf("%s in a project: %v, want a refusal", k, err)
		}
	}
	g := o
	g.Global = true
	add(t, g, "feedback", "use the verbs", "said so", "project", dir)
	add(t, g, "proposal", "a rule", "draft", "project", dir)
	if _, err := Add(g, AddInput{Kind: "memory", Title: "t", Body: strings.NewReader("b")}); !IsRefusal(err) {
		t.Errorf("memory --global: %v, want a refusal (memory is project-scoped, D14)", err)
	}
	if _, err := Add(o, AddInput{Kind: "learning", Title: "t", Project: dir, Body: strings.NewReader("b")}); !IsUsage(err) {
		t.Errorf("--project without --global: %v, want a usage error", err)
	}
	list, err := List(g, ListFilter{Kind: "proposal", Project: dir})
	if err != nil || len(list) != 1 || list[0].Status != "queued" || list[0].Project != dir {
		t.Fatalf("list --global --kind proposal --project: %+v %v", list, err)
	}
	add(t, o, "learning", "project one", "b")
	if s := trailText(t, dir); strings.Contains(s, "feedback") || strings.Contains(s, "proposal") {
		t.Errorf("a global record reached the export:\n%s", s)
	}
}

// Status per kind (D6): defaults, the allowed values, plan derived from its
// body, handoff with none.
func TestStatusRules(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	g := o
	g.Global = true
	for _, tc := range []struct {
		kind, status, body string
		global             bool
		want               string // "" with ok=false: refused
		ok                 bool
	}{
		{"bug", "", "b", false, "open", true},
		{"bug", "graduated", "b", false, "graduated", true},
		{"bug", "closed", "b", false, "", false},
		{"adr", "", "b", false, "accepted", true},
		{"adr", "superseded:ADR-047a", "b", false, "superseded:ADR-047a", true},
		{"adr", "superseded:soon", "b", false, "", false},
		{"plan", "", "- [x] done\n- [ ] next\n", false, "open", true},
		{"plan", "", "- [x] done\n```\n- [ ] in a fence\n```\n", false, "closed", true},
		{"plan", "open", "- [ ] x\n", false, "", false},
		{"spec", "", "b", false, "current", true},
		{"guide", "retired", "b", false, "retired", true},
		{"deploy", "open", "b", false, "", false},
		{"proposal", "", "b", true, "queued", true},
		{"proposal", "applied:3797da5", "b", true, "applied:3797da5", true},
		{"proposal", "applied:HEAD", "b", true, "", false},
		{"handoff", "", "b", false, "", true},
		{"handoff", "closed", "b", false, "", false},
		{"review", "", "b", false, "closed", true},
		{"review", "open", "b", false, "", false},
		{"memory", "", "b", false, "closed", true},
	} {
		oo := o
		if tc.global {
			oo = g
		}
		id, err := Add(oo, AddInput{Kind: tc.kind, Title: tc.kind + " " + tc.status, Status: tc.status, Body: strings.NewReader(tc.body)})
		if !tc.ok {
			if err == nil {
				t.Errorf("%s --status %q: taken as %s, want refused", tc.kind, tc.status, id)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s --status %q: %v", tc.kind, tc.status, err)
			continue
		}
		recs, _, err := Get(oo, []string{id})
		if err != nil || recs[0].Status != tc.want {
			t.Errorf("%s --status %q: status %q, want %q (%v)", tc.kind, tc.status, recs[0].Status, tc.want, err)
		}
	}
	// A plan's status follows its body on edit, and --status is refused.
	id := add(t, o, "plan", "the plan", "- [ ] one\n")
	if err := Edit(o, EditInput{ID: id, Body: strings.NewReader("- [x] one\n")}); err != nil {
		t.Fatal(err)
	}
	if recs, _, _ := Get(o, []string{id}); recs[0].Status != "closed" {
		t.Errorf("plan after ticking its last box: %q, want closed", recs[0].Status)
	}
	if err := Edit(o, EditInput{ID: id, Status: str("open")}); err == nil {
		t.Error("edit --status on a plan was taken")
	}
	open, err := List(o, ListFilter{Open: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range open {
		switch {
		case r.Kind == "bug" && r.Status == "open", r.Kind == "plan" && r.Status == "open",
			(r.Kind == "spec" || r.Kind == "guide" || r.Kind == "deploy") && r.Status == "current":
		default:
			t.Errorf("--open listed %s %s %s", r.ID, r.Kind, r.Status)
		}
	}
}

// IDs (D7): BUG and ADR numbered max+1 per scope and compared as numbers,
// ADR-047a kept, past 999; every other kind <KIND>-<date>-<slug>.
func TestIDs(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	if id := add(t, o, "bug", "a", "b"); id != "BUG-001" {
		t.Fatalf("first: %s", id)
	}
	add(t, o, "bug", "b", "b", "id", "BUG-010")
	if id := add(t, o, "bug", "c", "b"); id != "BUG-011" {
		t.Errorf("after BUG-010: %s, want BUG-011 (max+1)", id)
	}
	if _, err := Add(o, AddInput{Kind: "bug", Title: "dup", ID: "BUG-010", Body: strings.NewReader("b")}); !IsRefusal(err) {
		t.Errorf("an explicit duplicate id: %v, want a refusal", err)
	}
	if _, err := Add(o, AddInput{Kind: "bug", Title: "x", ID: "ADR-001", Body: strings.NewReader("b")}); err == nil {
		t.Error("a bug given an ADR id was taken")
	}
	add(t, o, "adr", "lettered", "b", "id", "ADR-047a")
	add(t, o, "adr", "plain", "b", "id", "ADR-047")
	if id := add(t, o, "adr", "next", "b"); id != "ADR-048" {
		t.Errorf("after ADR-047a: %s, want ADR-048", id)
	}
	add(t, o, "bug", "nines", "b", "id", "BUG-999")
	if id := add(t, o, "bug", "past", "b"); id != "BUG-1000" {
		t.Errorf("after BUG-999: %s, want BUG-1000", id)
	}
	if id := add(t, o, "bug", "past2", "b"); id != "BUG-1001" {
		t.Errorf("after BUG-1000: %s, want BUG-1001 (numeric, not string, max)", id)
	}
	// Numbering is per scope: the global scope starts at 001.
	g := o
	g.Global = true
	if id := add(t, g, "bug", "global one", "b"); id != "BUG-001" {
		t.Errorf("global scope: %s, want BUG-001", id)
	}
	if id := add(t, o, "plan", "Brain store: Phase 1!", "- [ ] x"); id != "PLAN-2026-10-25-1430-brain-store-phase-1" {
		t.Errorf("plan id %s", id)
	}
	if id := add(t, o, "plan", "Brain store: Phase 1!", "- [ ] x"); id != "PLAN-2026-10-25-1430-brain-store-phase-1-2" {
		t.Errorf("same minute, same slug: %s, want a -2 suffix", id)
	}
	if id := add(t, o, "testing", "verify", "log"); id != "TEST-2026-10-25-1430-verify" {
		t.Errorf("testing id %s, want the TEST prefix", id)
	}
	add(t, o, "spec", "record", "b", "id", "SPEC-fyr-record")
	if _, err := Add(o, AddInput{Kind: "spec", Title: "x", ID: "PLAN-2026-x", Body: strings.NewReader("b")}); err == nil {
		t.Error("a spec given a PLAN id was taken")
	}
	// The export orders numbers numerically.
	var bugs []string
	for _, l := range strings.Split(strings.TrimSpace(trailText(t, dir)), "\n") {
		if i := strings.Index(l, `"id":"BUG-`); i >= 0 {
			bugs = append(bugs, l[i+6:strings.Index(l[i+6:], `"`)+i+6])
		}
	}
	if got := strings.Join(bugs, " "); got != "BUG-001 BUG-010 BUG-011 BUG-999 BUG-1000 BUG-1001" {
		t.Errorf("export order %s", got)
	}
}

// TestHelperAdd is not a test: TestTwoProcessesAllocateDistinctIDs runs this
// binary twice at once with BRAIN_HELPER set, and each adds bugs.
func TestHelperAdd(t *testing.T) {
	spec := os.Getenv("BRAIN_HELPER")
	if spec == "" {
		t.Skip("helper process")
	}
	parts := strings.Split(spec, "|")
	dir, home := parts[0], parts[1]
	count, _ := strconv.Atoi(parts[2])
	o := Options{Home: home, Dir: dir}
	for i := 0; i < count; i++ {
		if _, err := Add(o, AddInput{Kind: "bug", Title: fmt.Sprintf("from %d #%d", os.Getpid(), i), Body: strings.NewReader("b")}); err != nil {
			fmt.Fprintln(os.Stderr, "add:", err)
			os.Exit(3)
		}
	}
}

// Two processes allocating at once get distinct IDs: the number is taken
// inside the write's transaction (D7, D20), and the export agrees.
func TestTwoProcessesAllocateDistinctIDs(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	const each = 15
	var wg sync.WaitGroup
	errs := make([]error, 2)
	outs := make([]bytes.Buffer, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := exec.Command(os.Args[0], "-test.run=^TestHelperAdd$")
			c.Env = append(os.Environ(), "BRAIN_HELPER="+dir+"|"+home+"|"+strconv.Itoa(each))
			c.Stdout, c.Stderr = &outs[i], &outs[i]
			errs[i] = c.Run()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("helper %d: %v\n%s", i, err, outs[i].String())
		}
	}
	list, err := List(opts(dir, home), ListFilter{Kind: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range list {
		if seen[r.ID] {
			t.Errorf("duplicate id %s", r.ID)
		}
		seen[r.ID] = true
	}
	if len(seen) != 2*each {
		t.Fatalf("%d distinct ids, want %d", len(seen), 2*each)
	}
	for n := 1; n <= 2*each; n++ {
		if id := fmt.Sprintf("BUG-%03d", n); !seen[id] {
			t.Errorf("%s missing: allocation skipped a number", id)
		}
	}
	if lines := strings.Count(trailText(t, dir), "\n"); lines != 2*each {
		t.Errorf("trail.jsonl has %d lines, want %d: the export lost a concurrent write", lines, 2*each)
	}
}

// The export is byte-stable for the same rows whatever order they were
// written in, sorted by ID, ends in a newline, keeps the key order, and
// leaves memory out.
func TestExportByteStable(t *testing.T) {
	home := t.TempDir()
	type rec struct{ kind, id, title, body string }
	rows := []rec{
		{"bug", "BUG-002", "two", "b2"},
		{"bug", "BUG-010", "ten", "b10 <html> & \"quotes\"\nline two"},
		{"adr", "ADR-001", "one", "a1"},
		{"spec", "SPEC-fyr-record", "the spec", "s"},
		{"learning", "LEARNING-2026-10-25-1430-x", "x", "l"},
	}
	write := func(order []int) string {
		dir := repo(t)
		o := opts(dir, home)
		for _, i := range order {
			r := rows[i]
			add(t, o, r.kind, r.title, r.body, "id", r.id)
		}
		add(t, o, "memory", "local only", "never exported")
		return trailText(t, dir)
	}
	a := write([]int{0, 1, 2, 3, 4})
	b := write([]int{4, 3, 2, 1, 0})
	if a != b {
		t.Fatalf("the same rows exported differently:\n%s\n---\n%s", a, b)
	}
	if !strings.HasSuffix(a, "}\n") {
		t.Errorf("no final newline")
	}
	if strings.Contains(a, "never exported") || strings.Contains(a, `"memory"`) {
		t.Errorf("memory reached the export:\n%s", a)
	}
	lines := strings.Split(strings.TrimSuffix(a, "\n"), "\n")
	var ids []string
	for _, l := range lines {
		if !strings.HasPrefix(l, `{"date":"2026-10-25","id":"`) {
			t.Errorf("key order: %s", l)
		}
		for _, k := range []string{`"date"`, `"id"`, `"kind"`, `"status"`, `"title"`, `"updated"`, `"body"`} {
			if !strings.Contains(l, k+":") {
				t.Errorf("line lacks %s: %s", k, l)
			}
		}
		if strings.Index(l, `"updated"`) > strings.Index(l, `"body"`) || strings.Index(l, `"kind"`) > strings.Index(l, `"status"`) {
			t.Errorf("key order: %s", l)
		}
		i := strings.Index(l, `"id":"`) + 6
		ids = append(ids, l[i:i+strings.Index(l[i:], `"`)])
	}
	if !sort.SliceIsSorted(ids, func(i, j int) bool { return idLess(ids[i], ids[j]) }) {
		t.Errorf("not sorted: %v", ids)
	}
	if !strings.Contains(a, `<html> & \"quotes\"\nline two`) {
		t.Errorf("the body is not written as plain JSON (no HTML escaping): %s", a)
	}
}

// Load (D21): a file git changed replaces the project's exported rows; the
// file wins; memory stays.
func TestLoadReplacesRowsFromAChangedFile(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	add(t, o, "bug", "kept", "b")
	add(t, o, "bug", "dropped", "b")
	add(t, o, "memory", "local", "m")
	s := trailText(t, dir)
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	edited := strings.Replace(lines[0], `"title":"kept"`, `"title":"kept, retitled by a pull"`, 1)
	extra := `{"date":"2026-10-24","id":"ADR-063","kind":"adr","status":"accepted","title":"the brain","updated":"2026-10-24T10:00:00Z","body":"decided"}`
	if err := os.WriteFile(trailFile(dir), []byte(edited+"\n"+extra+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := List(o, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range list {
		got[r.ID] = r.Title
	}
	if got["BUG-001"] != "kept, retitled by a pull" || got["ADR-063"] != "the brain" {
		t.Errorf("the file did not win: %v", got)
	}
	if _, ok := got["BUG-002"]; ok {
		t.Errorf("a row the file no longer holds survived: %v", got)
	}
	if len(got) != 3 {
		t.Errorf("rows %v, want BUG-001, ADR-063 and the memory", got)
	}
	// The next write numbers from the loaded rows and exports at once.
	if id := add(t, o, "bug", "after", "b"); id != "BUG-002" {
		t.Errorf("after load: %s", id)
	}
	if !strings.Contains(trailText(t, dir), "ADR-063") {
		t.Error("the write after a load dropped a loaded record")
	}
}

// A brain.db lost: the project's records come back from trail.jsonl on the
// next call (R7, Failure modes).
func TestLostStoreRestoredFromTrail(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	add(t, o, "plan", "open plan", "- [ ] x")
	add(t, o, "bug", "a bug", "b")
	for _, p := range []string{Path(home), Path(home) + "-wal", Path(home) + "-shm"} {
		os.Remove(p)
	}
	list, err := List(o, ListFilter{Open: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("restored %d open records, want 2: %+v", len(list), list)
	}
}

// A bad file is refused with its line number — a duplicate ID with both,
// a git conflict marker, a line that is not JSON — and every verb refuses
// until it parses.
func TestLoadRefusals(t *testing.T) {
	good := `{"date":"2026-10-24","id":"BUG-001","kind":"bug","status":"open","title":"t","updated":"2026-10-24T10:00:00Z","body":"b"}`
	other := `{"date":"2026-10-24","id":"BUG-002","kind":"bug","status":"open","title":"u","updated":"2026-10-24T10:00:00Z","body":"b"}`
	for name, tc := range map[string]struct {
		file string
		want []string
	}{
		"duplicate id":    {good + "\n" + other + "\n" + good + "\n", []string{"line 3", "line 1", "BUG-001"}},
		"conflict marker": {good + "\n<<<<<<< HEAD\n" + other + "\n=======\n>>>>>>> theirs\n", []string{"line 2", "conflict"}},
		"bad json":        {good + "\n{\"id\": \n", []string{"line 2"}},
		"unknown key":     {strings.Replace(good, `"body"`, `"extra":1,"body"`, 1) + "\n", []string{"line 1"}},
		"missing key":     {strings.Replace(good, `,"updated":"2026-10-24T10:00:00Z"`, "", 1) + "\n", []string{"line 1", "updated"}},
		"memory exported": {strings.Replace(strings.Replace(good, `"bug"`, `"memory"`, 1), `BUG-001`, `MEMORY-2026-10-24-1000-x`, 1) + "\n", []string{"line 1"}},
		"bad status":      {strings.Replace(good, `"open"`, `"maybe"`, 1) + "\n", []string{"line 1", "maybe"}},
		"bad id":          {strings.Replace(good, `BUG-001`, `ADR-001`, 1) + "\n", []string{"line 1"}},
	} {
		t.Run(name, func(t *testing.T) {
			dir, home := repo(t), t.TempDir()
			o := opts(dir, home)
			os.MkdirAll(filepath.Join(dir, ".agents"), 0o755)
			if err := os.WriteFile(trailFile(dir), []byte(tc.file), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := List(o, ListFilter{})
			if !IsRefusal(err) {
				t.Fatalf("list on a bad file: %v, want a refusal", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal %q lacks %q", err, w)
				}
			}
			if _, err := Add(o, AddInput{Kind: "bug", Title: "x", Body: strings.NewReader("b")}); !IsRefusal(err) {
				t.Errorf("a write on a bad file: %v, want a refusal", err)
			}
			if got, _ := os.ReadFile(trailFile(dir)); string(got) != tc.file {
				t.Errorf("a refused load rewrote the file")
			}
			// Fixed by hand, it loads.
			os.WriteFile(trailFile(dir), []byte(good+"\n"), 0o644)
			if list, err := List(o, ListFilter{}); err != nil || len(list) != 1 {
				t.Errorf("after the fix: %v %v", list, err)
			}
		})
	}
}

// A symlinked .agents or trail.jsonl is refused: the export never writes
// through a link a commit could carry. Each link is proven to exist first.
func TestSymlinkedExportPathRefused(t *testing.T) {
	t.Run(".agents", func(t *testing.T) {
		dir, home := repo(t), t.TempDir()
		elsewhere := t.TempDir()
		if err := os.Symlink(elsewhere, filepath.Join(dir, ".agents")); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Lstat(filepath.Join(dir, ".agents")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("the fixture's symlink is not there: %v", err)
		}
		_, err := Add(opts(dir, home), AddInput{Kind: "learning", Title: "t", Body: strings.NewReader("b")})
		if !IsRefusal(err) || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("add with a symlinked .agents: %v, want a refusal", err)
		}
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Errorf("wrote through the link: %v", entries)
		}
	})
	t.Run("trail.jsonl", func(t *testing.T) {
		dir, home := repo(t), t.TempDir()
		os.MkdirAll(filepath.Join(dir, ".agents"), 0o755)
		target := filepath.Join(t.TempDir(), "outside.jsonl")
		os.WriteFile(target, nil, 0o644)
		if err := os.Symlink(target, trailFile(dir)); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Lstat(trailFile(dir)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("the fixture's symlink is not there: %v", err)
		}
		_, err := Add(opts(dir, home), AddInput{Kind: "learning", Title: "t", Body: strings.NewReader("b")})
		if !IsRefusal(err) || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("add with a symlinked trail.jsonl: %v, want a refusal", err)
		}
		if b, _ := os.ReadFile(target); len(b) != 0 {
			t.Errorf("wrote through the link: %q", b)
		}
	})
}

// get's output goes through printable: a record cannot drive the terminal
// or forge a line, while its newlines and tabs stay.
func TestGetOutputPrintable(t *testing.T) {
	r := Record{ID: "BUG-001", Kind: "bug", Status: "open", Date: "2026-10-25",
		Title: "evil\x1b[31m title‮", Body: "line one\x1b]0;x\x07\n\tindented forged"}
	got := FormatGet(r)
	if strings.ContainsAny(got, "\x1b\x07‮ ") {
		t.Errorf("control characters survived: %q", got)
	}
	want := "BUG-001  bug  open  2026-10-25  evil?[31m title?\nline one?]0;x?\n\tindented?forged\n"
	if got != want {
		t.Errorf("FormatGet = %q, want %q", got, want)
	}
}

// D16: a project whose trail is still files refuses every record verb and
// names the command; a migrated project (trail.jsonl present) does not.
func TestUnmigratedProjectRefused(t *testing.T) {
	for _, docs := range []string{"docs", filepath.Join(".agents", "docs")} {
		dir, home := repo(t), t.TempDir()
		o := opts(dir, home)
		os.MkdirAll(filepath.Join(dir, docs, "plans"), 0o755)
		checks := map[string]error{}
		_, checks["add"] = Add(o, AddInput{Kind: "learning", Title: "t", Body: strings.NewReader("b")})
		_, _, checks["get"] = Get(o, []string{"BUG-001"})
		checks["edit"] = Edit(o, EditInput{ID: "BUG-001", Status: str("fixed")})
		_, checks["list"] = List(o, ListFilter{})
		for verb, err := range checks {
			if !IsRefusal(err) || !strings.Contains(err.Error(), "fyr record import") {
				t.Errorf("%s: %s in an unmigrated project: %v, want a refusal naming fyr record import", docs, verb, err)
			}
		}
		if _, err := os.Stat(trailFile(dir)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: a refused verb created trail.jsonl", docs)
		}
		// The global scope is not the project's: it still answers.
		g := o
		g.Global = true
		if _, err := List(g, ListFilter{}); err != nil {
			t.Errorf("%s: list --global in an unmigrated project: %v", docs, err)
		}
		os.MkdirAll(filepath.Join(dir, ".agents"), 0o755)
		if err := os.WriteFile(trailFile(dir), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := List(o, ListFilter{}); err != nil {
			t.Errorf("%s: with trail.jsonl present: %v", docs, err)
		}
	}
}

// Outside a git checkout the project scope refuses; --global still works.
func TestOutsideACheckout(t *testing.T) {
	home := t.TempDir()
	o := opts(t.TempDir(), home)
	if _, err := List(o, ListFilter{}); !IsRefusal(err) {
		t.Errorf("list outside a checkout: %v, want a refusal", err)
	}
	o.Global = true
	if _, err := Add(o, AddInput{Kind: "learning", Title: "t", Body: strings.NewReader("b")}); err != nil {
		t.Errorf("add --global outside a checkout: %v", err)
	}
}

// A linked worktree shares the main worktree's scope (memory.Root's rule),
// but its export is its own checkout's file, the one its branch commits.
func TestLinkedWorktreeExportsToItsOwnCheckout(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, dir, "worktree", "add", "-q", "-b", "side", wt)
	id := add(t, opts(wt, home), "learning", "from the worktree", "b")
	if _, err := os.Stat(trailFile(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a write in the linked worktree wrote the main worktree's trail.jsonl")
	}
	if !strings.Contains(trailText(t, wt), id) {
		t.Errorf("the linked worktree's trail.jsonl lacks %s", id)
	}
	recs, _, err := Get(opts(wt, home), []string{id})
	if err != nil || recs[0].Scope != dir {
		r, _ := filepath.EvalSymlinks(dir)
		if err != nil || recs[0].Scope != r {
			t.Errorf("scope %v (%v), want the main worktree %s", recs, err, dir)
		}
	}
}

// list --all covers every scope; --project needs --global.
func TestListScopes(t *testing.T) {
	home := t.TempDir()
	a, b := repo(t), repo(t)
	add(t, opts(a, home), "learning", "in a", "x")
	add(t, opts(b, home), "learning", "in b", "x")
	g := opts(a, home)
	g.Global = true
	add(t, g, "feedback", "global fb", "x")
	only, err := List(opts(a, home), ListFilter{})
	if err != nil || len(only) != 1 || only[0].Title != "in a" {
		t.Errorf("project list: %+v %v", only, err)
	}
	all, err := List(opts(a, home), ListFilter{All: true})
	if err != nil || len(all) != 3 {
		t.Errorf("--all: %+v %v", all, err)
	}
	if line := FormatLine(all[0], true); !strings.Contains(line, "[") {
		t.Errorf("--all line names no scope: %q", line)
	}
	if _, err := List(opts(a, home), ListFilter{Project: a}); !IsUsage(err) {
		t.Errorf("--project without --global: %v, want usage", err)
	}
}

// Search ranks records by BM25 over title and body in the project and
// global scopes; --global covers every scope.
func TestSearchRecords(t *testing.T) {
	home := t.TempDir()
	a, b := repo(t), repo(t)
	add(t, opts(a, home), "bug", "trailer co-authored slipped in", "Rule: no trailer.")
	add(t, opts(b, home), "bug", "other project trailer", "x")
	g := opts(a, home)
	g.Global = true
	add(t, g, "feedback", "trailer feedback", "no co-authored lines")
	hits, err := Search(opts(a, home), []string{"trailer", "co-authored"}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "BUG-001" || hits[0].Label != "BUG-001" {
		t.Fatalf("project search: %+v", hits)
	}
	if hits[1].Label != "global:"+hits[1].ID {
		t.Errorf("a global hit is not labelled: %q", hits[1].Label)
	}
	if !strings.Contains(hits[0].Snippet, "»") && !strings.Contains(hits[0].Title, "trailer") {
		t.Errorf("snippet %q", hits[0].Snippet)
	}
	all, err := Search(g, []string{"trailer"}, "", 10)
	if err != nil || len(all) != 3 {
		t.Errorf("--global: %+v %v", all, err)
	}
	if k, err := Search(opts(a, home), []string{"trailer"}, "feedback", 10); err != nil || len(k) != 1 {
		t.Errorf("--kind feedback: %+v %v", k, err)
	}
	if k, err := Search(opts(a, home), []string{"trailer"}, "code", 10); err != nil || len(k) != 0 {
		t.Errorf("--kind code: %+v %v", k, err)
	}
}
