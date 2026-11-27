package fyr

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/redact"
)

type workflowAgentCall struct {
	AgentID        int    `json:"agent_id"`
	Turn           int    `json:"turn"`
	TypeName       string `json:"type_name"`
	Role           string `json:"role"`
	Model          string `json:"model"`
	Workspace      string `json:"workspace"`
	Prompt         string `json:"prompt"`
	StandingPrompt string `json:"standing_prompt,omitempty"`
}

type workflowRunResult struct {
	Prompts []string            `json:"prompts"`
	Calls   []workflowAgentCall `json:"calls"`
	Output  map[string]any      `json:"output"`
}

const pyWorkflowHarness = `
import ast, asyncio, contextlib, io, json, os, sys

sys.dont_write_bytecode = True
script_path, args_json = sys.argv[1], sys.argv[2]
script_dir = os.path.dirname(os.path.abspath(script_path))
if not sys.path or sys.path[0] != script_dir:
    sys.path.insert(0, script_dir)
args = json.loads(args_json)
mocks = json.loads(os.environ.get("FYR_WORKFLOW_MOCKS", "{}") or "{}")
prompts = []
calls = []
next_aid = [0]

def phase(_name): pass
def log(_msg): pass

def _lookup_mock(type_name, role, turn):
    slice_id = ""
    if ":" in role:
        slice_id = role.split(":", 1)[1].strip().split(" ", 1)[0]
    for key in (
        f"{type_name}:{slice_id}:r{turn}" if slice_id else "",
        f"{type_name}:{slice_id}" if slice_id else "",
        f"{type_name}:r{turn}",
        type_name,
    ):
        if key and key in mocks:
            val = mocks[key]
            if isinstance(val, list):
                return val[min(turn - 1, len(val) - 1)] if val else {}
            return val
    return None

class _AgentHandle:
    def __init__(self, aid, prompt, schema, role, type_name, workspace, model):
        self.aid, self.prompt, self.schema = aid, prompt or "", schema
        self.role, self.type_name = role or "", type_name or "self"
        self.workspace, self.model = workspace or "inherit", model or ""
        self.turns, self._oneshot = 0, None

    async def _run(self, turn_prompt, schema=None):
        self.turns += 1
        p = turn_prompt if turn_prompt is not None else self.prompt
        prompts.append(p)
        calls.append({
            "agent_id": self.aid,
            "turn": self.turns,
            "type_name": self.type_name,
            "role": self.role,
            "model": self.model,
            "workspace": self.workspace,
            "prompt": p,
            "standing_prompt": self.prompt,
        })
        slice_id = self.role.split(":", 1)[1].strip().split(" ", 1)[0] if ":" in self.role else "s1"
        res = {
            "stage": self.type_name,
            "lens": "code-docs",
            "agent": self.type_name,
            "role": self.type_name,
            "slice_id": slice_id,
            "status": "DONE" if self.type_name == "builder" else "COMPLETE",
            "verdict": "APPROVE",
            "blocking": False,
            "high_findings": 0,
            "branch": "",
            "commit": "abc1234",
            "files_changed": [],
            "verify_ok": True,
            "tool_calls": 12,
            "artifact_path": ".agents/scratch/agents/mock.md",
            "not_examined": "none",
            "summary": "ok",
        }
        ov = _lookup_mock(self.type_name, self.role, self.turns)
        if isinstance(ov, dict):
            for k, v in ov.items():
                if v is None:
                    res.pop(k, None)
                else:
                    res[k] = v
        return res

    def __call__(self, turn_prompt, schema=None):
        return self._run(turn_prompt, schema or self.schema)

    def __await__(self):
        if self._oneshot is None:
            self._oneshot = self._run(self.prompt, self.schema)
        return self._oneshot.__await__()

def agent(prompt=None, schema=None, role="", type_name="self", workspace="inherit", model=""):
    next_aid[0] += 1
    return _AgentHandle(next_aid[0], prompt, schema, role, type_name, workspace, model)

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
	rawMocks       map[string]any
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
	cmd := exec.Command("python3", "-B", "-c", pyWorkflowHarness, absScript, string(rawArgs))
	if opts.workDir != "" {
		cmd.Dir = opts.workDir
	}
	ov := map[string]string{
		"FYR_WORKFLOW_MOCKS":       "",
		"FYR_TEST_CONVERSATION_ID": opts.conversationID,
	}
	if opts.rawMocks != nil {
		rawMocks, err := json.Marshal(opts.rawMocks)
		if err != nil {
			t.Fatal(err)
		}
		ov["FYR_WORKFLOW_MOCKS"] = string(rawMocks)
	} else if opts.mocks != nil {
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

func runWorkflowScriptExpectError(t *testing.T, script string, args map[string]any) string {
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
	cmd := exec.Command("python3", "-B", "-c", pyWorkflowHarness, absScript, string(rawArgs))
	cmd.Env = overrideEnv(os.Environ(), map[string]string{"FYR_WORKFLOW_MOCKS": "", "FYR_TEST_CONVERSATION_ID": ""})
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("%s unexpectedly succeeded:\n%s", script, out)
	}
	return string(out)
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
	if out, err := exec.Command("python3", "-B", "-c", pySchemaCheck, "workflows/preflight_gate.py").CombinedOutput(); err != nil {
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
	if out, err := exec.Command("python3", "-B", "-c", pyASTCheck, "workflows/review_parallel.py").CombinedOutput(); err != nil {
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

func TestWorkflowScriptsPhaseLoop(t *testing.T) {
	disjointSlices := func() []map[string]any {
		return []map[string]any{
			{
				"id": "slice-a", "goal": "Add alpha helper",
				"files": []string{"internal/alpha/*.go"}, "verify_cmd": "go test -race ./internal/alpha/...",
				"judge_criterion": "alpha tests cover edge cases", "branch": "work/slice-a",
			},
			{
				"id": "slice-b", "goal": "Add beta helper",
				"files": []string{"internal/beta/**"}, "verify_cmd": "go test -race ./internal/beta/...",
				"judge_criterion": "beta tests cover edge cases", "branch": "work/slice-b",
			},
		}
	}

	t.Run("clean PASS across two disjoint slices lands sequentially then verifies main", func(t *testing.T) {
		binDir, workDir, homeDir := t.TempDir(), t.TempDir(), t.TempDir()
		logFile := filepath.Join(workDir, "fyr.log")
		fakeFyr := `echo "$*" >> ` + logFile + `
