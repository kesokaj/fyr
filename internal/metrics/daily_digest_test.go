package metrics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

func TestDailyDigestBuildSavePathAndFailOpenReads(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s := openT(t, Path(home))

	// Seed 6 sessions yesterday with bash skips & PreInvocation timeouts to trigger high signals.
	ts := now.Add(-6 * time.Hour).UnixMilli()
	for i := 0; i < 6; i++ {
		sid := fmt.Sprintf("dd-s-%d", i)
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES (?, 'agy', '/work/app', ?, ?)`,
			sid, ts+int64(i)*1000, ts+int64(i)*1000+500); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 2; j++ {
			ext := fmt.Sprintf("%s:b:%d", sid, j)
			if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES (?, ?, ?, 'tool', 'Bash')`, sid, ext, ts+10+int64(j)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES (?, ?, 0, 'cat')`, sid, ext); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.db.Exec(`INSERT INTO turn_vets(project_root, conversation_id, turn_num, intent, gate, score, duration_ms, timed_out, created_at) VALUES ('/work/app', ?, 1, 'TASK', 'PROCEED', 80, 260, 1, ?)`, sid, ts+20); err != nil {
			t.Fatal(err)
		}
	}

	// Seed brain.db with both auto/apply.go REVIEW format and PROPOSAL queued record.
	bdb := filepath.Join(home, harness.StateDir, "brain.db")
	sqliteT(t, bdb,
		`CREATE TABLE records(rid INTEGER PRIMARY KEY, scope TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, date TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, project TEXT NOT NULL DEFAULT '', updated TEXT NOT NULL, provenance TEXT NOT NULL DEFAULT '', UNIQUE(scope, id))`,
		`INSERT INTO records VALUES (1, '', 'REVIEW-2026-11-21-auto', 'review', 'closed', '2026-11-21', 'auto fix skills/golang', 'Applied by fyr auto (report REVIEW-1, handoff HANDOFF-1).\nsig:global:search_grep_low install pending\nrevert: git revert abc1234', '/work/app', '2026-11-21T09:00:00Z', 'derived')`,
		`INSERT INTO records VALUES (2, '', 'PROPOSAL-001', 'proposal', 'queued', '2026-11-21', 'propose guard', 'sig:global:bash_read_bypass', '/work/app', '2026-11-21T09:05:00Z', 'derived')`,
	)

	d, err := BuildDailyDigest(s, home, "/work/app", now)
	if err != nil {
		t.Fatalf("BuildDailyDigest: %v", err)
	}
	if d.Day != "2026-11-21" || d.Verdict != VerdictRegress || len(d.Signals) == 0 || len(d.Actions) != 2 {
		t.Fatalf("unexpected BuildDailyDigest: %+v", d)
	}
	if d.Actions[0].RevertCmd != "git revert abc1234" || d.Actions[0].AppliedSHA != "abc1234" || !d.Actions[0].InstallPending {
		t.Fatalf("REVIEW action parsing missed apply.go format: %+v", d.Actions[0])
	}

	wantPath := filepath.Join(home, harness.StateDir, "reports", "latest-daily-digest.json")
	if gotPath := LatestDailyDigestPath(home); gotPath != wantPath {
		t.Fatalf("LatestDailyDigestPath = %q, want %q", gotPath, wantPath)
	}
	if err := SaveDailyDigest(s, d); err != nil {
		t.Fatal(err)
	}
	if err := WriteLatestDailyDigest(home, d); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(filepath.Dir(wantPath))
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("reports dir perm = %v, err=%v; want 0700", dirInfo.Mode().Perm(), err)
	}
	fileInfo, err := os.Stat(wantPath)
	if err != nil || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("digest file perm = %v, err=%v; want 0600", fileInfo.Mode().Perm(), err)
	}
	readBack, err := ReadLatestDailyDigest(home)
	if err != nil || readBack == nil || readBack.Day != d.Day || len(readBack.Actions) != 2 {
		t.Fatalf("ReadLatestDailyDigest = (%+v, %v)", readBack, err)
	}

	// Fail-open negative tests: empty home, missing, corrupt, oversized, symlink.
	if got, err := ReadLatestDailyDigest(""); err != nil || got != nil {
		t.Fatalf("empty home ReadLatestDailyDigest = (%+v, %v)", got, err)
	}
	if got, err := ReadLatestDailyDigest(t.TempDir()); err != nil || got != nil {
		t.Fatalf("missing ReadLatestDailyDigest = (%+v, %v)", got, err)
	}
	if err := os.WriteFile(wantPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadLatestDailyDigest(home); err != nil || got != nil {
		t.Fatalf("corrupt ReadLatestDailyDigest = (%+v, %v)", got, err)
	}
	if err := os.WriteFile(wantPath, bytes.Repeat([]byte("a"), maxDailyDigestBytes+10), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadLatestDailyDigest(home); err != nil || got != nil {
		t.Fatalf("oversized ReadLatestDailyDigest = (%+v, %v)", got, err)
	}
	_ = os.Remove(wantPath)
	target := filepath.Join(t.TempDir(), "outside.json")
	_ = os.WriteFile(target, []byte(`{"day":"2026-11-21","verdict":"steady"}`), 0o600)
	if err := os.Symlink(target, wantPath); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadLatestDailyDigest(home); err != nil || got != nil {
		t.Fatalf("symlink ReadLatestDailyDigest = (%+v, %v)", got, err)
	}
}

func TestDailyDigestTransitionsVerdictsAndRecovery(t *testing.T) {
	prev := &DailyDigest{
		Day:     "2026-11-20",
		Verdict: VerdictRegress,
		Signals: []DigestAction{
			{SignalID: "sig:global:pre_invocation_timeouts", Severity: "high", Rule: "pre_invocation_timeouts"},
			{SignalID: "sig:global:eval_pass_rate_low", Severity: "medium", Rule: "eval_pass_rate_low"},
		},
	}
	curSignals := []DigestAction{
		{SignalID: "sig:global:pre_invocation_timeouts", Severity: "high", Rule: "pre_invocation_timeouts"},
		{SignalID: "sig:global:bash_read_bypass", Severity: "medium", Rule: "bash_read_bypass"},
	}
	newReg, resolved, persistHigh := computeSignalTransitions(curSignals, prev)
	if fmt.Sprint(newReg) != "[sig:global:bash_read_bypass]" ||
		fmt.Sprint(resolved) != "[sig:global:eval_pass_rate_low]" ||
		fmt.Sprint(persistHigh) != "[sig:global:pre_invocation_timeouts]" {
		t.Fatalf("transitions mismatch: new=%v resolved=%v high=%v", newReg, resolved, persistHigh)
	}

	// Verdict checks: improving, steady, regressing.
	imp := DailyDigest{
		Day: "2026-11-21",
		Rolling7d: RollingKPIDeltas{
			SearchGrepRatio: &MetricDelta{Current: 2.0, Previous: 1.0, Delta: 1.0},
		},
	}
	if got := computeDigestVerdict(imp); got != VerdictImproving {
		t.Fatalf("improving verdict = %q, want %q", got, VerdictImproving)
	}
	steady := DailyDigest{Day: "2026-11-21"}
	if got := computeDigestVerdict(steady); got != VerdictSteady {
		t.Fatalf("steady verdict = %q, want %q", got, VerdictSteady)
	}
	regAST := DailyDigest{
		Day:     "2026-11-21",
		ASTDiff: ASTStructuralDiff{HasPrevious: true, ProdPkgCycles: 1},
	}
	if got := computeDigestVerdict(regAST); got != VerdictRegress {
		t.Fatalf("regressing AST verdict = %q, want %q", got, VerdictRegress)
	}

	// EvaluateActionRecovery + RecordDigestAppliedAction with BaselineValue/Metric.
	home := t.TempDir()
	s := openT(t, Path(home))
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	baseRatio := 0.4
	act := DigestAppliedAction{
		SignalID: "sig:global:search_grep_low", ReportDate: "2026-11-20",
		ActionScope: scopeAutoSkill, RevertCmd: "git revert deadbee",
		AppliedSHA: "deadbee", Metric: "C.search_grep_ratio.agy", BaselineValue: &baseRatio,
	}
	if err := RecordDigestAppliedAction(s, home, "", now, act); err != nil {
		t.Fatalf("RecordDigestAppliedAction: %v", err)
	}
	saved, err := LatestDailyDigest(s)
	if err != nil || saved == nil || len(saved.Actions) != 1 || saved.Actions[0].BaselineValue == nil || *saved.Actions[0].BaselineValue != 0.4 {
		t.Fatalf("RecordDigestAppliedAction persisted = (%+v, %v)", saved, err)
	}
	if disk, err := ReadLatestDailyDigest(home); err != nil || disk == nil || len(disk.Actions) != 1 {
		t.Fatalf("ReadLatestDailyDigest after RecordDigestAppliedAction = (%+v, %v)", disk, err)
	}

	sum := newSummary(now.AddDate(0, 0, -7), now)
	if got := EvaluateActionRecovery(act, sum, prev, "2026-11-20", 3, nil); got != StatusPendingData {
		t.Fatalf("same-day recovery = %q, want pending_data", got)
	}
	if got := EvaluateActionRecovery(act, sum, prev, "2026-11-21", 1, nil); got != StatusPendingData {
		t.Fatalf("under min sessions recovery = %q, want pending_data", got)
	}
	sum.C.SearchGrepRatio["agy"] = ptrF(1.8)
	if got := EvaluateActionRecovery(act, sum, prev, "2026-11-21", 3, nil); got != StatusRecovered {
		t.Fatalf("improved ratio recovery = %q, want recovered", got)
	}
	sum.C.SearchGrepRatio["agy"] = ptrF(0.3)
	if got := EvaluateActionRecovery(act, sum, prev, "2026-11-21", 3, nil); got != StatusStillRegressing {
		t.Fatalf("worsened ratio recovery = %q, want still_regressing", got)
	}
}

func TestDailyBannerInHTMLMDAndJSON(t *testing.T) {
	sum := reportFixture(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

	// Nil DailyDigest leaves HTML/MD/JSON free of Daily Direction & Actions.
	var htmlNil, mdNil, jsonNil bytes.Buffer
	_ = RenderReport(&htmlNil, sum, nil, now)
	_ = RenderMarkdownReport(&mdNil, sum, nil, now)
	_ = RenderJSONReport(&jsonNil, sum, nil, now)
	for _, out := range []string{htmlNil.String(), mdNil.String(), jsonNil.String()} {
		if strings.Contains(out, "Daily Direction & Actions") || strings.Contains(out, "daily_digest") {
			t.Fatalf("nil DailyDigest unexpectedly rendered banner:\n%s", out)
		}
	}

	sum.DailyDigest = &DailyDigest{
		Day:             "2026-11-21",
		Verdict:         VerdictRegress,
		NewRegressions:  []string{"sig:global:bash_read_bypass"},
		ResolvedSignals: []string{"sig:global:eval_pass_rate_low"},
		PersistentHigh:  []string{"sig:global:pre_invocation_timeouts"},
		Actions: []DigestAppliedAction{{
			SignalID: "sig:global:search_grep_low", ReportDate: "2026-11-20",
			ActionScope: scopeAutoSkill, Status: StatusStillRegressing,
			InstallPending: true, RevertCmd: "git revert abc1234",
		}},
		Signals: []DigestAction{{
			SignalID: "sig:global:xss", Severity: "high", Rule: "pre_invocation_timeouts",
			ActionScope: scopeProposeOnly, RecommendedAction: `<script>alert(1)</script>|pipe`,
		}},
		ASTSnapshot: search.StructuralSignals{TotalCodeFiles: 10},
	}

	var htmlBuf, mdBuf, jsonBuf bytes.Buffer
	if err := RenderReport(&htmlBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := RenderMarkdownReport(&mdBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := RenderJSONReport(&jsonBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}

	html, md := htmlBuf.String(), mdBuf.String()
	if strings.Contains(html, "<script>alert(1)</script>") || strings.Contains(html, "ZgotmplZ") {
		t.Fatalf("HTML banner did not escape XSS safely:\n%s", html)
	}
	for _, want := range []string{
		"Daily Direction &amp; Actions", "regressing", "new_regressions", "resolved_signals",
		"persistent_high", "git revert abc1234", "install pending", "&lt;script&gt;alert(1)&lt;/script&gt;",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q:\n%s", want, html)
		}
	}
	for _, want := range []string{
		"## Daily Direction & Actions", "Direction: **regressing** (2026-11-21)",
		"new_regressions: sig:global:bash_read_bypass", "resolved_signals: sig:global:eval_pass_rate_low",
		"persistent_high: sig:global:pre_invocation_timeouts", "git revert abc1234", `\|pipe`,
	} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown missing %q:\n%s", want, md)
		}
	}
	var jr JSONReport
	if err := json.Unmarshal(jsonBuf.Bytes(), &jr); err != nil || jr.DailyDigest == nil || jr.DailyDigest.Verdict != VerdictRegress {
		t.Fatalf("JSONReport missing daily_digest: %+v, err=%v", jr.DailyDigest, err)
	}
}
