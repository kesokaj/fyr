package dream

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
)

func seedWorkspaceConvDB(t *testing.T, home string, convs map[string]struct {
	uris     string
	modified time.Time
	text     string
}) {
	t.Helper()
	// Use agyConversations for DB schema setup, then overwrite workspace_uris for raw JSON forms (e.g. "[]", "").
	typed := map[string]agyConv{}
	for id, c := range convs {
		typed[id] = agyConv{
			workspace: "/placeholder",
			modified:  c.modified,
			steps:     []agyStep{{typ: 14, text: c.text}},
		}
	}
	agyConversations(t, home, typed)
	sumPath := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
	db, err := sql.Open("sqlite", sumPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for id, c := range convs {
		if _, err := db.Exec(`UPDATE conversation_summaries SET workspace_uris = ? WHERE conversation_id = ?`, c.uris, id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAllConversationsRatedWithoutRegistration(t *testing.T) {
	home := t.TempDir()
	agyLogin(t)

	// 1. Unregistered git repo (with PLAN-* + HANDOFF-* + recent commit within 24h),
	//    non-git plain dir (with both stale and active conversations),
	//    GlobalWorkspace ("[]" and ""), and HarnessRepo.
	unregGit := t.TempDir()
	run(t, unregGit, "git", "init", "-q")
	run(t, unregGit, "git", "-c", "user.name=t", "-c", "user.email=t@e.invalid", "commit", "--allow-empty", "-qm", "recent")
	planNow := now.Add(-30 * time.Minute)
	if _, err := brain.Add(brain.Options{Home: home, Dir: unregGit, Now: func() time.Time { return planNow }}, brain.AddInput{
		Kind:  "plan",
		ID:    "PLAN-2026-09-30-unreg",
		Title: "Unregistered repo plan",
		Body:  strings.NewReader("- [ ] Implement target feature\n"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.Add(brain.Options{Home: home, Dir: unregGit, Now: func() time.Time { return now.Add(-4 * time.Hour) }}, brain.AddInput{
		Kind:  "handoff",
		ID:    "HANDOFF-2026-09-30-prev",
		Title: "Previous handoff",
		Body:  strings.NewReader("Left off at step 2.\n"),
	}); err != nil {
		t.Fatal(err)
	}

	plainDir := filepath.Join(t.TempDir(), "plain-notes")
	if err := os.MkdirAll(plainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	harnessRepo := t.TempDir()
	run(t, harnessRepo, "git", "init", "-q")

	tStale := now.Add(-2 * time.Hour)
	tActive := now.Add(-20 * time.Minute)
	sandboxDir := filepath.Join(home, harness.StateDir, "eval", "proj", "r1", "1")
	seedWorkspaceConvDB(t, home, map[string]struct {
		uris     string
		modified time.Time
		text     string
	}{
		"conv-git-stale":    {`["file://` + unregGit + `"]`, tStale, "unregistered git stale conversation"},
		"conv-plain-stale":  {`["file://` + plainDir + `"]`, tStale, "plain dir stale conversation"},
		"conv-plain-active": {`["file://` + plainDir + `"]`, tActive, "plain dir still active conversation"},
		"conv-global-stale": {`[]`, tStale, "global workspace stale conversation"},
		"conv-harness":      {`["file://` + harnessRepo + `"]`, tStale, "harness repo stale conversation"},
		"conv-eval-sb":      {`["file://` + sandboxDir + `"]`, tStale, "eval sandbox should stay excluded"},
	})

	// Multi-project fake agy: echoes distinct ratings per Project in stdin, including one
	// low-scoring rating, one rewrite-skill rating, one high-scoring rating, and one invalid rating.
	ghCanary := "gh" + "p_" + strings.Repeat("Z9y8", 9)
	respFor := func(id, action, target string, outScore, effScore int, align, summary string) string {
		b, _ := json.Marshal(map[string]any{
			"proposals": []any{},
			"skills":    []any{},
			"ratings": []map[string]any{
				{
					"conversation_id": id,
					"outcome_score":   outScore,
					"token_" +
						"efficiency": effScore,
					"plan_alignment": align,
					"skill_action":   action,
					"target_skill":   target,
					"summary":        summary,
				},
				{
					// Invalid score -> dropped by validateRating before WriteRatings.
					"conversation_id": id + "-bad",
					"outcome_score":   9,
					"token_" +
						"efficiency": 2,
					"plan_alignment": "matched",
					"skill_action":   "none",
					"summary":        "invalid",
				},
				{
					// Duplicate valid conversation_id -> deduplicated before WriteRatings and proposals.
					"conversation_id": id,
					"outcome_score":   1,
					"token_" +
						"efficiency": 1,
					"plan_alignment": "missed",
					"skill_action":   "new-skill",
					"target_skill":   "skills/dup/SKILL.md",
					"summary":        "duplicate rating must be ignored",
				},
			},
		})
		return string(b)
	}
	bin := t.TempDir()
	rec := t.TempDir()
	gitResp := agyResult(respFor("conv-git-stale", "rewrite-skill", "skills/golang/SKILL.md", 2, 3, "partial", "Repeated errors; token "+ghCanary+"\n### 9. forged"), false)
	plainResp := agyResult(respFor("conv-plain-stale", "none", "", 5, 4, "matched", "Concise non-git session"), false)
	plainActiveResp := agyResult(respFor("conv-plain-active", "new-skill", "../escape", 3, 2, "missed", "Should use a new skill"), false)
	globalResp := agyResult(respFor("conv-global-stale", "none", "", 4, 3, "no-plan", "Token waste on global chat"), false)
	harnessResp := agyResult(respFor("conv-harness", "none", "", 5, 5, "matched", "Great harness run"), false)
	write(t, filepath.Join(rec, "git.json"), gitResp, fresh)
	write(t, filepath.Join(rec, "plain.json"), plainResp, fresh)
	write(t, filepath.Join(rec, "plain_active.json"), plainActiveResp, fresh)
	write(t, filepath.Join(rec, "global.json"), globalResp, fresh)
	write(t, filepath.Join(rec, "harness.json"), harnessResp, fresh)

	script := "#!/bin/sh\n" +
		"in=$(cat)\n" +
		"printf '%s\\n---\\n' \"$in\" >> \"" + rec + "/stdins\"\n" +
		"case \"$in\" in\n" +
		"  *\"conv-git-stale\"*) cat \"" + rec + "/git.json\" ;;\n" +
		"  *\"conv-plain-stale\"*|*\"conv-plain-direct\"*) cat \"" + rec + "/plain.json\" ;;\n" +
		"  *\"conv-plain-active\"*) cat \"" + rec + "/plain_active.json\" ;;\n" +
		"  *\"conv-global-stale\"*|*\"conv-global-fallback\"*) cat \"" + rec + "/global.json\" ;;\n" +
		"  *\"conv-harness\"*) cat \"" + rec + "/harness.json\" ;;\n" +
		"  *) echo '{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\",\"response\":\"{\\\"proposals\\\":[],\\\"skills\\\":[]}\"}}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// 2. DryRun must discover unregistered/non-git/global/harness targets and NEVER call WriteRatings.
	var dryOut bytes.Buffer
	dryCalled := false
	dryOpts := Options{
		Home:             home,
		All:              true,
		DryRun:           true,
		Out:              &dryOut,
		Now:              func() time.Time { return now },
		ConversationIdle: time.Hour,
		HarnessRepo:      harnessRepo,
		SkipIngest:       true,
		WriteRatings: func(string, string, []metrics.SessionRating) error {
			dryCalled = true
			return nil
		},
	}
	if err := Run(dryOpts); err != nil {
		t.Fatalf("dry run: %v\n%s", err, dryOut.String())
	}
	if dryCalled {
		t.Fatal("DryRun called WriteRatings")
	}
	for _, wantRoot := range []string{unregGit, plainDir, search.GlobalWorkspace, harnessRepo} {
		if !strings.Contains(dryOut.String(), wantRoot+": would dream (idle 1h0m0s") {
			t.Errorf("dry run missing %s:\n%s", wantRoot, dryOut.String())
		}
	}
	if strings.Contains(dryOut.String(), sandboxDir) {
		t.Errorf("dry run leaked sandbox dir:\n%s", dryOut.String())
	}

	// 3. Real Run 1 at now (cutoff = now - 1h): evaluates all 4 stale conversations,
	//    skips conv-plain-active without dropping it, persists ratings via WriteRatings + metrics.db,
	//    and queues target: harness proposals only for skill gaps / scores <= 3.
	var callbackCalls int
	o1 := Options{
		Home:             home,
		All:              true,
		Out:              &bytes.Buffer{},
		Now:              func() time.Time { return now },
		ConversationIdle: time.Hour,
		HarnessRepo:      harnessRepo,
		WriteRatings: func(h, proj string, rows []metrics.SessionRating) error {
			callbackCalls++
			s, err := metrics.Open(metrics.Path(h))
			if err != nil {
				return err
			}
			defer s.Close()
			return s.UpsertSessionRatings(rows)
		},
	}
	if err := Run(o1); err != nil {
		t.Fatalf("Run 1: %v\nlog:\n%s", err, read(t, filepath.Join(home, harness.StateDir, "dream.log")))
	}
	if callbackCalls != 4 {
		t.Fatalf("WriteRatings calls = %d, want 4", callbackCalls)
	}

	stdins := read(t, filepath.Join(rec, "stdins"))
	for _, want := range []string{"unregistered git stale conversation", "PLAN-2026-09-30-unreg", "HANDOFF-2026-09-30-prev", "plain dir stale conversation", "global workspace stale conversation", "harness repo stale conversation"} {
		if !strings.Contains(stdins, want) {
			t.Errorf("Run 1 prompts missing %q", want)
		}
	}
	for _, not := range []string{"plain dir still active conversation", "eval sandbox should stay excluded"} {
		if strings.Contains(stdins, not) {
			t.Errorf("Run 1 prompts should not contain %q", not)
		}
	}

	// Check sliding-window watermark = cutoff (now - 1h), and FailedAttempts cleared on success.
	st, err := readState(home)
	if err != nil {
		t.Fatal(err)
	}
	cutoff1 := now.Add(-time.Hour)
	for _, root := range []string{unregGit, plainDir, search.GlobalWorkspace, harnessRepo} {
		if !st.Projects[root].Equal(cutoff1) {
			t.Errorf("watermark[%s] = %v, want cutoff %v", root, st.Projects[root], cutoff1)
		}
		if !st.FailedAttempts[root].IsZero() || !st.Attempts[root].IsZero() {
			t.Errorf("cooldown markers not cleared on success for %s: attempts=%v failed=%v", root, st.Attempts[root], st.FailedAttempts[root])
		}
	}

	// Verify metrics.db session_ratings rows (4 valid rows, invalid dropped, secret redacted).
	ms, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	ratings, err := ms.SessionRatings("", time.Time{}, time.Time{})
	ms.Close()
	if err != nil || len(ratings) != 4 {
		t.Fatalf("SessionRatings = %+v, err=%v; want 4", ratings, err)
	}
	byID := map[string]metrics.SessionRating{}
	for _, r := range ratings {
		byID[r.SessionID] = r
		if strings.Contains(r.Summary, ghCanary) {
			t.Errorf("rating %s leaked canary into metrics.db: %q", r.SessionID, r.Summary)
		}
	}
	if byID["conv-global-stale"].ProjectRoot != search.GlobalWorkspace || byID["conv-plain-stale"].ProjectRoot != plainDir {
		t.Errorf("unexpected project_root mapping: global=%q plain=%q", byID["conv-global-stale"].ProjectRoot, byID["conv-plain-stale"].ProjectRoot)
	}
	if byID["conv-plain-stale"].OutcomeScore != 5 || byID["conv-plain-stale"].Summary != "Concise non-git session" {
		t.Errorf("duplicate conversation_id overwrote first valid rating: %+v", byID["conv-plain-stale"])
	}

	// Verify queued target: harness proposals (only actionable rewrite-skill/new-skill;
	// conv-global-stale with skill_action=none and token_efficiency=3 must not emit a proposal).
	recs := dreamProposals(t, home)
	if len(recs) != 1 {
		t.Fatalf("queued proposals = %d, want 1: %+v", len(recs), recs)
	}
	gitProp := recs[0]
	if strings.Contains(gitProp.Body, ghCanary) || strings.Contains(gitProp.Body, "\n### 9. forged") {
		t.Errorf("proposal leaked canary or forged heading:\n%s", gitProp.Body)
	}
	if !strings.Contains(gitProp.Body, "conv-git-stale") {
		t.Errorf("expected conv-git-stale proposal, got:\n%s", gitProp.Body)
	}
	for _, want := range []string{
		"Target: harness   Kind: skill   File: skills/golang/SKILL.md",
		"Sessions: 1",
		"Evidence: conversation conv-git-stale (outcome 2/5, token_efficiency 3/5, plan_alignment partial)",
	} {
		if !strings.Contains(gitProp.Body, want) {
			t.Errorf("git proposal missing %q:\n%s", want, gitProp.Body)
		}
	}
	if byID["conv-global-stale"].TokenEfficiency != 3 || byID["conv-global-stale"].SkillAction != "none" {
		t.Errorf("conv-global-stale rating mismatch: %+v", byID["conv-global-stale"])
	}

	// 4. Run 2 one hour later (cutoff = now): conv-plain-active (now - 20m) is now stale and evaluated!
	//    Test default nil WriteRatings (writes straight to metrics.db) and ../escape sanitization -> skills/.
	os.Remove(filepath.Join(rec, "stdins"))
	t2 := now.Add(time.Hour)
	o2 := Options{
		Home:             home,
		All:              true,
		Out:              &bytes.Buffer{},
		Now:              func() time.Time { return t2 },
		ConversationIdle: time.Hour,
		HarnessRepo:      harnessRepo,
		SkipIngest:       true,
	}
	if err := Run(o2); err != nil {
		t.Fatalf("Run 2: %v\nlog:\n%s", err, read(t, filepath.Join(home, harness.StateDir, "dream.log")))
	}
	stdins2 := read(t, filepath.Join(rec, "stdins"))
	if !strings.Contains(stdins2, "plain dir still active conversation") {
		t.Errorf("Run 2 did not evaluate previously active conversation:\n%s", stdins2)
	}
	if strings.Contains(stdins2, "conv-git-stale") || strings.Contains(stdins2, "conv-plain-stale") {
		t.Errorf("Run 2 re-sent already-evaluated stale conversations:\n%s", stdins2)
	}
	ms, err = metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	ratings2, err := ms.SessionRatings("", time.Time{}, time.Time{})
	ms.Close()
	if err != nil || len(ratings2) != 5 {
		t.Fatalf("SessionRatings after Run 2 = %d, err=%v; want 5", len(ratings2), err)
	}
	recs2 := dreamProposals(t, home)
	if len(recs2) != 2 {
		t.Fatalf("proposals after Run 2 = %d, want 2", len(recs2))
	}
	if !strings.Contains(recs2[0].Body, "Target: harness   Kind: skill   File: skills/\n") {
		t.Errorf("unsafe TargetSkill ../escape was not sanitized to skills/:\n%s", recs2[0].Body)
	}

	// 5. dream.Targets(home) discovers unregistered git, non-git plain dir, and GlobalWorkspace.
	tgts, err := Targets(home)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	tgtSet := map[string]string{}
	for _, tg := range tgts {
		tgtSet[tg.Root] = tg.Skip
	}
	for _, wantRoot := range []string{unregGit, plainDir, search.GlobalWorkspace} {
		skip, ok := tgtSet[wantRoot]
		if !ok || skip != "" {
			t.Errorf("Targets missing or skipped %s (ok=%v skip=%q): %+v", wantRoot, ok, skip, tgts)
		}
	}

	// 6. --project supports non-git plainDir and GlobalWorkspace, plus "session_id" JSON fallback.
	if err := os.WriteFile(filepath.Join(rec, "plain.json"), []byte(agyResult(`{"ratings":[{"session_id":"conv-plain-direct","outcome_score":4,"token`+
		`_efficiency":5,"plan_alignment":"matched","skill_action":"none","summary":"session_id fallback works"}]}`, false)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rec, "global.json"), []byte(agyResult(`{"ratings":[{"conversation_id":"conv-global-fallback","outcome_score":2,"token`+
		`_efficiency":4,"plan_alignment":"partial","skill_action":"rewrite-skill","target_skill":"skills/docs-ste100-80/SKILL.md","summary":"global fallback test"}]}`, false)), 0o600); err != nil {
		t.Fatal(err)
	}
	t3 := t2.Add(time.Hour)
	seedWorkspaceConvDB(t, home, map[string]struct {
		uris     string
		modified time.Time
		text     string
	}{
		"conv-plain-direct":    {`["file://` + plainDir + `"]`, t3.Add(-10 * time.Minute), "direct non-git project run conversation"},
		"conv-global-fallback": {`[]`, t3.Add(-10 * time.Minute), "direct global workspace fallback conversation"},
	})
	if err := Run(Options{
		Home:       home,
		Project:    plainDir,
		Out:        &bytes.Buffer{},
		Now:        func() time.Time { return t3 },
		SkipIngest: true,
	}); err != nil {
		t.Fatalf("--project plainDir: %v", err)
	}
	if err := Run(Options{
		Home:       home,
		Project:    search.GlobalWorkspace,
		Out:        &bytes.Buffer{},
		Now:        func() time.Time { return t3 },
		SkipIngest: true,
	}); err != nil {
		t.Fatalf("--project GlobalWorkspace: %v", err)
	}
	ms, err = metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	plainRows, err := ms.SessionRatings(plainDir, time.Time{}, time.Time{})
	globalRows, gerr := ms.SessionRatings(search.GlobalWorkspace, time.Time{}, time.Time{})
	ms.Close()
	if err != nil || len(plainRows) != 3 {
		t.Fatalf("plainDir SessionRatings = %+v, err=%v; want 3", plainRows, err)
	}
	if gerr != nil || len(globalRows) != 2 {
		t.Fatalf("GlobalWorkspace SessionRatings = %+v, err=%v; want 2", globalRows, gerr)
	}
	recs3 := dreamProposals(t, home)
	if len(recs3) != 3 || strings.Contains(recs3[0].Project, search.GlobalWorkspace) || !sameDir(recs3[0].Project, home) {
		t.Fatalf("GlobalWorkspace fallback proposal Project = %q (len=%d), want %q (not %s)", recs3[0].Project, len(recs3), home, search.GlobalWorkspace)
	}
}

func TestConversationIdleFailedCooldownAndWriteRatingsError(t *testing.T) {
	f := newFixture(t)
	agyLogin(t)
	plain := t.TempDir()
	agyConversations(t, f.home, map[string]agyConv{
		"c1": {plain, now.Add(-2 * time.Hour), []agyStep{{typ: 14, text: "failing write test conversation"}}},
	})
	resp := agyResult(`{"proposals":[],"skills":[],"ratings":[{"conversation_id":"c1","outcome_score":2,"token`+
		`_efficiency":2,"plan_alignment":"missed","skill_action":"new-skill","target_skill":"skills/x/SKILL.md","summary":"s"}]}`, false)
	rec := fakeCLI(t, "agy", resp, 0)

	at := now
	o := Options{
		Home:             f.home,
		All:              true,
		Out:              &bytes.Buffer{},
		Now:              func() time.Time { return at },
		ConversationIdle: time.Hour,
		Projects:         func(string) ([]search.Project, error) { return []search.Project{{Root: plain, Name: "plain"}}, nil },
		Scopes:           noScopes,
		SkipIngest:       true,
		WriteRatings:     func(string, string, []metrics.SessionRating) error { return errors.New("metrics locked") },
	}
	var ee *ExitError
	if err := Run(o); !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("Run err = %v, want exit 1", err)
	}
	// propose runs before WriteRatings, so the proposal was queued while ratings
	// and watermark did not persist; recovery deduplicates the queued proposal.
	if recs := dreamProposals(t, f.home); len(recs) != 1 {
		t.Errorf("proposals after WriteRatings error = %d, want 1: %+v", len(recs), recs)
	}
	st, _ := readState(f.home)
	if !st.Projects[plain].IsZero() || !st.FailedAttempts[plain].Equal(now) {
		t.Fatalf("state after WriteRatings failure: projects=%v failedAttempts=%v", st.Projects, st.FailedAttempts)
	}

	// Within 24h cooldown, failed attempt is skipped.
	os.Remove(filepath.Join(rec, "stdin"))
	at = now.Add(2 * time.Hour)
	if err := Run(o); err != nil {
		t.Fatalf("cooldown run err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(rec, "stdin")); !os.IsNotExist(err) {
		t.Error("failed attempt retried within 24h cooldown")
	}

	// Past 24h cooldown and WriteRatings fixed -> succeeds, deduplicates proposal, and clears FailedAttempts.
	at = now.Add(25 * time.Hour)
	o.WriteRatings = nil
	if err := Run(o); err != nil {
		t.Fatalf("recovery run err = %v", err)
	}
	if recs := dreamProposals(t, f.home); len(recs) != 1 {
		t.Errorf("proposals after recovery = %d, want 1 (deduplicated): %+v", len(recs), recs)
	}
	st, _ = readState(f.home)
	if !st.FailedAttempts[plain].IsZero() || !st.Projects[plain].Equal(at.Add(-time.Hour)) {
		t.Fatalf("state after recovery: projects=%v failedAttempts=%v", st.Projects, st.FailedAttempts)
	}
}
