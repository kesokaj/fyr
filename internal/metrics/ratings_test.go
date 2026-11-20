package metrics

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

func TestSessionRatingsMigrationUpsertQueryAndReports(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "metrics.db")

	// 1. Create a schema-7 store with an existing session row, then migrate to schema 8.
	saved := migrations
	if len(saved) < 8 {
		t.Fatalf("len(migrations) = %d, want >= 8", len(saved))
	}
	migrations = saved[:7]
	s7, err := Open(dbPath)
	if err != nil {
		migrations = saved
		t.Fatalf("open schema 7: %v", err)
	}
	if _, err := s7.db.Exec(`INSERT INTO sessions(id, vendor) VALUES ('keep-s1', 'agy')`); err != nil {
		s7.Close()
		migrations = saved
		t.Fatalf("insert schema-7 session: %v", err)
	}
	s7.Close()
	migrations = saved

	s := openT(t, dbPath)
	if got := s.meta(t, "schema"); got != "11" {
		t.Fatalf("schema = %q, want 11", got)
	}
	var kept int
	if err := s.db.QueryRow(`SELECT count(*) FROM sessions WHERE id = 'keep-s1'`).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("lost existing session across migration 8: kept=%d err=%v", kept, err)
	}

	// 2. Reject invalid ratings before writing.
	badRows := []SessionRating{
		{SessionID: "", OutcomeScore: 3, TokenEfficiency: 3, PlanAlignment: "matched", SkillAction: "none"},
		{SessionID: "x", OutcomeScore: 0, TokenEfficiency: 3, PlanAlignment: "matched", SkillAction: "none"},
		{SessionID: "x", OutcomeScore: 4, TokenEfficiency: 6, PlanAlignment: "matched", SkillAction: "none"},
		{SessionID: "x", OutcomeScore: 4, TokenEfficiency: 4, PlanAlignment: "bogus", SkillAction: "none"},
		{SessionID: "x", OutcomeScore: 4, TokenEfficiency: 4, PlanAlignment: "matched", SkillAction: "delete-all"},
		{SessionID: "x", EvaluatedAt: -1, OutcomeScore: 4, TokenEfficiency: 4, PlanAlignment: "matched", SkillAction: "none"},
	}
	for _, bad := range badRows {
		if err := s.UpsertSessionRatings([]SessionRating{bad}); err == nil {
			t.Errorf("UpsertSessionRatings(%+v) succeeded, want validation error", bad)
		}
	}

	// 3. Upsert + idempotent update + Export round-trip.
	t1 := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	tOld := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	rows := []SessionRating{
		{
			SessionID:       "conv-1",
			ProjectRoot:     "/repo/alpha",
			EvaluatedAt:     t1.UnixMilli(),
			OutcomeScore:    2,
			TokenEfficiency: 3,
			PlanAlignment:   "partial",
			SkillAction:     "rewrite-skill",
			TargetSkill:     "skills/golang/SKILL.md",
			Summary:         "initial rating",
		},
		{
			SessionID:       "conv-2",
			ProjectRoot:     "/repo/alpha/sub",
			EvaluatedAt:     t2.Unix(), // seconds normalized transparently
			OutcomeScore:    5,
			TokenEfficiency: 4,
			PlanAlignment:   "matched",
			SkillAction:     "none",
			Summary:         "subpath rating",
		},
		{
			SessionID:       "conv-global",
			ProjectRoot:     search.GlobalWorkspace,
			EvaluatedAt:     t2.UnixMilli(),
			OutcomeScore:    3,
			TokenEfficiency: 5,
			PlanAlignment:   "no-plan",
			SkillAction:     "new-skill",
			TargetSkill:     "skills/new-helper/SKILL.md",
			Summary:         "global rating",
		},
		{
			SessionID:       "conv-old",
			ProjectRoot:     "/repo/alpha",
			EvaluatedAt:     tOld.UnixMilli(),
			OutcomeScore:    1,
			TokenEfficiency: 1,
			PlanAlignment:   "missed",
			SkillAction:     "rewrite-skill",
			Summary:         "outside window",
		},
	}
	if err := s.UpsertSessionRatings(rows); err != nil {
		t.Fatalf("UpsertSessionRatings initial: %v", err)
	}
	// Upsert conv-1 again with updated score to verify conflict update + idempotency.
	rows[0].OutcomeScore = 3
	rows[0].Summary = "updated rating"
	if err := s.UpsertSessionRatings([]SessionRating{rows[0]}); err != nil {
		t.Fatalf("UpsertSessionRatings update: %v", err)
	}

	all, err := s.SessionRatings("", time.Time{}, time.Time{})
	if err != nil || len(all) != 4 {
		t.Fatalf("all ratings = %d, err=%v; want 4", len(all), err)
	}
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)

	alphaRatings, err := s.SessionRatings("/repo/alpha", since, now)
	if err != nil || len(alphaRatings) != 2 {
		t.Fatalf("alpha window ratings = %+v, err=%v; want 2 (conv-2, conv-1)", alphaRatings, err)
	}
	if alphaRatings[1].OutcomeScore != 3 || alphaRatings[1].Summary != "updated rating" {
		t.Errorf("conv-1 was not updated idempotently: %+v", alphaRatings[1])
	}

	globalRatings, err := s.SessionRatings(search.GlobalWorkspace, since, now)
	if err != nil || len(globalRatings) != 1 || globalRatings[0].SessionID != "conv-global" {
		t.Fatalf("global window ratings = %+v, err=%v; want [conv-global]", globalRatings, err)
	}

	// Verify Export includes session_ratings.
	expPath := filepath.Join(dir, "export.jsonl")
	if err := s.Export(expPath); err != nil {
		t.Fatalf("Export: %v", err)
	}
	expBytes, err := os.ReadFile(expPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(expBytes), `"table":"session_ratings"`) {
		t.Errorf("Export missing session_ratings table:\n%s", string(expBytes))
	}

	// 4. Show() populates Summary.A rating aggregates and respects project/label filters.
	sumAll, err := s.Show(ShowOptions{Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show all: %v", err)
	}
	// conv-1 (3, 3), conv-2 (5, 4), conv-global (3, 5) -> count=3, outcome=11/3, efficiency=12/3=4
	if sumAll.A.RatingsCount != 3 {
		t.Fatalf("RatingsCount = %d, want 3", sumAll.A.RatingsCount)
	}
	if sumAll.A.MeanOutcomeScore == nil || *sumAll.A.MeanOutcomeScore != round4(11.0/3.0) {
		t.Errorf("MeanOutcomeScore = %v, want %.4f", sumAll.A.MeanOutcomeScore, round4(11.0/3.0))
	}
	if sumAll.A.MeanTokenEfficiency == nil || *sumAll.A.MeanTokenEfficiency != 4.0 {
		t.Errorf("MeanTokenEfficiency = %v, want 4.0", sumAll.A.MeanTokenEfficiency)
	}

	sumAlpha, err := s.Show(ShowOptions{Since: since, Now: now, Project: "/repo/alpha"})
	if err != nil {
		t.Fatalf("Show alpha: %v", err)
	}
	// conv-1 (3, 3), conv-2 (5, 4) -> count=2, outcome=4.0, efficiency=3.5
	if sumAlpha.A.RatingsCount != 2 || sumAlpha.A.MeanOutcomeScore == nil || *sumAlpha.A.MeanOutcomeScore != 4.0 || sumAlpha.A.MeanTokenEfficiency == nil || *sumAlpha.A.MeanTokenEfficiency != 3.5 {
		t.Errorf("Show alpha ratings = count %d out %v eff %v", sumAlpha.A.RatingsCount, sumAlpha.A.MeanOutcomeScore, sumAlpha.A.MeanTokenEfficiency)
	}

	sumLabel, err := s.Show(ShowOptions{Since: since, Now: now, Label: "eval=smoke"})
	if err != nil {
		t.Fatalf("Show label: %v", err)
	}
	if sumLabel.A.RatingsCount != 0 || sumLabel.A.MeanOutcomeScore != nil || sumLabel.A.MeanTokenEfficiency != nil {
		t.Errorf("labelled Show leaked unlabelled ratings: %+v", sumLabel.A)
	}

	// 5. RenderMarkdownReport and RenderReport render rated sessions and means.
	var mdBuf, htmlBuf bytes.Buffer
	genAt := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	if err := RenderMarkdownReport(&mdBuf, sumAlpha, nil, genAt); err != nil {
		t.Fatalf("RenderMarkdownReport: %v", err)
	}
	if err := RenderReport(&htmlBuf, sumAlpha, nil, genAt); err != nil {
		t.Fatalf("RenderReport: %v", err)
	}
	for _, want := range []string{"| Rated sessions | 2 |", "| Mean outcome / token efficiency | 4.00 / 3.50 |"} {
		if !strings.Contains(mdBuf.String(), want) {
			t.Errorf("markdown report missing %q:\n%s", want, mdBuf.String())
		}
	}
	for _, want := range []string{"<td>rated sessions</td><td>2</td>", "<td>mean outcome / token efficiency</td><td>4.00 / 3.50</td>", "<td>Rated sessions</td><td>2</td>", "<td>Mean outcome / token efficiency</td><td>4.00 / 3.50</td>"} {
		if !strings.Contains(htmlBuf.String(), want) {
			t.Errorf("HTML report missing %q:\n%s", want, htmlBuf.String())
		}
	}
}