case "$1" in
  gate) printf '{"ok":true,"findings":[],"judgeVerdict":"PASS"}\n' ;;
  land) printf '{"ok":true,"branch":"%s","landCommit":"deadbeef"}\n' "$2" ;;
  verify) printf '{"ok":true}\n' ;;
  *) echo "unexpected cmd: $*" >&2; exit 2 ;;
esac`
		writeFakeBin(t, binDir, "fyr", fakeFyr)
		res := runWorkflowScriptWithOpts(t, "workflows/phase_loop.py", map[string]any{
			"plan_id": "PLAN-demo", "phase_name": "Wave 3", "date": "2026-11-22",
			"slices": disjointSlices(), "exec_cli": true, "cross_family": true, "judge_model": "claude-opus-5-5-high",
		}, workflowExecOpts{
			workDir: workDir, pathPrefix: binDir, homeDir: homeDir,
			rawMocks: map[string]any{
				"builder:slice-a": map[string]any{"status": "DONE", "branch": "work/slice-a", "commit": "111aaaa", "files_changed": []string{"internal/alpha/a.go"}, "verify_ok": true},
				"builder:slice-b": map[string]any{"status": "DONE", "branch": "work/slice-b", "commit": "222bbbb", "files_changed": []string{"internal/beta/sub/b.go"}, "verify_ok": true},
			},
		})
		if res.Output["status"] != "PASS" {
			t.Fatalf("status = %v, want PASS (%+v)", res.Output["status"], res.Output)
		}
		if len(res.Calls) != 2 || res.Calls[0].TypeName != "builder" || res.Calls[1].TypeName != "builder" ||
			res.Calls[0].Workspace != "share" || res.Calls[0].Model != "inherit" || res.Calls[0].AgentID == res.Calls[1].AgentID {
			t.Fatalf("unexpected builder calls: %+v", res.Calls)
		}
		landed, _ := res.Output["landed"].([]any)
		if len(landed) != 2 {
			t.Fatalf("landed len = %d, want 2 (%+v)", len(landed), res.Output)
		}
		rawLog, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatalf("read fyr log: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(string(rawLog)), "\n")
		var landSeq []string
		var sawVerify, sawJudge, sawJudgeModel bool
		for _, ln := range lines {
			if strings.HasPrefix(ln, "gate ") {
				if strings.Contains(ln, "--judge ") {
					sawJudge = true
				}
				if strings.Contains(ln, "--judge-model claude-opus-5-5-high") {
					sawJudgeModel = true
				}
			}
			if strings.HasPrefix(ln, "land ") {
				for _, forbidden := range []string{"--judge", "--judge-model", "--worker-model", "--worker-session"} {
					if strings.Contains(ln, forbidden) {
						t.Errorf("land CLI line unexpectedly contained %q (judge must run once in gate):\n%s", forbidden, ln)
					}
				}
				landSeq = append(landSeq, strings.Fields(ln)[1])
			}
			if strings.HasPrefix(ln, "verify --json") {
				sawVerify = true
			}
		}
		if strings.Join(landSeq, ",") != "work/slice-a,work/slice-b" || !sawVerify || !sawJudge || !sawJudgeModel {
			t.Fatalf("unexpected CLI sequence (land=%v verify=%v judge=%v judgeModel=%v):\n%s", landSeq, sawVerify, sawJudge, sawJudgeModel, rawLog)
		}
	})

	t.Run("round-1 FAIL feeds structured DefectPacket to SAME stateful builder then round-2 PASS", func(t *testing.T) {
		res := runWorkflowScriptWithOpts(t, "workflows/phase_loop.py", map[string]any{
			"phase_name": "Wave 3", "slices": disjointSlices()[:1], "run_reviewer": true,
		}, workflowExecOpts{
			rawMocks: map[string]any{
				"builder:slice-a": []any{
					map[string]any{"status": "DONE", "branch": "work/slice-a", "commit": "111aaaa", "files_changed": []string{"internal/alpha/a.go"}, "verify_ok": true},
					map[string]any{"status": "DONE", "branch": "work/slice-a", "commit": "111bbbb", "files_changed": []string{"internal/alpha/a.go"}, "verify_ok": true},
				},
				"gate:slice-a": []any{
					map[string]any{"ok": false, "findings": []any{map[string]any{"check": "judge", "detail": "missing nil guard in Parse"}}, "judgeVerdict": "FAIL"},
					map[string]any{"ok": true, "findings": []any{}, "judgeVerdict": "PASS"},
				},
				"reviewer": map[string]any{"role": "reviewer", "verdict": "APPROVE", "blocking": false, "summary": "clean"},
			},
		})
		if res.Output["status"] != "PASS" {
			t.Fatalf("status = %v, want PASS (%+v)", res.Output["status"], res.Output)
		}
		var bCalls []workflowAgentCall
		var rCalls []workflowAgentCall
		for _, c := range res.Calls {
			switch c.TypeName {
			case "builder":
				bCalls = append(bCalls, c)
			case "reviewer":
				rCalls = append(rCalls, c)
			}
		}
		if len(bCalls) != 2 || bCalls[0].AgentID != bCalls[1].AgentID || bCalls[0].Turn != 1 || bCalls[1].Turn != 2 {
			t.Fatalf("round-2 must reuse SAME stateful builder: %+v", bCalls)
		}
		if !strings.Contains(bCalls[1].Prompt, "Defect Packet") || !strings.Contains(bCalls[1].Prompt, "error_hash") || !strings.Contains(bCalls[1].Prompt, "missing nil guard in Parse") {
			t.Fatalf("round-2 prompt missing structured DefectPacket:\n%s", bCalls[1].Prompt)
		}
		if len(rCalls) != 1 || rCalls[0].Model != "pro" {
			t.Fatalf("expected 1 pro reviewer call after round-2 gate ok, got %+v", rCalls)
		}
	})

	t.Run("round-2 FAIL escalates with zero fyr land calls", func(t *testing.T) {
		res := runWorkflowScriptWithOpts(t, "workflows/phase_loop.py", map[string]any{
			"phase_name": "Wave 3", "slices": disjointSlices()[:1],
		}, workflowExecOpts{
			rawMocks: map[string]any{
				"builder:slice-a": map[string]any{"status": "DONE", "branch": "work/slice-a", "commit": "111aaaa", "files_changed": []string{"internal/alpha/a.go"}, "verify_ok": true},
				"gate:slice-a": []any{
					map[string]any{"ok": false, "findings": []any{map[string]any{"check": "numstat", "detail": "defect one"}}},
					map[string]any{"ok": false, "findings": []any{map[string]any{"check": "numstat", "detail": "defect two"}}},
				},
			},
		})
		if res.Output["status"] != "ESCALATE" {
			t.Fatalf("status = %v, want ESCALATE", res.Output["status"])
		}
		if landed, _ := res.Output["landed"].([]any); len(landed) != 0 {
			t.Fatalf("expected zero landed slices on ESCALATE, got %+v", landed)
		}
		escs, _ := res.Output["escalations"].([]any)
		if len(escs) != 1 || !strings.Contains(escs[0].(map[string]any)["reason"].(string), "failed after 2 round(s)") {
			t.Fatalf("unexpected escalations: %+v", escs)
		}
	})

	t.Run("repeating error_hash triggers immediate anti-stuck ESCALATE", func(t *testing.T) {
		res := runWorkflowScriptWithOpts(t, "workflows/phase_loop.py", map[string]any{
			"phase_name": "Wave 3", "slices": disjointSlices()[:1],
		}, workflowExecOpts{
			rawMocks: map[string]any{
				"builder:slice-a": map[string]any{"status": "DONE", "branch": "work/slice-a", "commit": "111aaaa", "files_changed": []string{"internal/alpha/a.go"}, "verify_ok": true},
				"gate:slice-a": []any{
					map[string]any{"ok": false, "findings": []any{map[string]any{"check": "judge", "detail": "Same Failure Text"}}},
					map[string]any{"ok": false, "findings": []any{map[string]any{"check": "judge", "detail": "  same   failure text  "}}},
				},
			},
		})
		if res.Output["status"] != "ESCALATE" {
			t.Fatalf("status = %v, want ESCALATE", res.Output["status"])
		}
		escs, _ := res.Output["escalations"].([]any)
		if len(escs) != 1 || !strings.Contains(escs[0].(map[string]any)["reason"].(string), "repeating error_hash") {
			t.Fatalf("expected repeating error_hash escalation, got %+v", escs)
		}
		if landed, _ := res.Output["landed"].([]any); len(landed) != 0 {
			t.Fatalf("expected zero landed slices on stuck ESCALATE, got %+v", landed)
		}
	})

	t.Run("out-of-lease edit halts immediately without round-2 loop-back", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			rawMocks map[string]any
		}{
			{
				"builder reports nested file outside single-star glob",
				map[string]any{
					"builder:slice-a": map[string]any{
						"status": "DONE", "branch": "work/slice-a", "commit": "111aaaa",
						"files_changed": []string{"internal/alpha/nested/escape.go"}, "verify_ok": true,
					},
				},
			},
			{
				"fyr gate reports lease violation",
				map[string]any{
					"builder:slice-a": map[string]any{
						"status": "DONE", "branch": "work/slice-a", "commit": "111aaaa",
						"files_changed": []string{"internal/alpha/a.go"}, "verify_ok": true,
					},
					"gate:slice-a": map[string]any{
						"ok":       false,
						"findings": []any{map[string]any{"check": "lease", "path": "PRD.md", "detail": "file outside lease"}},
					},
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				res := runWorkflowScriptWithOpts(t, "workflows/phase_loop.py", map[string]any{
					"phase_name": "Wave 3", "slices": disjointSlices()[:1],
				}, workflowExecOpts{rawMocks: tc.rawMocks})
				if res.Output["status"] != "ESCALATE" || len(res.Calls) != 1 {
					t.Fatalf("expected immediate 1-turn ESCALATE, got status=%v calls=%d (%+v)", res.Output["status"], len(res.Calls), res.Output)
				}
				escs, _ := res.Output["escalations"].([]any)
				if len(escs) != 1 || !strings.Contains(escs[0].(map[string]any)["reason"].(string), "out-of-lease edit") {
					t.Fatalf("expected out-of-lease escalation, got %+v", escs)
				}
			})
		}
	})

	t.Run("overlapping files between slices rejected up front", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			filesA []string
			filesB []string
		}{
			{"recursive dir overlaps file", []string{"internal/a/**"}, []string{"internal/a/sub/x.go"}},
			{"overlapping sibling globs", []string{"internal/a/*.go"}, []string{"internal/a/*_test.go"}},
			{"cross-dir wildcard globs overlap", []string{"internal/*/x.go"}, []string{"internal/a/*.go"}},
			{"middle globstar overlaps concrete path", []string{"a/**/b"}, []string{"a/x/y/b"}},
			{"overlapping character classes", []string{"internal/a/[ab].go"}, []string{"internal/a/[bc].go"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				errOut := runWorkflowScriptExpectError(t, "workflows/phase_loop.py", map[string]any{
					"slices": []map[string]any{
						{"id": "s1", "goal": "g1", "files": tc.filesA},
						{"id": "s2", "goal": "g2", "files": tc.filesB},
					},
				})
				if !strings.Contains(errOut, "overlapping files lease") {
					t.Fatalf("expected overlapping files lease error, got:\n%s", errOut)
				}
			})
		}
		for _, tc := range []struct {
			name   string
			filesA []string
			filesB []string
		}{
			{"disjoint cross-dir suffix", []string{"internal/*/x.go"}, []string{"internal/a/*.md"}},
			{"disjoint middle globstar prefix", []string{"a/**/b"}, []string{"a-x/b"}},
			{"disjoint character classes", []string{"internal/a/[ab].go"}, []string{"internal/a/[cd].go"}},
		} {
			t.Run("disjoint "+tc.name, func(t *testing.T) {
				dry := runWorkflowScript(t, "workflows/phase_loop.py", map[string]any{
					"dry_run": true,
					"slices": []map[string]any{
						{"id": "s1", "goal": "g1", "files": tc.filesA},
						{"id": "s2", "goal": "g2", "files": tc.filesB},
					},
				})
				if dry.Output["status"] != "DRY_RUN" {
					t.Fatalf("disjoint %s failed dry_run: %+v", tc.name, dry.Output)
				}
			})
		}
	})

	t.Run("middle globstar lease matches a/b and a/x/b but rejects ab and a-x/b", func(t *testing.T) {
		slice := []map[string]any{{
			"id": "glob-s", "goal": "Edit b under a", "files": []string{"a/**/b"}, "branch": "work/glob-s",
		}}
		okRes := runWorkflowScriptWithOpts(t, "workflows/phase_loop.py", map[string]any{
			"phase_name": "Wave 3", "slices": slice,
		}, workflowExecOpts{
			rawMocks: map[string]any{
				"builder:glob-s": map[string]any{
					"status": "DONE", "branch": "work/glob-s", "commit": "1234567",
					"files_changed": []string{"a/b", "a/x/b", "a/x/y/b"}, "verify_ok": true,
				},
			},
		})
		if okRes.Output["status"] != "PASS" {
			t.Fatalf("a/b and a/x/b should match a/**/b: %+v", okRes.Output)
		}
		for _, badPath := range []string{"ab", "a-x/b", "a/xb"} {
			t.Run("rejects "+badPath, func(t *testing.T) {
				badRes := runWorkflowScriptWithOpts(t, "workflows/phase_loop.py", map[string]any{
					"phase_name": "Wave 3", "slices": slice,
				}, workflowExecOpts{
					rawMocks: map[string]any{
						"builder:glob-s": map[string]any{
							"status": "DONE", "branch": "work/glob-s", "commit": "1234567",
							"files_changed": []string{badPath}, "verify_ok": true,
						},
					},
				})
				if badRes.Output["status"] != "ESCALATE" || len(badRes.Calls) != 1 {
					t.Fatalf("%q should be out-of-lease ESCALATE in 1 turn, got status=%v calls=%d (%+v)", badPath, badRes.Output["status"], len(badRes.Calls), badRes.Output)
				}
			})
		}
	})

	t.Run("untrusted builder branch ref fails closed with zero gate or land calls", func(t *testing.T) {
		for _, badBranch := range []string{"--judge=evil", "../bad", "main", "work/slice-b", "work/foo..bar", "work/bad.lock", "work/a//b"} {
			t.Run(badBranch, func(t *testing.T) {
				binDir, workDir, homeDir := t.TempDir(), t.TempDir(), t.TempDir()
				logFile := filepath.Join(workDir, "fyr.log")
				writeFakeBin(t, binDir, "fyr", `echo "$*" >> `+logFile+`; echo "must not run" >&2; exit 99`)
				res := runWorkflowScriptWithOpts(t, "workflows/phase_loop.py", map[string]any{
					"phase_name": "Wave 3", "slices": disjointSlices()[:1], "exec_cli": true,
				}, workflowExecOpts{
					workDir: workDir, pathPrefix: binDir, homeDir: homeDir,
					rawMocks: map[string]any{
						"builder:slice-a": map[string]any{
							"status": "DONE", "branch": badBranch, "commit": "111aaaa",
							"files_changed": []string{"internal/alpha/a.go"}, "verify_ok": true,
						},
					},
				})
				if res.Output["status"] != "ESCALATE" || len(res.Calls) != 1 {
					t.Fatalf("branch %q: expected 1-turn ESCALATE, got status=%v calls=%d (%+v)", badBranch, res.Output["status"], len(res.Calls), res.Output)
				}
				escs, _ := res.Output["escalations"].([]any)
				if len(escs) != 1 || !strings.Contains(escs[0].(map[string]any)["reason"].(string), "invalid branch") {
					t.Fatalf("branch %q: expected invalid branch escalation, got %+v", badBranch, escs)
				}
				if _, err := os.Stat(logFile); !os.IsNotExist(err) {
					raw, _ := os.ReadFile(logFile)
					t.Fatalf("branch %q invoked fyr CLI despite invalid ref:\n%s", badBranch, raw)
				}
			})
		}
	})

	t.Run("missing CONVERSATION_ID without FYR_WORKFLOW_MOCKS fails closed as ESCALATE", func(t *testing.T) {
		res := runWorkflowScript(t, "workflows/phase_loop.py", map[string]any{
			"plan_id": "PLAN-failclosed", "phase_name": "Wave 4", "slices": disjointSlices()[:1],
		})
		if res.Output["status"] != "ESCALATE" {
			t.Fatalf("status = %v, want ESCALATE (%+v)", res.Output["status"], res.Output)
		}
		if res.Output["exec_cli"] != false || res.Output["mocked"] != true {
			t.Fatalf("expected exec_cli=false mocked=true, got %+v", res.Output)
		}
		if reason, _ := res.Output["escalation_reason"].(string); !strings.Contains(reason, "exec_cli disabled without mocks") {
			t.Fatalf("unexpected escalation_reason = %q (%+v)", reason, res.Output)
		}
		if landed, _ := res.Output["landed"].([]any); len(landed) != 0 || len(res.Calls) != 0 {
			t.Fatalf("expected zero landed slices and zero agent calls, got landed=%v calls=%d", landed, len(res.Calls))
		}
	})

	t.Run("omitted builder verify_ok defaults to false and fails closed", func(t *testing.T) {
		res := runWorkflowScriptWithOpts(t, "workflows/phase_loop.py", map[string]any{
			"phase_name": "Wave 4", "slices": disjointSlices()[:1],
		}, workflowExecOpts{
			rawMocks: map[string]any{
				"builder:slice-a": map[string]any{
					"status": "DONE", "branch": "work/slice-a", "commit": "111aaaa",
					"files_changed": []string{"internal/alpha/a.go"}, "verify_ok": nil,
				},
			},
		})
		if res.Output["status"] != "ESCALATE" {
			t.Fatalf("omitted verify_ok must ESCALATE, got %v (%+v)", res.Output["status"], res.Output)
		}
		if res.Output["exec_cli"] != false || res.Output["mocked"] != true {
			t.Fatalf("expected exec_cli=false mocked=true in final JSON, got %+v", res.Output)
		}
		if landed, _ := res.Output["landed"].([]any); len(landed) != 0 {
			t.Fatalf("omitted verify_ok must not land: %+v", landed)
		}
	})

	t.Run("dry_run validates slices with zero agent calls and AST pins literal models", func(t *testing.T) {
		dry := runWorkflowScript(t, "workflows/phase_loop.py", map[string]any{
			"phase_name": "Wave 3", "slices": disjointSlices(), "dry_run": true,
		})
		if dry.Output["status"] != "DRY_RUN" || len(dry.Calls) != 0 {
			t.Fatalf("dry_run status=%v calls=%d, want DRY_RUN and 0", dry.Output["status"], len(dry.Calls))
		}
		if _, hasExec := dry.Output["exec_cli"]; !hasExec {
			t.Fatalf("dry_run output missing exec_cli: %+v", dry.Output)
		}
		if _, hasMock := dry.Output["mocked"]; !hasMock {
			t.Fatalf("dry_run output missing mocked: %+v", dry.Output)
		}
		pyASTCheck := `
