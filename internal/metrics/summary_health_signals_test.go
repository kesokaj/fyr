package metrics

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

func TestMetricsV10BackgroundRuns(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)

	rows := []BackgroundRunRow{
		{TS: now.Add(-2 * time.Hour).UnixMilli(), Kind: "dream", Outcome: "pass", DurationMS: 1200, ProjectsChecked: 3, ProposalsWritten: 2, RatingsWritten: 4},
		{TS: now.Add(-1 * time.Hour).UnixMilli(), Kind: "auto", Outcome: "fail", DurationMS: 3400, ProjectsChecked: 2, WorkerRan: 1, Applied: 0},
		{TS: now.Add(-48 * time.Hour).UnixMilli(), Kind: "dream", Outcome: "pass", DurationMS: 500, ProjectsChecked: 1},
	}
	for _, r := range rows {
		if err := RecordBackgroundRun(home, r); err != nil {
			t.Fatalf("RecordBackgroundRun(%+v): %v", r, err)
		}
	}
	assertBackgroundRunValidationAndSummary(t, home, since, now)
}

func assertBackgroundRunValidationAndSummary(t *testing.T, home string, since, now time.Time) {
	t.Helper()
	s := openT(t, Path(home))
	for _, bad := range []BackgroundRunRow{
		{TS: 100, Kind: "invalid", Outcome: "pass", DurationMS: 10},
		{TS: 100, Kind: "dream", Outcome: "bad/path", DurationMS: 10},
		{TS: -1, Kind: "auto", Outcome: "pass", DurationMS: 10},
		{TS: 100, Kind: "auto", Outcome: "pass", DurationMS: 10, ProjectsChecked: -1},
	} {
		if err := s.InsertBackgroundRun(bad); err == nil {
			t.Fatalf("expected validation error for %+v", bad)
		}
	}
	got, err := s.QueryBackgroundRuns(since, now)
	if err != nil || len(got) != 2 || got[0].Kind != "auto" || got[1].Kind != "dream" {
		t.Fatalf("QueryBackgroundRuns = %+v, err=%v", got, err)
	}
	sum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if sum.Health.DreamRuns.Runs != 1 || sum.Health.DreamRuns.ProposalsWritten != 2 || sum.Health.AutoRuns.Failed != 1 || sum.Health.AutoRuns.WorkersRan != 1 {
		t.Fatalf("Health runs = dream:%+v auto:%+v", sum.Health.DreamRuns, sum.Health.AutoRuns)
	}
	expFile := filepath.Join(t.TempDir(), "export.jsonl")
	if err := s.Export(expFile); err != nil {
		t.Fatalf("Export: %v", err)
	}
	expBytes, err := os.ReadFile(expFile)
	if err != nil || !strings.Contains(string(expBytes), `"table":"background_runs"`) {
		t.Fatalf("Export missing background_runs: err=%v\n%s", err, string(expBytes))
	}
}

func TestSandboxFilterAndOptIn(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	alpha := filepath.Join(t.TempDir(), "alpha")
	realApp := "/work/real-app"
	s := openT(t, Path(home))
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)
	ts := now.Add(-time.Hour).UnixMilli()

	roots := []string{
		alpha, realApp, "/tmp/fyr-eval/s1", "/work/real-home-smoke-1",
		"/work/smoke-case", "/work/proj/.agents/scratch/sub",
		"/work/fyr-agy-probes-9", "/Users/alice", "/", home,
	}
	for i, r := range roots {
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES (?, 'agy', ?, ?, ?)`,
			fmt.Sprintf("sb-%d", i), r, ts+int64(i)*1000, ts+int64(i)*1000+500); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO turn_vets(project_root, conversation_id, turn_num, intent, gate, score, created_at) VALUES (?, ?, 1, 'TASK', 'PROCEED', 85, ?)`,
			r, fmt.Sprintf("sb-%d", i), ts+int64(i)*1000); err != nil {
			t.Fatal(err)
		}
	}
	assertSandboxFilterModes(t, s, home, alpha, since, now)
}

func assertSandboxFilterModes(t *testing.T, s *Store, home, alpha string, since, now time.Time) {
	t.Helper()
	defSum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(defSum.Projects) != 2 || defSum.E.TurnVets != 2 {
		t.Fatalf("default Projects=%d TurnVets=%d (%+v), want 2 and 2 (alpha and /work/real-app)", len(defSum.Projects), defSum.E.TurnVets, defSum.Projects)
	}
	hasAlpha := false
	for _, p := range defSum.Projects {
		if p.Project == filepath.Clean(alpha) {
			hasAlpha = true
		}
	}
	if !hasAlpha {
		t.Fatalf("temp fixture %s was wrongly filtered: %+v", alpha, defSum.Projects)
	}
	optIn, err := s.Show(ShowOptions{Home: home, Since: since, Now: now, IncludeSandboxes: true})
	if err != nil || len(optIn.Projects) <= len(defSum.Projects) || optIn.E.TurnVets <= defSum.E.TurnVets {
		t.Fatalf("IncludeSandboxes Projects=%+v TurnVets=%d, err=%v", optIn.Projects, optIn.E.TurnVets, err)
	}
	explicit, err := s.Show(ShowOptions{Home: home, Since: since, Now: now, Project: "/tmp/fyr-eval/s1"})
	if err != nil || explicit.Sessions["agy"] != 1 || explicit.E.TurnVets != 1 {
		t.Fatalf("explicit sandbox Project sessions=%+v TurnVets=%d, err=%v; want 1 and 1", explicit.Sessions, explicit.E.TurnVets, err)
	}
}

