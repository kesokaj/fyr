package auto

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/search"
)

func multiPoolUsageText(gemFive, gemWeek, claudeFive, claudeWeek string) string {
	return "Gemini Models\tWeekly Limit Remaining\t" + gemWeek + "\t2026-11-29T00:00:00Z\n" +
		"Gemini Models\tFive Hour Limit Remaining\t" + gemFive + "\t2026-11-22T23:00:00Z\n" +
		"Claude and GPT models\tWeekly Limit Remaining\t" + claudeWeek + "\t2026-11-29T00:00:00Z\n" +
		"Claude and GPT models\tFive Hour Limit Remaining\t" + claudeFive + "\t2026-11-22T23:00:00Z\n"
}

func runV910AutoPerPoolG9(t *testing.T) {
	// Gemini healthy (97%/86%), Claude depleted (disabled/14%).
	mixed := multiPoolUsageText("97%", "86%", "disabled", "14%")
	if line, stop := gateBudgetForModel(0, "gemini-4-pro-high", mixed, nil); stop {
		t.Fatalf("Gemini task halted while Gemini pool is 97%%/86%%: %q", line)
	}
	if line, stop := gateBudgetForModel(0, "claude-opus-4-6", mixed, nil); !stop || !strings.Contains(line, "G9 ") {
		t.Fatalf("Claude task should halt at week=14%%: stop=%v line=%q", stop, line)
	}
	// Target pool under 30% week or under 50% 5h must halt even if other pool is healthy.
	gemWeekLow := multiPoolUsageText("90%", "29%", "95%", "95%")
	if _, stop := gateBudgetForModel(0, "gemini-4-pro-high", gemWeekLow, nil); !stop {
		t.Fatalf("Gemini task must halt when Gemini week < 30%%")
	}
	gemFiveLow := multiPoolUsageText("49%", "85%", "95%", "95%")
	if _, stop := gateBudgetForModel(0, "gemini-4-flash-medium", gemFiveLow, nil); !stop {
		t.Fatalf("Gemini task must halt when Gemini 5h < 50%%")
	}
	// Unreadable or missing target pool must fail closed.
	claudeOnly := "Claude and GPT models\tWeekly Limit Remaining\t90%\t2026-11-29T00:00:00Z\n" +
		"Claude and GPT models\tFive Hour Limit Remaining\t90%\t2026-11-22T23:00:00Z\n"
	if _, stop := gateBudgetForModel(0, "gemini-4-pro-high", claudeOnly, nil); !stop {
		t.Fatalf("missing target pool must fail closed")
	}
	if _, stop := gateBudgetForModel(0, "gemini-4-pro-high", mixed, errors.New("probe failed")); !stop {
		t.Fatalf("readErr must fail closed")
	}

	// End-to-end Run: catalog picks Gemini builder:pro; Claude week=14% must NOT block Gemini worker.
	home := t.TempDir()
	realHome := t.TempDir()
	root, _, _ := initHarnessEvalGateFixture(t, home, realHome, "")
	cat := models.BuildCatalog(runAt, []models.CatalogEntry{
		{ID: "gemini-4-pro-high", Family: "gemini", Tier: "pro", Status: "active"},
		{ID: "gemini-4-flash-medium", Family: "gemini", Tier: "flash", Status: "active"},
		{ID: "claude-opus-4-6", Family: "anthropic", Tier: "pro", Status: "active"},
	}, nil, nil)
	if err := models.SaveCatalog(home, cat); err != nil {
		t.Fatalf("SaveCatalog: %v", err)
	}
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Env = &fakeEnv{targets: targetsOf(root), usage: mixed}
	workerRan := false
	candWork := makeCandidateBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
	o.Work = func(ctx context.Context, wo isolate.WorkerOptions, paths ...string) ([]isolate.WorkerResult, error) {
		workerRan = true
		return candWork(ctx, wo, paths...)
	}
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		return EvalOutcome{Row: req.Row, Passed: 3, Graded: req.Runs, RecordID: "TEST-g9"}, nil
	}
	if err := Run(o, false); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if !workerRan {
		t.Fatalf("Gemini worker was blocked by Claude 14%% pool:\n%s", out.String())
	}
}

