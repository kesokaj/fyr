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
