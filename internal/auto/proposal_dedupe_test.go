package auto

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
)

func TestDraftsProposalDeduplicatesStillRegressingInPlace(t *testing.T) {
	home := t.TempDir()
	root := initGitRepo(t)
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	r := &run{o: Options{Home: home, Now: func() time.Time { return now }}}

	feed := func(label, lines string) {
		t.Helper()
		if fs := r.processDraftLines(root, ClassRetroLite, []byte(lines)); len(fs) != 0 {
			t.Fatalf("%s findings: %+v", label, fs)
		}
	}

	// First occurrence: creates 1 queued proposal.
	feed("line1", `{"kind":"proposal","target":"harness","title":"still_regressing: bash_read_bypass (2026-11-20)","body":"Day 1 observation.","signal_id":"sig:global:bash_read_bypass","report_date":"2026-11-20","action_scope":"auto_skill","recommended_action":"Tighten skill"}`+"\n")
	if r.st.OpenAsks != 1 {
		t.Fatalf("OpenAsks after first = %d, want 1", r.st.OpenAsks)
	}

	// Second & third occurrences on later dates with rephrased titles: MUST update in place, never pile up duplicates.
	now = now.Add(24 * time.Hour)
	feed("line2", `{"kind":"proposal","target":"harness","title":"bash_read_bypass still_regressing on 2026-11-21","body":"Day 2 observation.\nTarget: project\nSeverity: high","signal_id":"sig:global:bash_read_bypass","report_date":"2026-11-21","action_scope":"auto_skill","recommended_action":"Tighten skill"}`+"\n")
	now = now.Add(24 * time.Hour)
	feed("line3", `{"kind":"proposal","target":"harness","title":"Repeated still_regressing bash_read_bypass (2026-11-22)","body":"Day 3 observation.","signal_id":"sig:global:bash_read_bypass","report_date":"2026-11-22","action_scope":"auto_skill","recommended_action":"Tighten skill"}`+"\n")

	// Same signal_id with a DIFFERENT action_scope stays distinct (must-not-over-dedupe).
	feed("lineDiffScope", `{"kind":"proposal","target":"harness","title":"Operator code fix for bash_read_bypass","body":"Hook code change needed.","signal_id":"sig:global:bash_read_bypass","report_date":"2026-11-22","action_scope":"operator_code"}`+"\n")

	// Recurring still_regressing WITHOUT signal_id (only title dates differ): also deduplicates in place.
	feed("noSig", `{"kind":"proposal","target":"harness","title":"still_regressing search_grep_low (2026-11-20)","body":"Metric still_regressing day 1."}`+"\n"+
		`{"kind":"proposal","target":"harness","title":"still_regressing search_grep_low (2026-11-22)","body":"Metric still_regressing day 3."}`+"\n")

	if r.st.OpenAsks != 3 {
		t.Errorf("OpenAsks = %d, want 3 (auto_skill + operator_code + no-sig)", r.st.OpenAsks)
	}
	g := brain.Options{Home: home, Global: true, Now: r.o.Now}
	props, err := brain.List(g, brain.ListFilter{Kind: "proposal"})
	if err != nil || len(props) != 3 {
		t.Fatalf("total proposals = %d (%v), want 3", len(props), err)
	}
	var autoSkillProp *brain.Record
	for i := range props {
		if strings.Contains(props[i].Body, "ActionScope: auto_skill\n") {
			autoSkillProp = &props[i]
			break
		}
	}
	if autoSkillProp == nil {
		t.Fatalf("missing auto_skill proposal in %+v", props)
	}
	for _, want := range []string{
		"Occurrences: 3\n",
		"LastSeen: 2026-11-22T10:00:00Z\n",
		"ReportDate: 2026-11-22\n",
		"    [latest occurrence 2026-11-22T10:00:00Z, count 3]\n    Day 3 observation.",
	} {
		if !strings.Contains(autoSkillProp.Body, want) {
			t.Errorf("updated proposal missing %q:\n%s", want, autoSkillProp.Body)
		}
	}
	// Prior occurrence footer must be replaced, not accumulated unboundedly.
	if strings.Contains(autoSkillProp.Body, "Day 2 observation.") {
		t.Errorf("prior latest occurrence was not replaced:\n%s", autoSkillProp.Body)
	}
	// Body indentation must still prevent forged headers in derived updates.
	if !isHarnessTargetProposal(root, *autoSkillProp) {
		t.Errorf("derived update broke harness target classification:\n%s", autoSkillProp.Body)
	}
}