func runV910Ephemeral(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "fake-home")
	t.Setenv("HOME", fakeHome)

	blocked := []string{
		"/",
		"/tmp",
		fakeHome,
		fakeHome + "/",
		"/home/u/.config/fyr/auto/wt-1",
		"/home/u/.config/fyr/eval/row-1",
		"/home/u/.config/fyr/sandbox/run-1",
		"/home/u/.config/fyr/dev",
		"/home/u/.config/fyr/dev/fyr-eval",
		"/home/u/.config/fyr/dream-agy-home-4242",
		"/home/u/.config/fyr/dream-agy-home-99/sub",
		"/var/tmp/real-home-smoke",
		"/tmp/real-home-smoke-20261122",
		"/home/u/.gemini/antigravity-cli/brain/8c313de2/scratch",
		"/home/u/.gemini/antigravity-cli/brain/8c313de2/scratch/sub",
	}
	for _, p := range blocked {
		if !IsEphemeralPath(p) {
			t.Errorf("IsEphemeralPath(%q) = false, want true", p)
		}
		if !dream.IsRefusedWorkspace(p, fakeHome) {
			t.Errorf("dream.IsRefusedWorkspace(%q, %q) = false, want true", p, fakeHome)
		}
	}

	allowed := []string{
		"",
		".",
		search.GlobalWorkspace,
		filepath.Join(fakeHome, "workspace", "fyr"),
		"/tmp/TestRepoWorkspace/app",
		"/home/u/code/my-app",
	}
	for _, p := range allowed {
		if IsEphemeralPath(p) {
			t.Errorf("IsEphemeralPath(%q) = true, want false", p)
		}
		if dream.IsRefusedWorkspace(p, fakeHome) {
			t.Errorf("dream.IsRefusedWorkspace(%q, %q) = true, want false", p, fakeHome)
		}
	}
}

func runV910EvalPrefixAndPreDispatchFeasibility(t *testing.T) {
	suite := []eval.Row{
		{ID: "feature", Prompt: "plan a two-phase feature"},
		{ID: "feature-empty", Prompt: "build from scratch"},
		{ID: "dreamer", Agent: "dreamer", Prompt: "dispatch dreamer"},
		{ID: "retro", Prompt: "run retro"},
		{ID: "feedback-log", Prompt: "record correction"},
		{ID: "project-memory", Prompt: "remember Friday"},
	}
	mustMatch := [][2]string{
		{"feature", "skills/fyr-feature/SKILL.md"},
		{"feature-empty", "fyr-feature"},
		{"dreamer", "skills/fyr-dream/SKILL.md"},
		{"retro", "skills/fyr-retro/SKILL.md"},
		{"feedback-log", "skills/fyr-feedback-log/SKILL.md"},
		{"project-memory", "skills/fyr-project-memory/SKILL.md"},
	}
	for _, tc := range mustMatch {
		var row eval.Row
		for _, r := range suite {
			if r.ID == tc[0] {
				row = r
				break
			}
		}
		if !rowNamesTarget(row, tc[1]) {
			t.Errorf("rowNamesTarget(%q, %q) = false, want true", tc[0], tc[1])
		}
	}
	if hasMatchingEvalRows(suite, []string{"skills/unmapped-skill/"}) {
		t.Errorf("hasMatchingEvalRows matched unmapped-skill unexpectedly")
	}
	if !hasMatchingEvalRows(suite, []string{"skills/fyr-feature/"}) {
		t.Errorf("hasMatchingEvalRows missed skills/fyr-feature/")
	}

	// Pre-dispatch check must quarantine 0-row skill without consuming a daily worker slot or cooldown.
	home := t.TempDir()
	realHome := t.TempDir()
	root, _, defaultID := initHarnessEvalGateFixture(t, home, realHome, "")
	rejected := "rejected"
	if err := brain.Edit(brain.Options{Home: home, Global: true}, brain.EditInput{ID: defaultID, Status: &rejected}); err != nil {
		t.Fatal(err)
	}
	writeTree(t, root, "skills/unmapped-skill/SKILL.md", "---\nname: unmapped-skill\ndescription: x\n---\n# v1\n")
	git(t, root, "add", "skills/unmapped-skill/SKILL.md")
	git(t, root, "commit", "-q", "-m", "add unmapped skill")
	git(t, root, "push", "-q")
	backdate(t, root)

	unmappedID, err := brain.Add(brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt.Add(-time.Hour) }}, brain.AddInput{
		Kind: "proposal", Status: "queued", Project: root,
		Title: "Dream, harness — tweak skills/unmapped-skill/SKILL.md",
		Body:  strings.NewReader("Target: harness\nEvidence: BUG-700\n\nUpdate `skills/unmapped-skill/SKILL.md`.\n"),
	})
	if err != nil {
		t.Fatal(err)
	}

	workerCalls := 0
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Work = func(_ context.Context, _ isolate.WorkerOptions, _ ...string) ([]isolate.WorkerResult, error) {
		workerCalls++
		return nil, nil
	}
	if err := Run(o, false); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if workerCalls != 0 {
		t.Fatalf("workerCalls = %d, want 0 for unmapped 0-row skill", workerCalls)
	}
	st, _, err := loadState(home)
	if err != nil {
		t.Fatal(err)
	}
	if st.ModelRuns != 0 || !st.LastModelRun.IsZero() {
		t.Fatalf("0-row skill wasted slot/cooldown: ModelRuns=%d LastModelRun=%v", st.ModelRuns, st.LastModelRun)
	}
	rec := getGlobalProposal(t, home, unmappedID)
	if !strings.Contains(rec.Body, "## Eval gate") || !strings.Contains(rec.Body, "no GUIDE-eval row") {
		t.Fatalf("unmapped proposal was not quarantined with clear reason:\n%s", rec.Body)
	}
}

