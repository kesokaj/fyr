package fyr

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type workflowAgentCall struct {
	TypeName  string `json:"type_name"`
	Role      string `json:"role"`
	Model     string `json:"model"`
	Workspace string `json:"workspace"`
	Prompt    string `json:"prompt"`
}

type workflowRunResult struct {
	Prompts []string            `json:"prompts"`
	Calls   []workflowAgentCall `json:"calls"`
	Output  map[string]any      `json:"output"`
}

const pyWorkflowHarness = `
import ast, asyncio, contextlib, io, json, os, sys

script_path, args_json = sys.argv[1], sys.argv[2]
args = json.loads(args_json)
mocks = json.loads(os.environ.get("FYR_WORKFLOW_MOCKS", "{}") or "{}")
prompts = []
calls = []

def phase(_name): pass
def log(_msg): pass

async def agent(prompt, schema=None, role="", type_name="", workspace="", model=""):
    prompts.append(prompt)
    calls.append({
        "type_name": type_name,
        "role": role,
        "model": model,
        "workspace": workspace,
        "prompt": prompt,
    })
    res = {
        "stage": type_name,
        "lens": "code-docs",
        "agent": type_name,
        "status": "COMPLETE",
        "verdict": "APPROVE",
        "blocking": False,
        "high_findings": 0,
        "artifact_path": ".agents/scratch/agents/mock.md",
        "not_examined": "none",
        "summary": "ok",
    }
    if type_name in mocks:
        for k, v in mocks[type_name].items():
            if v is None:
                res.pop(k, None)
            else:
                res[k] = v
    return res

async def parallel(items, fn):
    return [await fn(item) for item in items]

with open(script_path, "r", encoding="utf-8") as f:
    src = f.read()

ns = {
    "__name__": "__main__",
    "args": args,
    "phase": phase,
    "log": log,
    "agent": agent,
    "parallel": parallel,
}
conv_id = os.environ.get("FYR_TEST_CONVERSATION_ID", "")
if conv_id:
    ns["CONVERSATION_ID"] = conv_id
flags = ast.PyCF_ALLOW_TOP_LEVEL_AWAIT
code = compile(src, script_path, "exec", flags=flags)
buf = io.StringIO()
with contextlib.redirect_stdout(buf):
    ret = eval(code, ns)
    if asyncio.iscoroutine(ret):
        asyncio.run(ret)

print(json.dumps({"prompts": prompts, "calls": calls, "output": json.loads(buf.getvalue())}))
`

type workflowExecOpts struct {
	mocks          map[string]map[string]any
	workDir        string
	pathPrefix     string
	homeDir        string
	conversationID string
}

func runWorkflowScript(t *testing.T, script string, args map[string]any) workflowRunResult {
	return runWorkflowScriptWithOpts(t, script, args, workflowExecOpts{})
}

func runWorkflowScriptWithMocks(t *testing.T, script string, args map[string]any, mocks map[string]map[string]any) workflowRunResult {
	return runWorkflowScriptWithOpts(t, script, args, workflowExecOpts{mocks: mocks})
}

func overrideEnv(base []string, overrides map[string]string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, drop := overrides[k]; drop {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		if v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}

func runWorkflowScriptWithOpts(t *testing.T, script string, args map[string]any, opts workflowExecOpts) workflowRunResult {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	absScript, err := filepath.Abs(script)
	if err != nil {
		t.Fatal(err)
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "-c", pyWorkflowHarness, absScript, string(rawArgs))
	if opts.workDir != "" {
		cmd.Dir = opts.workDir
	}
	ov := map[string]string{
		"FYR_WORKFLOW_MOCKS":       "",
		"FYR_TEST_CONVERSATION_ID": opts.conversationID,
	}
	if opts.mocks != nil {
		rawMocks, err := json.Marshal(opts.mocks)
		if err != nil {
			t.Fatal(err)
		}
		ov["FYR_WORKFLOW_MOCKS"] = string(rawMocks)
	}
	if opts.pathPrefix != "" {
		ov["PATH"] = opts.pathPrefix + string(os.PathListSeparator) + os.Getenv("PATH")
	}
	if opts.homeDir != "" {
		ov["HOME"] = opts.homeDir
	}
	cmd.Env = overrideEnv(os.Environ(), ov)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", script, err, out)
	}
	var res workflowRunResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode harness output for %s: %v\n%s", script, err, out)
	}
	return res
}

