package dream

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
)

func TestRatingProposalsDistinctSessionsAndTargetSanitization(t *testing.T) {
	home := t.TempDir()
	o := Options{Home: home}

	if got := cleanTargetSkill("AGENTS.md"); got != "" {
		t.Errorf("cleanTargetSkill(AGENTS.md) = %q, want empty", got)
	}
	if got := cleanTargetSkill("cmd/fyr/main.go"); got != "" {
		t.Errorf("cleanTargetSkill(cmd/fyr/main.go) = %q, want empty", got)
	}
	if got := cleanTargetSkill("skills"); got != "skills/" {
		t.Errorf("cleanTargetSkill(skills) = %q, want skills/", got)
	}
	if got := cleanTargetSkill("skills/golang/SKILL.md"); got != "skills/golang/SKILL.md" {
		t.Errorf("cleanTargetSkill(skills/golang/SKILL.md) = %q", got)
	}

	// Non-skills/ TargetSkill sanitizes to skills/ via validateRating and ratingProposal.
	col := Collected{
		Root: "/proj",
		Inputs: []Input{
			{Kind: kindTranscript, Label: "antigravity s1"},
			{Kind: kindTranscript, Label: "antigravity s2"},
			{Kind: kindTranscript, Label: "antigravity s3"},
			{Kind: kindTranscript, Label: "antigravity s4"},
			{Kind: kindTranscript, Label: "antigravity s5"},
		},
	}
	ratings := o.validRatings(col, []sessionRating{
		{ConversationID: "s1", OutcomeScore: 2, TokenEfficiency: 3, PlanAlignment: "partial", SkillAction: "rewrite-skill", TargetSkill: "skills/golang/SKILL.md", Summary: "first golang gap"},
		{ConversationID: "s2", OutcomeScore: 2, TokenEfficiency: 2, PlanAlignment: "missed", SkillAction: "rewrite-skill", TargetSkill: "skills/golang/SKILL.md", Summary: "second golang gap"},
		{ConversationID: "s3", OutcomeScore: 2, TokenEfficiency: 3, PlanAlignment: "missed", SkillAction: "rewrite-skill", TargetSkill: "AGENTS.md", Summary: "bad target must become skills/"},
		{ConversationID: "s4", OutcomeScore: 5, TokenEfficiency: 5, PlanAlignment: "matched", SkillAction: "none", TargetSkill: "", Summary: "healthy session"},
		{ConversationID: "s5", OutcomeScore: 1, TokenEfficiency: 1, PlanAlignment: "missed", SkillAction: "none", TargetSkill: "", Summary: "low score with none action"},
	}, now)
	if len(ratings) != 5 {
		t.Fatalf("validRatings len = %d, want 5", len(ratings))
	}
	if ratings[2].TargetSkill != "" {
		t.Errorf("AGENTS.md TargetSkill not cleared: %q", ratings[2].TargetSkill)
	}

	props := o.buildProposals("HEAD\n\n", nil, ratings)
	if len(props) != 3 {
		t.Fatalf("buildProposals len = %d, want 3: %+v", len(props), props)
	}
	// s1 and s2 share skills/golang/SKILL.md -> Sessions: 2.
	for _, i := range []int{0, 1} {
		if !strings.Contains(props[i].Body, "File: skills/golang/SKILL.md\nSessions: 2\n") {
			t.Errorf("proposal %d missing Sessions: 2 for skills/golang/SKILL.md:\n%s", i, props[i].Body)
		}
	}
	// s3 sanitized AGENTS.md -> skills/, and only 1 session (s4/s5 had skill_action=none) -> Sessions: 1.
	if !strings.Contains(props[2].Body, "File: skills/\nSessions: 1\n") {
		t.Errorf("proposal 2 missing File: skills/ and Sessions: 1:\n%s", props[2].Body)
	}
}

