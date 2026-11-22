package state

import (
	"archive/zip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
)

func initGitRepo(t *testing.T, remoteURL string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", remoteURL}} {
		if remoteURL == "" && args[0] == "remote" {
			continue
		}
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func seedSourceState(t *testing.T) (home, dirAlpha, dirBeta string, tOld, tNew time.Time) {
	t.Helper()
	home = t.TempDir()
	dirAlpha, dirBeta = initGitRepo(t, "git@github.com:org/alpha.git"), initGitRepo(t, "https://github.com/org/beta.git")
	tOld = time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	tNew = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	oAOld := brain.Options{Home: home, Dir: dirAlpha, Now: func() time.Time { return tOld }}
	oANew := brain.Options{Home: home, Dir: dirAlpha, Now: func() time.Time { return tNew }}
	oBNew := brain.Options{Home: home, Dir: dirBeta, Now: func() time.Time { return tNew }}
	oGlob := brain.Options{Home: home, Global: true, Now: func() time.Time { return tNew }}
	for _, c := range []struct {
		o  brain.Options
		in brain.AddInput
	}{
		{oAOld, brain.AddInput{Kind: "adr", ID: "ADR-001", Title: "Alpha old ADR", Body: strings.NewReader("Immutable log")}},
		{oANew, brain.AddInput{Kind: "change", Title: "Alpha recent change", Body: strings.NewReader("Implemented Nebula sync")}},
		{oBNew, brain.AddInput{Kind: "bug", ID: "BUG-001", Title: "Beta bug", Body: strings.NewReader("Beta symptom")}},
		{oGlob, brain.AddInput{Kind: "feedback", Title: "Alpha feedback", Project: dirAlpha, Body: strings.NewReader("never skip tests")}},
	} {
		if _, err := brain.Add(c.o, c.in); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = brain.MemoryAdd(oANew, brain.MemoryEntry{Type: "procedural", Name: "alpha-op", Description: "op", Provenance: "operator"}, strings.NewReader("alpha sacred operator rule"))
	_, _ = brain.MemoryAdd(oANew, brain.MemoryEntry{Type: "semantic", Name: "alpha-der", Description: "der", Provenance: "derived"}, strings.NewReader("alpha derived rule"))
	_, _ = brain.ProposeAll(home, dirAlpha, []brain.Proposal{{Title: "Alpha proposal", Body: "tighten lint"}}, tNew)
	for _, tr := range []metrics.TrialInput{
		{Run: "bench/alpha-old/1", Label: "eval=alpha", Status: "pass", StartedAt: tOld, Seconds: 4, Model: "gemini-4-neon-medium", ProjectRoot: dirAlpha, RepoID: "git:github.com/org/alpha"},
		{Run: "bench/alpha-new/1", Label: "eval=alpha", Status: "pass", StartedAt: tNew, Seconds: 5, Model: "gemini-4-neon-high", ProjectRoot: dirAlpha, RepoID: "git:github.com/org/alpha"},
		{Run: "bench/beta-new/1", Label: "eval=beta", Status: "fail", Cause: "check", StartedAt: tNew, Seconds: 6, Model: "gemini-4-neon-low", ProjectRoot: dirBeta, RepoID: "git:github.com/org/beta"},
	} {
		if err := metrics.RecordTrial(home, tr); err != nil {
			t.Fatal(err)
		}
	}
	_ = metrics.RecordJudgeVerdict(home, metrics.JudgeVerdictRow{
		TS: tNew.UnixMilli(), ProjectRoot: dirAlpha, RepoID: "git:github.com/org/alpha", Target: "work/p5",
		Model: "claude-opus-5-5-high", WorkerModel: "gemini-4-neon-high", WorkerSessionID: "trial:bench/alpha-new/1", CriterionHash: "crit1", Verdict: "PASS",
	})
	return home, dirAlpha, dirBeta, tOld, tNew
}

func TestExportDefaultExcludesMemoryAndWritesAtomic0600Manifest(t *testing.T) {
	srcHome, dirAlpha, _, _, tNew := seedSourceState(t)
	outZip := filepath.Join(t.TempDir(), "default.zip")
	res, err := Export(ExportOptions{Home: srcHome, Dir: dirAlpha, OutPath: outZip, FyrVersion: "v9.6.0-test", Now: func() time.Time { return tNew }})
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(outZip); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat %s: fi=%v err=%v", outZip, fi, err)
	}
	if res.Manifest.FyrVersion != "v9.6.0-test" || res.Manifest.BrainSchema != MaxBrainSchema || res.Manifest.MetricsSchema != MaxMetricsSchema || res.Manifest.IncludeMemory {
		t.Errorf("unexpected manifest: %+v", res.Manifest)
	}
	for _, forbidden := range []string{"memory", "feedback", "proposal"} {
		if slices.Contains(res.Manifest.RecordClasses, forbidden) {
			t.Errorf("leaked class %q in %v", forbidden, res.Manifest.RecordClasses)
		}
	}
	if res.Manifest.Counts.BrainRecords != 3 || len(res.Manifest.SHA256[BrainRecordsEntry]) != 64 || len(res.Manifest.SHA256[MetricsSanitizedEntry]) != 64 {
		t.Errorf("unexpected counts/sha256: %+v", res.Manifest)
	}
	zr, err := zip.OpenReader(outZip)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != 3 {
		t.Fatalf("zip entries = %d, want 3", len(zr.File))
	}
}

func TestRoundTripIncludeMemoryDryRunIdempotencyAndOperatorGuard(t *testing.T) {
	srcHome, dirAlpha, _, _, tNew := seedSourceState(t)
	bundle := filepath.Join(t.TempDir(), "with-mem.zip")
	if expRes, err := Export(ExportOptions{Home: srcHome, Dir: dirAlpha, IncludeMemory: true, OutPath: bundle, FyrVersion: "v9.6.0", Now: func() time.Time { return tNew }}); err != nil || !expRes.Manifest.IncludeMemory || expRes.Manifest.Counts.BrainRecords != 7 {
		t.Fatalf("Export --include-memory = %+v, %v", expRes, err)
	}
	dstHome, dstAlpha := t.TempDir(), initGitRepo(t, "https://github.com/org/alpha.git")
	if dryRes, err := Import(ImportOptions{Home: dstHome, Dir: dstAlpha, BundlePath: bundle, IncludeMemory: true, DryRun: true}); err != nil || !dryRes.DryRun || dryRes.Insert == 0 || dryRes.Brain.Inserted != 7 {
		t.Fatalf("Import --dry-run = %+v, %v", dryRes, err)
	}
	if entries, err := os.ReadDir(dstHome); err != nil || len(entries) != 0 {
		t.Fatalf("dry-run wrote into dstHome: %v (%v)", entries, err)
	}
	if noMemRes, err := Import(ImportOptions{Home: dstHome, Dir: dstAlpha, BundlePath: bundle}); err != nil || noMemRes.Brain.Inserted != 3 {
		t.Fatalf("Import default = %+v, %v", noMemRes, err)
	}
	if memCards, err := brain.MemoryShow(brain.Options{Home: dstHome, Dir: dstAlpha}); err != nil || memCards != "" {
		t.Fatalf("memory leaked without --include-memory: %q (%v)", memCards, err)
	}
	oDst := brain.Options{Home: dstHome, Dir: dstAlpha, Now: func() time.Time { return tNew.Add(-24 * time.Hour) }}
	_, _ = brain.MemoryAdd(oDst, brain.MemoryEntry{Type: "semantic", Name: "alpha-der", Description: "pinned", Provenance: "operator"}, strings.NewReader("sacred dst operator body"))
	if withMemRes, err := Import(ImportOptions{Home: dstHome, Dir: dstAlpha, BundlePath: bundle, IncludeMemory: true}); err != nil || withMemRes.OperatorPreserved != 1 || withMemRes.Conflict != 1 || withMemRes.Brain.Inserted != 3 {
		t.Fatalf("Import --include-memory = %+v, %v", withMemRes, err)
	}
	if gotPinned, _, err := brain.Get(oDst, []string{"MEMORY-alpha-der"}); err != nil || gotPinned[0].Provenance != "operator" || !strings.Contains(gotPinned[0].Body, "sacred dst operator body") {
		t.Fatalf("operator provenance overwritten: %+v (%v)", gotPinned, err)
	}
	if hits, err := brain.Search(oDst, []string{"nebula"}, "", 10); err != nil || len(hits) != 1 {
		t.Fatalf("records_fts search = %+v (%v)", hits, err)
	}
	if idemRes, err := Import(ImportOptions{Home: dstHome, Dir: dstAlpha, BundlePath: bundle, IncludeMemory: true}); err != nil || idemRes.Insert != 0 || idemRes.Updated != 0 {
		t.Fatalf("re-import not idempotent: %+v (%v)", idemRes, err)
	}
}

func TestRepoCurrentSinceSymlinkAndEmptyManifest(t *testing.T) {
	srcHome, dirAlpha, _, _, tNew := seedSourceState(t)
	expFiltered, err := Export(ExportOptions{
		Home: srcHome, Dir: dirAlpha, Repo: "current",
		Since: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), OutPath: filepath.Join(t.TempDir(), "alpha-since.zip"), Now: func() time.Time { return tNew },
	})
	if err != nil || expFiltered.Manifest.Counts.BrainRecords != 1 || expFiltered.Manifest.Counts.Trials != 1 || !slices.Equal(expFiltered.Manifest.RepoIDs, []string{"git:github.com/org/alpha"}) {
		t.Fatalf("filtered export = %+v (%v)", expFiltered.Manifest, err)
	}
	allZip := filepath.Join(t.TempDir(), "all.zip")
	if _, err := Export(ExportOptions{Home: srcHome, Dir: dirAlpha, OutPath: allZip}); err != nil {
		t.Fatal(err)
	}
	dstBeta := initGitRepo(t, "git@github.com:org/beta.git")
	if impBeta, err := Import(ImportOptions{Home: t.TempDir(), Dir: dstBeta, BundlePath: allZip, Repo: "current"}); err != nil || impBeta.Brain.Inserted != 1 || impBeta.Metrics.Sessions != 1 || impBeta.Metrics.Trials != 1 || impBeta.Metrics.JudgeVerdicts != 0 {
		t.Fatalf("Import --repo current (beta) = %+v, %v", impBeta, err)
	}
	victim, linkOut := filepath.Join(t.TempDir(), "victim.txt"), filepath.Join(t.TempDir(), "link.zip")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, linkOut); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(ExportOptions{Home: srcHome, OutPath: linkOut}); err == nil {
		t.Fatal("Export followed symlink OutPath")
	}
	emptyRes, err := Export(ExportOptions{Home: t.TempDir(), OutPath: filepath.Join(t.TempDir(), "empty.zip")})
	if err != nil {
		t.Fatal(err)
	}
	rawM, err := json.Marshal(emptyRes.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawM), ":null") {
		t.Errorf("null slices in manifest: %s", string(rawM))
	}
}

