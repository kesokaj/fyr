package search

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func runWatchdogRaw(t *testing.T, home, dir string, payload map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var out bytes.Buffer
	hookEv, _ := payload["hook_event_name"].(string)
	if err := RunHook(Options{Home: home, Dir: dir, Hook: hookEv}, bytes.NewReader(raw), &out); err != nil {
		t.Fatalf("RunHook(%v): %v", payload["hook_event_name"], err)
	}
	return out.String()
}

func runWatchdogPre(t *testing.T, home, dir, convID, tool string, args map[string]any, extra ...func(map[string]any)) HookDecisionOutput {
	t.Helper()
	p := map[string]any{
		"hook_event_name": "pre-tool-use",
		"conversation_id": convID,
		"tool_call":       map[string]any{"name": tool, "args": args},
	}
	for _, fn := range extra {
		fn(p)
	}
	raw := runWatchdogRaw(t, home, dir, p)
	var dec HookDecisionOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &dec); err != nil {
		t.Fatalf("unmarshal decision %q: %v", raw, err)
	}
	return dec
}

func runWatchdogPost(t *testing.T, home, dir, convID, tool string, args map[string]any, errStr, topID, tcID string) {
	t.Helper()
	tc := map[string]any{"name": tool, "args": args}
	if tcID != "" {
		tc["id"] = tcID
	}
	p := map[string]any{
		"hook_event_name": "post-tool-use",
		"conversation_id": convID,
		"tool_call":       tc,
		"error":           errStr,
	}
	if topID != "" {
		p["toolCallId"] = topID
	}
	if got := runWatchdogRaw(t, home, dir, p); got != "{}\n" {
		t.Fatalf("post-tool-use output = %q, want %q", got, "{}\\n")
	}
}