func runV910DeterministicRowPlanner(t *testing.T) {
	suite := []eval.Row{
		{ID: "feature", Prompt: "/fyr-feature main", Kind: eval.KindCapability},
		{ID: "feature-empty", Prompt: "/fyr-feature empty", Kind: eval.KindCapability},
		{ID: "feature-recon", Prompt: "/fyr-feature recon", Kind: eval.KindCapability},
		{ID: "feature-routing", Prompt: "routing", Kind: eval.KindCapability},
		{ID: "dream", Prompt: "/fyr-dream", Kind: eval.KindCapability},
		{ID: "dreamer", Agent: "dreamer", Prompt: "dispatch dreamer", Kind: eval.KindCapability},
		{ID: "retro", Prompt: "/fyr-retro", Kind: eval.KindCapability},
		{ID: "reg-a", Prompt: "reg a", Kind: eval.KindRegression},
		{ID: "reg-b", Prompt: "reg b", Kind: eval.KindRegression},
	}
	holdout := []eval.Row{
		{ID: "holdout-inject-fence", Kind: eval.KindAdversarial},
		{ID: "holdout-resume-readonly", Kind: eval.KindRegression},
		{ID: "holdout-secret-boundary", Kind: eval.KindAdversarial},
	}

	maxRows := maxEvalGateSessions / (2 * gateRuns)
	for _, skill := range []string{"fyr-feature", "fyr-dream", "fyr-retro"} {
		plan1 := selectGateRows(suite, holdout, []string{skill}, maxRows)
		plan2 := selectGateRows(suite, holdout, []string{skill}, maxRows)
		if len(plan1) == 0 || len(plan1) > maxRows {
			t.Fatalf("%s: planned %d rows, want 1..%d", skill, len(plan1), maxRows)
		}
		if len(plan1)*2*gateRuns > maxEvalGateSessions {
			t.Fatalf("%s: planned sessions %d > %d", skill, len(plan1)*2*gateRuns, maxEvalGateSessions)
		}
		if len(plan1) != len(plan2) {
			t.Fatalf("%s: planner not deterministic", skill)
		}
		for i := range plan1 {
			if plan1[i] != plan2[i] {
				t.Fatalf("%s: row %d differed: %+v vs %+v", skill, i, plan1[i], plan2[i])
			}
		}
		hasHoldout := false
		for _, r := range plan1 {
			if r.IsHoldout {
				hasHoldout = true
			}
		}
		if !hasHoldout {
			t.Errorf("%s: planned rows %+v omitted holdout", skill, plan1)
		}
	}

	// End-to-end: fyr-feature with 4 target rows + 3 holdouts applies at 100% within <= 24 sessions.
	home := t.TempDir()
	realHome := t.TempDir()
	extra := "| `feature` |  | /fyr-feature main |  | `true` |  | named | 1 | capability | feature |  |\n" +
		"| `feature-empty` |  | /fyr-feature empty |  | `true` |  | named | 1 | capability | feature |  |\n" +
		"| `feature-recon` |  | /fyr-feature recon |  | `true` |  | named | 1 | capability | feature |  |\n" +
		"| `feature-routing` |  | /fyr-feature routing |  | `true` |  | named | 1 | capability | feature |  |\n"
	root, holdoutPath, defaultID := initHarnessEvalGateFixture(t, home, realHome, extra)
	rejected := "rejected"
	if err := brain.Edit(brain.Options{Home: home, Global: true}, brain.EditInput{ID: defaultID, Status: &rejected}); err != nil {
		t.Fatal(err)
	}
	multiHoldout := "# Holdout\n\n| id | setup | prompt | prompt2 | check | agent | grants | version | kind | cluster | expect |\n" +
		"|---|---|---|---|---|---|---|---|---|---|---|\n" +
		"| `holdout-1` |  | h1 |  | `true` |  | named | 1 | adversarial | holdout |  |\n" +
		"| `holdout-2` |  | h2 |  | `true` |  | named | 1 | regression | holdout |  |\n" +
		"| `holdout-3` |  | h3 |  | `true` |  | named | 1 | adversarial | holdout |  |\n"
	if err := os.WriteFile(holdoutPath, []byte(multiHoldout), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTree(t, root, "skills/fyr-feature/SKILL.md", "---\nname: fyr-feature\ndescription: feature\n---\n# feature v1\n")
	git(t, root, "add", "skills/fyr-feature/SKILL.md")
	git(t, root, "commit", "-q", "-m", "add fyr-feature")
	git(t, root, "push", "-q")
	backdate(t, root)

	if _, err := brain.Add(brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt.Add(-time.Hour) }}, brain.AddInput{
		Kind: "proposal", Status: "queued", Project: root,
		Title: "Dream, harness — tighten skills/fyr-feature/SKILL.md",
		Body:  strings.NewReader("Target: harness\nEvidence: BUG-701\n\nUpdate `skills/fyr-feature/SKILL.md`.\n"),
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Work = func(_ context.Context, _ isolate.WorkerOptions, kickoffs ...string) ([]isolate.WorkerResult, error) {
		name := strings.TrimSuffix(filepath.Base(kickoffs[0]), ".md")
		base := git(t, root, "rev-parse", "HEAD")
		tip := commitPlumbing(t, root, base, "100644", "skills/fyr-feature/SKILL.md", "---\nname: fyr-feature\ndescription: feature\n---\n# feature v2\n", "tighten fyr-feature")
		git(t, root, "update-ref", "refs/heads/work/"+name, tip)
		return []isolate.WorkerResult{{Name: name, Base: base, Head: tip, Commits: 1, Verdict: "PASS"}}, nil
	}
	sessions := 0
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		n := req.Runs
		sessions += n
		return EvalOutcome{Row: req.Row, Passed: n, Graded: n, RecordID: "TEST-plan"}, nil
	}
	if err := Run(o, false); err != nil {
		t.Fatalf("Run fyr-feature: %v\n%s", err, out.String())
	}
	if sessions == 0 || sessions > maxEvalGateSessions {
		t.Fatalf("sessions = %d, want 1..%d", sessions, maxEvalGateSessions)
	}
	gotSkill, err := os.ReadFile(filepath.Join(root, "skills/fyr-feature/SKILL.md"))
	if err != nil || !strings.Contains(string(gotSkill), "# feature v2") {
		t.Fatalf("fyr-feature did not apply after passing planned <=24-session gate:\n%s\n%s", string(gotSkill), out.String())
	}
}