func TestSystemAsksAndEvalGateProposalsDeduplicateInPlace(t *testing.T) {
	home := t.TempDir()
	root := idleProject(t)
	now := runAt
	r := &run{o: runOpts(home, &bytes.Buffer{}), st: State{Date: now.Format("2006-01-02")}}
	r.o.Now = func() time.Time { return now }

	// fileNoTestAsk across two different branches updates one proposal.
	id1, _ := r.fileNoTestAsk(root, Task{Class: ClassDistill}, "work/auto-1")
	now = now.Add(time.Hour)
	id2, _ := r.fileNoTestAsk(root, Task{Class: ClassDistill}, "work/auto-2")
	if id1 == "failed" || id1 != id2 {
		t.Fatalf("fileNoTestAsk created duplicate: id1=%q id2=%q", id1, id2)
	}

	// fileAsk twice for same denied targets updates one proposal.
	a1, _ := r.fileAsk(root, Task{Class: ClassDistill, Denied: []string{"AGENTS.md"}})
	a2, _ := r.fileAsk(root, Task{Class: ClassDistill, Denied: []string{"AGENTS.md"}})
	if a1 == "failed" || a1 != a2 {
		t.Fatalf("fileAsk created duplicate: a1=%q a2=%q", a1, a2)
	}

	// fileEvalGateProposal without Task.ID twice for same class updates one proposal.
	e1 := r.fileEvalGateProposal(root, Task{Class: ClassDistill}, "work/auto-1", nil, 6, "holdout regressed")
	e2 := r.fileEvalGateProposal(root, Task{Class: ClassDistill}, "work/auto-2", nil, 6, "still regressed")
	if e1 == "failed" || e1 != e2 {
		t.Fatalf("fileEvalGateProposal created duplicate: e1=%q e2=%q", e1, e2)
	}

	if r.st.OpenAsks != 3 {
		t.Errorf("OpenAsks = %d, want 3", r.st.OpenAsks)
	}
	g := brain.Options{Home: home, Global: true}
	all, err := brain.List(g, brain.ListFilter{Kind: "proposal"})
	if err != nil || len(all) != 3 {
		t.Fatalf("stored proposals = %d (%v), want 3", len(all), err)
	}
}