func seedWatchdogSummaryDB(t *testing.T, home string) {
	t.Helper()
	agyDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(agyDir, 0o755); err != nil {
		t.Fatalf("mkdir agyDir: %v", err)
	}
	db, err := sql.Open("sqlite", DSN(filepath.Join(agyDir, "conversation_summaries.db")))
	if err != nil {
		t.Fatalf("open summaries db: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE conversation_summaries (conversation_id text, last_modified_time datetime NOT NULL, workspace_uris text NOT NULL, agent_name text NOT NULL DEFAULT '', parent_conversation_id text NOT NULL DEFAULT '')`)
	if err != nil {
		t.Fatalf("create summaries: %v", err)
	}
	rows := [][3]string{
		{"conv-self", "", "conv-main"},
		{"conv-main", "", ""},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO conversation_summaries VALUES (?, '2026-11-18T10:00:00Z', '[]', ?, ?)`, r[0], r[1], r[2]); err != nil {
			t.Fatalf("insert summary: %v", err)
		}
	}
}

func TestWatchdogCircuitBreaker(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n\nfunc a() {}\n")
	commitAll(t, dir, "add a.go")
	seedWatchdogSummaryDB(t, home)

	// 1. Poll breaker + reset via non-poll call + denied calls never recorded.
	if dec := runWatchdogPre(t, home, dir, "conv-poll", "manage_task", map[string]any{"Action": "list"}); dec.Decision != "allow" {
		t.Fatalf("poll 1: %+v", dec)
	}
	if dec := runWatchdogPre(t, home, dir, "conv-poll", "manage_subagents", map[string]any{"action": "status"}); dec.Decision != "allow" {
		t.Fatalf("poll 2: %+v", dec)
	}
	if dec := runWatchdogPre(t, home, dir, "conv-poll", "manage_task", map[string]any{"Action": ""}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[POLL_LOOP]") {
		t.Fatalf("poll 3 expected [POLL_LOOP] deny, got %+v", dec)
	}
	db := openDB(t, dir)
	var toolCalls, ringCnt int
	if err := db.QueryRow(`SELECT tool_calls FROM session_meta WHERE conversation_id = 'conv-poll'`).Scan(&toolCalls); err != nil || toolCalls != 2 {
		t.Fatalf("denied call leaked into tool_calls=%d err=%v", toolCalls, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM watchdog_ring WHERE conversation_id = 'conv-poll'`).Scan(&ringCnt); err != nil || ringCnt != 2 {
		t.Fatalf("denied call leaked into watchdog_ring=%d err=%v", ringCnt, err)
	}
	if dec := runWatchdogPre(t, home, dir, "conv-poll", "manage_task", map[string]any{"Action": "kill"}); dec.Decision != "allow" {
		t.Fatalf("kill reset expected allow, got %+v", dec)
	}
	if dec := runWatchdogPre(t, home, dir, "conv-poll", "manage_task", map[string]any{"Action": "list"}); dec.Decision != "allow" {
		t.Fatalf("poll after kill expected allow, got %+v", dec)
	}

	// 2. 3x identical error loop using fallback top-level toolCallId + empty ID fallback.
	failCmd := map[string]any{"CommandLine": "false"}
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("tc-%d", i)
		extra := func(p map[string]any) { p["toolCallId"] = id }
		if dec := runWatchdogPre(t, home, dir, "conv-id", "run_command", failCmd, extra); dec.Decision != "allow" {
			t.Fatalf("err %d allow failed: %+v", i, dec)
		}
		if i < 3 {
			runWatchdogPost(t, home, dir, "conv-id", "run_command", failCmd, "exit 1", id, "")
		} else {
			runWatchdogPost(t, home, dir, "conv-id", "run_command", failCmd, "exit 1", "", "")
		}
	}
	if dec := runWatchdogPre(t, home, dir, "conv-id", "run_command", failCmd); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[ERROR_LOOP]") {
		t.Fatalf("4th identical error expected [ERROR_LOOP], got %+v", dec)
	}

	// 3. TDD-safe green/red alternation NEVER triggers A-B-A-B, whereas all-error A-B-A-B DOES.
	tddEdit := map[string]any{"TargetFile": filepath.Join(dir, "tdd.go"), "CodeContent": "package main\n"}
	tddTest := map[string]any{"CommandLine": "go test ./..."}
	for i := 0; i < 2; i++ {
		if dec := runWatchdogPre(t, home, dir, "conv-tdd", "write_file", tddEdit); dec.Decision != "allow" {
			t.Fatalf("tdd write %d: %+v", i, dec)
		}
		runWatchdogPost(t, home, dir, "conv-tdd", "write_file", tddEdit, "", "", "")
		if dec := runWatchdogPre(t, home, dir, "conv-tdd", "run_command", tddTest); dec.Decision != "allow" {
			t.Fatalf("tdd test %d: %+v", i, dec)
		}
		runWatchdogPost(t, home, dir, "conv-tdd", "run_command", tddTest, "test fail", "", "")
	}
	if dec := runWatchdogPre(t, home, dir, "conv-tdd", "write_file", tddEdit); dec.Decision != "allow" {
		t.Fatalf("tdd green/red falsely blocked: %+v", dec)
	}

	cmdA, cmdB := map[string]any{"CommandLine": "echo A"}, map[string]any{"CommandLine": "echo B"}
	for i, c := range []map[string]any{cmdA, cmdB, cmdA, cmdB} {
		if dec := runWatchdogPre(t, home, dir, "conv-ab", "run_command", c); dec.Decision != "allow" {
			t.Fatalf("ab %d: %+v", i, dec)
		}
		runWatchdogPost(t, home, dir, "conv-ab", "run_command", c, "boom", "", "")
	}
	if dec := runWatchdogPre(t, home, dir, "conv-ab", "run_command", cmdA); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[ERROR_LOOP]") {
		t.Fatalf("A-B-A-B all-error expected [ERROR_LOOP], got %+v", dec)
	}
	if dec := runWatchdogPre(t, home, dir, "conv-ab", "read_file", map[string]any{"AbsolutePath": filepath.Join(dir, "a.go")}); dec.Decision != "allow" {
		t.Fatalf("distinct recovery read_file after A-B-A-B should allow, got %+v", dec)
	}
	cmdC := map[string]any{"CommandLine": "echo recover"}
	if dec := runWatchdogPre(t, home, dir, "conv-ab", "run_command", cmdC); dec.Decision != "allow" {
		t.Fatalf("distinct recovery cmdC after A-B-A-B should allow, got %+v", dec)
	}

	// 4. Role budgets (reviewer @ 25/40, self @ 90/120, main uncapped) & exemptions.
	revExtra := func(p map[string]any) { p["last_user_input"] = "You are a senior code reviewer." }
	if dec := runWatchdogPre(t, home, dir, "conv-rev", "read_file", map[string]any{"AbsolutePath": filepath.Join(dir, "a.go")}, revExtra); dec.Decision != "allow" {
		t.Fatalf("rev init: %+v", dec)
	}
	_, _ = db.Exec(`UPDATE session_meta SET tool_calls = 25 WHERE conversation_id = 'conv-rev'`)
	if dec := runWatchdogPre(t, home, dir, "conv-rev", "run_command", map[string]any{"CommandLine": "true"}); dec.Decision != "allow" || !strings.Contains(dec.Reason, "[SUBAGENT_BUDGET_WARN]") {
		t.Fatalf("rev warn expected [SUBAGENT_BUDGET_WARN], got %+v", dec)
	}
	_, _ = db.Exec(`UPDATE session_meta SET tool_calls = 40 WHERE conversation_id = 'conv-rev'`)
	if dec := runWatchdogPre(t, home, dir, "conv-rev", "run_command", map[string]any{"CommandLine": "git log"}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[SUBAGENT_BUDGET]") {
		t.Fatalf("rev cap expected [SUBAGENT_BUDGET], got %+v", dec)
	}
	exemptCases := []struct {
		tool string
		args map[string]any
	}{
		{"finish", map[string]any{"summary": "done"}},
		{"send_message", map[string]any{"Recipient": "lead", "Message": "hi"}},
		{"write_file", map[string]any{"TargetFile": filepath.Join(dir, ".agents", "scratch", "agents", "rev.md"), "CodeContent": "# R\n"}},
		{"run_command", map[string]any{"CommandLine": "git status && git add a.go && git commit -m ok"}},
		{"run_command", map[string]any{"CommandLine": "fyr --home " + home + " git-check"}},
	}
	for _, tc := range exemptCases {
		if dec := runWatchdogPre(t, home, dir, "conv-rev", tc.tool, tc.args); dec.Decision != "allow" {
			t.Fatalf("exempt %s denied at cap: %+v", tc.tool, dec)
		}
	}
	var revAfterExempt int
	if err := db.QueryRow(`SELECT tool_calls FROM session_meta WHERE conversation_id = 'conv-rev'`).Scan(&revAfterExempt); err != nil || revAfterExempt != 40 {
		t.Fatalf("exempt calls incremented conv-rev tool_calls=%d (err=%v), want 40", revAfterExempt, err)
	}

	if dec := runWatchdogPre(t, home, dir, "conv-self", "run_command", map[string]any{"CommandLine": "true"}); dec.Decision != "allow" {
		t.Fatalf("self init: %+v", dec)
	}
	_, _ = db.Exec(`UPDATE session_meta SET tool_calls = 90 WHERE conversation_id = 'conv-self'`)
	if dec := runWatchdogPre(t, home, dir, "conv-self", "run_command", map[string]any{"CommandLine": "true"}); dec.Decision != "allow" || !strings.Contains(dec.Reason, "[SUBAGENT_BUDGET_WARN]") {
		t.Fatalf("self warn: %+v", dec)
	}
	_, _ = db.Exec(`UPDATE session_meta SET tool_calls = 120 WHERE conversation_id = 'conv-self'`)
	if dec := runWatchdogPre(t, home, dir, "conv-self", "run_command", map[string]any{"CommandLine": "true"}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[SUBAGENT_BUDGET]") {
		t.Fatalf("self cap: %+v", dec)
	}

	if dec := runWatchdogPre(t, home, dir, "conv-main", "run_command", map[string]any{"CommandLine": "true"}); dec.Decision != "allow" {
		t.Fatalf("main init: %+v", dec)
	}
	var mainRole, mainName string
	_ = db.QueryRow(`SELECT agent_role, agent_name FROM session_meta WHERE conversation_id = 'conv-main'`).Scan(&mainRole, &mainName)
	if mainRole != "main" || mainName != "" {
		t.Fatalf("main role/name = %q/%q, want main/\"\"", mainRole, mainName)
	}
	_, _ = db.Exec(`UPDATE session_meta SET tool_calls = 200 WHERE conversation_id = 'conv-main'`)
	if dec := runWatchdogPre(t, home, dir, "conv-main", "run_command", map[string]any{"CommandLine": "true"}); dec.Decision != "allow" || dec.Reason != "" {
		t.Fatalf("main uncapped failed: %+v", dec)
	}

	// BUG-558: swarm builder/coder workers receive 90/120 budget, while audit roles receive 25/40.
	seedExtraSummary := func(convID, agentName, parentID string) {
		sumDB, err := sql.Open("sqlite", DSN(filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")))
		if err != nil {
			t.Fatal(err)
		}
		defer sumDB.Close()
		if _, err := sumDB.Exec(`INSERT INTO conversation_summaries VALUES (?, '2026-11-18T10:00:00Z', '[]', ?, ?)`, convID, agentName, parentID); err != nil {
			t.Fatal(err)
		}
	}
	seedExtraSummary("conv-builder", "builder-1c", "conv-main")
	seedExtraSummary("conv-sec", "security", "conv-main")
	if dec := runWatchdogPre(t, home, dir, "conv-builder", "run_command", map[string]any{"CommandLine": "true"}); dec.Decision != "allow" {
		t.Fatalf("builder init: %+v", dec)
	}
	_, _ = db.Exec(`UPDATE session_meta SET tool_calls = 40 WHERE conversation_id = 'conv-builder'`)
	if dec := runWatchdogPre(t, home, dir, "conv-builder", "run_command", map[string]any{"CommandLine": "true"}); dec.Decision != "allow" || dec.Reason != "" {
		t.Fatalf("builder-1c at 40/120 must allow cleanly (BUG-558), got %+v", dec)
	}
	_, _ = db.Exec(`UPDATE session_meta SET tool_calls = 90 WHERE conversation_id = 'conv-builder'`)
	if dec := runWatchdogPre(t, home, dir, "conv-builder", "run_command", map[string]any{"CommandLine": "true"}); dec.Decision != "allow" || !strings.Contains(dec.Reason, "[SUBAGENT_BUDGET_WARN]") {
		t.Fatalf("builder-1c at 90/120 expected [SUBAGENT_BUDGET_WARN], got %+v", dec)
	}
	_, _ = db.Exec(`UPDATE session_meta SET tool_calls = 120 WHERE conversation_id = 'conv-builder'`)
	if dec := runWatchdogPre(t, home, dir, "conv-builder", "run_command", map[string]any{"CommandLine": "true"}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[SUBAGENT_BUDGET]") {
		t.Fatalf("builder-1c at 120/120 expected [SUBAGENT_BUDGET], got %+v", dec)
	}
	if dec := runWatchdogPre(t, home, dir, "conv-sec", "run_command", map[string]any{"CommandLine": "true"}); dec.Decision != "allow" {
		t.Fatalf("sec init: %+v", dec)
	}
	_, _ = db.Exec(`UPDATE session_meta SET tool_calls = 40 WHERE conversation_id = 'conv-sec'`)
	if dec := runWatchdogPre(t, home, dir, "conv-sec", "run_command", map[string]any{"CommandLine": "true"}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[SUBAGENT_BUDGET]") {
		t.Fatalf("security at 40/40 expected [SUBAGENT_BUDGET], got %+v", dec)
	}

	// SD66: non-empty callID absent from watchdog_ring does not retroactively mark prior call as error.
	okArgs := map[string]any{"CommandLine": "echo ok"}
	if dec := runWatchdogPre(t, home, dir, "conv-sd66", "run_command", okArgs, func(p map[string]any) { p["toolCallId"] = "tc-ok-1" }); dec.Decision != "allow" {
		t.Fatalf("sd66 allow: %+v", dec)
	}
	runWatchdogPost(t, home, dir, "conv-sd66", "run_command", okArgs, "", "tc-ok-1", "")
	runWatchdogPost(t, home, dir, "conv-sd66", "run_command", okArgs, "denied", "tc-absent-99", "")
	var sd66Err int
	if err := db.QueryRow(`SELECT is_error FROM watchdog_ring WHERE conversation_id = 'conv-sd66' AND tool_call_id = 'tc-ok-1'`).Scan(&sd66Err); err != nil || sd66Err != 0 {
		t.Fatalf("absent callID poisoned prior call is_error=%d err=%v", sd66Err, err)
	}

	// SD91: main session quoting a subagent prompt phrase stays main and uncapped.
	seedExtraSummary("conv-main-quote", "", "")
	quoteExtra := func(p map[string]any) {
		p["last_user_input"] = `Please investigate why mentioning "you are a senior code reviewer" triggered cap.`
	}
	if dec := runWatchdogPre(t, home, dir, "conv-main-quote", "run_command", map[string]any{"CommandLine": "true"}, quoteExtra); dec.Decision != "allow" {
		t.Fatalf("sd91 main quote init: %+v", dec)
	}
	var mqRole string
	_ = db.QueryRow(`SELECT agent_role FROM session_meta WHERE conversation_id = 'conv-main-quote'`).Scan(&mqRole)
	if mqRole != "main" {
		t.Fatalf("sd91 main session latched role %q, want main", mqRole)
	}
	// SD91: unseeded session with quoted phrase mid-prompt also stays main.
	if dec := runWatchdogPre(t, home, dir, "conv-unseeded-quote", "run_command", map[string]any{"CommandLine": "true"}, quoteExtra); dec.Decision != "allow" {
		t.Fatalf("sd91 unseeded quote init: %+v", dec)
	}
	var uqRole string
	_ = db.QueryRow(`SELECT agent_role FROM session_meta WHERE conversation_id = 'conv-unseeded-quote'`).Scan(&uqRole)
	if uqRole != "main" {
		t.Fatalf("sd91 unseeded mid-prompt quote latched role %q, want main", uqRole)
	}

	// SD69: watchdog exemption rejects $(...), backticks, <(...), >(...) outside single quotes.
	for _, badExempt := range []string{
		"git status $(id)",
		"git status `id`",
		"git diff <(id)",
		`git commit -m "$(id)"`,
	} {
		if isWatchdogExemptBash(map[string]any{"CommandLine": badExempt}) {
			t.Errorf("isWatchdogExemptBash(%q) = true, want false", badExempt)
		}
	}
	if !isWatchdogExemptBash(map[string]any{"CommandLine": "git commit -m 'literal $(ok)'"}) {
		t.Errorf("single-quoted $(ok) should remain watchdog-exempt")
	}

	// 5. 64-row ring buffer prune.
	for i := 0; i < 70; i++ {
		_ = runWatchdogPre(t, home, dir, "conv-prune", "run_command", map[string]any{"CommandLine": fmt.Sprintf("echo %d", i)})
	}
	var pruned int
	if err := db.QueryRow(`SELECT COUNT(*) FROM watchdog_ring WHERE conversation_id = 'conv-prune'`).Scan(&pruned); err != nil || pruned != 64 {
		t.Fatalf("ring prune count = %d (err=%v), want 64", pruned, err)
	}
}

func TestInteractiveBashGuard(t *testing.T) {
	denies := []string{
		"git commit", "git commit -a", "git commit --amend",
		"git rebase -i HEAD~2", "git rebase --interactive main", "git rebase -Xtheirs -i",
		"git add -i", "git add -p", "git add --patch a.go",
		"sudo ls", "sudo -u root ls",
		"vim foo.go", "less README.md", "git diff | less",
		"echo $(vim f)", `echo "$(git commit)"`, "echo `less README.md`",
		"cat <(sudo ls)", "(vim f)", "$'foo\\'bar'; vim f",
		"git commit &> /dev/null", "echo ok |& less",
	}
	for _, cmd := range denies {
		deny, ov := checkInteractiveBashGuard(map[string]any{"CommandLine": cmd})
		if !strings.HasPrefix(deny, "[INTERACTIVE_STDIN] ") || ov != nil {
			t.Fatalf("cmd %q: deny=%q ov=%v, want [INTERACTIVE_STDIN] deny", cmd, deny, ov)
		}
	}

	allows := []string{
		`git commit -m "ok"`, `git commit -am "ok"`, `git commit -m"ok"`,
		`git commit --amend --no-edit`, `git commit -C HEAD`, `git -C repo commit -m "ok"`,
		`git rebase -Xtheirs main`, `git rebase -Xpatience main`, `git rebase -Xignore-space-change main`,
		`git rebase -s recursive --onto main HEAD~2`, `git rebase --onto main --strategy=ort`,
		`git commit -m "ok" &> /dev/null`, `echo $'foo\'bar'; echo ok`,
		`sudo -n ls`, `command -v vim`,
		"cat <<'EOF'\nvim\ngit commit\nEOF",
		`echo "vim" # git commit`,
		`echo '$(vim f)'`,
		`GIT_TERMINAL_PROMPT=1 git fetch origin`,
		`export GIT_TERMINAL_PROMPT=0; git fetch origin`,
		`env GIT_TERMINAL_PROMPT=0 git fetch origin`,
	}
	for _, cmd := range allows {
		deny, ov := checkInteractiveBashGuard(map[string]any{"CommandLine": cmd})
		if deny != "" || ov != nil {
			t.Fatalf("cmd %q unexpectedly guarded: deny=%q ov=%v", cmd, deny, ov)
		}
	}

	for _, cmd := range []string{
		`echo "GIT_TERMINAL_PROMPT=1" && git fetch origin`,
		`git fetch origin # GIT_TERMINAL_PROMPT=0`,
		`git commit -m "GIT_TERMINAL_PROMPT=0" && git fetch origin`,
	} {
		deny, ov := checkInteractiveBashGuard(map[string]any{"CommandLine": cmd})
		if deny != "" || ov == nil || ov["CommandLine"] != "export GIT_TERMINAL_PROMPT=0; "+cmd {
			t.Fatalf("cmd %q should still inject GIT_TERMINAL_PROMPT=0, got deny=%q ov=%v", cmd, deny, ov)
		}
	}

	for _, key := range []string{"CommandLine", "commandLine", "command_line", "Command", "command", "cmd"} {
		in := map[string]any{key: "git fetch origin"}
		deny, ov := checkInteractiveBashGuard(in)
		if deny != "" || ov == nil || ov[key] != "export GIT_TERMINAL_PROMPT=0; git fetch origin" {
			t.Fatalf("key %q overwrite mismatch: deny=%q ov=%v", key, deny, ov)
		}
	}

	dir, home := setupHookRepo(t), t.TempDir()
	dec := runWatchdogPre(t, home, dir, "conv-git", "run_command", map[string]any{"commandLine": "git push origin work"})
	if dec.Decision != "allow" || !strings.Contains(dec.Reason, "[AUTO_GIT_TERMINAL_PROMPT]") || dec.Overwrite["commandLine"] != "export GIT_TERMINAL_PROMPT=0; git push origin work" {
		t.Fatalf("end-to-end RunHook overwrite mismatch: %+v", dec)
	}

	noHooksDir := setupHookRepo(t)
	noHooksPath := filepath.Join(noHooksDir, ".agents", "scratch", "no-hooks")
	if err := os.MkdirAll(filepath.Dir(noHooksPath), 0o755); err != nil {
		t.Fatalf("mkdir no-hooks parent: %v", err)
	}
	if err := os.WriteFile(noHooksPath, nil, 0o600); err != nil {
		t.Fatalf("write no-hooks: %v", err)
	}
	if dec := runWatchdogPre(t, home, noHooksDir, "conv-nohooks", "run_command", map[string]any{"CommandLine": "vim foo.go"}); dec.Decision != "allow" || dec.Reason != "" {
		t.Fatalf("no-hooks should bypass [INTERACTIVE_STDIN], got %+v", dec)
	}
	if dec := runWatchdogPre(t, home, noHooksDir, "conv-nohooks", "run_command", map[string]any{"commandLine": "git push origin work"}); dec.Decision != "allow" || dec.Reason != "" || len(dec.Overwrite) != 0 {
		t.Fatalf("no-hooks should bypass GIT_TERMINAL_PROMPT=0 overwrite, got %+v", dec)
	}
	for _, args := range []map[string]any{{"CommandLine": "vim foo.go"}, {"commandLine": "git push origin work"}} {
		var buf bytes.Buffer
		in := HookInput{ConversationID: "conv-nohooks", ToolCall: HookToolCall{Name: "run_command", Args: args}}
		if err := handlePreToolBash(home, noHooksDir, in, &buf); err != nil {
			t.Fatalf("handlePreToolBash: %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &dec); err != nil {
			t.Fatalf("unmarshal %q: %v", buf.String(), err)
		}
		if dec.Decision != "allow" || dec.Reason != "" || len(dec.Overwrite) != 0 {
			t.Fatalf("handlePreToolBash with no-hooks should allow without overwrite/reason (%v), got %+v", args, dec)
		}
	}
}

func TestRelayBashGuardPushAndTag(t *testing.T) {
	t.Run("FYR_RELAY=1 denies push and mutating tag, allows read-only tag list", func(t *testing.T) {
		t.Setenv("FYR_RELAY", "1")
		for _, cmd := range []string{
			"git push",
			"git push origin main",
			"git -C sub push --tags",
			"echo ok && git push origin HEAD",
			"echo $(git push)",
			"git tag v9.9.0",
			"git tag -a v9.9.0 -m release",
			"git tag -s v9.9.0 -m signed",
			"git tag -f v9.9.0",
			"git tag -d v9.9.0",
			"git tag -l -d v9.9.0",
			"git -C repo tag --annotate v1",
		} {
			deny, ov := checkInteractiveBashGuard(map[string]any{"CommandLine": cmd})
			if !strings.HasPrefix(deny, "[RELAY_GUARD] ") || ov != nil {
				t.Errorf("FYR_RELAY=1 cmd %q: deny=%q ov=%v, want [RELAY_GUARD] deny", cmd, deny, ov)
			}
		}
		for _, cmd := range []string{
			"git tag",
			"git tag -l",
			"git tag --list",
			`git tag --list "v9.*"`,
			"git tag -l v9.*",
			"git status",
			`git commit -m "local commit"`,
		} {
			deny, ov := checkInteractiveBashGuard(map[string]any{"CommandLine": cmd})
			if deny != "" || ov != nil {
				t.Errorf("FYR_RELAY=1 read-only/local cmd %q unexpectedly denied: deny=%q ov=%v", cmd, deny, ov)
			}
		}
		dir, home := setupHookRepo(t), t.TempDir()
		if dec := runWatchdogPre(t, home, dir, "conv-relay-push", "run_command", map[string]any{"CommandLine": "git push origin main"}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[RELAY_GUARD]") {
			t.Fatalf("PreToolUse FYR_RELAY=1 git push: got %+v, want deny [RELAY_GUARD]", dec)
		}
		if dec := runWatchdogPre(t, home, dir, "conv-relay-tag", "run_command", map[string]any{"CommandLine": "git tag --list"}); dec.Decision != "allow" {
			t.Fatalf("PreToolUse FYR_RELAY=1 git tag --list: got %+v, want allow", dec)
		}
	})

	t.Run("FYR_RELAY unset does not trigger RELAY_GUARD", func(t *testing.T) {
		t.Setenv("FYR_RELAY", "")
		if deny, _ := checkInteractiveBashGuard(map[string]any{"CommandLine": "git push origin main"}); deny != "" {
			t.Errorf("FYR_RELAY unset git push unexpectedly denied: %q", deny)
		}
		if deny, _ := checkInteractiveBashGuard(map[string]any{"CommandLine": "git tag v1.0.0"}); deny != "" {
			t.Errorf("FYR_RELAY unset git tag unexpectedly denied: %q", deny)
		}
	})
}

func TestLookupWatchdogSummaryRoleROAndSymlinkReject(t *testing.T) {
	home := t.TempDir()
	seedWatchdogSummaryDB(t, home)

	// Direct roDSN query must not create state dir or watchdog-agy temp dirs.
	role, name, found := lookupWatchdogSummaryRole(home, "conv-self")
	if !found || role != "self" || name != "self" {
		t.Fatalf("lookupWatchdogSummaryRole(conv-self) = (%q, %q, %v), want (self, self, true)", role, name, found)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != ".gemini" {
			t.Fatalf("unexpected side-effect entry %q in home dir after roDSN lookup", e.Name())
		}
	}
	// Missing conversation returns sql.ErrNoRows handled cleanly without copy fallback.
	roleMiss, _, foundMiss := lookupWatchdogSummaryRole(home, "conv-missing")
	if foundMiss || roleMiss != "main" {
		t.Fatalf("lookupWatchdogSummaryRole(conv-missing) = (%q, %v), want (main, false)", roleMiss, foundMiss)
	}

	// Symlinked -wal companion must be refused fail-closed.
	agyDir := filepath.Join(home, ".gemini", "antigravity-cli")
	walTarget := filepath.Join(t.TempDir(), "evil.wal")
	if err := os.WriteFile(walTarget, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	walLink := filepath.Join(agyDir, "conversation_summaries.db-wal")
	_ = os.Remove(walLink)
	if err := os.Symlink(walTarget, walLink); err != nil {
		t.Fatal(err)
	}
	if r, _, ok := lookupWatchdogSummaryRole(home, "conv-self"); ok || r != "main" {
		t.Fatalf("expected symlinked -wal to be refused, got (%q, %v)", r, ok)
	}
}