import ast, sys
src = open(sys.argv[1], encoding="utf-8").read()
tree = ast.parse(src, sys.argv[1], type_comments=False)
calls = [n for n in ast.walk(tree) if isinstance(n, ast.Call) and isinstance(n.func, ast.Name) and n.func.id == "agent"]
assert len(calls) == 4, f"expected 4 literal agent() calls, got {len(calls)}"
for c in calls:
    kw = {k.arg: k.value for k in c.keywords}
    tn, m = kw.get("type_name"), kw.get("model")
    assert isinstance(tn, ast.Constant) and isinstance(m, ast.Constant), f"non-literal type_name/model: {ast.dump(c)}"
    if tn.value == "builder":
        assert m.value == "inherit", f"builder model={m.value!r}"
    else:
        assert tn.value in ("reviewer", "security", "verifier") and m.value == "pro", f"guarded call {tn.value!r}/{m.value!r}"
`
		if out, err := exec.Command("python3", "-B", "-c", pyASTCheck, "workflows/phase_loop.py").CombinedOutput(); err != nil {
			t.Fatalf("phase_loop.py AST check failed: %v\n%s", err, out)
		}
	})
}

func TestV910WorkflowsPhaseLoopLandJudgeTrailLeaseAndDynamicFamily(t *testing.T) {
	t.Run("phase_loop refuses .agents/trail.jsonl lease patterns and allows safe globs", func(t *testing.T) {
		for _, bad := range []string{".agents/trail.jsonl", "./.agents/trail.jsonl", ".agents/trail.*", ".agents/*", ".agents/**", ".agents", ".agents/", "**", "**/*.jsonl"} {
			errOut := runWorkflowScriptExpectError(t, "workflows/phase_loop.py", map[string]any{
				"dry_run": true,
				"slices":  []map[string]any{{"id": "s1", "goal": "g1", "files": []string{bad}}},
			})
			if !strings.Contains(errOut, "trail.jsonl") {
				t.Fatalf("lease %q: expected trail.jsonl refusal, got:\n%s", bad, errOut)
			}
		}
		okDry := runWorkflowScript(t, "workflows/phase_loop.py", map[string]any{
			"dry_run": true,
			"slices": []map[string]any{
				{"id": "s1", "goal": "g1", "files": []string{"docs/**", ".agents/rules/**", "*.go"}},
			},
		})
		if okDry.Output["status"] != "DRY_RUN" {
			t.Fatalf("safe leases rejected: %+v", okDry.Output)
		}
	})

	t.Run("phase_loop passes judge and worker flags to gate and extracts structured land failure detail", func(t *testing.T) {
		binDir, workDir, homeDir := t.TempDir(), t.TempDir(), t.TempDir()
		logFile := filepath.Join(workDir, "fyr.log")
		recFile := filepath.Join(workDir, "record_run.json")
		fakeFyr := `echo "$*" >> ` + logFile + `
