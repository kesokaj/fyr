package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// docsFixture is an unmigrated project: a committed .agents/docs tree with
// one file of each case D8 decides, and a README that references a review.
func docsFixture(t *testing.T) string {
	t.Helper()
	dir := repo(t)
	files := map[string]string{
		"README.md":                                         "See `.agents/docs/reviews/2026-01-04-kept-by-readme.md`.\n",
		".agents/docs/plans/2026-01-01-open.md":             "---\nowner: main\ndate: 2026-01-01\n---\n# Plan — still open\n\n- [x] done\n- [ ] left\n\nEvidence: `testing/2026-01-02-ref.md`.\n",
		".agents/docs/plans/2026-01-01-closed.md":           "---\nowner: main\ndate: 2026-01-01\n---\n# Plan — closed\n\n- [x] done\n",
		".agents/docs/testing/2026-01-02-ref.md":            "---\nowner: verifier\ndate: 2026-01-02\n---\n# Verify — referenced\n\nPASS, see TEST-2026-01-05-by-id.\n",
		".agents/docs/testing/2026-01-05-by-id.md":          "# Verify — named by ID\n\nPASS\n",
		".agents/docs/testing/2026-01-03-unref.md":          "# Verify — nobody cites me\n\nPASS\n",
		".agents/docs/testing/guide.md":                     "---\nowner: main\ndate: 2025-12-01\n---\n# Testing guide\n\n§ 1 run go test.\n",
		".agents/docs/reviews/2026-01-04-kept-by-readme.md": "# Review — kept by README\n\nnothing\n",
		".agents/docs/reviews/2026-01-04-dropped.md":        "# Review — dropped\n\nnothing\n",
		".agents/docs/handoffs/2026-01-01-1000-old.md":      "# Handoff — old\n\nold\n",
		".agents/docs/handoffs/2026-01-02-1000-new.md":      "---\nowner: main\ndate: 2026-01-02\n---\n# Handoff — new\n\nnew\n",
		".agents/docs/changes/2026-01-01-1000-change.md":    "# Change\n\ndone\n",
		".agents/docs/retros/2026-01-01-retro.md":           "# Retro\n\nnothing\n",
		".agents/docs/specs/fyr/record.md":                  "---\nowner: main\ndate: 2026-01-01\n---\n# `fyr record` — the brain store\n\nspec text\n",
		".agents/docs/deploy/local.md":                      "# Local \x1b[31m" + strings.Repeat("long ", 200) + "\n\nbuild it \x01 \x1b[0m\n",
		".agents/docs/bugs/BUG-001-closed.md":               "---\nowner: main\ndate: 2026-01-01\nid: BUG-001\nstatus: fixed\n---\n# BUG-001: a closed bug\n\n## Symptom\nlong symptom text\n\n## Prevention\nThe rule that stays.\n",
		".agents/docs/bugs/BUG-002-open.md":                 "---\nowner: main\ndate: 2026-01-02\nid: BUG-002\nstatus: open\n---\n# BUG-002: an open bug\n\n## Symptom\nstill broken\n\n## Prevention\nTBD\n",
		".agents/docs/bugs/BUG-003-grad.md":                 "---\nowner: main\ndate: 2026-01-03\nid: BUG-003\nstatus: graduated to a test\n---\n# BUG-003: graduated\n\n## Symptom\nx\n\n## Prevention\nA test pins it.\n",
		".agents/docs/bugs/INDEX.md":                        "# Bugs\n\n| ID | Date | Area | Status | Symptom | Prevention |\n|---|---|---|---|---|---|\n| BUG-001 | 2026-01-01 | a | fixed | s | p |\n| BUG-002 | 2026-01-02 | a | open | s | p |\n\n## Graduated\n\n| ID | Area | Graduated to |\n|---|---|---|\n| BUG-003 | a | `TestThing` |\n",
		".agents/docs/architecture/DECISIONS.md":            "# Architecture Decisions\n\nAppend-only.\n\n## ADR-001: First     (2026-01-01) — Superseded by ADR-002\n\nfirst body\n\n## ADR-002: Second     (2026-01-02) — Accepted; part superseded by ADR-047a\n\nsecond body\n\n## ADR-047a: Lettered     (2026-01-03) — Accepted\n\nthird body\n",
	}
	for rel, text := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "fixture")
	return dir
}