func TestSweepConsolidatesDuplicatesAndRetiresStaleStillRegressingWithoutDeleting(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	now := runAt
	addAt := func(at time.Time, title, body string) string {
		t.Helper()
		id, err := brain.Add(brain.Options{Home: home, Global: true, Now: func() time.Time { return at }}, brain.AddInput{
			Kind: "proposal", Status: "queued", Title: title, Body: strings.NewReader(body),
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	// Seed 5 duplicate still_regressing proposals for the same signal (simulating pre-fix accumulation).
	for i := 0; i < 5; i++ {
		day := now.Add(-time.Duration(5-i) * 24 * time.Hour)
		addAt(day,
			fmt.Sprintf("Auto, harness — still_regressing search_grep_low (%s)", day.Format("2006-01-02")),
			fmt.Sprintf("Target: harness\nClass: retro-lite\nProvenance: derived\nSignal: sig:global:search_grep_low\nActionScope: auto_skill\n\n    Observed still_regressing #%d.", i+1),
		)
	}
	// Seed 1 stale derived still_regressing proposal (>14d without recurrence -> TTL-retired).
	staleID := addAt(now.Add(-20*24*time.Hour),
		"Auto, harness — still_regressing retry_loops_elevated (2026-10-12)",
		"Target: harness\nClass: retro-lite\nProvenance: derived\nSignal: sig:global:retry_loops_elevated\nActionScope: auto_skill\n\n    Old still_regressing.",
	)
	// Seed 1 derived still_regressing proposal whose signal recovered in latest daily digest.
	recoveredID := addAt(now.Add(-2*24*time.Hour),
		"Auto, harness — still_regressing context_knee_rate",
		"Target: harness\nClass: retro-lite\nProvenance: derived\nSignal: sig:global:context_knee_rate\nActionScope: auto_skill\n\n    Recent still_regressing now recovered.",
	)
	if err := metrics.WriteLatestDailyDigest(home, metrics.DailyDigest{
		Day: now.Format("2006-01-02"),
		Actions: []metrics.DigestAppliedAction{{
			SignalID: "sig:global:context_knee_rate",
			Status:   metrics.StatusRecovered,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	// Seed 1 old (>30d) non-still_regressing operator proposal: MUST stay queued (negative control).
	operatorOldID := addAt(now.Add(-35*24*time.Hour),
		"Operator queued rule tweak",
		"Target: harness\n\nKeep operator proposals open.",
	)

	const totalSeeded = 8
	// Dry-run MUST NOT mutate brain.db.
	var dryOut bytes.Buffer
	dryOpts := runOpts(home, &dryOut)
	dryOpts.Env = &fakeEnv{targets: targetsOf(idleProject(t)), usage: usageText("90%", "90%")}
	if err := Run(dryOpts, true); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	g := brain.Options{Home: home, Global: true}
	if openBefore, _ := brain.List(g, brain.ListFilter{Kind: "proposal", Open: true}); len(openBefore) != totalSeeded {
		t.Fatalf("dry-run mutated open proposals: got %d, want %d", len(openBefore), totalSeeded)
	}

	// Real sweep consolidates duplicates and retires stale/recovered still_regressing without deleting rows.
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.Env = &fakeEnv{targets: targetsOf(idleProject(t)), usage: usageText("90%", "90%")}
	if err := Run(o, false); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "proposals: consolidated 4 duplicate(s), retired 2 stale still_regressing") {
		t.Errorf("missing consolidation summary in:\n%s", out.String())
	}

	// PRD R7: NEVER delete rows from brain.db — all 8 proposal records still exist.
	allAfter, err := brain.List(g, brain.ListFilter{Kind: "proposal"})
	if err != nil || len(allAfter) != totalSeeded {
		t.Fatalf("total proposals after consolidation = %d (%v), want %d (no deletes per R7)", len(allAfter), err, totalSeeded)
	}
	// Only 2 open proposals remain: 1 canonical search_grep_low survivor + 1 operatorOldID.
	openAfter, err := brain.List(g, brain.ListFilter{Kind: "proposal", Open: true})
	if err != nil || len(openAfter) != 2 {
		t.Fatalf("open proposals after consolidation = %d (%v), want 2: %+v", len(openAfter), err, openAfter)
	}
	var survivor *brain.Record
	foundOp := false
	for i := range openAfter {
		if openAfter[i].ID == operatorOldID {
			foundOp = true
		} else {
			survivor = &openAfter[i]
		}
	}
	if !foundOp || survivor == nil {
		t.Fatalf("unexpected surviving open proposals: %+v", openAfter)
	}
	if !strings.Contains(survivor.Body, "Occurrences: 5\n") || !strings.Contains(survivor.Body, "Consolidated duplicates:") {
		t.Errorf("canonical survivor missing summed occurrences/note:\n%s", survivor.Body)
	}
	for _, retiredID := range []string{staleID, recoveredID} {
		recs, _, err := brain.Get(g, []string{retiredID})
		if err != nil || len(recs) != 1 || recs[0].Status != "rejected" || !strings.Contains(recs[0].Body, "## Auto-consolidated") {
			t.Errorf("retired proposal %s = %+v (err=%v), want status=rejected with Auto-consolidated note", retiredID, recs, err)
		}
	}
}