func writeFakeBin(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertSafeArtifactPrompt(t *testing.T, prompt string) {
	t.Helper()
	const marker = "at `.agents/scratch/agents/"
	idx := strings.Index(prompt, marker)
	if idx < 0 {
		t.Fatalf("prompt missing artifact marker %q:\n%s", marker, prompt)
	}
	rest := prompt[idx+len(marker):]
	end := strings.IndexAny(rest, "` \n")
	if end < 0 {
		end = len(rest)
	}
	rel := rest[:end]
	if strings.Contains(rel, "/") || strings.Contains(rel, "..") || !strings.HasSuffix(rel, ".md") {
		t.Fatalf("unsafe artifact filename %q in prompt", ".agents/scratch/agents/"+rel)
	}
}

func TestWorkflowScriptsPreflightGateEdges(t *testing.T) {
	empty := runWorkflowScript(t, "workflows/preflight_gate.py", map[string]any{
		"base_ref":     "v0.1.0",
		"run_reviewer": "false",
		"run_security": "false",
		"run_verifier": "false",
	})
	if len(empty.Prompts) != 0 {
		t.Fatalf("string 'false' flags spawned %d dispatches; want 0", len(empty.Prompts))
	}
	if got := empty.Output["gate_verdict"]; got != "NO-SHIP" {
		t.Fatalf("zero-stage gate_verdict = %v; want NO-SHIP (fail closed)", got)
	}

	trav := runWorkflowScript(t, "workflows/preflight_gate.py", map[string]any{
		"base_ref":     "v0.1.0",
		"run_reviewer": "true",
		"run_security": false,
		"run_verifier": false,
		"version":      "../../evil/v9",
		"date":         "2026/../15",
	})
	if len(trav.Prompts) != 1 {
		t.Fatalf("got %d dispatches, want 1", len(trav.Prompts))
	}
	assertSafeArtifactPrompt(t, trav.Prompts[0])

	// Reviewer returning blocking: False + verdict: REQUEST_CHANGES must fail closed (NO-SHIP) and skip verifier.
	reqChanges := runWorkflowScriptWithMocks(t, "workflows/preflight_gate.py", map[string]any{
		"base_ref":     "v0.1.0",
		"run_reviewer": true,
		"run_security": false,
		"run_verifier": true,
	}, map[string]map[string]any{
		"reviewer": {"blocking": false, "status": "COMPLETE", "verdict": "REQUEST_CHANGES"},
	})
	if got := reqChanges.Output["gate_verdict"]; got != "NO-SHIP" {
		t.Fatalf("REQUEST_CHANGES with blocking=false yielded gate_verdict=%v; want NO-SHIP", got)
	}
	if len(reqChanges.Calls) != 1 {
		t.Fatalf("prior_blocked should skip verifier; got %d calls: %+v", len(reqChanges.Calls), reqChanges.Calls)
	}

	// Verifier returning blocking: False, status: COMPLETE, verdict: FAIL must yield NO-SHIP.
	verFail := runWorkflowScriptWithMocks(t, "workflows/preflight_gate.py", map[string]any{
		"base_ref":     "v0.1.0",
		"run_reviewer": true,
		"run_security": true,
		"run_verifier": true,
	}, map[string]map[string]any{
		"verifier": {"blocking": false, "status": "COMPLETE", "verdict": "FAIL"},
	})
	if got := verFail.Output["gate_verdict"]; got != "NO-SHIP" {
		t.Fatalf("verifier FAIL with blocking=false yielded gate_verdict=%v; want NO-SHIP", got)
	}

	// Verifier returning status: PASS + blocking: false after clean reviewer/security yields SHIP.
	verPass := runWorkflowScriptWithMocks(t, "workflows/preflight_gate.py", map[string]any{
		"base_ref":     "v0.1.0",
		"run_reviewer": true,
		"run_security": true,
		"run_verifier": true,
	}, map[string]map[string]any{
		"verifier": {"blocking": false, "status": "PASS", "verdict": "PASS"},
	})
	if got := verPass.Output["gate_verdict"]; got != "SHIP" {
		t.Fatalf("clean PASS stages yielded gate_verdict=%v; want SHIP", got)
	}

	// Regression cases (AG30): omitted blocking + FINDINGS / spaced or hyphenated verdicts / wrong stage status fail closed.
	for _, tc := range []struct {
		name      string
		sec, ver  bool
		mocks     map[string]map[string]any
		wantGate  string
		wantCalls int
	}{
		{
			name:      "security FINDINGS with omitted blocking",
			sec:       true,
			ver:       true,
			mocks:     map[string]map[string]any{"security": {"blocking": nil, "status": "COMPLETE", "verdict": "FINDINGS"}},
			wantGate:  "NO-SHIP",
			wantCalls: 2,
		},
		{
			name:      "reviewer spaced REQUEST CHANGES with omitted blocking",
			sec:       false,
			ver:       true,
			mocks:     map[string]map[string]any{"reviewer": {"blocking": nil, "status": "COMPLETE", "verdict": "REQUEST CHANGES"}},
			wantGate:  "NO-SHIP",
			wantCalls: 1,
		},
		{
			name:      "reviewer hyphenated request-changes",
			sec:       false,
			ver:       true,
			mocks:     map[string]map[string]any{"reviewer": {"blocking": false, "status": "COMPLETE", "verdict": "request-changes"}},
			wantGate:  "NO-SHIP",
			wantCalls: 1,
		},
		{
			name:      "reviewer IN PROGRESS status blocks",
			sec:       false,
			ver:       true,
			mocks:     map[string]map[string]any{"reviewer": {"blocking": false, "status": "IN PROGRESS", "verdict": "APPROVE"}},
			wantGate:  "NO-SHIP",
			wantCalls: 1,
		},
		{
			name:      "security PASS status (not COMPLETE) blocks",
			sec:       true,
			ver:       true,
			mocks:     map[string]map[string]any{"security": {"blocking": false, "status": "PASS", "verdict": "CLEAR"}},
			wantGate:  "NO-SHIP",
			wantCalls: 2,
		},
		{
			name:      "reviewer non-canonical stage blocks",
			sec:       false,
			ver:       true,
			mocks:     map[string]map[string]any{"reviewer": {"stage": "reviewer-code", "blocking": false, "status": "COMPLETE", "verdict": "APPROVE"}},
			wantGate:  "NO-SHIP",
			wantCalls: 1,
		},
		{
			name: "clean security CLEAR + reviewer APPROVE + verifier PASS yields SHIP",
			sec:  true,
			ver:  true,
			mocks: map[string]map[string]any{
				"security": {"blocking": false, "status": "COMPLETE", "verdict": "CLEAR"},
				"verifier": {"blocking": false, "status": "PASS", "verdict": "PASS"},
			},
			wantGate:  "SHIP",
			wantCalls: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runWorkflowScriptWithMocks(t, "workflows/preflight_gate.py", map[string]any{
				"base_ref":     "v0.1.0",
				"run_reviewer": true,
				"run_security": tc.sec,
				"run_verifier": tc.ver,
			}, tc.mocks)
			if got := res.Output["gate_verdict"]; got != tc.wantGate {
				t.Fatalf("%s: gate_verdict=%v, want %s", tc.name, got, tc.wantGate)
			}
			if len(res.Calls) != tc.wantCalls {
				t.Fatalf("%s: got %d calls, want %d: %+v", tc.name, len(res.Calls), tc.wantCalls, res.Calls)
			}
		})
	}

	const pySchemaCheck = `
import ast, sys
tree = ast.parse(open(sys.argv[1], encoding="utf-8").read(), sys.argv[1])
body = [
    n for n in tree.body
    if isinstance(n, (ast.Import, ast.ImportFrom, ast.FunctionDef))
    or (isinstance(n, ast.Assign) and any(isinstance(t, ast.Name) and t.id == "STAGE_SCHEMA" for t in n.targets))
]
ns = {}
exec(compile(ast.Module(body=body, type_ignores=[]), sys.argv[1], "exec"), ns)
stage_enum = ns["STAGE_SCHEMA"]["properties"]["stage"].get("enum")
assert stage_enum == ["reviewer", "security", "verifier"], f"STAGE_SCHEMA stage enum={stage_enum}"
enum = ns["STAGE_SCHEMA"]["properties"]["verdict"].get("enum")
want = ["APPROVE", "APPROVE_WITH_NITS", "REQUEST_CHANGES", "CLEAR", "FINDINGS", "PASS", "FAIL", "BLOCKED"]
assert enum == want, f"STAGE_SCHEMA verdict enum={enum}, want {want}"
`
	if out, err := exec.Command("python3", "-c", pySchemaCheck, "workflows/preflight_gate.py").CombinedOutput(); err != nil {
		t.Fatalf("preflight_gate.py STAGE_SCHEMA check failed: %v\n%s", err, out)
	}
}