func runV910AdaptiveCooldown(t *testing.T) {
	if modelRunsPerDay != 4 {
		t.Fatalf("modelRunsPerDay = %d, want static 4", modelRunsPerDay)
	}
	cases := []struct {
		week  int
		known bool
		want  time.Duration
	}{
		{90, true, time.Hour},
		{70, true, time.Hour},
		{69, true, 2 * time.Hour},
		{50, true, 2 * time.Hour},
		{49, true, 4 * time.Hour},
		{30, true, 4 * time.Hour},
		{0, false, 4 * time.Hour},
	}
	for _, tc := range cases {
		if got := adaptiveModelCooldown(tc.week, tc.known); got != tc.want {
			t.Errorf("adaptiveModelCooldown(%d, %v) = %s, want %s", tc.week, tc.known, got, tc.want)
		}
	}

	// Verify live runner uses 1h cooldown at >=70% weekly quota.
	home := t.TempDir()
	root := idleDreamProject(t, home)
	writeConfig(t, home, validConfig)
	now := runAt
	workerCalls := 0
	var out bytes.Buffer
	o := driftOpts(home, root, &out)
	o.Env = &fakeEnv{targets: targetsOf(root), usage: usageText("85%", "90%")}
	o.Now = func() time.Time { return now }
	o.Work = func(_ context.Context, _ isolate.WorkerOptions, paths ...string) ([]isolate.WorkerResult, error) {
		workerCalls++
		name := strings.TrimSuffix(filepath.Base(paths[0]), ".md")
		git(t, root, "branch", "work/"+name)
		return []isolate.WorkerResult{{Name: name, Verdict: "PASS"}}, nil
	}
	if err := Run(o, false); err != nil || workerCalls != 1 {
		t.Fatalf("run 1: err=%v workers=%d\n%s", err, workerCalls, out.String())
	}
	now = runAt.Add(30 * time.Minute)
	out.Reset()
	if err := Run(o, false); err != nil || workerCalls != 1 || !strings.Contains(out.String(), "G9 cooldown:") {
		t.Fatalf("run at +30m (85%% wk): err=%v workers=%d out:\n%s", err, workerCalls, out.String())
	}
	now = runAt.Add(61 * time.Minute)
	out.Reset()
	if err := Run(o, false); err != nil || workerCalls != 2 {
		t.Fatalf("run at +61m (85%% wk): err=%v workers=%d out:\n%s", err, workerCalls, out.String())
	}
}

