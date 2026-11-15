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
	for _, want := range []string{`"surface": "SESSION_START"`, `"date": "2026-11-15"`, `Phase 2b`, `Wire SessionStart`, `"branch": "main"`, `"dirtyGoCount": 1`, `"main.go"`} {
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
	if err := writeVerifyStamp(dir, strings.Repeat("0", 64)); err != nil {
		t.Fatalf("writeVerifyStamp stale: %v", err)
	}
	if d := runStop(t, home, dir, map[string]any{"conversationId": "conv-stale"}); d.Decision != "continue" {
		t.Fatalf("stale stamp Stop = %+v, want continue", d)
	}
	state, err := ComputeGoTreeState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeVerifyStamp(dir, state.TreeSHA256); err != nil {
		t.Fatalf("writeVerifyStamp current: %v", err)
	}
	if d := runStop(t, home, dir, map[string]any{"conversationId": "conv-fresh"}); d.Decision != "allow" {
		t.Fatalf("matching stamp Stop = %+v, want allow", d)
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
	tracked, dirty, err := checkTrackedGoEntries(root, []gitIndexEntry{e}, true)
	if err != nil || len(tracked) != 0 || len(dirty) != 0 {
		t.Fatalf("traversal entry not ignored cleanly: tracked=%v dirty=%v err=%v", tracked, dirty, err)
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