func importedIDs(res *ImportResult) map[string]Imported {
	out := map[string]Imported{}
	for _, r := range res.Records {
		out[r.ID] = r
	}
	return out
}

func TestImportKeepsWhatD8Keeps(t *testing.T) {
	dir, home := docsFixture(t), t.TempDir()
	res, err := Import(opts(dir, home), "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := importedIDs(res)
	for _, id := range []string{
		"PLAN-2026-01-01-open",             // an open plan
		"TEST-2026-01-02-ref",              // referenced by a path in the open plan
		"TEST-2026-01-05-by-id",            // referenced by ID in a kept record (closure)
		"REVIEW-2026-01-04-kept-by-readme", // referenced from README.md
		"HANDOFF-2026-01-02-1000-new",      // the newest handoff
		"BUG-001", "BUG-002", "BUG-003",    // every bug
		"ADR-001", "ADR-002", "ADR-047a", // every ADR, one record each
		"SPEC-fyr-record", "DEPLOY-local", "GUIDE-testing",
	} {
		if _, ok := got[id]; !ok {
			t.Errorf("%s not imported; got %v", id, keys(got))
		}
	}
	for _, id := range []string{"PLAN-2026-01-01-closed", "TEST-2026-01-03-unref", "REVIEW-2026-01-04-dropped",
		"HANDOFF-2026-01-01-1000-old", "CHANGE-2026-01-01-1000-change", "RETRO-2026-01-01-retro"} {
		if _, ok := got[id]; ok {
			t.Errorf("%s imported; D8 drops it", id)
		}
	}
	if len(got) != 14 {
		t.Errorf("%d records imported, want 14: %v", len(got), keys(got))
	}
	// Every tracked file under the docs root is listed for git rm: the
	// kept and the dropped alike, INDEX.md and DECISIONS.md included.
	if len(res.Remove) != 19 {
		t.Errorf("%d paths to remove, want all 19 tracked files: %v", len(res.Remove), res.Remove)
	}
	for _, p := range res.Remove {
		if !strings.HasPrefix(p, ".agents/docs/") {
			t.Errorf("remove path %q is not relative to the checkout's docs root", p)
		}
	}

	recs, _, err := Get(opts(dir, home), []string{"BUG-001", "BUG-002", "BUG-003", "ADR-001", "ADR-002", "PLAN-2026-01-01-open", "SPEC-fyr-record", "GUIDE-testing", "HANDOFF-2026-01-02-1000-new"})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Record{}
	for _, r := range recs {
		byID[r.ID] = r
	}
	closed := byID["BUG-001"]
	if closed.Status != "fixed" || closed.Title != "a closed bug" || strings.Contains(closed.Body, "long symptom") || !strings.Contains(closed.Body, "The rule that stays.") {
		t.Errorf("a closed bug is trimmed to its title and prevention rule: %+v", closed)
	}
	if open := byID["BUG-002"]; open.Status != "open" || !strings.Contains(open.Body, "still broken") {
		t.Errorf("an open bug is kept whole: %+v", open)
	}
	if g := byID["BUG-003"]; g.Status != "graduated" || !strings.Contains(g.Body, "TestThing") {
		t.Errorf("a graduated bug keeps where it graduated to: %+v", g)
	}
	if a := byID["ADR-001"]; a.Status != "superseded:ADR-002" || a.Title != "First" || a.Date != "2026-01-01" || !strings.Contains(a.Body, "first body") || strings.Contains(a.Body, "second body") {
		t.Errorf("ADR-001: %+v", a)
	}
	if a := byID["ADR-002"]; a.Status != "accepted" {
		t.Errorf("a partial supersession stays accepted: %+v", a)
	}
	if p := byID["PLAN-2026-01-01-open"]; p.Status != "open" || p.Date != "2026-01-01" || p.Title != "Plan — still open" || strings.Contains(p.Body, "owner: main") {
		t.Errorf("open plan: %+v", p)
	}
	if s := byID["SPEC-fyr-record"]; s.Status != "current" || s.Kind != "spec" {
		t.Errorf("spec: %+v", s)
	}
	if h := byID["HANDOFF-2026-01-02-1000-new"]; h.Status != "" {
		t.Errorf("handoff: %+v", h)
	}
	text := trailText(t, dir)
	if !strings.Contains(text, `"id":"ADR-047a"`) {
		t.Errorf("the import exports at once: %s", text)
	}
	if _, err := parseTrail([]byte(text)); err != nil {
		t.Errorf("the load refuses what the import wrote: %v", err)
	}
}

func keys(m map[string]Imported) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestImportDryRunWritesNothing(t *testing.T) {
	dir, home := docsFixture(t), t.TempDir()
	res, err := Import(opts(dir, home), "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != 14 {
		t.Errorf("the dry run reports what the real run stores: %d records", len(res.Records))
	}
	if _, err := os.Lstat(trailFile(dir)); !os.IsNotExist(err) {
		t.Errorf("dry run wrote trail.jsonl: %v", err)
	}
	if _, err := os.Lstat(Path(home)); !os.IsNotExist(err) {
		t.Errorf("dry run created brain.db: %v", err)
	}
	if git(t, dir, "status", "--porcelain") != "" {
		t.Error("dry run changed the tree")
	}
}

func TestImportRefusesADirtyTree(t *testing.T) {
	dir, home := docsFixture(t), t.TempDir()
	os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o644)
	_, err := Import(opts(dir, home), "", false)
	if !IsRefusal(err) || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("a dirty tree must be refused, got %v", err)
	}
	if _, err := os.Lstat(trailFile(dir)); !os.IsNotExist(err) {
		t.Error("a refused import wrote trail.jsonl")
	}
}

