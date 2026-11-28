package fyr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
