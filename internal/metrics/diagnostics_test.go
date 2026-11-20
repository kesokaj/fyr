package metrics

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

func TestDiagnosticReportShapeOnly(t *testing.T) {
	home := t.TempDir()
	s := openT(t, Path(home))
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)
	sensitiveRoot := "/Users/alice/private-corp/payments-api"
	seedDiagnosticStoreFixture(t, s, sensitiveRoot, now)

	sumGlobal, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show global: %v", err)
	}
	sumProj, err := s.Show(ShowOptions{Home: home, Since: since, Now: now, Project: sensitiveRoot})
	if err != nil {
		t.Fatalf("Show project: %v", err)
	}
	assertDiagnosticReportNoLeak(t, sumGlobal.Diagnostics, "sig:global:", sensitiveRoot, home)
	assertDiagnosticReportNoLeak(t, sumProj.Diagnostics, "sig:proj-", sensitiveRoot, home)
}

func seedDiagnosticStoreFixture(t *testing.T, s *Store, root string, now time.Time) {
	t.Helper()
	ts := now.Add(-2 * time.Hour).UnixMilli()
	for i := 0; i < 6; i++ {
		sid := fmt.Sprintf("conv-canary-%d", i)
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES (?, 'agy', ?, ?, ?)`,
			sid, root, ts+int64(i)*1000, ts+int64(i)*1000+500); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, is_error, context, cmd_hash) VALUES (?, 'm1', ?, 'model', '', 0, 210000, ''), (?, 't1', ?, 'tool', 'Grep', 1, 0, '/Users/alice/raw-arg'), (?, 't2', ?, 'tool', 'Bash', 1, 0, 'canary-cmd')`,
			sid, ts+10, sid, ts+20, sid, ts+30); err != nil {
			t.Fatal(err)
		}
	}
	var ratings []SessionRating
	for i := 0; i < 5; i++ {
		ratings = append(ratings, SessionRating{
			SessionID: fmt.Sprintf("conv-canary-%d", i), ProjectRoot: root, EvaluatedAt: ts,
			OutcomeScore: 2, TokenEfficiency: 2, PlanAlignment: "missed", SkillAction: "rewrite-skill",
			TargetSkill: "skills/golang/SKILL.md", Summary: "leaked-prose about /Users/alice/private-corp",
		})
	}
	if err := s.UpsertSessionRatings(ratings); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.InsertBackgroundRun(BackgroundRunRow{TS: ts + int64(i), Kind: "dream", Outcome: "fail", DurationMS: 100}); err != nil {
			t.Fatal(err)
		}
	}
}

func assertDiagnosticReportNoLeak(t *testing.T, rep *DiagnosticReport, wantPrefix, sensitiveRoot, home string) {
	t.Helper()
	if rep == nil || len(rep.Signals) == 0 {
		t.Fatalf("expected non-empty DiagnosticReport for %s, got %+v", wantPrefix, rep)
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{
		sensitiveRoot, "Users/alice", "private-corp", "payments-api",
		"conv-canary", "leaked-prose", "raw-arg", "canary-cmd", "skills/golang", home,
	} {
		if forbidden != "" && strings.Contains(text, forbidden) {
			t.Fatalf("DiagnosticReport JSON leaked %q:\n%s", forbidden, text)
		}
	}
	for i, sig := range rep.Signals {
		if !strings.HasPrefix(sig.SignalID, wantPrefix) || strings.Contains(sig.SignalID, "/") {
			t.Fatalf("invalid SignalID %q (want prefix %q)", sig.SignalID, wantPrefix)
		}
		if i > 0 && severityRank(rep.Signals[i-1].Severity) < severityRank(sig.Severity) {
			t.Fatalf("signals out of severity order: %+v", rep.Signals)
		}
	}
}

func TestDiagnosticReportDedupAndMinSamples(t *testing.T) {
	er, sr, lowScore := 0.50, 0.10, 2.0
	under := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
	under.Sessions["agy"] = 4
	under.Health.MainSessions = 4
	under.Health.SessionsOverKnee = 4
	under.B.ToolCalls["agy"] = 20
	under.D.ToolErrorRate["agy"] = &er
	under.C.GrepCalls["agy"] = 10
	under.C.SearchGrepRatio["agy"] = &sr
	under.A.RatingsCount = 4
	under.A.MeanOutcomeScore = &lowScore
	under.F = WorkSummary{Workers: 2, Failed: 2}
	under.Health.DreamRuns = BackgroundKindStats{Runs: 2, Failed: 2}
	under.Health.Structural = search.StructuralSignals{HiddenCouplingPairs: 2}

	rep := BuildDiagnosticReport(*under, []EvalRow{{Kind: "capability", N: 2, C: 0}}, "global", "")
	if len(rep.Signals) != 0 {
		t.Fatalf("below-min-sample inputs triggered signals: %+v", rep.Signals)
	}
	assertDiagnosticDedupAndCycleMin(t, under)
}

func assertDiagnosticDedupAndCycleMin(t *testing.T, base *Summary) {
	t.Helper()
	base.Health.Structural.ProdPkgCycles = 1
	base.Health.Structural.TotalProdPkgs = 4
	rep := BuildDiagnosticReport(*base, nil, "project", "/work/svc")
	if len(rep.Signals) != 1 || rep.Signals[0].Rule != "prod_pkg_cycles" || rep.Signals[0].Severity != "high" {
		t.Fatalf("want single high prod_pkg_cycles signal, got %+v", rep.Signals)
	}

	dups := dedupAndSortSignals([]DiagnosticSignal{
		{SignalID: "sig:global:b_rule", Severity: "low", Rule: "b_rule"},
		{SignalID: "sig:global:a_rule", Severity: "medium", Rule: "a_rule"},
		{SignalID: "sig:global:b_rule", Severity: "high", Rule: "b_rule"},
		{SignalID: "sig:global:c_rule", Severity: "medium", Rule: "c_rule"},
	})
	if len(dups) != 3 || dups[0].SignalID != "sig:global:b_rule" || dups[0].Severity != "high" ||
		dups[1].SignalID != "sig:global:a_rule" || dups[2].SignalID != "sig:global:c_rule" {
		t.Fatalf("dedupAndSortSignals = %+v", dups)
	}
}
