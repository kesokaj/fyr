package metrics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestReportJSONAndEmptyCost(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -7)
	ahome, _ := agyFixture(t)
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Home: ahome})
	sum, err := s.Show(ShowOptions{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}

	// Empty cost across all series suppresses "Cost per day (USD)" SVG chart.
	var htmlBuf bytes.Buffer
	if err := RenderReport(&htmlBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(htmlBuf.String(), "Cost per day (USD)") {
		t.Error("agy-only report with nil CostUSD drew Cost per day (USD) chart")
	}

	// RenderJSONReport & WriteJSONReport (0600, O_EXCL refusing overwrite).
	var jsonBuf bytes.Buffer
	if err := RenderJSONReport(&jsonBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	var parsed JSONReport
	if err := json.Unmarshal(jsonBuf.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal RenderJSONReport: %v\n%s", err, jsonBuf.String())
	}
	if parsed.GeneratedAt != "2026-11-20T12:00:00Z" || parsed.Scorecard.ToolCalls != sum.B.ToolCalls["agy"] {
		t.Errorf("unexpected JSONReport: %+v", parsed)
	}
	jsonOut := filepath.Join(t.TempDir(), "nested", "report.json")
	if err := WriteJSONReport(sum, nil, jsonOut, now); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(jsonOut)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat %s: err=%v mode=%v, want 0600", jsonOut, err, fi.Mode().Perm())
	}
	if err := WriteJSONReport(sum, nil, jsonOut, now); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second WriteJSONReport = %v, want refusing to overwrite", err)
	}

	// DailyReportPath and WriteDailyReport quiet O_EXCL no-op on same day.
	home := t.TempDir()
	dailyPath := DailyReportPath(home, now)
	wantSuffix := filepath.Join(harness.StateDir, "reports", "2026-11-20-daily-metrics.html")
	if !strings.HasSuffix(dailyPath, wantSuffix) {
		t.Fatalf("DailyReportPath = %q, want suffix %q", dailyPath, wantSuffix)
	}
	if err := WriteDailyReport(sum, nil, home, since); err != nil {
		t.Fatalf("first WriteDailyReport: %v", err)
	}
	firstFile := DailyReportPath(home, since)
	info1, err := os.Stat(firstFile)
	if err != nil || info1.Mode().Perm() != 0o600 {
		t.Fatalf("daily report mode: err=%v mode=%v", err, info1.Mode().Perm())
	}
	sentinel := []byte("<!-- first daily write -->")
	if err := os.WriteFile(firstFile, sentinel, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteDailyReport(sum, nil, home, since); err != nil {
		t.Fatalf("second same-day WriteDailyReport = %v, want quiet nil", err)
	}
	gotBytes, err := os.ReadFile(firstFile)
	if err != nil || !bytes.Equal(gotBytes, sentinel) {
		t.Fatalf("second WriteDailyReport mutated existing file: %q", gotBytes)
	}
}

func TestReportScorecardAndAutonomy(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -7)
	prevTS := since.Add(-24 * time.Hour).UnixMilli()
	curTS := now.Add(-2 * time.Hour).UnixMilli()

	home := t.TempDir()
	proj := filepath.Join(t.TempDir(), "repo")
	seedGitBranchesForAutonomyT(t, proj, "work/auto-fix-1", "work/auto-fix-2", "feature/other")

	s := openT(t, Path(home))
	// Prior window session for deltas.
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, model, started_at, ended_at) VALUES ('P1', 'agy', ?, 'gemini-4-carbon', ?, ?)`,
		proj, prevTS, prevTS+30000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, context) VALUES ('P1', 'pu1', ?, 'user', '', 0), ('P1', 'pg1', ?, 'generation', '', 100000), ('P1', 'pt1', ?, 'tool', 'Bash', 0)`,
		prevTS+10, prevTS+20, prevTS+30); err != nil {
		t.Fatal(err)
	}

	// Current window: 6 sessions to exceed minDiagnosticSessions (5) and trigger > 5 signals.
	for i := 0; i < 6; i++ {
		sid := fmt.Sprintf("C%d", i)
		ts := curTS + int64(i)*1000
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, model, started_at, ended_at) VALUES (?, 'agy', ?, 'gemini-4-carbon', ?, ?)`,
			sid, proj, ts, ts+500); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, context) VALUES (?, ?, ?, 'user', 0), (?, ?, ?, 'generation', 320000)`,
			sid, sid+":u", ts+1, sid, sid+":g", ts+2); err != nil {
			t.Fatal(err)
		}
		// Grep calls (low search:grep), Bash errors + retry loops, redundant Read calls.
		for j := 0; j < 2; j++ {
			if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, is_error, cmd_hash) VALUES (?, ?, ?, 'tool', 'Bash', 1, 'samehash')`,
				sid, fmt.Sprintf("%s:b:%d", sid, j), ts+10+int64(j)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, cmd_hash) VALUES (?, ?, ?, 'tool', 'Read', 'filehash:1-20')`,
				sid, fmt.Sprintf("%s:r:%d", sid, j), ts+20+int64(j)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES (?, ?, ?, 'tool', 'Grep')`,
			sid, sid+":grep", ts+30); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO events(session_id, ts, kind, value) VALUES (?, ?, 'correction', '1'), (?, ?, 'bug', '1')`,
			sid, ts+40, sid, ts+41); err != nil {
			t.Fatal(err)
		}
	}
	// Guarded downgrade subagent.
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, parent_id, agent_type, model, started_at, ended_at) VALUES ('SUB_DOWN', 'agy', ?, 'C0', 'reviewer', 'gemini-3.8-flash', ?, ?)`,
		proj, curTS+50, curTS+100); err != nil {
		t.Fatal(err)
	}

	// Background runs + session ratings.
	for _, bg := range []BackgroundRunRow{
		{TS: curTS, Kind: "dream", Outcome: "pass", DurationMS: 1500, ProjectsChecked: 2, ProposalsWritten: 3, RatingsWritten: 2},
		{TS: curTS + 100, Kind: "dream", Outcome: "fail", DurationMS: 500, ProjectsChecked: 1},
		{TS: curTS + 200, Kind: "auto", Outcome: "pass", DurationMS: 2400, ProjectsChecked: 2, WorkerRan: 1, Applied: 1},
	} {
		if err := s.InsertBackgroundRun(bg); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertSessionRatings([]SessionRating{{
		SessionID: "C0", ProjectRoot: proj, EvaluatedAt: now.Add(-time.Hour).UnixMilli(),
		OutcomeScore: 4, TokenEfficiency: 5, PlanAlignment: "matched",
	}}); err != nil {
		t.Fatal(err)
	}

	// Seed brain.db with 2 queued and 1 closed proposal.
	bdb := filepath.Join(home, harness.StateDir, "brain.db")
	sqliteT(t, bdb,
		`CREATE TABLE records(rid INTEGER PRIMARY KEY, scope TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, date TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, project TEXT NOT NULL DEFAULT '', updated TEXT NOT NULL, provenance TEXT NOT NULL DEFAULT '', UNIQUE(scope, id))`,
		fmt.Sprintf(`INSERT INTO records VALUES (1, '', 'PROPOSAL-001', 'proposal', 'queued', '2026-11-20', 'p1', 'b1', '%s', '2026-11-20T10:00:00Z', 'derived')`, proj),
		fmt.Sprintf(`INSERT INTO records VALUES (2, '', 'PROPOSAL-002', 'proposal', 'queued', '2026-11-20', 'p2', 'b2', '%s', '2026-11-20T10:01:00Z', 'derived')`, proj),
		fmt.Sprintf(`INSERT INTO records VALUES (3, '', 'PROPOSAL-003', 'proposal', 'accepted', '2026-11-20', 'p3', 'b3', '%s', '2026-11-20T10:02:00Z', 'derived')`, proj),
	)

	sum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if sum.Diagnostics == nil || len(sum.Diagnostics.Signals) < 6 {
		t.Fatalf("expected >= 6 diagnostic signals to test top-5 cap, got %+v", sum.Diagnostics)
	}
	if sum.Health.KeptAutoBranches != 2 || sum.Health.ProposalsQueued != 2 || sum.Health.ProposalsClosed != 1 {
		t.Fatalf("autonomy counts = branches:%d queued:%d closed:%d, want 2, 2, 1",
			sum.Health.KeptAutoBranches, sum.Health.ProposalsQueued, sum.Health.ProposalsClosed)
	}

	var htmlBuf, mdBuf bytes.Buffer
	if err := RenderReport(&htmlBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := RenderMarkdownReport(&mdBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	html, md := htmlBuf.String(), mdBuf.String()
	for _, want := range []string{
		"0. Scorecard",
		"Top-5 Diagnostic Signals",
		"Peak Context (>= 180k knee % & >= 300k target %)",
		"Search:Grep Ratio",
		"Repeat Reads %",
		"Blocked Ledger / Clamp Saves",
		"Tool Error Rate %",
		"Guarded Subagent Downgrades",
		"Prior Window",
		"sig:global:",
		"Background Autonomy",
		"Dream runs (pass / fail)",
		"2 (1 / 1)",
		"Auto runs (pass / fail)",
		"1 (1 / 0)",
		"Auto workers run / applied commits",
		"1 / 1",
		"Session ratings (outcome / token efficiency)",
		"1 (4.00 / 5.00)",
		"Kept work/auto-* branches",
		"PROPOSAL-* records (queued / closed)",
		"2 / 1",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
		if !strings.Contains(html, want) && !strings.Contains(html, esc(want)) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if n := strings.Count(md, "| sig:global:"); n != 5 {
		t.Errorf("markdown rendered %d diagnostic signal rows, want top 5", n)
	}

	// Clean degradation when git and brain.db are absent.
	emptyHome := t.TempDir()
	sEmpty := openT(t, Path(emptyHome))
	sumEmpty, err := sEmpty.Show(ShowOptions{Home: emptyHome, Project: filepath.Join(emptyHome, "no-git"), Since: since, Now: now})
	if err != nil {
		t.Fatalf("empty Show: %v", err)
	}
	if sumEmpty.Health.KeptAutoBranches != 0 || sumEmpty.Health.ProposalsQueued != 0 || sumEmpty.Health.ProposalsClosed != 0 {
		t.Fatalf("absent git/brain did not degrade to 0: %+v", sumEmpty.Health)
	}
}

func seedGitBranchesForAutonomyT(t *testing.T, dir string, branches ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "main.go")
	runGit("-c", "user.email=t@example.invalid", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")
	for _, b := range branches {
		runGit("branch", b)
	}
}