func TestDreamTrajectoryDigestKnee(t *testing.T) {
	t.Parallel()
	st, exec := openDreamDigestTestStore(t)
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	root := "/work/knee-proj"

	for i, sid := range []string{"k1", "k2", "k3", "k4"} {
		exec(`INSERT INTO sessions(id, vendor, project_root, started_at) VALUES (?, 'agy', ?, ?)`,
			sid, root, base.Add(time.Duration(i)*time.Minute).UnixMilli())
	}
	exec(`INSERT INTO steps(session_id, ext_id, ts, kind, context) VALUES ('k1', '1', 100, 'model', 180000)`)
	exec(`INSERT INTO steps(session_id, ext_id, ts, kind, context) VALUES ('k2', '1', 100, 'model', 40000)`)
	exec(`INSERT INTO events(session_id, ts, kind, ref, value) VALUES ('k2', 100, 'knee_exceeded', '', '1')`)
	exec(`INSERT INTO steps(session_id, ext_id, ts, kind, context) VALUES ('k3', '1', 100, 'model', 210000)`)
	exec(`INSERT INTO events(session_id, ts, kind, ref, value) VALUES ('k3', 100, 'KNEE_EXCEEDED', '', '1')`)
	exec(`INSERT INTO steps(session_id, ext_id, ts, kind, context) VALUES ('k4', '1', 100, 'model', 179999)`)

	d, err := st.DreamTrajectoryDigest(root, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("DreamTrajectoryDigest: %v", err)
	}
	if d.Sessions != 4 || d.KneeExceeded != 3 {
		t.Fatalf("digest = %+v, want Sessions=4 KneeExceeded=3", d)
	}
}

func TestSummaryNumbersV10(t *testing.T) {
	t.Parallel()
	score, eff, vet, rrPct := 4.25, 3.75, 82.5, 65.0
	sum := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
	sum.Sessions["agy"] = 5
	sum.A.RatingsCount = 5
	sum.A.MeanOutcomeScore = &score
	sum.A.MeanTokenEfficiency = &eff
	sum.C.RedundantReads = map[string]int{"agy": 4}
	sum.C.RangedReadPct = map[string]*float64{"agy": &rrPct}
	sum.E = SecondBrain{TurnVets: 9, MeanVetScore: &vet, VetFirstTurns: 7, BypassedTurns: 2, PivotsSeeded: 6, LedgerSaved: 11}
	sum.F = WorkSummary{Waves: 2, Workers: 4, Passed: 3, Failed: 1, JudgePassed: 3, JudgeFailed: 1, JudgeFallback: 1}
	sum.Health.SessionsOverKnee = 2
	sum.Health.MainSessions = 5
	sum.Health.GuardedDowngrades = 1
	sum.Health.Structural = search.StructuralSignals{
		HiddenCouplingPairs: 3, HiddenCouplingEditPairs: 3, HiddenCouplingReadPairs: 2,
		HiddenCouplingRate: 0.5, ProdPkgCycles: 1, TotalCodeFiles: 10, TotalProdPkgs: 2,
	}
	sum.Health.DreamRuns = BackgroundKindStats{Runs: 2, Passed: 2, ProposalsWritten: 3}
	sum.Health.AutoRuns = BackgroundKindStats{Runs: 1, Failed: 1, WorkersRan: 1}

	assertNumbersV10Keys(t, sum)
}

