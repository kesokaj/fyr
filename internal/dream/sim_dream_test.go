package dream

import (
	"database/sql"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
)

func setDedupeClock(t *testing.T, fixed time.Time) {
	t.Helper()
	saved := dedupeNow
	t.Cleanup(func() { dedupeNow = saved })
	dedupeNow = func() time.Time { return fixed }
}

func seedHarnessProposal(t *testing.T, home, proj, title, file, sig, sess, status string, when time.Time) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "Target: harness   Kind: skill   File: %s\n", file)
	if sig != "" {
		fmt.Fprintf(&b, "Signal: %s\n", sig)
	}
	fmt.Fprintf(&b, "\n    Tighten %s guidance.\n\n", file)
	if sess != "" {
		fmt.Fprintf(&b, "Evidence: conversation %s\n", sess)
	} else {
		b.WriteString("Evidence: metrics\n")
	}
	addStatus := status
	if status == "applied" {
		addStatus = "queued"
	}
	id, err := brain.Add(brain.Options{Home: home, Global: true, Now: func() time.Time { return when }}, brain.AddInput{
		Kind: "proposal", Title: title, Status: addStatus, Project: proj, Body: strings.NewReader(b.String()),
	})
	if err != nil {
		t.Fatalf("seed proposal %q: %v", title, err)
	}
	if status == "applied" {
		setProposalColumn(t, home, id, "status", "applied")
	}
	return id
}