case "$1" in
  gate) printf '{"ok":true,"findings":[],"judgeVerdict":"PASS"}\n' ;;
  land) printf '{"ok":false,"branch":"%s","verify":{"ok":false,"steps":[{"name":"go-test","ok":false,"detail":"TestFoo failed"}]}}\n' "$2"; exit 1 ;;
  metrics) cat > ` + recFile + `; printf '{"id":"ok"}\n' ;;
  *) echo "unexpected cmd: $*" >&2; exit 2 ;;
esac`
		writeFakeBin(t, binDir, "fyr", fakeFyr)
		res := runWorkflowScriptWithOpts(t, "workflows/phase_loop.py", map[string]any{
			"plan_id": "PLAN-v910", "phase_name": "Wave 4", "date": "2026-11-23",
			"exec_cli": true, "cross_family": true, "judge_model": "claude-opus-5-5-high",
			"worker_model": "gemini-4-carbon-high", "worker_session": "conv-w1",
			"slices": []map[string]any{{
				"id": "s1", "goal": "g1", "files": []string{"internal/a/**"},
				"judge_criterion": "check s1", "branch": "work/s1",
			}},
		}, workflowExecOpts{
			workDir: workDir, pathPrefix: binDir, homeDir: homeDir,
			rawMocks: map[string]any{
				"builder:s1": map[string]any{"status": "DONE", "branch": "work/s1", "commit": "111aaaa", "files_changed": []string{"internal/a/a.go"}, "verify_ok": true},
			},
		})
		if res.Output["status"] != "ESCALATE" {
			t.Fatalf("expected ESCALATE on land fail, got %+v", res.Output)
		}
		escs, _ := res.Output["escalations"].([]any)
		if len(escs) != 1 {
			t.Fatalf("expected 1 escalation, got %+v", escs)
		}
		reason, _ := escs[0].(map[string]any)["reason"].(string)
		if strings.Contains(reason, "unknown") || !strings.Contains(reason, "go-test") || !strings.Contains(reason, "TestFoo failed") {
			t.Fatalf("expected structured verify step failure detail, got %q", reason)
		}
		rawLog, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatalf("read fyr.log: %v", err)
		}
		logText := string(rawLog)
		var gateLine, landLine string
		for _, ln := range strings.Split(strings.TrimSpace(logText), "\n") {
			if strings.HasPrefix(ln, "gate ") {
				gateLine = ln
			}
			if strings.HasPrefix(ln, "land ") {
				landLine = ln
			}
		}
		for _, want := range []string{"--judge check s1", "--judge-model claude-opus-5-5-high", "--worker-model gemini-4-carbon-high", "--worker-session conv-w1"} {
			if !strings.Contains(gateLine, want) {
				t.Errorf("gate CLI line missing %q:\n%s", want, gateLine)
			}
		}
		for _, forbidden := range []string{"--judge", "--judge-model", "--worker-model", "--worker-session"} {
			if strings.Contains(landLine, forbidden) {
				t.Errorf("land CLI line unexpectedly contains %q (judge must run once in gate):\n%s", forbidden, landLine)
			}
		}
		rawRec, err := os.ReadFile(recFile)
		if err != nil {
			t.Fatalf("expected phase_loop to emit metrics record-run: %v\nlog:\n%s", err, logText)
		}
		var row map[string]any
		if err := json.Unmarshal(rawRec, &row); err != nil || row["kind"] != "phase_loop" || row["status"] != "ESCALATE" {
			t.Fatalf("unexpected record-run payload: err=%v row=%+v raw=%s", err, row, rawRec)
		}
	})

	t.Run("phase_loop land=false with judge_criterion still runs fyr gate --judge without calling land", func(t *testing.T) {
		binDir, workDir, homeDir := t.TempDir(), t.TempDir(), t.TempDir()
		logFile := filepath.Join(workDir, "fyr.log")
		fakeFyr := `echo "$*" >> ` + logFile + `