func assertNumbersV10Keys(t *testing.T, sum *Summary) {
	t.Helper()
	n := sum.Numbers()
	wants := map[string]float64{
		"A.ratings_count":                         5,
		"A.mean_outcome_score":                    4.25,
		"A.mean_token_efficiency":                 3.75,
		"C.redundant_reads.agy":                   4,
		"C.ranged_read_pct.agy":                   65,
		"E.turn_vets":                             9,
		"E.mean_vet_score":                        82.5,
		"F.workers":                               4,
		"F.judge_failed":                          1,
		"H.sessions_over_knee":                    2,
		"H.guarded_downgrades":                    1,
		"H.structural.hidden_coupling_pairs":      3,
		"H.structural.hidden_coupling_edit_pairs": 3,
		"H.structural.hidden_coupling_read_pairs": 2,
		"H.structural.prod_pkg_cycles":            1,
		"H.background_runs.total":                 3,
		"H.background_runs.dream.runs":            2,
		"H.background_runs.auto.workers_ran":      1,
	}
	for k, want := range wants {
		if got, ok := n[k]; !ok || got != want {
			t.Errorf("Numbers()[%q] = (%v, %v), want %v", k, got, ok, want)
		}
	}
	sum.A.MeanOutcomeScore = nil
	sum.Health.Structural = search.StructuralSignals{}
	sum.Health.DreamRuns = BackgroundKindStats{}
	sum.Health.AutoRuns = BackgroundKindStats{}
	n2 := sum.Numbers()
	for _, absent := range []string{"A.mean_outcome_score", "H.structural.prod_pkg_cycles", "H.structural.hidden_coupling_edit_pairs", "H.background_runs.total"} {
		if _, ok := n2[absent]; ok {
			t.Errorf("expected %q to be absent when empty", absent)
		}
	}
}

func TestStructuralHealthSignals(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	proj := seedStructuralRepoWithSearchDB(t, home)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)

	s := openT(t, Path(home))
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES ('s-prev', 'agy', ?, ?, ?), ('s-cur', 'agy', ?, ?, ?)`,
		proj, since.Add(-2*time.Hour).UnixMilli(), since.Add(-time.Hour).UnixMilli(),
		proj, now.Add(-2*time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	sum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	st := sum.Health.Structural
	if st.TotalCodeFiles < 2 || st.HiddenCouplingPairs != 2 || st.HiddenCouplingEditPairs != 1 || st.HiddenCouplingReadPairs != 1 || st.GodNodeClampHits < 2 {
		t.Fatalf("Structural = %+v, want >=2 files, total=2 edit=1 read=1 coupling pairs, >=2 clamp hits", st)
	}
	if sum.Diagnostics != nil {
		for _, sig := range sum.Diagnostics.Signals {
			if sig.Rule == "hidden_coupling" {
				t.Fatalf("unexpected hidden_coupling warning with edit=1 read=1: %+v", sig)
			}
		}
	}
	if sum.Deltas == nil || sum.Deltas.PreviousSessions != 1 {
		t.Fatalf("Deltas = %+v, want PreviousSessions=1", sum.Deltas)
	}
	assertStructuralAccSplitCoupling(t)
}

func assertStructuralAccSplitCoupling(t *testing.T) {
	t.Helper()
	var acc structuralAcc
	acc.add(search.StructuralSignals{HiddenCouplingPairs: 2, HiddenCouplingEditPairs: 1, HiddenCouplingReadPairs: 1, TotalCodeFiles: 4})
	acc.add(search.StructuralSignals{HiddenCouplingPairs: 3, HiddenCouplingEditPairs: 2, HiddenCouplingReadPairs: 1, TotalCodeFiles: 4})
	got := acc.finish()
	if got.HiddenCouplingPairs != 5 || got.HiddenCouplingEditPairs != 3 || got.HiddenCouplingReadPairs != 2 {
		t.Fatalf("structuralAcc split mismatch: %+v", got)
	}
	sum := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
	sum.Health.Structural = got
	rep := BuildDiagnosticReport(*sum, nil, "global", "")
	if len(rep.Signals) != 1 || rep.Signals[0].Rule != "hidden_coupling" {
		t.Fatalf("expected co_edit=3 to trigger hidden_coupling, got %+v", rep.Signals)
	}
}

func seedStructuralRepoWithSearchDB(t *testing.T, home string) string {
	t.Helper()
	proj := filepath.Join(t.TempDir(), "svc")
	for rel, body := range map[string]string{
		".gitignore": ".agents/scratch/*\n",
		"go.mod":     "module example.com/svc\n\ngo 1.24\n",
		"alpha/a.go": "package alpha\n\nfunc Alpha() int { return 1 }\n",
		"beta/b.go":  "package beta\n\nfunc Beta() int { return 2 }\n",
		"gamma/c.go": "package gamma\n\nfunc Gamma() int { return 3 }\n",
	} {
		full := filepath.Join(proj, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "t"}, {"config", "user.email", "t@example.com"}, {"add", "."}, {"commit", "-q", "-m", "seed"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = proj
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := search.Index(search.Options{Home: home, Dir: proj}, io.Discard); err != nil {
		t.Fatalf("search.Index: %v", err)
	}
	dbPath := filepath.Join(proj, ".agents", "scratch", "search.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO co_activity(file_a, file_b, kind, count, updated_at) VALUES
		('alpha/a.go', 'beta/b.go', 'co_modified', 4, 100),
		('beta/b.go', 'gamma/c.go', 'co_read', 5, 100)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at) VALUES ('c1', 0, ?, -5, -5, 'h1', 1, 2, 100)`,
		filepath.Join(proj, "alpha/a.go")); err != nil {
		t.Fatal(err)
	}
	return proj
}