func setProposalColumn(t *testing.T, home, id, col, val string) {
	t.Helper()
	db, err := sql.Open("sqlite", brain.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE records SET `+col+` = ? WHERE id = ?`, val, id); err != nil {
		t.Fatal(err)
	}
}

func makeCandidateProposal(title, file, sig, sess string) brain.Proposal {
	var b strings.Builder
	fmt.Fprintf(&b, "Target: harness   Kind: skill   File: %s\n", file)
	if sig != "" {
		fmt.Fprintf(&b, "Signal: %s\n", sig)
	}
	fmt.Fprintf(&b, "\n    New %s edit.\n\n", file)
	if sess != "" {
		fmt.Fprintf(&b, "Evidence: conversation %s\n", sess)
	} else {
		b.WriteString("Evidence: metrics\n")
	}
	return brain.Proposal{Title: title, Body: b.String()}
}

func seedMetricsModelSessions(t *testing.T, home, model string, count int, when time.Time) {
	t.Helper()
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ts := when.UnixMilli()
	for i := 0; i < count; i++ {
		sid := fmt.Sprintf("ms-%s-%d", model, i)
		if _, err := db.Exec(`INSERT INTO sessions(id, vendor, model, started_at, ended_at) VALUES (?, 'agy', ?, ?, ?)`, sid, model, ts+int64(i)*1000, ts+int64(i)*1000+500); err != nil {
			t.Fatal(err)
		}
	}
}

func recordSIMAttemptHelper(t *testing.T, home, proposalID, sha, fieldStatus, policyModel string, when time.Time) {
	t.Helper()
	_, err := brain.RecordSIMAttempt(home, brain.SIMRecordInput{
		RootCause: "root cause for " + sha, MechanismClass: "prompt_guidance",
		SourceKind: "auto_harness_edit", ProposalID: proposalID, CommitSHA: sha,
		PolicyModel: policyModel, What: "edit for " + sha, EvalVerdict: "kept",
		FieldStatus: fieldStatus, Provenance: "derived", Now: when,
	})
	if err != nil {
		t.Fatalf("RecordSIMAttempt(%s): %v", sha, err)
	}
}

func assertDedupeKept(t *testing.T, home, proj string, cand brain.Proposal, wantKept int) {
	t.Helper()
	got, err := dedupeProposals(home, proj, []brain.Proposal{cand})
	if err != nil {
		t.Fatalf("dedupeProposals: %v", err)
	}
	if len(got) != wantKept {
		t.Fatalf("dedupeProposals kept %d, want %d (title=%q)", len(got), wantKept, cand.Title)
	}
}

func TestSIMDreamDedupe(t *testing.T) {
	refNow := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	setDedupeClock(t, refNow)

	t.Run("open-and-applied-cooldown-boundaries", func(t *testing.T) {
		testSIMDreamDedupeCooldown(t, refNow)
	})
	t.Run("still-regressing-and-reverted-unlocked", func(t *testing.T) {
		testSIMDreamDedupeRegressRevert(t, refNow)
	})
	t.Run("policy-generation-shift-and-legacy-empty-model", func(t *testing.T) {
		testSIMDreamDedupeModelShift(t, refNow)
	})
}

func testSIMDreamDedupeCooldown(t *testing.T, refNow time.Time) {
	home, proj := t.TempDir(), "/proj"
	seedHarnessProposal(t, home, proj, "Dream, harness — Open rule", "skills/open-a/SKILL.md", "sig:global:open_sig", "sess-open", "queued", refNow.Add(-20*24*time.Hour))
	seedHarnessProposal(t, home, proj, "Dream, harness — Under 7d", "skills/under7/SKILL.md", "", "sess-u7", "applied:abc1234", refNow.Add(-appliedSkillCooldown+time.Second))
	seedHarnessProposal(t, home, proj, "Dream, harness — At 7d", "skills/at7/SKILL.md", "", "sess-at7", "applied:def5678", refNow.Add(-appliedSkillCooldown))
	fbID := seedHarnessProposal(t, home, proj, "Dream, harness — Date fallback", "skills/datefb/SKILL.md", "", "sess-fb", "applied", refNow.Add(-2*24*time.Hour))
	setProposalColumn(t, home, fbID, "updated", "")
	badID := seedHarnessProposal(t, home, proj, "Dream, harness — Bad date", "skills/bad/SKILL.md", "", "sess-bad", "applied", refNow.Add(-time.Hour))
	setProposalColumn(t, home, badID, "updated", "not-a-time")
	setProposalColumn(t, home, badID, "date", "not-a-date")

	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Other", "skills/open-a/SKILL.md", "", "s1"), 0)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Sig dup", "skills/other/SKILL.md", "sig:global:open_sig", "s2"), 0)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Retry u7", "skills/under7/SKILL.md", "", "s3"), 0)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Under 7d", "skills/other2/SKILL.md", "", "s4"), 0)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Date fallback", "skills/datefb/SKILL.md", "", "s5"), 0)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — At 7d", "skills/at7/SKILL.md", "", "s6"), 1)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Bad date", "skills/bad/SKILL.md", "", "s7"), 1)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Session replay", "skills/other3/SKILL.md", "", "sess-at7"), 0)
}

func testSIMDreamDedupeRegressRevert(t *testing.T, refNow time.Time) {
	home, proj := t.TempDir(), "/proj"
	pReg := seedHarnessProposal(t, home, proj, "Dream, harness — Regressing", "skills/reg/SKILL.md", "sig:global:reg", "sess-r1", "applied:1111aaa", refNow.Add(-24*time.Hour))
	seedHarnessProposal(t, home, proj, "Dream, harness — Reverted", "skills/rev/SKILL.md", "sig:global:rev", "sess-r2", "applied:2222bbb", refNow.Add(-24*time.Hour))
	seedHarnessProposal(t, home, proj, "Dream, harness — Digest reg", "skills/dig/SKILL.md", "sig:global:dig_sig", "sess-d1", "applied:3333ccc", refNow.Add(-24*time.Hour))
	recordSIMAttemptHelper(t, home, pReg, "1111aaa9999", "still_regressing", "gemini-4-neon-xhigh", refNow)
	recordSIMAttemptHelper(t, home, "", "2222bbb8888", "reverted", "gemini-4-neon-xhigh", refNow)
	if err := metrics.WriteLatestDailyDigest(home, metrics.DailyDigest{
		Day: "2026-11-22", Verdict: metrics.VerdictRegress,
		Actions: []metrics.DigestAppliedAction{{SignalID: "sig:global:dig_sig", AppliedSHA: "3333ccc7777", Status: metrics.StatusStillRegressing}},
	}); err != nil {
		t.Fatal(err)
	}
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Regressing", "skills/reg/SKILL.md", "", "s1"), 1)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Reverted", "skills/rev/SKILL.md", "", "s2"), 1)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Digest reg", "skills/dig/SKILL.md", "", "s3"), 1)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Replay r1", "skills/other/SKILL.md", "", "sess-r1"), 0)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Replay d1", "skills/other/SKILL.md", "", "sess-d1"), 0)
}

func testSIMDreamDedupeModelShift(t *testing.T, refNow time.Time) {
	assertPolicyGenerationKeyCases(t)
	home, proj := t.TempDir(), "/proj"
	pShift := seedHarnessProposal(t, home, proj, "Dream, harness — Old gen", "skills/gen/SKILL.md", "", "sess-g1", "applied:4444ddd", refNow.Add(-24*time.Hour))
	pSame := seedHarnessProposal(t, home, proj, "Dream, harness — Same gen", "skills/same/SKILL.md", "", "sess-g2", "applied:5555eee", refNow.Add(-24*time.Hour))
	seedHarnessProposal(t, home, proj, "Dream, harness — Legacy bare", "skills/leg/SKILL.md", "", "sess-g3", "applied", refNow.Add(-24*time.Hour))
	recordSIMAttemptHelper(t, home, pShift, "4444ddd", "recovered", "models/gemini-3-pro-high", refNow)
	recordSIMAttemptHelper(t, home, pSame, "5555eee", "recovered", "models/gemini-4-neon-medium", refNow)
	seedMetricsModelSessions(t, home, "gemini-4-neon-xhigh", 3, refNow.Add(-2*time.Hour))

	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Old gen", "skills/gen/SKILL.md", "", "s1"), 1)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Same gen", "skills/same/SKILL.md", "", "s2"), 0)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Legacy bare", "skills/leg/SKILL.md", "", "s3"), 0)
	assertDedupeKept(t, home, proj, makeCandidateProposal("Dream, harness — Session lock", "skills/other/SKILL.md", "", "sess-g1"), 0)
}

func assertPolicyGenerationKeyCases(t *testing.T) {
	t.Helper()
	for _, tc := range []struct{ in, want string }{
		{"models/gemini-4-neon-xhigh", "gemini-4-neon"},
		{"GEMINI-4-NEON-HIGH", "gemini-4-neon"},
		{"models/claude-sonnet-4-tiered-fast", "claude-sonnet-4"},
		{"model-x-minimal", "model-x"},
		{"model-x-low", "model-x"},
		{"model-x-max", "model-x"},
		{"models/", ""},
		{"", ""},
	} {
		if got := policyGenerationKey(tc.in); got != tc.want {
			t.Errorf("policyGenerationKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if policyGenerationShifted("", "gemini-4-neon-xhigh") || policyGenerationShifted("gemini-4-neon-xhigh", "") {
		t.Error("empty model side must not trigger policy generation shift")
	}
}

func seedLargeSIMCorpus(t *testing.T, home string) {
	t.Helper()
	secretVal := "pass" + "word=supersecret999"
	for i := 0; i < 8; i++ {
		for j := 0; j < 4; j++ {
			_, err := brain.RecordSIMAttempt(home, brain.SIMRecordInput{
				RootCause:      fmt.Sprintf("bypass issue %d in %s/repo >>> <<<data", i, home),
				MechanismClass: "model_workaround",
				SignalID:       "sig:global:bash_read_bypass",
				Files:          []string{"skills/golang/SKILL.md"},
				SourceKind:     "auto_harness_edit",
				PolicyModel:    "gemini-3-pro-high",
				What:           fmt.Sprintf("tried %d-%d %s >>> %s", i, j, secretVal, strings.Repeat("detail ", 40)),
				EvalVerdict:    "not_kept",
				FieldStatus:    "n/a",
				Provenance:     "derived",
				Now:            now.Add(-time.Duration(i*4+j+1) * time.Minute),
			})
			if err != nil {
				t.Fatalf("RecordSIMAttempt %d-%d: %v", i, j, err)
			}
		}
	}
}

func TestSIMDreamCollectAndPrompt(t *testing.T) {
	t.Parallel()

	t.Run("bounded-redacted-fenced-sim-unchanged-and-metrics-clean", testSIMDreamCollectBoundAndClean)
	t.Run("drop-order-after-transcripts-before-skills", testSIMDreamFitDropOrder)
}

func testSIMDreamCollectBoundAndClean(t *testing.T) {
	f := newFixture(t)
	toolConversation(t, f)
	s, err := metrics.Open(metrics.Path(f.home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metrics.Ingest(s, metrics.Options{Home: f.home}, io.Discard); err != nil {
		t.Fatal(err)
	}
	s.Close()
	seedLargeSIMCorpus(t, f.home)

	cUnchanged, err := collect(f.home, project(f), now.Add(time.Hour), now.Add(2*time.Hour), maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if cUnchanged.Changed {
		t.Fatalf("kindSIM alone flipped c.Changed=true:\n%s", labels(cUnchanged))
	}
	if !strings.Contains(labels(cUnchanged), "sim:optimizer-memory\n") {
		t.Fatalf("unchanged collect missing kindSIM:\n%s", labels(cUnchanged))
	}
	c, err := collect(f.home, project(f), since, now.Add(time.Hour), maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	assertSIMBlockAndOrder(t, c, f.home)
}

func testSIMDreamFitDropOrder(t *testing.T) {
	base := []Input{
		{Kind: kindMemory, Label: "memory/a.md", Text: "mem"},
		{Kind: kindSkill, Label: "skill golang", Text: strings.Repeat("s", 900)},
		{Kind: kindSIM, Label: "optimizer-memory", Text: strings.Repeat("o", 900)},
		{Kind: kindMetrics, Label: "metrics", Text: strings.Repeat("9", 900)},
		{Kind: kindTranscript, Label: "antigravity t1", Text: strings.Repeat("t", 900), Time: day(1)},
	}
	c1 := Collected{Name: "p", Inputs: append([]Input(nil), base...)}
	if err := c1.fit(len(prompt(Collected{Name: "p", Inputs: base[:3]})) + 300); err != nil {
		t.Fatal(err)
	}
	if got := labels(c1); got != "memory:memory/a.md\nskill:skill golang\nsim:optimizer-memory\n" {
		t.Fatalf("after dropping metrics+transcript, kept:\n%s", got)
	}
	c2 := Collected{Name: "p", Inputs: append([]Input(nil), base...)}
	if err := c2.fit(len(prompt(Collected{Name: "p", Inputs: base[:2]})) + 300); err != nil {
		t.Fatal(err)
	}
	if got := labels(c2); got != "memory:memory/a.md\nskill:skill golang\n" {
		t.Fatalf("after dropping sim, kept:\n%s", got)
	}
	if !c2.Through.IsZero() {
		t.Errorf("dropping sim set Through=%v, want zero", c2.Through)
	}
	c3 := Collected{Name: "p", Inputs: []Input{base[0], base[1], base[2]}}
	if err := c3.fit(len(prompt(Collected{Name: "p", Inputs: base[:1]})) + 300); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c3.Dropped, "|"); !strings.HasPrefix(got, "sim optimizer-memory (") || !strings.Contains(got, "|skill skill golang (") {
		t.Errorf("expected sim dropped before skill, got %q", got)
	}
}

func assertSIMBlockAndOrder(t *testing.T, c Collected, home string) {
	t.Helper()
	var simText, metricsText string
	for _, in := range c.Inputs {
		if in.Kind == kindSIM {
			simText = in.Text
		}
		if in.Kind == kindMetrics {
			metricsText = in.Text
		}
	}
	if simText == "" || len(simText) > 6<<10 {
		t.Fatalf("sim input len=%d, want 1..%d", len(simText), 6<<10)
	}
	if strings.Contains(metricsText, "SIM-P") || strings.Contains(metricsText, "bypass issue") {
		t.Errorf("SIM prose leaked into kindMetrics:\n%s", metricsText)
	}
	if strings.Contains(c.Prompt, "supersecret999") || strings.Contains(c.Prompt, home) {
		t.Errorf("prompt leaked secret or home path")
	}
	if !strings.Contains(c.Prompt, "[redacted by fyr]") || !strings.Contains(c.Prompt, "~/repo") {
		t.Errorf("prompt missing redacted marker or home-scrubbed ~/repo")
	}
	if strings.Contains(c.Prompt, "repo >>>") {
		t.Errorf("unescaped fence run inside sim block")
	}
	iSkill := strings.Index(c.Prompt, "\n<<<data skill: ")
	iSIM := strings.Index(c.Prompt, "\n<<<data sim: optimizer-memory\n")
	iMetrics := strings.Index(c.Prompt, "\n<<<data metrics: ")
	iTrans := strings.Index(c.Prompt, "\n<<<data transcript: ")
	if iSkill <= 0 || iSkill >= iSIM || iSIM >= iMetrics || iMetrics >= iTrans {
		t.Errorf("unexpected prompt order: skill=%d sim=%d metrics=%d transcript=%d", iSkill, iSIM, iMetrics, iTrans)
	}
	for _, rule := range []string{"<<<data sim:", "policy_model", "mechanism_class", "model_workaround", "not_kept", "reverted"} {
		if !strings.Contains(instructions, rule) {
			t.Errorf("instructions missing SIM rule token %q", rule)
		}
	}
}