func TestV910MetricsV14AndStateRoundTrip(t *testing.T) {
	srcHome, dirAlpha, _, tNow := seedV14MetricsSource(t)
	bundle := filepath.Join(t.TempDir(), "v14-state.zip")
	expRes, err := Export(ExportOptions{
		Home: srcHome, Dir: dirAlpha, OutPath: bundle,
		FyrVersion: "v9.10.0-test", Now: func() time.Time { return tNow },
	})
	if err != nil {
		t.Fatalf("Export v14: %v", err)
	}
	m := expRes.Manifest
	if m.MetricsSchema != 14 || m.Counts.OrchestrationRuns != 2 || m.Counts.BackgroundRuns != 1 || m.Counts.TurnVets != 1 || m.Counts.Trials != 1 {
		t.Fatalf("unexpected v14 manifest: %+v", m)
	}
	if !slices.Contains(m.RepoIDs, "git:github.com/org/alpha") || !slices.Contains(m.RepoIDs, "git:github.com/org/beta") {
		t.Fatalf("manifest RepoIDs missing orchestration repos: %v", m.RepoIDs)
	}
	rawMetrics, _, _ := readZipEntry(t, bundle, MetricsSanitizedEntry)
	if strings.Contains(string(rawMetrics), dirAlpha) || strings.Contains(string(rawMetrics), "project_root") {
		t.Fatalf("metrics_sanitized.ndjson leaked host project_root:\n%s", string(rawMetrics))
	}

	assertV14ImportAndIdempotency(t, bundle, tNow)
	assertV13LegacyCompatAndV15Refusal(t)
}