func TestImportRefusesAScriptInTheTrail(t *testing.T) {
	dir, home := docsFixture(t), t.TempDir()
	p := filepath.Join(dir, ".agents", "docs", "testing", "kit", "run.sh")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "kit")
	_, err := Import(opts(dir, home), "", true)
	if !IsRefusal(err) || !strings.Contains(err.Error(), "run.sh") {
		t.Fatalf("a script is not a record (D17): want a refusal naming it, got %v", err)
	}
}

func TestImportThenRecordVerbsWork(t *testing.T) {
	dir, home := docsFixture(t), t.TempDir()
	if _, err := List(opts(dir, home), ListFilter{}); !IsRefusal(err) {
		t.Fatalf("before the import the project is unmigrated (D16): %v", err)
	}
	if _, err := Import(opts(dir, home), "", false); err != nil {
		t.Fatal(err)
	}
	recs, err := List(opts(dir, home), ListFilter{Open: true, Kind: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].ID != "PLAN-2026-01-01-open" {
		t.Errorf("open plans after the import: %+v", recs)
	}
	if id := add(t, opts(dir, home), "bug", "next", "body"); id != "BUG-004" {
		t.Errorf("the next bug after an import is BUG-004, got %s", id)
	}
	// A second import finds every record stored and adds nothing.
	git(t, dir, "add", ".agents/trail.jsonl")
	git(t, dir, "commit", "-qm", "trail")
	res, err := Import(opts(dir, home), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != 0 || res.Skipped != 14 {
		t.Errorf("a re-import stores nothing new: %d records, %d skipped", len(res.Records), res.Skipped)
	}
}

func TestImportTakesTheProjectMemoryStore(t *testing.T) {
	dir, home := docsFixture(t), t.TempDir()
	store := filepath.Join(dir, ".agents", "memory")
	os.MkdirAll(store, 0o700)
	os.WriteFile(filepath.Join(store, "MEMORY.md"), []byte("- [keep-x](keep-x.md) — keep x\n"), 0o600)
	os.WriteFile(filepath.Join(store, "keep-x.md"), []byte("---\nname: keep-x\ndescription: \"keep x in mind\"\nmetadata:\n  node_type: memory\n  type: project\n  modified: 2026-01-01T00:00:00Z\n---\n\nThe fact.\n"), 0o600)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".agents/memory\n"), 0o644)
	git(t, dir, "add", ".gitignore")
	git(t, dir, "commit", "-qm", "ignore")
	res, err := Import(opts(dir, home), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Memory) != 2 {
		t.Errorf("the memory store's files are listed for removal: %v", res.Memory)
	}
	show, err := MemoryShow(opts(dir, home))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, "keep-x") || !strings.Contains(show, "keep x in mind") {
		t.Errorf("the imported memory shows: %q", show)
	}
	if strings.Contains(trailText(t, dir), "The fact.") {
		t.Error("a memory reached trail.jsonl (D14)")
	}
}