func TestProposalDeduplicationAndDraftAtomicity(t *testing.T) {
	home := t.TempDir()
	projA := t.TempDir()
	projB := t.TempDir()
	o := Options{Home: home, Now: func() time.Time { return now }}
	conf := filepath.Join(home, harness.StateDir)
	colA := Collected{
		Root: projA, Name: "proj-a",
		Inputs: []Input{
			{Kind: kindTranscript, Label: "antigravity s1"},
			{Kind: kindTranscript, Label: "antigravity s2"},
		},
	}
	respA := response{
		Proposals: []proposal{{Title: "Vet first", Kind: "rule", Target: "project", File: "AGENTS.md", Text: "Run go vet", Evidence: "s1"}},
		Skills:    []draft{{Name: "new-check", Description: "d", Body: "body"}},
		Ratings: []sessionRating{
			{ConversationID: "s1", OutcomeScore: 2, TokenEfficiency: 2, PlanAlignment: "partial", SkillAction: "rewrite-skill", TargetSkill: "skills/golang/SKILL.md", Summary: "gap 1"},
			{ConversationID: "s2", OutcomeScore: 2, TokenEfficiency: 2, PlanAlignment: "missed", SkillAction: "rewrite-skill", TargetSkill: "skills/golang/SKILL.md", Summary: "gap 2"},
		},
	}

	// Failing propose() leaves zero draft directories and zero ratings.
	saved := propose
	propose = func(string, string, []brain.Proposal, time.Time) ([]string, error) {
		return nil, errors.New("propose failed")
	}
	ratingsCalled := false
	o.WriteRatings = func(string, string, []metrics.SessionRating) error { ratingsCalled = true; return nil }
	if _, _, err := o.writeCounts(conf, colA, respA, "agy", now); err == nil {
		t.Fatal("expected propose error")
	}
	propose = saved
	if ratingsCalled {
		t.Error("WriteRatings ran despite propose failure")
	}
	if entries, _ := os.ReadDir(filepath.Join(conf, "proposals", "dream", "drafts")); len(entries) != 0 {
		t.Errorf("drafts written despite propose failure: %v", entries)
	}

	// Failing WriteRatings leaves zero draft directories on disk.
	o.WriteRatings = func(string, string, []metrics.SessionRating) error { return errors.New("ratings err") }
	if _, _, err := o.writeCounts(conf, colA, respA, "agy", now); err == nil {
		t.Fatal("expected WriteRatings error")
	}
	if entries, _ := os.ReadDir(filepath.Join(conf, "proposals", "dream", "drafts")); len(entries) != 0 {
		t.Errorf("drafts written despite WriteRatings failure: %v", entries)
	}

	// Recovery: batch collapses s1/s2 -> 1 skill proposal; rule + draft wrapper = 3 queued;
	// draft directory has no -2 suffix.
	o.WriteRatings = nil
	if _, _, err := o.writeCounts(conf, colA, respA, "agy", now); err != nil {
		t.Fatalf("recovery write failed: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(conf, "proposals", "dream", "drafts"))
	if err != nil || len(entries) != 1 || strings.HasSuffix(entries[0].Name(), "-2") {
		t.Fatalf("draft dirs = %v, err=%v; want single non-suffixed draft", entries, err)
	}
	if recs := dreamProposals(t, home); len(recs) != 3 {
		t.Fatalf("queued recs = %d, want 3: %+v", len(recs), recs)
	}

	// Cross-project run targeting same queued skill (skills/golang/SKILL.md) deduplicates;
	// marking proposals applied also blocks re-proposing the same target/title.
	colB := Collected{Root: projB, Name: "proj-b", Inputs: []Input{{Kind: kindTranscript, Label: "antigravity s3"}}}
	respB := response{
		Proposals: []proposal{{Title: "Vet first", Kind: "rule", Target: "project", File: "AGENTS.md", Text: "Run go vet", Evidence: "s3"}},
		Ratings:   []sessionRating{{ConversationID: "s3", OutcomeScore: 2, TokenEfficiency: 2, PlanAlignment: "missed", SkillAction: "rewrite-skill", TargetSkill: "skills/golang/SKILL.md", Summary: "gap 3"}},
	}
	n, _, err := o.writeCounts(conf, colB, respB, "agy", now)
	if err != nil || n != 1 {
		t.Fatalf("projB write n=%d err=%v, want n=1", n, err)
	}
	applied := "applied:abc1234"
	for _, rec := range dreamProposals(t, home) {
		if err := brain.Edit(brain.Options{Home: home, Global: true}, brain.EditInput{ID: rec.ID, Status: &applied}); err != nil {
			t.Fatal(err)
		}
	}
	colB.Inputs = []Input{{Kind: kindTranscript, Label: "antigravity s4"}}
	respB.Ratings[0].ConversationID = "s4"
	if n, _, err := o.writeCounts(conf, colB, respB, "agy", now); err != nil || n != 0 {
		t.Fatalf("against applied proposals n=%d err=%v, want n=0", n, err)
	}
}

func TestRecordSuccessAdvancesPastBoundaryAndContextTime(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	run(t, root, "git", "init", "-q")
	sinceT := now.Add(-2 * time.Hour)
	cutoffT := now.Add(-time.Hour)
	if _, err := brain.Add(brain.Options{Home: home, Dir: root, Now: func() time.Time { return sinceT.Add(-time.Hour) }}, brain.AddInput{
		Kind: "plan", ID: "PLAN-2026-09-30-big", Title: "Big plan", Body: strings.NewReader(strings.Repeat("p", 3000)),
	}); err != nil {
		t.Fatal(err)
	}
	c := Collected{
		Root: root, Name: "proj", Changed: true,
		Inputs: []Input{
			{Kind: kindMemory, Label: "memory/m.md", Text: "m"},
			{Kind: kindTranscript, Label: "antigravity t1", Text: strings.Repeat("x", 1500), Time: sinceT.Add(time.Minute)},
		},
	}
	addOptionalPlanAndHandoff(home, root, sinceT, cutoffT, &c, c.adder(sinceT))
	if err := c.fit(len(prompt(Collected{Root: root, Name: "proj", Inputs: c.Inputs[:2]})) + 300); err != nil {
		t.Fatal(err)
	}
	if !c.Through.IsZero() {
		t.Errorf("untimed context plan held Through=%v, want zero", c.Through)
	}
	if len(c.Inputs) != 2 || c.Inputs[1].Label != "antigravity t1" {
		t.Errorf("dropped dated news instead of untimed plan context: %s", labels(c))
	}

	// recordSuccess advances strictly past since even if Through stopped at since.
	r := &dreamRun{
		o:  Options{Home: home, ConversationIdle: time.Hour},
		st: state{Projects: map[string]time.Time{root: sinceT}, Attempts: map[string]time.Time{root: now}, FailedAttempts: map[string]time.Time{root: now}},
		t0: now,
	}
	r.recordSuccess(root, Collected{Through: sinceT}, sinceT, cutoffT)
	if !r.st.Projects[root].After(sinceT) {
		t.Errorf("watermark did not advance past since: got %v, since %v", r.st.Projects[root], sinceT)
	}
}
