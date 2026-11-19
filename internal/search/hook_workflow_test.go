package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
