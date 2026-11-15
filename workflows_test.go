package fyr

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

type workflowRunResult struct {
	Prompts []string       `json:"prompts"`
	Output  map[string]any `json:"output"`
}

const pyWorkflowHarness = `
import ast, asyncio, contextlib, io, json, sys

script_path, args_json = sys.argv[1], sys.argv[2]
args = json.loads(args_json)
prompts = []

def phase(_name): pass
def log(_msg): pass

async def agent(prompt, schema=None, role="", type_name="", workspace="", model=""):
    prompts.append(prompt)
    return {
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

print(json.dumps({"prompts": prompts, "output": json.loads(buf.getvalue())}))
`

func runWorkflowScript(t *testing.T, script string, args map[string]any) workflowRunResult {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("python3", "-c", pyWorkflowHarness, script, string(rawArgs)).CombinedOutput()
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
}