case "$1" in
  gate) printf '{"ok":true,"findings":[],"judgeVerdict":"PASS"}\n' ;;
  metrics) printf '{"id":"ok"}\n' ;;
  *) echo "unexpected cmd: $*" >&2; exit 2 ;;
esac`
		writeFakeBin(t, binDir, "fyr", fakeFyr)
		res := runWorkflowScriptWithOpts(t, "workflows/phase_loop.py", map[string]any{
			"plan_id": "PLAN-noland", "phase_name": "Wave 5", "date": "2026-11-23",
			"exec_cli": true, "cross_family": true, "land": false,
			"judge_model": "claude-opus-5-5-high", "worker_model": "gemini-4-carbon-high", "worker_session": "conv-nl1",
			"slices": []map[string]any{{
				"id": "s1", "goal": "g1", "files": []string{"internal/a/**"},
				"judge_criterion": "check no-land judge", "branch": "work/s1",
			}},
		}, workflowExecOpts{
			workDir: workDir, pathPrefix: binDir, homeDir: homeDir,
			rawMocks: map[string]any{
				"builder:s1": map[string]any{"status": "DONE", "branch": "work/s1", "commit": "111aaaa", "files_changed": []string{"internal/a/a.go"}, "verify_ok": true},
			},
		})
		if res.Output["status"] != "PASS" {
			t.Fatalf("land=false status = %v, want PASS (%+v)", res.Output["status"], res.Output)
		}
		if landed, _ := res.Output["landed"].([]any); len(landed) != 0 {
			t.Fatalf("land=false unexpectedly landed slices: %+v", landed)
		}
		rawLog, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatalf("read fyr.log: %v", err)
		}
		var gateLine string
		for _, ln := range strings.Split(strings.TrimSpace(string(rawLog)), "\n") {
			if strings.HasPrefix(ln, "land ") {
				t.Errorf("land=false unexpectedly invoked fyr land:\n%s", ln)
			}
			if strings.HasPrefix(ln, "gate ") {
				gateLine = ln
			}
		}
		for _, want := range []string{"--judge check no-land judge", "--judge-model claude-opus-5-5-high", "--worker-model gemini-4-carbon-high", "--worker-session conv-nl1"} {
			if !strings.Contains(gateLine, want) {
				t.Errorf("land=false gate CLI line missing %q:\n%s", want, gateLine)
			}
		}
	})

	t.Run("review_parallel and preflight_gate dynamic caller_family anthropic picks gemini and records run", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			script string
			args   map[string]any
			agyOut string
			kind   string
		}{
			{
				name:   "review_parallel anthropic caller picks gemini",
				script: "workflows/review_parallel.py",
				args: map[string]any{
					"scope_text": "git diff HEAD", "lenses": []string{"code"},
					"cross_family": true, "caller_model": "claude-opus-5-5-high", "exec_cli": true,
				},
				agyOut: `{"conversation_id":"r1","status":"SUCCESS","denied_actions":[],"structured_output":{"lens":"code","agent":"reviewer","verdict":"APPROVE","artifact_path":".agents/scratch/agents/reviewer-today-worktree-code.md","high_findings":0,"summary":"gemini review ok"}}`,
				kind:   "review_parallel",
			},
			{
				name:   "preflight_gate anthropic caller picks gemini",
				script: "workflows/preflight_gate.py",
				args: map[string]any{
					"base_ref": "v9.9.0", "run_reviewer": true, "run_security": false, "run_verifier": false,
					"cross_family": true, "caller_family": "anthropic", "exec_cli": true,
				},
				agyOut: `{"conversation_id":"p1","status":"SUCCESS","denied_actions":[],"structured_output":{"stage":"reviewer","agent":"reviewer","status":"COMPLETE","verdict":"APPROVE","blocking":false,"artifact_path":".agents/scratch/agents/reviewer-today-preflight-candidate.md","not_examined":"none","summary":"gemini preflight ok"}}`,
				kind:   "preflight_gate",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				binDir, workDir, homeDir := t.TempDir(), t.TempDir(), t.TempDir()
				recFile := filepath.Join(workDir, "rec.json")
				fyrBody := `if [ "$1" = "models" ]; then
  case " $* " in *" --caller-family anthropic "*) ;; *) echo "missing --caller-family anthropic: $*" >&2; exit 2 ;; esac
  printf '{"model":"gemini-4-carbon-high","reason":"cross-family gemini"}\n'
  exit 0
