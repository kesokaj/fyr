package fyr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