func TestUpsertSessionRatingsBlocksCrossProjectOverwriteAndBounds(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ts := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC).UnixMilli()

	// Reject oversized session_id, target_skill, or summary before writing.
	oversized := []SessionRating{
		{SessionID: strings.Repeat("s", 129), ProjectRoot: "/repo/alpha", EvaluatedAt: ts, OutcomeScore: 4, TokenEfficiency: 4, PlanAlignment: "matched", SkillAction: "none"},
		{SessionID: "conv-ok", ProjectRoot: "/repo/alpha", EvaluatedAt: ts, OutcomeScore: 4, TokenEfficiency: 4, PlanAlignment: "matched", SkillAction: "rewrite-skill", TargetSkill: strings.Repeat("x", 201)},
		{SessionID: "conv-ok", ProjectRoot: "/repo/alpha", EvaluatedAt: ts, OutcomeScore: 4, TokenEfficiency: 4, PlanAlignment: "matched", SkillAction: "none", Summary: strings.Repeat("y", 401)},
	}
	for _, bad := range oversized {
		if err := s.UpsertSessionRatings([]SessionRating{bad}); err == nil {
			t.Errorf("UpsertSessionRatings(%+v) succeeded, want length bound error", bad)
		}
	}

	orig := SessionRating{
		SessionID:       "conv-owned",
		ProjectRoot:     "/repo/alpha",
		EvaluatedAt:     ts,
		OutcomeScore:    5,
		TokenEfficiency: 5,
		PlanAlignment:   "matched",
		SkillAction:     "none",
		Summary:         "legit alpha rating",
	}
	if err := s.UpsertSessionRatings([]SessionRating{orig}); err != nil {
		t.Fatalf("seed orig: %v", err)
	}

	// Attempt cross-project overwrite from /repo/beta, empty project, and __global__.
	for _, attackerRoot := range []string{"/repo/beta", "", search.GlobalWorkspace} {
		clobber := SessionRating{
			SessionID:       "conv-owned",
			ProjectRoot:     attackerRoot,
			EvaluatedAt:     ts + 1000,
			OutcomeScore:    1,
			TokenEfficiency: 1,
			PlanAlignment:   "missed",
			SkillAction:     "rewrite-skill",
			TargetSkill:     "skills/golang/SKILL.md",
			Summary:         "clobbered by " + attackerRoot,
		}
		if err := s.UpsertSessionRatings([]SessionRating{clobber}); err != nil {
			t.Fatalf("UpsertSessionRatings(%q): %v", attackerRoot, err)
		}
	}

	rows, err := s.SessionRatings("", time.Time{}, time.Time{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("SessionRatings = %+v, err=%v; want 1", rows, err)
	}
	if rows[0].ProjectRoot != "/repo/alpha" || rows[0].OutcomeScore != 5 || rows[0].Summary != "legit alpha rating" {
		t.Fatalf("cross-project upsert clobbered owned row: %+v", rows[0])
	}

	// Same ProjectRoot can still update idempotently.
	orig.OutcomeScore = 4
	orig.Summary = "updated by alpha"
	if err := s.UpsertSessionRatings([]SessionRating{orig}); err != nil {
		t.Fatalf("same-project update: %v", err)
	}
	rows, err = s.SessionRatings("/repo/alpha", time.Time{}, time.Time{})
	if err != nil || len(rows) != 1 || rows[0].OutcomeScore != 4 || rows[0].Summary != "updated by alpha" {
		t.Fatalf("same-project update did not apply: %+v, err=%v", rows, err)
	}

	// An unowned row (project_root == "") can be claimed by a concrete ProjectRoot.
	unowned := SessionRating{SessionID: "conv-unowned", ProjectRoot: "", EvaluatedAt: ts, OutcomeScore: 2, TokenEfficiency: 2, PlanAlignment: "partial", SkillAction: "none"}
	if err := s.UpsertSessionRatings([]SessionRating{unowned}); err != nil {
		t.Fatalf("seed unowned: %v", err)
	}
	unowned.ProjectRoot = "/repo/beta"
	unowned.OutcomeScore = 5
	if err := s.UpsertSessionRatings([]SessionRating{unowned}); err != nil {
		t.Fatalf("claim unowned: %v", err)
	}
	betaRows, err := s.SessionRatings("/repo/beta", time.Time{}, time.Time{})
	if err != nil || len(betaRows) != 1 || betaRows[0].OutcomeScore != 5 {
		t.Fatalf("unowned row not claimed by /repo/beta: %+v, err=%v", betaRows, err)
	}
}