fi
if [ "$1" = "metrics" ] && [ "$2" = "record-run" ]; then
  cat > ` + recFile + `
  printf '{"id":"ok"}\n'
  exit 0
fi
exit 2`
				agyBody := `case " $* " in *" --model gemini-4-carbon-high "*) ;; *) echo "wrong model: $*" >&2; exit 3 ;; esac
printf '%s\n' '` + tc.agyOut + `'`
				writeFakeBin(t, binDir, "fyr", fyrBody)
				writeFakeBin(t, binDir, "agy", agyBody)
				res := runWorkflowScriptWithOpts(t, tc.script, tc.args, workflowExecOpts{
					workDir: workDir, pathPrefix: binDir, homeDir: homeDir,
				})
				routing, _ := res.Output["model_routing"].(map[string]any)
				if routing["enabled"] != true || routing["model"] != "gemini-4-carbon-high" {
					t.Fatalf("expected gemini cross-family for anthropic caller, got routing=%+v calls=%+v", routing, res.Calls)
				}
				if len(res.Calls) != 0 {
					t.Fatalf("expected 0 fallback native calls, got %+v", res.Calls)
				}
				rawRec, err := os.ReadFile(recFile)
				if err != nil {
					t.Fatalf("expected %s to emit metrics record-run: %v", tc.script, err)
				}
				var row map[string]any
				if err := json.Unmarshal(rawRec, &row); err != nil || row["kind"] != tc.kind {
					t.Fatalf("unexpected record-run row: err=%v row=%+v", err, row)
				}
			})
		}

		// Must-fail side: anthropic caller receiving same-family claude pick skips agy and falls back.
		binDir, workDir, homeDir := t.TempDir(), t.TempDir(), t.TempDir()
		writeFakeBin(t, binDir, "fyr", `if [ "$1" = "models" ]; then printf '{"model":"claude-opus-5-5-high","fallback_reason":"gemini pool low"}\n'; exit 0; fi; exit 1`)
		writeFakeBin(t, binDir, "agy", `echo "must not spawn same-family agy" >&2; exit 99`)
		fb := runWorkflowScriptWithOpts(t, "workflows/review_parallel.py", map[string]any{
			"scope_text": "git diff HEAD", "lenses": []string{"code"},
			"cross_family": true, "caller_family": "anthropic",
		}, workflowExecOpts{workDir: workDir, pathPrefix: binDir, homeDir: homeDir})
		if routing, _ := fb.Output["model_routing"].(map[string]any); routing["enabled"] != false || routing["source"] != "fallback-anthropic" || len(fb.Calls) != 1 {
			t.Fatalf("same-family anthropic pick must fall back to native pro: routing=%+v calls=%+v", routing, fb.Calls)
		}
	})
}

func TestWorkflowPhaseLoopPreservesTrailingSlashDirectoryLease(t *testing.T) {
	binDir, workDir, homeDir := t.TempDir(), t.TempDir(), t.TempDir()
	logFile := filepath.Join(workDir, "fyr.log")
	fakeFyr := `echo "$*" >> ` + logFile + `