func TestMemoryAliasRoundTrip(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	id, err := MemoryAdd(o, MemoryEntry{Type: "feedback", Name: "no-tmp", Description: "scratch goes to .agents/scratch"}, strings.NewReader("Never /tmp.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if id != "MEMORY-no-tmp" {
		t.Errorf("id %s", id)
	}
	// The same name replaces, as `fyr memory add` always did.
	if _, err := MemoryAdd(o, MemoryEntry{Type: "feedback", Name: "no-tmp", Description: "scratch is .agents/scratch"}, strings.NewReader("Never /tmp, ever.\n")); err != nil {
		t.Fatal(err)
	}
	show, err := MemoryShow(o)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(show, "no-tmp") != 1 || !strings.Contains(show, "scratch is .agents/scratch") {
		t.Errorf("show: %q", show)
	}
	recs, _, err := Get(o, []string{"MEMORY-no-tmp"})
	if err != nil || !strings.Contains(recs[0].Body, "Never /tmp, ever.") || !strings.Contains(recs[0].Body, "type: feedback") {
		t.Fatalf("get: %+v %v", recs, err)
	}
	if _, err := os.Lstat(trailFile(dir)); !os.IsNotExist(err) {
		t.Error("a memory write created trail.jsonl")
	}
	for _, bad := range []MemoryEntry{{Type: "x", Name: "a", Description: "d"}, {Type: "user", Name: "Bad Name", Description: "d"}, {Type: "user", Name: "a", Description: ""}, {Type: "user", Name: "a", Description: "two\nlines"}} {
		if _, err := MemoryAdd(o, bad, strings.NewReader("b")); !IsRefusal(err) {
			t.Errorf("%+v: want a refusal, got %v", bad, err)
		}
	}
}

func TestMemoryAliasWorksInAnUnmigratedProject(t *testing.T) {
	dir, home := docsFixture(t), t.TempDir()
	o := opts(dir, home)
	if _, err := MemoryAdd(o, MemoryEntry{Type: "user", Name: "a", Description: "d"}, strings.NewReader("b")); err != nil {
		t.Fatalf("memory is never exported, so an unmigrated trail does not block it: %v", err)
	}
	if _, err := os.Lstat(trailFile(dir)); !os.IsNotExist(err) {
		t.Error("a memory write migrated the project by creating trail.jsonl")
	}
}

func TestMemoryShowCapsLines(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	for i := 0; i < 205; i++ {
		name := fmt.Sprintf("m-%03d", i)
		if _, err := MemoryAdd(o, MemoryEntry{Type: "user", Name: name, Description: "d"}, strings.NewReader("b")); err != nil {
			t.Fatal(err)
		}
	}
	show, err := MemoryShow(o)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(show, "\n- "); n > 200 || !strings.Contains(show, "more") {
		t.Errorf("show prints at most 200 memories and says how many more: %d lines", n)
	}
}

func TestImportGlobal(t *testing.T) {
	home := t.TempDir()
	conf := filepath.Join(home, harness.StateDir)
	os.MkdirAll(filepath.Join(conf, "proposals", "dream", "harness"), 0o700)
	os.WriteFile(filepath.Join(conf, "feedback.jsonl"), []byte(
		`{"ts":"2026-10-25T13:40:00+02:00","project":"/p/one","session":"abc","category":"style","signal":"correction","original":"o","corrected":"use tables not lists","user_quote":"tables!","rule_ref":null}`+"\n"+
			"not json\n"), 0o600)
	os.WriteFile(filepath.Join(conf, "retros.jsonl"), []byte(
		`{"ts":"2026-10-20T10:00:00Z","project":"/p/one","session":"abc","kind":"retro","record":"/p/one/.agents/docs/retros/x.md","proposals":[]}`+"\n"), 0o600)
	os.WriteFile(filepath.Join(conf, "proposals", "2026-10-21-retro.md"), []byte("# Proposal — add a rule\n\ntext\n"), 0o600)
	os.WriteFile(filepath.Join(conf, "proposals", "dream", "2026-10-22-one.md"), []byte("---\nowner: fyr dream\ndate: 2026-10-22\nproject: /p/one\n---\n# Dream proposals — one\n\nrule\n"), 0o600)
	o := Options{Home: home, Dir: t.TempDir(), Global: true, Now: func() time.Time { return fixed }}
	res, err := ImportGlobal(o, false)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, r := range res.Records {
		kinds[r.Kind]++
	}
	if kinds["feedback"] != 1 || kinds["retro"] != 1 || kinds["proposal"] != 2 {
		t.Errorf("kinds %v", kinds)
	}
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "feedback.jsonl line 2") {
		t.Errorf("a bad line is reported by number, not fatal: %v", res.Problems)
	}
	recs, err := List(o, ListFilter{Kind: "proposal", Project: "/p/one"})
	if err != nil || len(recs) != 1 || recs[0].Status != "queued" {
		t.Errorf("a dream proposal keeps its project: %+v %v", recs, err)
	}
	fb, err := List(o, ListFilter{Kind: "feedback"})
	if err != nil || len(fb) != 1 || fb[0].Project != "/p/one" || !strings.Contains(fb[0].Title, "use tables not lists") {
		t.Errorf("feedback: %+v %v", fb, err)
	}
	again, err := ImportGlobal(o, false)
	if err != nil || len(again.Records) != 0 || again.Skipped != 4 {
		t.Errorf("a re-run skips what is stored: %+v %v", again, err)
	}
}