func seedV14MetricsSource(t *testing.T) (home, dirAlpha, dirBeta string, tNow time.Time) {
	t.Helper()
	home = t.TempDir()
	dirAlpha = initGitRepo(t, "git@github.com:org/alpha.git")
	dirBeta = initGitRepo(t, "https://github.com/org/beta.git")
	tNow = time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)

	if err := metrics.RecordBackgroundRun(home, metrics.BackgroundRunRow{
		TS: tNow.Add(-time.Hour).UnixMilli(), Kind: "auto", Outcome: "halted",
		HaltReason: "BUDGET_FLOOR", DurationMS: 800, ProjectsChecked: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := metrics.RecordTrial(home, metrics.TrialInput{
		Run: "ab/golang/1", Label: "eval=ab", Status: "pass", StartedAt: tNow.Add(-30 * time.Minute),
		Seconds: 9, Model: "gemini-4-neon-high", PolicyModel: "gemini-4-neon-xhigh",
		ProjectRoot: dirAlpha, RepoID: "git:github.com/org/alpha",
		Skill: "golang", Arm: "treatment", Tokens: 12450, Defects: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := metrics.RecordOrchestrationRun(home, metrics.OrchestrationRunRow{
		ID: "orch-alpha-1", TS: tNow.Add(-20 * time.Minute).Format(time.RFC3339),
		Kind: "relay", ProjectRoot: dirAlpha, RepoID: "git:github.com/org/alpha",
		Status: "HALTED", HaltCode: "ZERO_PROGRESS", Units: 3, DefectRounds: 2,
		Passed: 2, Failed: 1, DurationMS: 31000,
		WorkerModel: "gemini-4-neon-high", JudgeModel: "claude-opus-5-5-high", Verdict: "FAIL",
	}); err != nil {
		t.Fatal(err)
	}
	if err := metrics.RecordOrchestrationRun(home, metrics.OrchestrationRunRow{
		ID: "orch-beta-1", TS: tNow.Add(-10 * time.Minute).Format(time.RFC3339),
		Kind: "swarm", ProjectRoot: dirBeta, RepoID: "git:github.com/org/beta",
		Status: "COMPLETE", Units: 2, Passed: 2, DurationMS: 19000,
		WorkerModel: "gemini-4-neon-medium", JudgeModel: "claude-sonnet-4-6", Verdict: "PASS",
	}); err != nil {
		t.Fatal(err)
	}
	ms, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	if _, err := metrics.ImportSanitizedState(ms, metrics.SanitizedMetricsExport{
		Schema: 14,
		TurnVets: []metrics.SanitizedTurnVet{{
			RepoID: "git:github.com/org/alpha", ConversationID: "conv-v14", TurnNum: 1,
			Intent: "FEATURE", Gate: "PROCEED", Score: 92, SuggestedSkills: "fyr-feature,golang",
			CreatedAt: tNow.Add(-15 * time.Minute).UnixMilli(),
		}},
	}, false); err != nil {
		t.Fatal(err)
	}
	return home, dirAlpha, dirBeta, tNow
}

func assertV14ImportAndIdempotency(t *testing.T, bundle string, tNow time.Time) {
	t.Helper()
	dstHome := t.TempDir()
	dryRes, err := Import(ImportOptions{Home: dstHome, BundlePath: bundle, DryRun: true})
	if err != nil || !dryRes.DryRun || dryRes.Metrics.OrchestrationRuns != 2 || dryRes.Metrics.BackgroundRuns != 1 || dryRes.Metrics.TurnVets != 1 || dryRes.Metrics.Trials != 1 {
		t.Fatalf("dry-run v14 import = %+v, err=%v", dryRes, err)
	}
	if entries, rerr := os.ReadDir(dstHome); rerr != nil || len(entries) != 0 {
		t.Fatalf("dry-run wrote to dstHome: %v (%v)", entries, rerr)
	}
	liveRes, err := Import(ImportOptions{Home: dstHome, BundlePath: bundle})
	if err != nil || liveRes.Metrics != dryRes.Metrics {
		t.Fatalf("live v14 import = %+v, err=%v; want metrics %+v", liveRes, err, dryRes.Metrics)
	}
	idemRes, err := Import(ImportOptions{Home: dstHome, BundlePath: bundle})
	if err != nil || idemRes.Insert != 0 || idemRes.Updated != 0 {
		t.Fatalf("v14 re-import not idempotent: %+v, err=%v", idemRes, err)
	}
	ms, err := metrics.Open(metrics.Path(dstHome))
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	exp, err := metrics.ExportSanitizedState(ms, "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(exp.BackgroundRuns) != 1 || exp.BackgroundRuns[0].HaltReason != "BUDGET_FLOOR" {
		t.Fatalf("round-trip BackgroundRuns = %+v", exp.BackgroundRuns)
	}
	if len(exp.TurnVets) != 1 || exp.TurnVets[0].SuggestedSkills != "fyr-feature,golang" {
		t.Fatalf("round-trip TurnVets = %+v", exp.TurnVets)
	}
	if len(exp.Trials) != 1 || exp.Trials[0].Skill != "golang" || exp.Trials[0].Arm != "treatment" || exp.Trials[0].Tokens != 12450 || exp.Trials[0].Defects != 1 {
		t.Fatalf("round-trip Trials = %+v", exp.Trials)
	}
	if len(exp.OrchestrationRuns) != 2 || exp.OrchestrationRuns[0].ID != "orch-alpha-1" || exp.OrchestrationRuns[0].HaltCode != "ZERO_PROGRESS" || exp.OrchestrationRuns[0].DefectRounds != 2 || exp.OrchestrationRuns[0].ProjectRoot != "" {
		t.Fatalf("round-trip OrchestrationRuns = %+v", exp.OrchestrationRuns)
	}
	// Repo-filtered import keeps only beta's orchestration run.
	betaHome := t.TempDir()
	betaRes, err := Import(ImportOptions{Home: betaHome, BundlePath: bundle, Repo: "git:github.com/org/beta"})
	if err != nil || betaRes.Metrics.OrchestrationRuns != 1 || betaRes.Metrics.Trials != 0 || betaRes.Metrics.TurnVets != 0 {
		t.Fatalf("repo-filtered v14 import = %+v, err=%v", betaRes, err)
	}
	_ = tNow
}

func assertV13LegacyCompatAndV15Refusal(t *testing.T) {
	t.Helper()
	// Legacy v13 bundle omitting halt_reason, suggested_skills, trials A/B cols, and orchestration_runs.
	v13Metrics := []byte(strings.Join([]string{
		`{"table":"background_runs","row":{"ts":1763553600000,"kind":"auto","outcome":"pass","duration_ms":300,"projects_checked":1,"proposals_written":0,"ratings_written":0,"worker_ran":0,"applied":0}}`,
		`{"table":"turn_vets","row":{"repo_id":"git:github.com/org/legacy","conversation_id":"conv-v13","turn_num":1,"intent":"TASK","gate":"PROCEED","score":85,"created_at":1763553600000}}`,
		`{"table":"trials","row":{"run":"legacy-v13/1","label":"eval=row1","harness":"v9.9.0","status":"pass","cause":"","started_at":1763553600000,"seconds":4,"policy_model":"gemini-4-neon-high"}}`,
	}, "\n") + "\n")
	v13Zip := writeCustomZip(t, []rawZipSpec{
		{name: ManifestEntry, body: validManifestBytes(t, 5, 13, nil, v13Metrics)},
		{name: BrainRecordsEntry},
		{name: MetricsSanitizedEntry, body: v13Metrics},
	})
	v13Home := t.TempDir()
	res, err := Import(ImportOptions{Home: v13Home, BundlePath: v13Zip})
	if err != nil || res.Metrics.BackgroundRuns != 1 || res.Metrics.TurnVets != 1 || res.Metrics.Trials != 1 || res.Metrics.OrchestrationRuns != 0 {
		t.Fatalf("legacy v13 import = %+v, err=%v", res, err)
	}

	// Future v15 bundle must be refused BEFORE touching target home or opening SQLite.
	v15Zip := writeCustomZip(t, []rawZipSpec{
		{name: ManifestEntry, body: validManifestBytes(t, 5, 15, nil, nil)},
		{name: BrainRecordsEntry},
		{name: MetricsSanitizedEntry},
	})
	assertRejectedNoDBTouch(t, v15Zip, "unsupported metrics_schema 15")
}
