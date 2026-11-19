package fyr

import (
	"encoding/json"
	"os"
	"os/exec"
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
flags = ast.PyCF_ALLOW_TOP_LEVEL_AWAIT
code = compile(src, script_path, "exec", flags=flags)
buf = io.StringIO()
with contextlib.redirect_stdout(buf):
    ret = eval(code, ns)
    if asyncio.iscoroutine(ret):
        asyncio.run(ret)

print(json.dumps({"prompts": prompts, "calls": calls, "output": json.loads(buf.getvalue())}))
`

func runWorkflowScript(t *testing.T, script string, args map[string]any) workflowRunResult {
	return runWorkflowScriptWithMocks(t, script, args, nil)
}

func runWorkflowScriptWithMocks(t *testing.T, script string, args map[string]any, mocks map[string]map[string]any) workflowRunResult {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "-c", pyWorkflowHarness, script, string(rawArgs))
	if mocks != nil {
		rawMocks, err := json.Marshal(mocks)
		if err != nil {
			t.Fatal(err)
		}
		cmd.Env = append(os.Environ(), "FYR_WORKFLOW_MOCKS="+string(rawMocks))
	}
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