// A file outside the trail's directories is left as a file: reported, and
// never on the list of paths to remove (a plain docs/ can hold a project's
// own documentation beside an old trail).
func TestImportLeavesUnknownFilesOffTheRemoveList(t *testing.T) {
	dir, home := docsFixture(t), t.TempDir()
	p := filepath.Join(dir, ".agents", "docs", "website", "index.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("# Our site\n"), 0o644)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "site")
	res, err := Import(opts(dir, home), "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Remove {
		if strings.Contains(r, "website/") {
			t.Errorf("%s is not a trail file but is listed for git rm", r)
		}
	}
	if len(res.Remove) != 19 || len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "website/index.md") {
		t.Errorf("remove %d (want 19), problems %v", len(res.Remove), res.Problems)
	}
}

// D28: a retired memory leaves `memory show`, and adding its name again
// brings it back.
func TestMemoryRetire(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	for _, n := range []string{"keep", "stale"} {
		if _, err := MemoryAdd(o, MemoryEntry{Type: "user", Name: n, Description: n + " fact"}, strings.NewReader("b")); err != nil {
			t.Fatal(err)
		}
	}
	retired := "retired"
	if err := Edit(o, EditInput{ID: "MEMORY-stale", Status: &retired}); err != nil {
		t.Fatalf("retire: %v", err)
	}
	show, _ := MemoryShow(o)
	if strings.Contains(show, "stale") || !strings.Contains(show, "keep") {
		t.Errorf("show after retiring: %q", show)
	}
	if _, err := MemoryAdd(o, MemoryEntry{Type: "user", Name: "stale", Description: "back again"}, strings.NewReader("b")); err != nil {
		t.Fatal(err)
	}
	if show, _ := MemoryShow(o); !strings.Contains(show, "back again") {
		t.Errorf("a re-added memory is shown again: %q", show)
	}
	bogus := "open"
	if err := Edit(o, EditInput{ID: "MEMORY-keep", Status: &bogus}); !IsUsage(err) {
		t.Errorf("memory takes closed or retired only: %v", err)
	}
}