case "$1" in
  gate) printf '{"ok":true,"findings":[]}\n' ;;
  land) printf '{"ok":true,"branch":"%s","landCommit":"deadbeef"}\n' "$2" ;;
  verify) printf '{"ok":true}\n' ;;
  metrics) printf '{"id":"ok"}\n' ;;
  *) echo "unexpected cmd: $*" >&2; exit 2 ;;
esac`
	writeFakeBin(t, binDir, "fyr", fakeFyr)

	res := runWorkflowScriptWithOpts(t, "workflows/phase_loop.py", map[string]any{
		"phase_name": "Trailing Slash Lease",
		"exec_cli":   true,
		"slices": []map[string]any{
			{"id": "s1", "goal": "g1", "files": []string{"internal/alpha/", "internal/beta//"}, "branch": "work/s1"},
		},
	}, workflowExecOpts{
		workDir: workDir, pathPrefix: binDir, homeDir: homeDir,
		rawMocks: map[string]any{
			"builder:s1": map[string]any{
				"status": "DONE", "branch": "work/s1", "commit": "111aaaa",
				"files_changed": []string{"internal/alpha/a.go", "internal/beta/sub/b.go"}, "verify_ok": true,
			},
		},
	})
	if res.Output["status"] != "PASS" {
		t.Fatalf("status = %v, want PASS (%+v)", res.Output["status"], res.Output)
	}
	rawLog, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read fyr log: %v", err)
	}
	logText := string(rawLog)
	for _, want := range []string{"gate work/s1", "land work/s1"} {
		found := false
		for _, ln := range strings.Split(strings.TrimSpace(logText), "\n") {
			if strings.HasPrefix(ln, want) {
				found = true
				if !strings.Contains(ln, "--lease internal/alpha/") || !strings.Contains(ln, "--lease internal/beta/") {
					t.Errorf("%s dropped trailing slash on directory lease:\n%s", want, ln)
				}
			}
		}
		if !found {
			t.Errorf("missing %q line in log:\n%s", want, logText)
		}
	}

	// Must-fail: sibling prefix outside trailing-slash dir escalates immediately.
	badRes := runWorkflowScriptWithOpts(t, "workflows/phase_loop.py", map[string]any{
		"phase_name": "Trailing Slash Escape",
		"slices": []map[string]any{
			{"id": "s1", "goal": "g1", "files": []string{"internal/alpha/"}, "branch": "work/s1"},
		},
	}, workflowExecOpts{
		rawMocks: map[string]any{
			"builder:s1": map[string]any{
				"status": "DONE", "branch": "work/s1", "commit": "111aaaa",
				"files_changed": []string{"internal/alpha2/a.go"}, "verify_ok": true,
			},
		},
	})
	if badRes.Output["status"] != "ESCALATE" || len(badRes.Calls) != 1 {
		t.Fatalf("sibling prefix must ESCALATE in 1 turn, got status=%v calls=%d", badRes.Output["status"], len(badRes.Calls))
	}

	// Overlap detection for trailing-slash dirs vs nested files and disjoint siblings.
	errOut := runWorkflowScriptExpectError(t, "workflows/phase_loop.py", map[string]any{
		"dry_run": true,
		"slices": []map[string]any{
			{"id": "s1", "goal": "g1", "files": []string{"internal/alpha/"}},
			{"id": "s2", "goal": "g2", "files": []string{"internal/alpha/sub.go"}},
		},
	})
	if !strings.Contains(errOut, "overlapping files lease") {
		t.Fatalf("expected overlapping files lease for trailing-slash dir vs nested file, got:\n%s", errOut)
	}
	disjoint := runWorkflowScript(t, "workflows/phase_loop.py", map[string]any{
		"dry_run": true,
		"slices": []map[string]any{
			{"id": "s1", "goal": "g1", "files": []string{"internal/alpha/"}},
			{"id": "s2", "goal": "g2", "files": []string{"internal/alpha2/"}},
		},
	})
	if disjoint.Output["status"] != "DRY_RUN" {
		t.Fatalf("disjoint trailing-slash dirs failed dry_run: %+v", disjoint.Output)
	}
}

func TestWorkflowScriptsSharedLibAndSyntax(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}

	// Verify ast.parse(..., mode='exec') succeeds on every workflows/*.py and fails on malformed syntax.
	scripts, err := filepath.Glob("workflows/*.py")
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) < 4 {
		t.Fatalf("expected >= 4 workflows/*.py including fyr_workflow_lib.py, got %v", scripts)
	}
	const pySyntaxCheck = `
import ast, sys
for p in sys.argv[1:]:
    with open(p, "r", encoding="utf-8") as fh:
        ast.parse(fh.read(), p, mode="exec")
try:
    ast.parse("def broken(:\n", "<bad>", mode="exec")
except SyntaxError:
    pass
else:
    raise AssertionError("ast.parse failed to reject malformed syntax")
