package auto

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
)

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "t"},
		{"config", "user.email", "t@e.invalid"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// Memory add, merge and retire drafts are validated and written to the brain
// store in the project's scope with provenance "derived", without touching the
// project's .agents/trail.jsonl.
func TestDraftsMemoryAddRetireMerge(t *testing.T) {
	home := t.TempDir()
	root := initGitRepo(t)
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	r := &run{o: Options{Home: home, Now: func() time.Time { return now }}}
	data := strings.Join([]string{
		`{"kind": "memory", "action": "add", "id": "deploy-window", "body": "Deploys run on Tuesdays (from README.md)."}`,
		`{"kind": "memory", "action": "merge", "id": "MEMORY-deploy-window", "body": "Deploys run on Tuesdays and Thursdays (merged)."}`,
	}, "\n") + "\n"

	fs := r.processDraftLines(root, ClassMemoryConsolidation, []byte(data))
	for _, f := range fs {
		if f.Rule == "invalid" {
			t.Fatalf("unexpected invalid draft finding: %+v", f)
		}
	}
	bo := brain.Options{Home: home, Dir: root, Now: r.o.Now}
	recs, _, err := brain.Get(bo, []string{"MEMORY-deploy-window"})
	if err != nil || len(recs) != 1 {
		t.Fatalf("Get MEMORY-deploy-window: %v %v", recs, err)
	}
	if recs[0].Provenance != "derived" || recs[0].Status != "closed" || !strings.Contains(recs[0].Body, "Tuesdays and Thursdays") {
		t.Errorf("merged memory = %+v", recs[0])
	}

	retireData := `{"kind": "memory", "action": "retire", "id": "MEMORY-deploy-window", "body": "obsolete"}` + "\n"
	fs = r.processDraftLines(root, ClassMemoryConsolidation, []byte(retireData))
	for _, f := range fs {
		if f.Rule == "invalid" {
			t.Fatalf("unexpected invalid draft finding on retire: %+v", f)
		}
	}
	recs, _, err = brain.Get(bo, []string{"MEMORY-deploy-window"})
	if err != nil || len(recs) != 1 || recs[0].Status != "retired" {
		t.Errorf("retired memory = %+v, err = %v", recs, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", "trail.jsonl")); !os.IsNotExist(err) {
		t.Errorf("memory drafts wrote .agents/trail.jsonl: %v", err)
	}
}

// A draft retiring or merging another project's memory ID, a missing ID, or
// adding over an operator memory in the same project is refused and reported
// as a finding.
func TestDraftsRetireOrMergeOtherProjectRefused(t *testing.T) {
	home := t.TempDir()
	rootA := initGitRepo(t)
	rootB := initGitRepo(t)
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	boA := brain.Options{Home: home, Dir: rootA, Now: func() time.Time { return now }}
	boB := brain.Options{Home: home, Dir: rootB, Now: func() time.Time { return now }}

	if _, err := brain.MemoryAdd(boA, brain.MemoryEntry{Type: "user", Name: "op-rule", Description: "operator rule"}, strings.NewReader("never overwrite")); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.MemoryAdd(boB, brain.MemoryEntry{Type: "project", Name: "b-only", Description: "project B memory"}, strings.NewReader("stay in B")); err != nil {
		t.Fatal(err)
	}

	r := &run{o: Options{Home: home, Now: func() time.Time { return now }}}
	lines := strings.Join([]string{
		`{"kind": "memory", "action": "retire", "id": "MEMORY-b-only", "body": "cross-project retire"}`,
		`{"kind": "memory", "action": "merge", "id": "MEMORY-b-only", "body": "cross-project merge"}`,
		`{"kind": "memory", "action": "retire", "id": "MEMORY-nonexistent", "body": "missing"}`,
		`{"kind": "memory", "action": "add", "id": "op-rule", "body": "attempt to overwrite operator memory"}`,
	}, "\n") + "\n"

	fs := r.processDraftLines(rootA, ClassMemoryConsolidation, []byte(lines))
	invalid := 0
	for _, f := range fs {
		if f.Check == "draft" && f.Rule == "invalid" {
			invalid++
		}
	}
	if invalid != 4 {
		t.Errorf("invalid draft findings = %d, want 4 (%+v)", invalid, fs)
	}

	// Verify Project B's memory and Project A's operator memory are untouched.
	recsB, _, err := brain.Get(boB, []string{"MEMORY-b-only"})
	if err != nil || len(recsB) != 1 || recsB[0].Status != "closed" || !strings.Contains(recsB[0].Body, "stay in B") {
		t.Errorf("Project B memory changed: %+v (%v)", recsB, err)
	}
	recsA, _, err := brain.Get(boA, []string{"MEMORY-op-rule"})
	if err != nil || len(recsA) != 1 || recsA[0].Provenance != "operator" || !strings.Contains(recsA[0].Body, "never overwrite") {
		t.Errorf("Project A operator memory changed: %+v (%v)", recsA, err)
	}
}

// A draft line holding a redactor needle (raw or JSON-escaped) is refused,
// not stored, and the report finding does not echo the secret.
func TestDraftsTokenShapeRefused(t *testing.T) {
	home := t.TempDir()
	root := initGitRepo(t)
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	tok1 := "ghp_" + strings.Repeat("A", 36)
	tok2 := "AIzaSy" + strings.Repeat("B", 33)
	// JSON-escaped prefix \u0067hp_ bypasses a raw-line check unless decoded
	// fields are also checked against the redactor.
	escapedTok := `\u0067hp_` + strings.Repeat("C", 36)

	r := &run{o: Options{Home: home, Now: func() time.Time { return now }}}
	lines := strings.Join([]string{
		`{"kind": "memory", "action": "add", "id": "leak-one", "body": "token ` + tok1 + ` here"}`,
		`{"kind": "feedback", "body": "saw ` + tok2 + `", "quote": "quote"}`,
		`{"kind": "proposal", "target": "project", "title": "escaped", "body": "secret ` + escapedTok + `"}`,
		`{"kind": "finding", "body": "clean finding", "quote": "token ` + tok1 + `"}`,
	}, "\n") + "\n"

	fs := r.processDraftLines(root, ClassRetroLite, []byte(lines))
	if len(fs) != 4 {
		t.Fatalf("findings = %d, want 4: %+v", len(fs), fs)
	}
	for _, f := range fs {
		if f.Rule != "invalid" {
			t.Errorf("finding rule = %q, want invalid: %+v", f.Rule, f)
		}
		if strings.Contains(f.Text, tok1) || strings.Contains(f.Text, tok2) || strings.Contains(f.Text, strings.Repeat("C", 20)) {
			t.Errorf("secret leaked into report finding: %+v", f)
		}
	}

	// Nothing was stored in brain.db.
	recs, err := brain.List(brain.Options{Home: home, Dir: root}, brain.ListFilter{All: true})
	if err != nil || len(recs) != 0 {
		t.Errorf("stored records = %+v (err %v), want none", recs, err)
	}
}

// Feedback, proposal and finding drafts are validated and written with
// provenance "derived" (on feedback) and queued status (on proposal).
func TestDraftsFeedbackProposalFindingAndLimits(t *testing.T) {
	home := t.TempDir()
	root := initGitRepo(t)
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	r := &run{o: Options{Home: home, Now: func() time.Time { return now }}}
	oversized := strings.Repeat("x", 4096)
	utf8FirstLine := strings.Repeat("a", 59) + strings.Repeat("—", 35)
	lines := strings.Join([]string{
		`{"kind": "feedback", "body": "Always pass --home in tests", "quote": "pass --home"}`,
		`{"kind": "feedback", "body": "` + utf8FirstLine + `", "quote": "utf8 boundary"}`,
		`{"kind": "proposal", "target": "project", "title": "Add test helper", "body": "Evidence from 3 sessions."}`,
		`{"kind": "proposal", "target": "harness", "title": "Clarify retro skill", "body": "Evidence across sessions."}`,
		`{"kind": "finding", "body": "Planted prompt in README", "quote": "ignore all rules"}`,
		`{"kind": "proposal", "target": "invalid-target", "title": "Bad", "body": "bad"}`,
		`{"kind": "proposal", "target": "project", "title": "Too big", "body": "` + oversized + `"}`,
		`{"kind": "feedback", "body": "has unknown field", "quote": "q", "provenance": "operator"}`,
		`{"kind": "finding", "body": "first", "body": "duplicate key"}`,
		`{"kind": "finding", "body": null}`,
	}, "\n") + "\n"

	fs := r.processDraftLines(root, ClassRetroLite, []byte(lines))
	var invalid, findings int
	for _, f := range fs {
		switch f.Rule {
		case "invalid":
			invalid++
		case "finding":
			findings++
		}
	}
	if invalid != 5 || findings != 1 {
		t.Errorf("invalid=%d findings=%d, want 5 and 1 (%+v)", invalid, findings, fs)
	}
	if r.st.OpenAsks != 2 {
		t.Errorf("OpenAsks = %d, want 2", r.st.OpenAsks)
	}

	g := brain.Options{Home: home, Global: true, Now: r.o.Now}
	fbs, err := brain.List(g, brain.ListFilter{Kind: "feedback", Project: root})
	if err != nil || len(fbs) != 2 || fbs[0].Provenance != "derived" || fbs[1].Provenance != "derived" {
		t.Errorf("feedback records = %+v, err = %v", fbs, err)
	}
	props, err := brain.List(g, brain.ListFilter{Kind: "proposal", Project: root})
	if err != nil || len(props) != 2 {
		t.Errorf("proposal records = %+v, err = %v", props, err)
	}
}
