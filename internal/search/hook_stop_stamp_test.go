package search

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/harness"
)

func initGitGoRepoForHooks(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q", "-b", "main")
	runGit("config", "user.name", "test")
	runGit("config", "user.email", "test@localhost")
	runGit("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc MainHelper() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "go.mod", "main.go", "README.md")
	runGit("commit", "-qm", "init")
	return dir
}

func TestSessionStartCapsuleAndSizeCap(t *testing.T) {
	dir := initGitGoRepoForHooks(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PLAN.md"), []byte("# PLAN — demo\n\n### Phase 2b — Hook events and routing\n- [x] Done step\n- [ ] Pending step\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "TODO.md"), []byte("# TODO\n\n## Now\n- [ ] Wire SessionStart\n- [ ] Add Stop stamp\n\n## Next\n- [ ] Phase 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc MainHelper() int { return 2 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	req, _ := json.Marshal(map[string]any{"conversationId": "conv-start-1", "workspacePaths": []string{dir}})
	var out bytes.Buffer
	t0 := time.Now()
	err := Search(Options{
		Home:    home,
		Hook:    "session-start",
		HookIn:  bytes.NewReader(req),
		nowFunc: func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) },
	}, &out)
	elapsed := time.Since(t0)
	if err != nil {
		t.Fatalf("Search(session-start): %v", err)
	}
	if elapsed >= 250*time.Millisecond {
		t.Errorf("SessionStart elapsed = %v, want < 250ms", elapsed)
	}
	var resp PreInvocationOutput
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.InjectSteps) != 1 || resp.InjectSteps[0].SystemMessage == nil {
		t.Fatalf("unexpected SessionStart response: %+v (%v)", resp, err)
	}
	msg := resp.InjectSteps[0].SystemMessage.SystemMessage
	if strings.Contains(out.String(), "terminationBehavior") {
		t.Errorf("SessionStart must omit terminationBehavior for agy protojson, got:\n%s", out.String())
	}
	if len(msg) > 1500 {
		t.Errorf("SessionStart capsule size = %d B, exceeds 1,500 B", len(msg))
	}
	for _, want := range []string{`fyr · SESSION_START · 2026-11-15`, `Phase 2b`, `Wire SessionStart`, `Branch:    main (1 dirty Go: main.go)`} {
		if !strings.Contains(msg, want) {
			t.Errorf("SessionStart capsule missing %q:\n%s", want, msg)
		}
	}

	// Bloated PLAN.md + TODO.md + many dirty files still trims strictly under 1,500 B.
	var todoBuf strings.Builder
	todoBuf.WriteString("## Now\n")
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&todoBuf, "- [ ] Task item %d %s\n", i, strings.Repeat("detail ", 10))
	}
	_ = os.WriteFile(filepath.Join(dir, "PLAN.md"), []byte("# "+strings.Repeat("VeryLongPlanTitle ", 30)+"\n### Phase 2b\n- [ ] box\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "TODO.md"), []byte(todoBuf.String()), 0o600)
	for i := 0; i < 20; i++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("extra_%02d_with_long_name_for_size_cap.go", i)), []byte("package main\n"), 0o600)
	}
	out.Reset()
	if err := Search(Options{Home: home, Hook: "session-start", HookIn: bytes.NewReader(req)}, &out); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.InjectSteps) != 1 {
		t.Fatalf("bloated SessionStart failed: %v", err)
	}
	if n := len(resp.InjectSteps[0].SystemMessage.SystemMessage); n > 1500 {
		t.Errorf("bloated SessionStart capsule = %d B, want <= 1,500 B", n)
	}

	// Missing PLAN.md falls back to BrainTurnContext.ActivePlan; non-git dir fails open.
	_ = os.Remove(filepath.Join(dir, "PLAN.md"))
	out.Reset()
	_ = Search(Options{
		Home:   home,
		Hook:   "session-start",
		HookIn: bytes.NewReader(req),
		BrainTurnContext: func(string, string, string, []string) TurnBrainContext {
			return TurnBrainContext{ActivePlan: &MatchedRecord{ID: "PLAN-123", Title: "Fallback active plan"}}
		},
	}, &out)
	if !strings.Contains(out.String(), "PLAN-123: Fallback active plan") {
		t.Errorf("expected ActivePlan fallback in SessionStart:\n%s", out.String())
	}
	nonGitReq, _ := json.Marshal(map[string]any{"conversationId": "conv-non", "workspacePaths": []string{t.TempDir()}})
	out.Reset()
	_ = Search(Options{Home: home, Hook: "session-start", HookIn: bytes.NewReader(nonGitReq)}, &out)
	var nonResp PreInvocationOutput
	if json.Unmarshal(out.Bytes(), &nonResp) != nil || len(nonResp.InjectSteps) != 0 {
		t.Errorf("non-git SessionStart should return empty injectSteps, got %+v", nonResp)
	}
}

