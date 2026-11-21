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