func TestWorkflowScriptsReviewParallelEdges(t *testing.T) {
	res := runWorkflowScript(t, "workflows/review_parallel.py", map[string]any{
		"scope_text": "git diff HEAD",
		"scope_slug": "../../escape/dir",
		"date":       "2026/../15",
		"lenses":     []string{"code", "docs"},
	})
	if len(res.Prompts) != 1 {
		t.Fatalf("code+docs should merge into 1 dispatch, got %d", len(res.Prompts))
	}
	assertSafeArtifactPrompt(t, res.Prompts[0])

	// String lenses ("code, docs") must split on commas/whitespace and merge into 1 code-docs dispatch.
	strRes := runWorkflowScript(t, "workflows/review_parallel.py", map[string]any{
		"scope_text": "git diff HEAD",
		"lenses":     "code, docs",
	})
	if len(strRes.Prompts) != 1 {
		t.Fatalf("string 'code, docs' should merge into 1 dispatch, got %d", len(strRes.Prompts))
	}
	if len(strRes.Calls) != 1 || strRes.Calls[0].TypeName != "reviewer" || strRes.Calls[0].Model != "pro" {
		t.Fatalf("string 'code, docs' unexpected call: %+v", strRes.Calls)
	}

	// Mixed explorer + reviewer lenses pass literal model="flash" and model="pro".
	mixed := runWorkflowScript(t, "workflows/review_parallel.py", map[string]any{
		"scope_text": "git diff HEAD",
		"lenses":     "project, security",
	})
	if len(mixed.Calls) != 2 || mixed.Calls[0].Model != "flash" || mixed.Calls[1].Model != "pro" {
		t.Fatalf("mixed explorer+security models = %+v, want [flash, pro]", mixed.Calls)
	}

	// AST check: _build_dispatch has < 50 lines and every agent(...) call in _run_one passes literal model="flash" or model="pro".
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	const pyASTCheck = `
import ast, sys
with open(sys.argv[1], "r", encoding="utf-8") as f:
    tree = ast.parse(f.read(), sys.argv[1])
funcs = {n.name: n for n in tree.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))}
bd = funcs.get("_build_dispatch")
assert bd is not None, "missing _build_dispatch"
lines = bd.end_lineno - bd.lineno + 1
assert lines < 50, f"_build_dispatch has {lines} lines, want < 50"
ro = funcs.get("_run_one")
assert ro is not None, "missing _run_one"
agent_calls = [
    n for n in ast.walk(ro)
    if isinstance(n, ast.Call) and isinstance(n.func, ast.Name) and n.func.id == "agent"
]
assert len(agent_calls) >= 2, f"expected >= 2 literal agent(...) calls in _run_one, got {len(agent_calls)}"
for call in agent_calls:
    kw = {k.arg: k.value for k in call.keywords}
    m = kw.get("model")
    assert isinstance(m, ast.Constant) and m.value in ("flash", "pro"), f"non-literal or bad model arg: {ast.dump(call)}"
`
	if out, err := exec.Command("python3", "-c", pyASTCheck, "workflows/review_parallel.py").CombinedOutput(); err != nil {
		t.Fatalf("review_parallel.py AST check failed: %v\n%s", err, out)
	}
}

