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

func TestPreToolUseWorkflowNonLiteralTypeRequiresExplicitProOnFast(t *testing.T) {
	home := t.TempDir()
	fastModel := "gemini-3.8-flash-medium"
	proModel := "gemini-4-carbon-high"
	wfDir := t.TempDir()

	omittedDyn := `await agent(prompt="p", schema={"type":"string"}, type_name=item["type_name"])`
	noneDyn := `await agent(prompt="p", schema={"type":"string"}, type_name=item["type_name"], model=None)`
	proDyn := `await agent(prompt="p", schema={"type":"string"}, type_name=item["type_name"], model="pro")`

	spOmitted := filepath.Join(wfDir, "dyn_omitted.py")
	spNone := filepath.Join(wfDir, "dyn_none.py")
	spPro := filepath.Join(wfDir, "dyn_pro.py")
	for p, src := range map[string]string{
		spOmitted: omittedDyn + "\n",
		spNone:    noneDyn + "\n",
		spPro:     proDyn + "\n",
	} {
		if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Inline Script with omitted or None model on fast session denies [MODEL_OVERRIDE].
	for _, tc := range []struct {
		name, script string
	}{
		{"inline omitted model", omittedDyn},
		{"inline model=None", noneDyn},
	} {
		d := runPreToolHookForTest(t, home, fastModel, "run_workflow", map[string]any{
			"Script":      tc.script,
			"Description": tc.name,
		})
		if d.Decision != "deny" || !strings.Contains(d.Reason, "[MODEL_OVERRIDE]") || !strings.Contains(d.Reason, `model="pro"`) {
			t.Errorf("%s on fast: got %+v, want deny with [MODEL_OVERRIDE] requiring model=\"pro\"", tc.name, d)
		}
	}

	// 2. ScriptPath with omitted or None model on fast session denies [AUTO_SUBAGENT_PRO].
	for _, tc := range []struct {
		name, path string
	}{
		{"ScriptPath omitted model", spOmitted},
		{"ScriptPath model=None", spNone},
	} {
		d := runPreToolHookInDir(t, home, wfDir, fastModel, "run_workflow", map[string]any{
			"ScriptPath":  tc.path,
			"Description": tc.name,
		})
		if d.Decision != "deny" || !strings.Contains(d.Reason, "[AUTO_SUBAGENT_PRO]") || !strings.Contains(d.Reason, `model="pro"`) {
			t.Errorf("%s on fast: got %+v, want deny with [AUTO_SUBAGENT_PRO] requiring model=\"pro\"", tc.name, d)
		}
	}

	// 3. Pro session allows omitted/None, and fast session allows explicit model="pro".
	for _, args := range []map[string]any{
		{"Script": omittedDyn, "Description": "pro inline omitted"},
		{"Script": noneDyn, "Description": "pro inline none"},
		{"ScriptPath": spOmitted, "Description": "pro path omitted"},
		{"ScriptPath": spNone, "Description": "pro path none"},
	} {
		if d := runPreToolHookInDir(t, home, wfDir, proModel, "run_workflow", args); d.Decision != "allow" || d.Overwrite != nil {
			t.Errorf("pro session (%s): got %+v, want clean allow", args["Description"], d)
		}
	}
	for _, args := range []map[string]any{
		{"Script": proDyn, "Description": "fast inline pro"},
		{"ScriptPath": spPro, "Description": "fast path pro"},
	} {
		if d := runPreToolHookInDir(t, home, wfDir, fastModel, "run_workflow", args); d.Decision != "allow" || d.Overwrite != nil {
			t.Errorf("fast session (%s): got %+v, want clean allow", args["Description"], d)
		}
	}
}

func TestPreToolUseRealWorkflowScriptsAllowedOnProAndFast(t *testing.T) {
	home := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	models := []string{"gemini-4-carbon-high", "gemini-3.8-flash-medium"}
	scripts := []string{
		filepath.Join("workflows", "review_parallel.py"),
		filepath.Join("workflows", "preflight_gate.py"),
	}
	for _, scriptPath := range scripts {
		src, err := os.ReadFile(filepath.Join(repoRoot, scriptPath))
		if err != nil {
			t.Fatalf("read %s: %v", scriptPath, err)
		}
		for _, m := range models {
			dPath := runPreToolHookInDir(t, home, repoRoot, m, "run_workflow", map[string]any{
				"ScriptPath":  scriptPath,
				"Description": "real script path " + filepath.Base(scriptPath),
			})
			if dPath.Decision != "allow" || dPath.Overwrite != nil {
				t.Errorf("%s ScriptPath on %s: got %+v, want clean allow", scriptPath, m, dPath)
			}
			dInline := runPreToolHookInDir(t, home, repoRoot, m, "run_workflow", map[string]any{
				"Script":      string(src),
				"Description": "real inline " + filepath.Base(scriptPath),
			})
			if dInline.Decision != "allow" || dInline.Overwrite != nil {
				t.Errorf("%s inline Script on %s: got %+v, want clean allow", scriptPath, m, dInline)
			}
		}
	}
}

func TestPreToolUseWorkflowOrchestratorAndAutoRequirePro(t *testing.T) {
	home := t.TempDir()
	wfDir := t.TempDir()
	spInherit := filepath.Join(wfDir, "inh.py")
	if err := os.WriteFile(spInherit, []byte("await agent(prompt='Check', schema={'type':'string'}, type_name='reviewer', model='inherit')\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	omitted := `await agent(prompt="Check", schema={"type":"string"}, type_name="reviewer")`
	inlineInherit := `await agent(prompt="Check", schema={"type":"string"}, type_name="security", model="inherit")`

	for _, m := range []string{"gemini-pro-orchestrator", "auto", "claude-sonnet-5-5-low"} {
		dOmit := runPreToolHookForTest(t, home, m, "run_workflow", map[string]any{"Script": omitted, "Description": "omit"})
		if dOmit.Decision != "deny" || !strings.Contains(dOmit.Reason, "[AUTO_SUBAGENT_PRO]") {
			t.Errorf("model %q omitted model: got %+v, want [AUTO_SUBAGENT_PRO] deny", m, dOmit)
		}
		dInh := runPreToolHookForTest(t, home, m, "run_workflow", map[string]any{"Script": inlineInherit, "Description": "inh"})
		if dInh.Decision != "allow" || dInh.Overwrite == nil {
			t.Fatalf("model %q inline inherit: got %+v, want allow+overwrite", m, dInh)
		}
		if s, _ := dInh.Overwrite["Script"].(string); !strings.Contains(s, `model="pro"`) {
			t.Errorf("model %q expected model=\"pro\" patch, got:\n%s", m, s)
		}
		dPath := runPreToolHookInDir(t, home, wfDir, m, "run_workflow", map[string]any{"ScriptPath": spInherit, "Description": "path-inh"})
		if dPath.Decision != "deny" || !strings.Contains(dPath.Reason, "[AUTO_SUBAGENT_PRO]") {
			t.Errorf("model %q ScriptPath inherit: got %+v, want [AUTO_SUBAGENT_PRO] deny", m, dPath)
		}
	}

	for _, m := range []string{"claude-sonnet-5-5-high", "gemini-4-carbon-high", "gemini-4-hydrogen-high"} {
		d := runPreToolHookForTest(t, home, m, "run_workflow", map[string]any{"Script": omitted, "Description": "keep"})
		if d.Decision != "allow" || d.Overwrite != nil {
			t.Errorf("model %q omitted model: got %+v, want clean allow", m, d)
		}
	}
}

func TestPreToolUseWorkflowBypassAndPathHardening(t *testing.T) {
	home := t.TempDir()
	proModel := "gemini-4-carbon-high"
	fastModel := "gemini-3.8-flash-medium"

	// SD87: *args splat, **kwargs without type_name, f-string interpolations, parenthesized, aliased, attribute calls.
	for _, tc := range []struct {
		name, model, script, wantSub string
	}{
		{"star args splat", proModel, `await agent("a", "b", *["c", "security", None, "flash"])`, "[MODEL_OVERRIDE]"},
		{"kwargs splat without type_name", proModel, `await agent(**{"type_name": "security", "model": "flash"})`, "[MODEL_OVERRIDE]"},
		{"f-string flash override", proModel, `x = f"{agent(prompt='p', schema={'type':'string'}, role='r', type_name='security', model='flash')}"`, "[MODEL_OVERRIDE]"},
		{"raw f-string backslash-brace flash override", proModel, `x = rf"\{agent(prompt='p', schema={'type':'string'}, role='r', type_name='security', model='flash')}"`, "[MODEL_OVERRIDE]"},
		{"f-string backslash-brace flash override", proModel, `x = f"\{agent(prompt='p', schema={'type':'string'}, role='r', type_name='security', model='flash')}"`, "[MODEL_OVERRIDE]"},
		{"triple f-string backslash-brace flash override", proModel, `x = f"""\{agent(prompt='p', schema={'type':'string'}, role='r', type_name='security', model='flash')}"""`, "[MODEL_OVERRIDE]"},
		{"f-string pep701 backslash-brace same quote", proModel, `x = f"\{agent(prompt='p', schema={'type':'string'}, role='r', type_name="security", model="flash")}"`, "[MODEL_OVERRIDE]"},
		{"f-string nested braces fast omitted", fastModel, `x = f"{ {'k': agent(prompt='p', schema={'type':'string'}, role='r', type_name='reviewer')} }"`, "[AUTO_SUBAGENT_PRO]"},
		{"parenthesized call", proModel, `await (agent)(prompt="p", type_name="security", model="flash")`, "[MODEL_OVERRIDE]"},
		{"double parenthesized call", proModel, `await ((agent))(prompt="p", type_name="reviewer", model="flash")`, "[MODEL_OVERRIDE]"},
		{"aliased call", proModel, "fn = agent\nawait fn(prompt='p', type_name='security', model='flash')", "[MODEL_OVERRIDE]"},
		{"parenthesized alias", proModel, "fn = (agent)\nawait fn(prompt='p', type_name='security', model='flash')", "[MODEL_OVERRIDE]"},
		{"attribute call", proModel, `await ns.agent(prompt="p", type_name="security", model="flash")`, "[MODEL_OVERRIDE]"},
	} {
		d := runPreToolHookForTest(t, home, tc.model, "run_workflow", map[string]any{"Script": tc.script, "Description": tc.name})
		if d.Decision != "deny" || !strings.Contains(d.Reason, tc.wantSub) {
			t.Errorf("%s: got %+v, want deny containing %q", tc.name, d, tc.wantSub)
		}
	}

	// SD87: benign f-string and normal literal call still allowed.
	okF := `msg = f"hello {name}"` + "\n" + `await agent(prompt=msg, schema={"type":"string"}, type_name="explorer", model="flash")`
	if d := runPreToolHookForTest(t, home, fastModel, "run_workflow", map[string]any{"Script": okF, "Description": "benign f-string"}); d.Decision != "allow" {
		t.Errorf("benign f-string unexpectedly denied: %+v", d)
	}

	// SD89: relative traversal outside dir, absolute outside dir, and symlink ancestor dir all fail closed.
	wsDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "evil.py")
	if err := os.WriteFile(outsideFile, []byte("print('hi')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	relEscape, err := filepath.Rel(wsDir, outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	linkParent := filepath.Join(wsDir, "linked_sub")
	if err := os.Symlink(outsideDir, linkParent); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, sp string
	}{
		{"relative traversal", relEscape},
		{"absolute outside workspace", outsideFile},
		{"symlink ancestor directory", filepath.Join("linked_sub", "evil.py")},
	} {
		d := runPreToolHookInDir(t, home, wsDir, proModel, "run_workflow", map[string]any{"ScriptPath": tc.sp, "Description": tc.name})
		if d.Decision != "deny" || !strings.Contains(d.Reason, "[MODEL_OVERRIDE]") {
			t.Errorf("%s (%s): got %+v, want [MODEL_OVERRIDE] deny", tc.name, tc.sp, d)
		}
	}
}

func TestSkipPythonWSAndComments(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		start int
		want  int
	}{
		{name: "spaces tabs newlines", src: "  \t\r\nfoo", start: 0, want: 5},
		{name: "comment to newline", src: "# hi there\n  x", start: 0, want: 13},
		{name: "comment at eof", src: "# trailing eof", start: 0, want: 14},
		{name: "lf continuation", src: "\\\n  y", start: 0, want: 4},
		{name: "crlf continuation", src: "\\\r\n\tz", start: 0, want: 4},
		{name: "bare backslash stops", src: "\\x", start: 0, want: 0},
		{name: "non-ws token stops", src: "agent()", start: 0, want: 0},
		{name: "past end unchanged", src: "abc", start: 5, want: 5},
	}
	for _, tc := range cases {
		if got := skipPythonWSAndComments(tc.src, tc.start); got != tc.want {
			t.Errorf("%s: skipPythonWSAndComments(%q, %d) = %d, want %d", tc.name, tc.src, tc.start, got, tc.want)
		}
	}
}

func TestV910Workflow(t *testing.T) {
	home := t.TempDir()
	wfDir := filepath.Join(home, ".gemini", "config", "workflows")
	if err := os.MkdirAll(wfDir, 0o700); err != nil {
		t.Fatal(err)
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	extRepo := t.TempDir()
	names := []string{"phase_loop.py", "review_parallel.py", "preflight_gate.py"}
	for _, name := range names {
		src, err := os.ReadFile(filepath.Join(repoRoot, "workflows", name))
		if err != nil {
			t.Fatalf("read workflow %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(wfDir, name), src, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range names {
		sp := filepath.Join(wfDir, name)
		for _, m := range []string{"gemini-4-carbon-high", "gemini-3.8-flash-medium"} {
			if d := runPreToolHookInDir(t, home, extRepo, m, "run_workflow", map[string]any{"ScriptPath": sp, "Description": name}); d.Decision != "allow" || d.Overwrite != nil {
				t.Errorf("installed %s on %s from external repo: got %+v, want allow", name, m, d)
			}
		}
	}

	// Must-fail: arbitrary path, .. escapes, symlinks, non-.py, >1MiB, and model override inside installed dir.
	outsideDir := t.TempDir()
	outsidePy := filepath.Join(outsideDir, "evil.py")
	if err := os.WriteFile(outsidePy, []byte("print('x')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePy, filepath.Join(wfDir, "sym.py")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "bad.sh"), []byte("echo hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "big.py"), bytes.Repeat([]byte("a"), (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "override.py"), []byte(`await agent(prompt="x", schema={"type":"string"}, type_name="reviewer", model="flash")`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	relToWf, err := filepath.Rel(extRepo, filepath.Join(wfDir, "phase_loop.py"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, sp string }{
		{"arbitrary outside", outsidePy},
		{"dotdot escape from workflows", filepath.Join(wfDir, "..", "escape.py")},
		{"relative dotdot to workflows", relToWf},
		{"symlink inside workflows", filepath.Join(wfDir, "sym.py")},
		{"non-py inside workflows", filepath.Join(wfDir, "bad.sh")},
		{"oversize py inside workflows", filepath.Join(wfDir, "big.py")},
		{"model override inside workflows", filepath.Join(wfDir, "override.py")},
	} {
		d := runPreToolHookInDir(t, home, extRepo, "gemini-4-carbon-high", "run_workflow", map[string]any{"ScriptPath": tc.sp, "Description": tc.name})
		if d.Decision != "deny" || !strings.Contains(d.Reason, "[MODEL_OVERRIDE]") {
			t.Errorf("%s (%s): got %+v, want deny [MODEL_OVERRIDE]", tc.name, tc.sp, d)
		}
	}
	symHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(symHome, ".gemini", "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(symHome, ".gemini", "config", "workflows")); err != nil {
		t.Fatal(err)
	}
	if d := runPreToolHookInDir(t, symHome, extRepo, "gemini-4-carbon-high", "run_workflow", map[string]any{
		"ScriptPath": filepath.Join(symHome, ".gemini", "config", "workflows", "evil.py"), "Description": "symlinked workflows dir",
	}); d.Decision != "deny" || !strings.Contains(d.Reason, "[MODEL_OVERRIDE]") {
		t.Errorf("symlinked workflows dir: got %+v, want deny [MODEL_OVERRIDE]", d)
	}
}

func TestV910Clamp(t *testing.T) {
	if maxReadFileBytes != 46080 {
		t.Fatalf("maxReadFileBytes = %d, want 46080", maxReadFileBytes)
	}
	dir, home := setupHookRepo(t), t.TempDir()
	var dense strings.Builder
	dense.WriteString("package sample\n\nfunc DenseTarget() int {\n")
	for i := 4; i < 350; i++ {
		prefix := fmt.Sprintf("\t// dense line %03d ", i)
		dense.WriteString(prefix + strings.Repeat("d", 116-len(prefix)) + "\n")
	}
	dense.WriteString("\treturn 1\n}\n")
	writeFile(t, dir, "dense_350.go", dense.String())
	writeFile(t, dir, "exact_16384.txt", strings.Repeat(strings.Repeat("x", 162)+"\n", 100)+strings.Repeat("y", 84))
	writeFile(t, dir, "under_16383.txt", strings.Repeat(strings.Repeat("x", 162)+"\n", 100)+strings.Repeat("y", 83))

	call := func(conv string, args map[string]any) HookDecisionOutput {
		t.Helper()
		req, _ := json.Marshal(map[string]any{"conversationId": conv, "workspacePaths": []string{dir}, "toolCall": map[string]any{"name": "read_file", "args": args}})
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("pre-tool-use: %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal %q: %v", out.String(), err)
		}
		return dec
	}

	d1 := call("conv-v910-dense", map[string]any{"Path": "dense_350.go"})
	if d1.Decision != "deny" || !strings.HasPrefix(d1.Reason, "[CLAMPED]\n") || !strings.Contains(d1.Reason, "func DenseTarget") {
		t.Fatalf("350-line ~39.9KB unranged = %+v, want deny [CLAMPED] with skeleton", d1)
	}
	if d2 := call("conv-v910-dense", map[string]any{"Path": "dense_350.go", "StartLine": 1, "EndLine": 100}); d2.Decision != "allow" {
		t.Fatalf("ranged retry 1..100 = %+v, want allow", d2)
	}
	if d := call("conv-v910-exact", map[string]any{"Path": "exact_16384.txt"}); d.Decision != "deny" || !strings.HasPrefix(d.Reason, "[CLAMPED]\n") {
		t.Fatalf("exact 16384B file = %+v, want deny [CLAMPED]", d)
	}
	if d := call("conv-v910-under", map[string]any{"Path": "under_16383.txt"}); d.Decision != "allow" {
		t.Fatalf("16383B <400L file = %+v, want allow", d)
	}
}

func TestV910Bash(t *testing.T) {
	for _, cmd := range []string{
		`read -p "Proceed? " ans`,
		`read -rp "Enter: " x`,
		`echo ok && read -p"Y/N: " y`,
		"ssh host",
		"ssh user@host",
		"ssh -p 2222 user@host",
		"ssh -t user@host ls",
		"ssh -tt -o BatchMode=yes user@host ls",
		"ssh -o BatchMode=yes user@host",
		"ssh user@host ls",
	} {
		if deny, ov := checkInteractiveBashGuard(map[string]any{"CommandLine": cmd}); !strings.HasPrefix(deny, "[INTERACTIVE_STDIN] ") || ov != nil {
			t.Errorf("interactive cmd %q: deny=%q ov=%v, want [INTERACTIVE_STDIN]", cmd, deny, ov)
		}
	}
	for _, cmd := range []string{
		"read -r line",
		"read -d p -r line",
		"ssh -o BatchMode=yes user@host ls",
		"ssh -oBatchMode=yes user@host ls -t",
		"ssh -V",
		"ssh -G myhost",
	} {
		if deny, ov := checkInteractiveBashGuard(map[string]any{"CommandLine": cmd}); deny != "" || ov != nil {
			t.Errorf("non-interactive cmd %q unexpectedly guarded: deny=%q ov=%v", cmd, deny, ov)
		}
	}
	for _, cmd := range []string{
		"sed -n '1,10p;20,30p' a.go",
		"awk 'NR>=1 && NR<=10' a.go",
		`python3 -c "print(open('a.go').read())"`,
		"grep -n '' a.go",
		"cat a.go 2>/dev/null",
		"cat a.go || true",
		"head -n 20 a.go b.go",
		"(cd sub && cat a.go)",
	} {
		if got := ParseBashFileReads(cmd); len(got) == 0 {
			t.Errorf("ParseBashFileReads(%q) = empty, want parsed read(s)", cmd)
		}
	}
}

func TestV910Watchdog(t *testing.T) {
	for _, role := range []string{"self", "builder", "builder-1", "builder_p7", "coder", "coder-2", "coder_x"} {
		if !IsBuilderSubagentRole(role) {
			t.Errorf("IsBuilderSubagentRole(%q) = false, want true", role)
		}
		if w, c := SubagentBudgetCap(role); w != 90 || c != 120 {
			t.Errorf("SubagentBudgetCap(%q) = (%d,%d), want (90,120)", role, w, c)
		}
	}
	for _, role := range []string{"reviewer", "security", "verifier", "explorer", "dreamer", "documentation", "research", "judge", "unknown-role", "sub-1", "", "main"} {
		if IsBuilderSubagentRole(role) {
			t.Errorf("IsBuilderSubagentRole(%q) = true, want false", role)
		}
		if w, c := SubagentBudgetCap(role); w != 25 || c != 40 {
			t.Errorf("SubagentBudgetCap(%q) = (%d,%d), want (25,40)", role, w, c)
		}
	}

	dir, home := setupHookRepo(t), t.TempDir()
	writeFile(t, dir, "a.go", "package main\n\nfunc a() {}\n")
	commitAll(t, dir, "add a.go")
	seedWatchdogSummaryDB(t, home)
	sumDB, err := sql.Open("sqlite", DSN(filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range [][3]string{
		{"conv-v910-b", "builder-p7", "conv-main"},
		{"conv-v910-u", "custom-audit", "conv-main"},
	} {
		if _, err := sumDB.Exec(`INSERT INTO conversation_summaries VALUES (?, '2026-11-23T05:00:00Z', '[]', ?, ?)`, r[0], r[1], r[2]); err != nil {
			t.Fatal(err)
		}
	}
	sumDB.Close()

	// Exempt wrap-up & denied calls must NOT increment session_meta.tool_calls.
	revExtra := func(p map[string]any) { p["last_user_input"] = "You are a senior code reviewer." }
	_ = runWatchdogPre(t, home, dir, "conv-v910-rev", "run_command", map[string]any{"CommandLine": "true"}, revExtra)
	_ = runWatchdogPre(t, home, dir, "conv-v910-b", "run_command", map[string]any{"CommandLine": "true"})
	_ = runWatchdogPre(t, home, dir, "conv-v910-u", "run_command", map[string]any{"CommandLine": "true"})
	db := openDB(t, dir)
	_, _ = db.Exec(`UPDATE session_meta SET tool_calls = 40 WHERE conversation_id = 'conv-v910-rev'`)
	_, _ = db.Exec(`UPDATE session_meta SET tool_calls = 120 WHERE conversation_id = 'conv-v910-b'`)
	_, _ = db.Exec(`UPDATE session_meta SET tool_calls = 40 WHERE conversation_id = 'conv-v910-u'`)

	exempts := []struct {
		tool string
		args map[string]any
	}{
		{"write_file", map[string]any{"TargetFile": filepath.Join(dir, ".agents", "scratch", "agents", "rev.md"), "CodeContent": "# Done\n"}},
		{"run_command", map[string]any{"CommandLine": "git status && git add a.go && git commit -m ok"}},
		{"run_command", map[string]any{"CommandLine": "git checkout --detach HEAD"}},
		{"finish", map[string]any{"summary": "done"}},
	}
	for _, conv := range []string{"conv-v910-rev", "conv-v910-b"} {
		for _, ex := range exempts {
			if d := runWatchdogPre(t, home, dir, conv, ex.tool, ex.args); d.Decision != "allow" {
				t.Fatalf("%s exempt %s denied: %+v", conv, ex.tool, d)
			}
		}
		if d := runWatchdogPre(t, home, dir, conv, "run_command", map[string]any{"CommandLine": "git log"}); d.Decision != "deny" {
			t.Fatalf("%s at cap should deny git log: %+v", conv, d)
		}
	}
	var revCalls, bCalls int
	_ = db.QueryRow(`SELECT tool_calls FROM session_meta WHERE conversation_id = 'conv-v910-rev'`).Scan(&revCalls)
	_ = db.QueryRow(`SELECT tool_calls FROM session_meta WHERE conversation_id = 'conv-v910-b'`).Scan(&bCalls)
	if revCalls != 40 || bCalls != 120 {
		t.Fatalf("exempt/denied leaked into tool_calls: reviewer=%d (want 40), builder=%d (want 120)", revCalls, bCalls)
	}
	// Unknown subagent role gets 40 cap (not 120).
	if d := runWatchdogPre(t, home, dir, "conv-v910-u", "run_command", map[string]any{"CommandLine": "true"}); d.Decision != "deny" || !strings.Contains(d.Reason, "40/40") {
		t.Fatalf("unknown subagent role at 40 = %+v, want deny 40/40", d)
	}
	// [ERROR_LOOP] replaces [TOKEN_LOOP].
	failCmd := map[string]any{"CommandLine": "false"}
	for i := 0; i < 3; i++ {
		_ = runWatchdogPre(t, home, dir, "conv-v910-err", "run_command", failCmd)
		runWatchdogPost(t, home, dir, "conv-v910-err", "run_command", failCmd, "exit 1", "", "")
	}
	if d := runWatchdogPre(t, home, dir, "conv-v910-err", "run_command", failCmd); d.Decision != "deny" || !strings.Contains(d.Reason, "[ERROR_LOOP]") || strings.Contains(d.Reason, "[TOKEN_LOOP]") {
		t.Fatalf("3x error loop = %+v, want deny [ERROR_LOOP]", d)
	}
}

func TestV910WorkflowClampBashAndWatchdog(t *testing.T) {
	t.Run("workflow", TestV910Workflow)
	t.Run("clamp", TestV910Clamp)
	t.Run("bash", TestV910Bash)
	t.Run("watchdog", TestV910Watchdog)
}

func TestHookWorkflowWorkspaceDotDotSymlinkEscape(t *testing.T) {
	home := t.TempDir()
	wsDir := t.TempDir()
	outsideBase := t.TempDir()
	outsideNested := filepath.Join(outsideBase, "nested")
	if err := os.MkdirAll(outsideNested, 0o755); err != nil {
		t.Fatal(err)
	}
	// Harmless file inside workspace vs malicious file outside workspace reached via symlink/..
	if err := os.WriteFile(filepath.Join(wsDir, "outside.py"), []byte("print('benign')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideBase, "outside.py"), []byte(`await agent(prompt="x", schema={"type":"string"}, type_name="reviewer", model="flash")`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideNested, filepath.Join(wsDir, "symlink")); err != nil {
		t.Fatal(err)
	}
	d := runPreToolHookInDir(t, home, wsDir, "gemini-4-carbon-high", "run_workflow", map[string]any{
		"ScriptPath":  "symlink/../outside.py",
		"Description": "dotdot through workspace symlink",
	})
	if d.Decision != "deny" || !strings.Contains(d.Reason, "[MODEL_OVERRIDE]") {
		t.Fatalf("symlink/../outside.py: got %+v, want deny [MODEL_OVERRIDE]", d)
	}
}