func TestStopHookVerifyStampLifecycle(t *testing.T) {
	runStop := func(t *testing.T, home, dir string, extra map[string]any) HookDecisionOutput {
		t.Helper()
		payload := map[string]any{
			"conversationId":    "conv-stop-1",
			"workspacePaths":    []string{dir},
			"terminationReason": "NO_TOOL_CALL",
		}
		for k, v := range extra {
			payload[k] = v
		}
		req, _ := json.Marshal(payload)
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "stop", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("Search(stop): %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal stop: %v (%s)", err, out.String())
		}
		return dec
	}

	// 1. Clean repo -> allow.
	dir := initGitGoRepoForHooks(t)
	home := t.TempDir()
	if d := runStop(t, home, dir, nil); d.Decision != "allow" {
		t.Fatalf("clean repo Stop = %+v, want allow", d)
	}

	// 2. Non-Go edit only -> allow.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Updated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := runStop(t, home, dir, nil); d.Decision != "allow" {
		t.Fatalf("markdown-only edit Stop = %+v, want allow", d)
	}

	// 3. Dirty .go file without verify.stamp -> continue ONCE, then allow on 2nd Stop.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc MainHelper() int { return 42 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	d1 := runStop(t, home, dir, nil)
	elapsed := time.Since(t0)
	if elapsed >= 250*time.Millisecond {
		t.Errorf("Stop elapsed = %v, want < 250ms", elapsed)
	}
	if d1.Decision != "continue" || !strings.Contains(d1.Reason, "[VERIFY_STAMP]") {
		t.Fatalf("1st dirty Stop = %+v, want continue + [VERIFY_STAMP]", d1)
	}
	if d2 := runStop(t, home, dir, nil); d2.Decision != "allow" {
		t.Fatalf("2nd Stop same turn/state = %+v, want allow", d2)
	}

	// 4. Stale stamp triggers continue on a fresh conversation, then matching stamp allows.
	writeTestVerifyStamp := func(payload map[string]any) {
		t.Helper()
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, filepath.FromSlash(verifyStampRelPath))
		if err := writeScratchStampAtomic(target, append(b, '\n')); err != nil {
			t.Fatalf("writeTestVerifyStamp: %v", err)
		}
	}
	writeTestVerifyStamp(map[string]any{"treeSha256": strings.Repeat("0", 64)})
	if d := runStop(t, home, dir, map[string]any{"conversationId": "conv-stale"}); d.Decision != "continue" {
		t.Fatalf("stale stamp Stop = %+v, want continue", d)
	}
	state, err := ComputeGoTreeState(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeTestVerifyStamp(map[string]any{"treeSha256": state.TreeSHA256})
	if d := runStop(t, home, dir, map[string]any{"conversationId": "conv-fresh"}); d.Decision != "allow" {
		t.Fatalf("matching stamp Stop = %+v, want allow", d)
	}
	// Legacy snake_case tree_sha256 field is also honored via trail.ReadVerifyStamp.
	writeTestVerifyStamp(map[string]any{"tree_sha256": state.TreeSHA256})
	if d := runStop(t, home, dir, map[string]any{"conversationId": "conv-legacy-sn"}); d.Decision != "allow" {
		t.Fatalf("legacy tree_sha256 stamp Stop = %+v, want allow", d)
	}

	// 5. ContinuationActive / ExecutionNum / USER_CANCELED bypass even on new untracked .go edit.
	if err := os.WriteFile(filepath.Join(dir, "added.go"), []byte("package main\n\nfunc Added() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, extra := range []map[string]any{
		{"conversationId": "conv-cont", "continuationActive": true},
		{"conversationId": "conv-exec", "executionNum": 1},
		{"conversationId": "conv-cancel", "terminationReason": "USER_CANCELED"},
	} {
		if d := runStop(t, home, dir, extra); d.Decision != "allow" {
			t.Errorf("bypass %+v returned %+v, want allow", extra, d)
		}
	}

	// 6. New untracked .go file with no matching stamp returns continue cleanly.
	if d := runStop(t, home, dir, map[string]any{"conversationId": "conv-untracked"}); d.Decision != "continue" || !strings.Contains(d.Reason, "added.go") {
		t.Errorf("untracked added.go Stop = %+v, want continue mentioning added.go", d)
	}

	// 7. Read-only subagent on already-dirty parent Go file is allowed on Stop, while writing parent is nudged once.
	if _, err := CheckPreWriteHashWithEdit(dir, home, dir, "conv-parent", "main.go", map[string]any{"Path": "main.go"}); err != nil {
		t.Fatalf("prewrite conv-parent: %v", err)
	}
	_ = markPreWriteInLedger(dir, "conv-parent", filepath.Join(dir, "main.go"))
	if dec, err := CheckAndRecordReadInSession(dir, home, dir, "conv-reviewer", "main.go", 1, 3, 0); err != nil || !dec.Allowed {
		t.Fatalf("read dirty main.go in conv-reviewer: dec=%+v err=%v", dec, err)
	}
	if _, err := CheckPreWriteHashWithEdit(dir, home, dir, "conv-reviewer", ".agents/scratch/agents/review.md", map[string]any{"Path": ".agents/scratch/agents/review.md"}); err != nil {
		t.Fatalf("prewrite review.md in conv-reviewer: %v", err)
	}
	if d := runStop(t, home, dir, map[string]any{"conversationId": "conv-reviewer"}); d.Decision != "allow" {
		t.Errorf("read-only subagent Stop = %+v, want allow on parent dirty tree", d)
	}
	if d := runStop(t, home, dir, map[string]any{"conversationId": "conv-parent"}); d.Decision != "continue" {
		t.Errorf("writing parent Stop 1 = %+v, want continue", d)
	}
	if d := runStop(t, home, dir, map[string]any{"conversationId": "conv-parent"}); d.Decision != "allow" {
		t.Errorf("writing parent Stop 2 = %+v, want allow", d)
	}
}

func TestComputeGoTreeStateNonGitIgnoresCwdIndex(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "index"), []byte("not-a-dirc-index"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	nonGit := t.TempDir()
	if err := os.WriteFile(filepath.Join(nonGit, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := ComputeGoTreeState(nonGit)
	if err != nil {
		t.Fatalf("ComputeGoTreeState(nonGit) with bogus cwd index: %v", err)
	}
	if state.GoFileCount != 1 || state.TreeSHA256 == "" {
		t.Fatalf("unexpected non-git GoTreeState: %+v", state)
	}
}

func TestGitIndexSafetyGuards(t *testing.T) {
	hdr := make([]byte, 12)
	copy(hdr[0:4], "DIRC")
	binary.BigEndian.PutUint32(hdr[4:8], 2)
	binary.BigEndian.PutUint32(hdr[8:12], 0x7fffffff)
	if _, err := parseGitIndex(hdr); err == nil {
		t.Fatal("parseGitIndex accepted absurd entry count on 12-byte buffer")
	}

	overflow := bytes.Repeat([]byte{0xff}, 16)
	overflow = append(overflow, 0x00)
	if _, _, err := decodeGitIndexPath(overflow, 0, 0, 4, "a/b.go"); err == nil {
		t.Fatal("decodeGitIndexPath accepted overflow varint")
	}

	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside_secret.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })
	e := gitIndexEntry{Path: "../outside_secret.go", Mode: 0o100644}
	tracked, dirty := checkTrackedGoEntries(root, []gitIndexEntry{e}, true)
	if len(tracked) != 0 || len(dirty) != 0 {
		t.Fatalf("traversal entry not ignored cleanly: tracked=%v dirty=%v", tracked, dirty)
	}
	if isTrackedEntryDirty(root, e) {
		t.Fatal("isTrackedEntryDirty accessed non-local traversal path")
	}
}

func TestSessionStartCapsuleUTF8AndClosedFence(t *testing.T) {
	c := sessionStartCapsule{
		Surface: "SESSION_START",
		Date:    "2026-11-15",
		Plan:    strings.Repeat("日本語─—🚀", 40),
		Now:     []string{"item one", "item two"},
		Status: sessionGitSummary{
			Branch:       strings.Repeat("feature/λ─", 20),
			DirtyGoCount: 1,
		},
	}
	for _, limit := range []int{120, 1500} {
		copyCap := c
		got := fitSessionStartCapsule(&copyCap, limit)
		if len(got) > limit {
			t.Errorf("limit=%d exceeded: len=%d", limit, len(got))
		}
		if !utf8.ValidString(got) {
			t.Errorf("limit=%d produced invalid UTF-8:\n%q", limit, got)
		}
		trimmed := strings.TrimRight(got, "\n")
		if !strings.HasSuffix(trimmed, "```") || strings.Count(trimmed, "```") < 2 {
			t.Errorf("limit=%d missing closed code fence:\n%s", limit, got)
		}
	}
}

func TestSessionStartReftableBranch(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-q", "--ref-format=reftable", "-b", "feature/reftable")
	runGit("config", "user.name", "test")
	runGit("config", "user.email", "test@localhost")
	runGit("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc Main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "go.mod", "main.go")
	runGit("commit", "-qm", "init")

	headData, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if err != nil {
		t.Fatalf("read .git/HEAD: %v", err)
	}
	if got := strings.TrimSpace(string(headData)); got != "ref: refs/heads/.invalid" {
		t.Fatalf(".git/HEAD = %q, want ref: refs/heads/.invalid", got)
	}

	home := t.TempDir()
	req, _ := json.Marshal(map[string]any{"conversationId": "conv-reftable", "workspacePaths": []string{dir}})
	var out bytes.Buffer
	if err := Search(Options{Home: home, Hook: "session-start", HookIn: bytes.NewReader(req)}, &out); err != nil {
		t.Fatalf("Search(session-start): %v", err)
	}
	var resp PreInvocationOutput
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.InjectSteps) != 1 || resp.InjectSteps[0].SystemMessage == nil {
		t.Fatalf("unexpected SessionStart response: %+v (%v)", resp, err)
	}
	msg := resp.InjectSteps[0].SystemMessage.SystemMessage
	if !strings.Contains(msg, "Branch:    feature/reftable") {
		t.Fatalf("SessionStart output missing feature/reftable branch:\n%s", msg)
	}
	if strings.Contains(msg, ".invalid") {
		t.Fatalf("SessionStart output leaked .invalid stub:\n%s", msg)
	}

	idxPath := filepath.Join(dir, ".git", "index")
	origIdx, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatalf("read .git/index: %v", err)
	}
	if err := os.WriteFile(idxPath, []byte("bad-index"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := collectSessionGitSummary(dir); got.Branch != "feature/reftable" || got.DirtyGoCount != 0 {
		t.Fatalf("corrupt index collectSessionGitSummary = %+v, want branch feature/reftable", got)
	}
	if err := os.WriteFile(idxPath, origIdx, 0o600); err != nil {
		t.Fatal(err)
	}

	runGit("checkout", "-q", "--detach", "HEAD")
	wantDetached := runGit("rev-parse", "--short=8", "HEAD")
	state, err := ComputeGoTreeState(dir)
	if err != nil {
		t.Fatalf("ComputeGoTreeState(detached): %v", err)
	}
	if state.Branch != wantDetached || strings.Contains(state.Branch, ".invalid") {
		t.Fatalf("detached ComputeGoTreeState.Branch = %q, want %q", state.Branch, wantDetached)
	}

	broken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(broken, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, ".git", "HEAD"), []byte("ref: refs/heads/.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	brokenState, err := ComputeGoTreeState(broken)
	if err != nil {
		t.Fatalf("ComputeGoTreeState(broken): %v", err)
	}
	if brokenState.Branch != "" {
		t.Fatalf("broken .invalid ComputeGoTreeState.Branch = %q, want empty", brokenState.Branch)
	}
}

func TestSessionStartDailyAlertAndFailOpen(t *testing.T) {
	dir := initGitGoRepoForHooks(t)
	home := t.TempDir()
	digestPath := filepath.Join(home, harness.StateDir, "reports", "latest-daily-digest.json")
	if err := os.MkdirAll(filepath.Dir(digestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(map[string]any{"conversationId": "conv-alert", "workspacePaths": []string{dir}})

	// 1. Non-alerting steady/improving digest emits no dailyAlert.
	steady := `{"day":"2026-11-21","verdict":"improving","actions":[{"signal_id":"sig:global:a","status":"recovered"}],"signals":[{"signal_id":"sig:global:b","severity":"medium","rule":"eval_pass_rate_low"}]}`
	if err := os.WriteFile(digestPath, []byte(steady), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := extractSessionDailyAlert(home); got != "" {
		t.Fatalf("non-alerting digest emitted %q, want empty", got)
	}

	// 2. Regressing + high signal + install_pending emits <= 2 lines, <= 220 B, and SessionStart finishes < 250ms.
	alertJSON := `{"day":"2026-11-21","verdict":"regressing","actions":[{"signal_id":"sig:global:search_grep_low","status":"still_regressing","install_pending":true}],"signals":[{"signal_id":"sig:global:pre_invocation_timeouts","severity":"high","rule":"pre_invocation_timeouts"}]}`
	if err := os.WriteFile(digestPath, []byte(alertJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	t0 := time.Now()
	if err := Search(Options{Home: home, Hook: "session-start", HookIn: bytes.NewReader(req)}, &out); err != nil {
		t.Fatalf("Search(session-start): %v", err)
	}
	if elapsed := time.Since(t0); elapsed >= 250*time.Millisecond {
		t.Errorf("SessionStart with dailyAlert elapsed = %v, want < 250ms", elapsed)
	}
	var resp PreInvocationOutput
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.InjectSteps) != 1 || resp.InjectSteps[0].SystemMessage == nil {
		t.Fatalf("unexpected SessionStart response: %+v (%v)", resp, err)
	}
	msg := resp.InjectSteps[0].SystemMessage.SystemMessage
	if len(msg) > 1500 || !strings.Contains(msg, "Daily:     daily regressing") || !strings.Contains(msg, "\n           actions: ") {
		t.Fatalf("SessionStart capsule missing aligned dailyAlert or > 1500 B:\n%s", msg)
	}
	alert := extractSessionDailyAlert(home)
	if len(alert) == 0 || len(alert) > 220 || strings.Count(alert, "\n") > 1 {
		t.Fatalf("alert violates bounds (len=%d, lines=%d): %q", len(alert), strings.Count(alert, "\n")+1, alert)
	}

	// 3. fitSessionStartCapsule trims DailyAlert > 120 chars and drops it when tight.
	tightCap := sessionStartCapsule{
		Surface:    "SESSION_START",
		Date:       "2026-11-21",
		Plan:       strings.Repeat("P", 70),
		Now:        []string{strings.Repeat("N", 1120)},
		DailyAlert: strings.Repeat("alert-", 35),
		Status:     sessionGitSummary{Branch: "main", DirtyGoCount: 1},
	}
	fitted := fitSessionStartCapsule(&tightCap, sessionStartMaxBytes)
	if len(fitted) > sessionStartMaxBytes || !utf8.ValidString(fitted) {
		t.Fatalf("fitSessionStartCapsule exceeded cap: len=%d", len(fitted))
	}
	if len(tightCap.DailyAlert) > 120 || tightCap.DailyAlert == "" {
		t.Fatalf("fitSessionStartCapsule did not shorten DailyAlert into (0..120]: len=%d", len(tightCap.DailyAlert))
	}
	dropCap := tightCap
	dropCap.DailyAlert = strings.Repeat("alert-", 35)
	dropped := fitSessionStartCapsule(&dropCap, 160)
	if len(dropped) > 160 || dropCap.DailyAlert != "" {
		t.Fatalf("fitSessionStartCapsule did not drop DailyAlert when tight: len=%d dailyAlert=%q", len(dropped), dropCap.DailyAlert)
	}

	// 4. Fail-open on empty home, missing, corrupt, oversized, and symlinked digest file.
	if got := extractSessionDailyAlert(""); got != "" {
		t.Fatalf("empty home returned %q", got)
	}
	for _, setup := range []func(){
		func() { _ = os.WriteFile(digestPath, []byte("{corrupt"), 0o600) },
		func() { _ = os.WriteFile(digestPath, bytes.Repeat([]byte("a"), sessionStartMaxFileRead+10), 0o600) },
		func() {
			_ = os.Remove(digestPath)
			tgt := filepath.Join(t.TempDir(), "other.json")
			_ = os.WriteFile(tgt, []byte(alertJSON), 0o600)
			_ = os.Symlink(tgt, digestPath)
		},
		func() { _ = os.Remove(digestPath) },
	} {
		setup()
		if got := extractSessionDailyAlert(home); got != "" {
			t.Errorf("fail-open case returned %q, want empty", got)
		}
	}
}

func TestSessionStartReadBoundedTextSymlinkSizeAndRedact(t *testing.T) {
	dir := initGitGoRepoForHooks(t)
	home := t.TempDir()
	outside := t.TempDir()

	// 1. Symlinked PLAN.md and TODO.md must be refused by readBoundedText via trail.OpenRegular.
	secPlan := filepath.Join(outside, "PLAN.md")
	secTodo := filepath.Join(outside, "TODO.md")
	if err := os.WriteFile(secPlan, []byte("# SecretPlan\n### Phase 9\n- [ ] Symlinked task\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secTodo, []byte("## Now\n- [ ] SecretTodoItem\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secPlan, filepath.Join(dir, "PLAN.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secTodo, filepath.Join(dir, "TODO.md")); err != nil {
		t.Fatal(err)
	}

	req, _ := json.Marshal(map[string]any{"conversationId": "conv-sec-start", "workspacePaths": []string{dir}})
	runStart := func(turnBrain func(string, string, string, []string) TurnBrainContext) string {
		t.Helper()
		var out bytes.Buffer
		if err := Search(Options{
			Home:             home,
			Hook:             "session-start",
			HookIn:           bytes.NewReader(req),
			BrainTurnContext: turnBrain,
		}, &out); err != nil {
			t.Fatalf("Search(session-start): %v", err)
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.InjectSteps) != 1 || resp.InjectSteps[0].SystemMessage == nil {
			t.Fatalf("unexpected SessionStart response: %+v (%v)", resp, err)
		}
		return resp.InjectSteps[0].SystemMessage.SystemMessage
	}

	msgLink := runStart(nil)
	for _, forbidden := range []string{"SecretPlan", "SecretTodoItem"} {
		if strings.Contains(msgLink, forbidden) {
			t.Fatalf("SessionStart followed symlinked file (%q):\n%s", forbidden, msgLink)
		}
	}

	// 2. Oversized PLAN.md and TODO.md (> sessionStartMaxFileRead) must be rejected.
	_ = os.Remove(filepath.Join(dir, "PLAN.md"))
	_ = os.Remove(filepath.Join(dir, "TODO.md"))
	bigPlan := "# OversizedPlan\n### Phase 1\n- [ ] " + strings.Repeat("x", sessionStartMaxFileRead+10) + "\n"
	bigTodo := "## Now\n- [ ] OversizedTodo " + strings.Repeat("y", sessionStartMaxFileRead+10) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "PLAN.md"), []byte(bigPlan), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "TODO.md"), []byte(bigTodo), 0o600); err != nil {
		t.Fatal(err)
	}
	msgBig := runStart(nil)
	for _, forbidden := range []string{"OversizedPlan", "OversizedTodo"} {
		if strings.Contains(msgBig, forbidden) {
			t.Fatalf("SessionStart read oversized file (%q):\n%s", forbidden, msgBig)
		}
	}

	// 3. Regular PLAN.md, TODO.md, and ActivePlan fallback redact credential shapes before rendering.
	tokPlan := "gh" + "p_" + strings.Repeat("P", 36)
	tokTodo := "AI" + "zaSy" + strings.Repeat("T", 33)
	tokBrain := "gh" + "p_" + strings.Repeat("B", 36)
	if err := os.WriteFile(filepath.Join(dir, "PLAN.md"), []byte("# Plan "+tokPlan+"\n### Phase 1\n- [ ] Open\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "TODO.md"), []byte("## Now\n- [ ] Task "+tokTodo+"\n- [ ] SafeTask\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	msgRedact := runStart(nil)
	for _, tok := range []string{tokPlan, tokTodo} {
		if strings.Contains(msgRedact, tok) {
			t.Fatalf("SessionStart leaked credential needle %q:\n%s", tok, msgRedact)
		}
	}
	if !strings.Contains(msgRedact, "SafeTask") || !strings.Contains(msgRedact, "[redacted by fyr]") {
		t.Fatalf("SessionStart did not keep redacted marker / safe task:\n%s", msgRedact)
	}

	_ = os.Remove(filepath.Join(dir, "PLAN.md"))
	msgBrain := runStart(func(string, string, string, []string) TurnBrainContext {
		return TurnBrainContext{ActivePlan: &MatchedRecord{ID: "PLAN-99", Title: "Use " + tokBrain}}
	})
	if strings.Contains(msgBrain, tokBrain) || !strings.Contains(msgBrain, "[redacted by fyr]") {
		t.Fatalf("SessionStart leaked ActivePlan fallback credential:\n%s", msgBrain)
	}
}