func runV910InstallPendingBanner(t *testing.T) {
	home := t.TempDir()
	realHome := t.TempDir()
	root, _, _ := initHarnessEvalGateFixture(t, home, realHome, "")
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Work = makeCandidateBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		n := req.Runs
		return EvalOutcome{Row: req.Row, Passed: n, Graded: n, RecordID: "TEST-inst"}, nil
	}
	if err := Run(o, false); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	st, found, err := loadState(home)
	if err != nil || !found || !st.InstallPending || st.InstallPendingSHA == "" {
		t.Fatalf("state missing persisted InstallPending: found=%v err=%v st=%+v", found, err, st)
	}
	if _, err := os.Stat(filepath.Join(realHome, ".gemini")); !os.IsNotExist(err) {
		t.Fatalf("realHome .gemini was touched by auto apply: err=%v", err)
	}
	out.Reset()
	if err := Status(o); err != nil {
		t.Fatalf("Status: %v", err)
	}
	gotStatus := out.String()
	if !strings.Contains(gotStatus, "install") || !strings.Contains(gotStatus, "pending") || !strings.Contains(gotStatus, "fyr install") {
		t.Fatalf("Status output missing install pending operator reminder banner:\n%s", gotStatus)
	}
}

func runV910HaltReasonPersistence(t *testing.T) {
	// 1. G3 power halt persists HaltReason into metrics.db.
	homePower := t.TempDir()
	writeConfig(t, homePower, validConfig)
	var out bytes.Buffer
	oPower := runOpts(homePower, &out)
	oPower.GOOS = "linux"
	oPower.Env = &fakeEnv{supplies: []Supply{{Name: "BAT0", Type: "Battery", Online: "0"}}}
	if err := Run(oPower, false); err != nil {
		t.Fatalf("G3 power run: %v", err)
	}
	sPower, err := metrics.Open(metrics.Path(homePower))
	if err != nil {
		t.Fatalf("open metrics.db: %v", err)
	}
	rowsPower, err := sPower.QueryBackgroundRuns(time.Time{}, time.Time{})
	sPower.Close()
	if err != nil || len(rowsPower) != 1 || rowsPower[0].HaltReason == "" || !strings.Contains(rowsPower[0].HaltReason, "G3_POWER") {
		t.Fatalf("G3 power background_runs = %+v (err=%v), want HaltReason containing G3_POWER", rowsPower, err)
	}

	// 2. G9 budget floor halt persists HaltReason into metrics.db.
	homeBudget := t.TempDir()
	writeConfig(t, homeBudget, validConfig)
	root := idleProject(t)
	out.Reset()
	oBudget := runOpts(homeBudget, &out)
	oBudget.Env = &fakeEnv{targets: targetsOf(root), usage: usageText("10%", "90%")}
	if err := Run(oBudget, false); err != nil {
		t.Fatalf("G9 budget run: %v", err)
	}
	sBudget, err := metrics.Open(metrics.Path(homeBudget))
	if err != nil {
		t.Fatalf("open metrics.db: %v", err)
	}
	rowsBudget, err := sBudget.QueryBackgroundRuns(time.Time{}, time.Time{})
	sBudget.Close()
	if err != nil || len(rowsBudget) != 1 || rowsBudget[0].HaltReason == "" || !strings.Contains(rowsBudget[0].HaltReason, "G9_BUDGET") {
		t.Fatalf("G9 budget background_runs = %+v (err=%v), want HaltReason containing G9_BUDGET", rowsBudget, err)
	}
}