`
	if out, err := exec.Command("python3", append([]string{"-B", "-c", pySyntaxCheck}, scripts...)...).CombinedOutput(); err != nil {
		t.Fatalf("ast.parse(mode='exec') check failed: %v\n%s", err, out)
	}

	// Verify preflight_gate.py and review_parallel.py delegate shared helpers to fyr_workflow_lib.
	for _, script := range []string{"workflows/preflight_gate.py", "workflows/review_parallel.py"} {
		raw, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		if !strings.Contains(src, "fyr_workflow_lib") {
			t.Errorf("%s does not import shared module fyr_workflow_lib", script)
		}
		for _, dupDef := range []string{
			"def _resolve_cross_family_route(",
			"def _unwrap_agy_json(",
			"async def _record_orchestration_run(",
			"async def _run_headless_cross_family(",
		} {
			if strings.Contains(src, dupDef) {
				t.Errorf("%s still defines duplicated helper %q instead of using fyr_workflow_lib", script, dupDef)
			}
		}
	}

	// Verify installed ~/.gemini/config/workflows/ layout works via ScriptPath and inline execution
	// and never loads an untrusted cwd/workflows/fyr_workflow_lib.py or cwd/fyr_workflow_lib.py.
	homeDir := t.TempDir()
	instDir := filepath.Join(homeDir, ".gemini", "config", "workflows")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"fyr_workflow_lib.py", "preflight_gate.py", "review_parallel.py"} {
		data, err := os.ReadFile(filepath.Join("workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(instDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	poisonWork := t.TempDir()
	if err := os.MkdirAll(filepath.Join(poisonWork, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	poison := []byte("raise RuntimeError('cwd hijack loaded')\n")
	for _, p := range []string{
		filepath.Join(poisonWork, "fyr_workflow_lib.py"),
		filepath.Join(poisonWork, "workflows", "fyr_workflow_lib.py"),
	} {
		if err := os.WriteFile(p, poison, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("installed ScriptPath loads sibling lib and ignores cwd poison", func(t *testing.T) {
		res := runWorkflowScriptWithOpts(t, filepath.Join(instDir, "preflight_gate.py"), map[string]any{
			"base_ref": "v0.1.0", "run_reviewer": true, "run_security": false, "run_verifier": false,
		}, workflowExecOpts{workDir: poisonWork, homeDir: homeDir})
		if res.Output["gate_verdict"] != "SHIP" {
			t.Fatalf("installed preflight_gate gate_verdict = %v, want SHIP", res.Output["gate_verdict"])
		}
		rev := runWorkflowScriptWithOpts(t, filepath.Join(instDir, "review_parallel.py"), map[string]any{
			"scope_text": "git diff HEAD", "lenses": []string{"code"},
		}, workflowExecOpts{workDir: poisonWork, homeDir: homeDir})
		if len(rev.Calls) != 1 || rev.Calls[0].TypeName != "reviewer" {
			t.Fatalf("installed review_parallel calls = %+v, want 1 reviewer call", rev.Calls)
		}
	})

	t.Run("inline exec resolves installed lib from HOME and ignores cwd poison", func(t *testing.T) {
		const inlineRunner = `
import ast, asyncio, contextlib, io, json, os, sys
sys.dont_write_bytecode = True
src = open(sys.argv[1], "r", encoding="utf-8").read()
async def _agent(*_a, **_kw):
    return {"stage": "reviewer", "lens": "code", "agent": "reviewer", "status": "COMPLETE", "verdict": "APPROVE", "blocking": False, "high_findings": 0, "artifact_path": ".agents/scratch/agents/m.md", "not_examined": "none", "summary": "ok"}
async def _parallel(items, fn):
    return [await fn(x) for x in items]
ns = {"__name__": "__main__", "args": json.loads(sys.argv[2]), "phase": lambda _n: None, "log": lambda _m: None, "agent": _agent, "parallel": _parallel}
code = compile(src, "<inline>", "exec", flags=ast.PyCF_ALLOW_TOP_LEVEL_AWAIT)
buf = io.StringIO()
with contextlib.redirect_stdout(buf):
    ret = eval(code, ns)
    if asyncio.iscoroutine(ret):
        asyncio.run(ret)
print(buf.getvalue())
`
		for _, tc := range []struct {
			script, argsJSON, wantKey string
		}{
			{filepath.Join(instDir, "preflight_gate.py"), `{"base_ref":"v0.1.0","run_reviewer":true,"run_security":false}`, "gate_verdict"},
			{filepath.Join(instDir, "review_parallel.py"), `{"scope_text":"git diff HEAD","lenses":["code"]}`, "results"},
		} {
			cmd := exec.Command("python3", "-B", "-c", inlineRunner, tc.script, tc.argsJSON)
			cmd.Dir = poisonWork
			cmd.Env = overrideEnv(os.Environ(), map[string]string{"HOME": homeDir, "FYR_WORKFLOW_MOCKS": ""})
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("inline exec %s failed: %v\n%s", tc.script, err, out)
			}
			var parsed map[string]any
			if err := json.Unmarshal(out, &parsed); err != nil || parsed[tc.wantKey] == nil {
				t.Fatalf("inline exec %s bad output: err=%v out=%s", tc.script, err, out)
			}
		}

		// Symlinked installed lib must be refused in inline mode instead of following link or loading cwd poison.
		symHome := t.TempDir()
		symInst := filepath.Join(symHome, ".gemini", "config", "workflows")
		if err := os.MkdirAll(symInst, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(instDir, "fyr_workflow_lib.py"), filepath.Join(symInst, "fyr_workflow_lib.py")); err != nil {
			t.Skip("symlinks unavailable")
		}
		cmd := exec.Command("python3", "-B", "-c", inlineRunner, filepath.Join(instDir, "preflight_gate.py"), `{"base_ref":"v0.1.0","run_reviewer":true,"run_security":false}`)
		cmd.Dir = poisonWork
		cmd.Env = overrideEnv(os.Environ(), map[string]string{"HOME": symHome, "FYR_WORKFLOW_MOCKS": ""})
		out, err := cmd.CombinedOutput()
		if err == nil || strings.Contains(string(out), "cwd hijack loaded") {
			t.Fatalf("symlinked installed lib should fail closed without loading cwd poison: err=%v out=%s", err, out)
		}
	})

	if _, err := os.Stat(filepath.Join("workflows", "__pycache__")); err == nil {
		t.Fatal("workflows/__pycache__ created during tests; breaks go:embed all:workflows parity")
	}
}

func TestWorkflowScriptsNoSecretPatterns(t *testing.T) {
	// Verify the guard catches the dict-literal shape while allowing separate key assignment.
	badLine := `"` + "pass" + `ed": units - failed,`
	if redact.Redact(badLine) == badLine {
		t.Fatalf("expected redact.Redact to flag dict-literal shape")
	}
	safeLine := `row["passed"] = units - failed`
	if redact.Redact(safeLine) != safeLine {
		t.Fatalf("expected redact.Redact to allow separate key assignment")
	}

	paths, err := filepath.Glob(filepath.Join("workflows", "*.py"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no workflows/*.py files found")
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if redact.Redact(line) != line {
				t.Errorf("%s:%d matched redact.Redact (would trip fyr gate [secrets])", path, i+1)
			}
		}
	}
}