func TestWorkflowScriptsCrossFamilyRouting(t *testing.T) {
	const (
		pickClaude = `printf '{"model":"claude-opus-5-5-high","reason":"anthropic pool healthy"}\n'`
		pickGemini = `printf '{"model":"gemini-4-carbon-high","fallback_reason":"anthropic quota low"}\n'`
		failIfRun  = `echo "unexpected invocation" >&2; exit 99`
	)
	newEnv := func(t *testing.T, fyrBody, agyBody string) workflowExecOpts {
		t.Helper()
		binDir, workDir, homeDir := t.TempDir(), t.TempDir(), t.TempDir()
		writeFakeBin(t, binDir, "fyr", fyrBody)
		writeFakeBin(t, binDir, "agy", agyBody)
		return workflowExecOpts{workDir: workDir, pathPrefix: binDir, homeDir: homeDir}
	}

	t.Run("review_parallel headless success and explorer stays flash", func(t *testing.T) {
		opts := newEnv(t,
			`[ "$1" = "models" ] && [ "$2" = "--pick" ] && [ "$3" = "judge" ] && [ "$4" = "--json" ] || exit 2`+"\n"+pickClaude,
			`case " $* " in *" --model claude-opus-5-5-high "*) ;; *) exit 3 ;; esac`+"\n"+
				`printf '{"conversation_id":"c1","status":"SUCCESS","denied_actions":[],"structured_output":{"lens":"code","agent":"reviewer","verdict":"APPROVE","artifact_path":".agents/scratch/agents/reviewer-2026-11-21-demo-code.md","high_findings":0,"summary":"headless review ok"}}\n'`,
		)
		res := runWorkflowScriptWithOpts(t, "workflows/review_parallel.py", map[string]any{
			"scope_text": "git diff HEAD", "scope_slug": "demo", "date": "2026-11-21",
			"lenses": []string{"project", "code"}, "cross_family": true,
		}, opts)
		routing, _ := res.Output["model_routing"].(map[string]any)
		if routing["enabled"] != true || routing["model"] != "claude-opus-5-5-high" || routing["source"] != "fyr-models-pick" {
			t.Fatalf("unexpected model_routing: %+v", routing)
		}
		if len(res.Calls) != 1 || res.Calls[0].TypeName != "explorer" || res.Calls[0].Model != "flash" {
			t.Fatalf("expected only explorer native flash call, got %+v", res.Calls)
		}
		art, err := os.ReadFile(filepath.Join(opts.workDir, ".agents", "scratch", "agents", "reviewer-2026-11-21-demo-code.md"))
		if err != nil || !strings.Contains(string(art), "claude-opus-5-5-high") || !strings.Contains(string(art), "headless review ok") {
			t.Fatalf("expected headless artifact with model and summary, err=%v content=%q", err, art)
		}
	})

	for _, tc := range []struct {
		name, fyrBody, agyBody, wantSource string
	}{
		{
			"review_parallel denied_actions falls back to native pro", pickClaude,
			`printf '{"conversation_id":"c2","status":"SUCCESS","denied_actions":[{"tool":"write_file"}],"structured_output":{"lens":"code","agent":"reviewer","verdict":"APPROVE","artifact_path":".agents/scratch/agents/x.md","high_findings":0,"summary":"denied"}}\n'`,
			"fyr-models-pick",
		},
		{"review_parallel agy nonzero exit falls back to native pro", pickClaude, `echo "headless error" >&2; exit 1`, "fyr-models-pick"},
		{"review_parallel gemini pick skips agy and falls back", pickGemini, failIfRun, "fallback-gemini"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := newEnv(t, tc.fyrBody, tc.agyBody)
			res := runWorkflowScriptWithOpts(t, "workflows/review_parallel.py", map[string]any{
				"scope_text": "git diff HEAD", "lenses": []string{"code"}, "cross_family": true,
			}, opts)
			routing, _ := res.Output["model_routing"].(map[string]any)
			if routing["source"] != tc.wantSource {
				t.Fatalf("routing source = %v, want %s (%+v)", routing["source"], tc.wantSource, routing)
			}
			if len(res.Calls) != 1 || res.Calls[0].TypeName != "reviewer" || res.Calls[0].Model != "pro" {
				t.Fatalf("expected fallback native pro call, got %+v", res.Calls)
			}
		})
	}

	t.Run("preflight_gate cross-family success raw JSON and fallbacks", func(t *testing.T) {
		for _, tc := range []struct {
			name, fyrBody, agyBody string
			wantCalls              int
			wantArt                bool
		}{
			{
				"envelope success", pickClaude,
				`printf '{"conversation_id":"p1","status":"SUCCESS","denied_actions":[],"structured_output":{"stage":"reviewer","agent":"reviewer","status":"COMPLETE","verdict":"APPROVE","blocking":false,"artifact_path":".agents/scratch/agents/reviewer-2026-11-21-preflight-v9.9.0.md","not_examined":"none","summary":"ship ready"}}\n'`,
				0, true,
			},
			{
				"raw stage JSON with status COMPLETE", pickClaude,
				`printf '{"stage":"reviewer","agent":"reviewer","status":"COMPLETE","verdict":"APPROVE","blocking":false,"artifact_path":".agents/scratch/agents/reviewer-2026-11-21-preflight-v9.9.0.md","not_examined":"none","summary":"raw json ok"}\n'`,
				0, true,
			},
			{
				"denied_actions falls back to native pro", pickClaude,
				`printf '{"conversation_id":"p2","status":"SUCCESS","denied_actions":[{"tool":"run_command"}],"structured_output":{}}\n'`,
				1, false,
			},
			{"gemini pick skips agy and uses native pro", pickGemini, failIfRun, 1, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				opts := newEnv(t, tc.fyrBody, tc.agyBody)
				res := runWorkflowScriptWithOpts(t, "workflows/preflight_gate.py", map[string]any{
					"base_ref": "v9.8.0", "version": "v9.9.0", "date": "2026-11-21",
					"run_reviewer": true, "run_security": false, "run_verifier": false, "cross_family": true,
				}, opts)
				if res.Output["gate_verdict"] != "SHIP" || len(res.Calls) != tc.wantCalls {
					t.Fatalf("gate=%v calls=%d (want SHIP, %d): %+v", res.Output["gate_verdict"], len(res.Calls), tc.wantCalls, res.Calls)
				}
				if tc.wantCalls == 1 && res.Calls[0].Model != "pro" {
					t.Fatalf("fallback model = %q, want pro", res.Calls[0].Model)
				}
				if tc.wantArt {
					if _, err := os.Stat(filepath.Join(opts.workDir, ".agents", "scratch", "agents", "reviewer-2026-11-21-preflight-v9.9.0.md")); err != nil {
						t.Fatalf("expected artifact file: %v", err)
					}
				}
			})
		}
	})

	t.Run("runtime-aware default honors CONVERSATION_ID and FYR_WORKFLOW_MOCKS", func(t *testing.T) {
		opts := newEnv(t, pickClaude,
			`printf '{"conversation_id":"c3","status":"SUCCESS","denied_actions":[],"structured_output":{"lens":"code","agent":"reviewer","verdict":"APPROVE","artifact_path":".agents/scratch/agents/reviewer-today-worktree-code.md","high_findings":0,"summary":"auto cross-family"}}\n'`,
		)
		opts.conversationID = "conv-live-1"
		on := runWorkflowScriptWithOpts(t, "workflows/review_parallel.py", map[string]any{
			"scope_text": "git diff HEAD", "lenses": []string{"code"},
		}, opts)
		if routing, _ := on.Output["model_routing"].(map[string]any); routing["enabled"] != true || len(on.Calls) != 0 {
			t.Fatalf("CONVERSATION_ID default should enable cross-family: routing=%+v calls=%+v", routing, on.Calls)
		}

		for _, tc := range []struct {
			name   string
			args   map[string]any
			convID string
			mocks  map[string]map[string]any
		}{
			{"no CONVERSATION_ID", map[string]any{"scope_text": "git diff HEAD", "lenses": []string{"code"}}, "", nil},
			{"CONVERSATION_ID with FYR_WORKFLOW_MOCKS", map[string]any{"scope_text": "git diff HEAD", "lenses": []string{"code"}}, "conv-live-2", map[string]map[string]any{}},
			{"CONVERSATION_ID with explicit cross_family=false", map[string]any{"scope_text": "git diff HEAD", "lenses": []string{"code"}, "cross_family": false}, "conv-live-3", nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				o := newEnv(t, failIfRun, failIfRun)
				o.conversationID, o.mocks = tc.convID, tc.mocks
				off := runWorkflowScriptWithOpts(t, "workflows/review_parallel.py", tc.args, o)
				if routing, _ := off.Output["model_routing"].(map[string]any); routing["enabled"] != false || len(off.Calls) != 1 {
					t.Fatalf("%s: expected disabled routing + 1 native call, got routing=%+v calls=%+v", tc.name, routing, off.Calls)
				}
			})
		}
	})
}
