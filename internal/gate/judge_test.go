package gate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func shSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func writeFakeGateAgy(t *testing.T, binDir, modelsText, usageText, verdictJSON string) string {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agyPath := filepath.Join(binDir, "agy")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"models\" ]; then\n" +
		"  printf '%s' " + shSingleQuote(modelsText) + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"-p\" ] && [ \"$2\" = \"/usage\" ]; then\n" +
		"  printf '%s' " + shSingleQuote(usageText) + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"-p\" ]; then\n" +
		"  case \"$*\" in *\"--output-format json\"*) ;; *) echo 'missing --output-format json' >&2; exit 40 ;; esac\n" +
		"  case \"$*\" in *--json-schema*) ;; *) echo 'missing --json-schema' >&2; exit 41 ;; esac\n" +
		"  case \"$*\" in *\"BEGIN DATA\"*) ;; *) echo 'missing BEGIN DATA fence' >&2; exit 42 ;; esac\n" +
		"  case \"$*\" in *\"Mul(a, b int)\"*) ;; *) echo 'missing diff' >&2; exit 43 ;; esac\n" +
		"  printf '%s\\n' " + shSingleQuote("{\"conversation_id\":\"c1\",\"status\":\"SUCCESS\",\"structured_output\":"+verdictJSON+"}") + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(agyPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return agyPath
}

func TestGateJudgeCrossFamily(t *testing.T) {
	ctx := context.Background()
	repo := initTestGoRepo(t)
	wt := addWorktreeBranch(t, repo, "feat-judge")
	writeFile(t, filepath.Join(wt, "pkg", "mul.go"), "package pkg\n\nfunc Mul(a, b int) int { return a * b }\n")
	runGit(t, wt, "add", "pkg/mul.go")
	runGit(t, wt, "commit", "-m", "add Mul")

	modelsText := "gemini-4-carbon-high\tGemini 4 Carbon (High)\n" +
		"claude-opus-5-5-high\tClaude Opus 5.5 (High)\n"
	healthyUsage := "Gemini Models\tWeekly Limit Remaining\t80%\tx\n" +
		"Gemini Models\tFive Hour Limit Remaining\t90%\tx\n" +
		"Claude and GPT models\tWeekly Limit Remaining\t50%\tx\n" +
		"Claude and GPT models\tFive Hour Limit Remaining\t70%\tx\n"

	t.Run("PASS with healthy cross-family Anthropic and RecordVerdict", func(t *testing.T) {
		home := t.TempDir()
		binDir := filepath.Join(t.TempDir(), "bin")
		writeFakeGateAgy(t, binDir, modelsText, healthyUsage, `{"verdict":"PASS","reason":"Mul matches spec"}`)
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		var recorded []JudgeVerdictRecord
		const crit = "Mul returns a * b in pkg/mul.go"
		rep, err := Gate(ctx, GateOptions{
			Dir:           repo,
			Target:        wt,
			Home:          home,
			Lease:         []string{"pkg/**"},
			Judge:         crit,
			RecordVerdict: func(r JudgeVerdictRecord) error { recorded = append(recorded, r); return nil },
		})
		if err != nil || !rep.OK {
			t.Fatalf("Gate PASS failed: err=%v rep=%+v", err, rep)
		}
		if rep.JudgeModel != "claude-opus-5-5-high" || rep.JudgeVerdict != "PASS" || rep.JudgeFallback != "" {
			t.Errorf("unexpected judge report fields: %+v", rep)
		}
		if len(recorded) != 1 {
			t.Fatalf("expected 1 verdict record, got %d", len(recorded))
		}
		sum := sha256.Sum256([]byte(crit))
		wantHash := hex.EncodeToString(sum[:])
		rec := recorded[0]
		if rec.Model != "claude-opus-5-5-high" || rec.Verdict != "PASS" || rec.CriterionHash != wantHash || rec.TS <= 0 {
			t.Errorf("unexpected verdict record: %+v (want hash %s)", rec, wantHash)
		}
	})

	t.Run("FAIL records verdict and fails gate", func(t *testing.T) {
		home := t.TempDir()
		binDir := filepath.Join(t.TempDir(), "bin")
		writeFakeGateAgy(t, binDir, modelsText, healthyUsage, `{"verdict":"FAIL","reason":"overflow not handled"}`)
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		var recorded []JudgeVerdictRecord
		rep, err := Gate(ctx, GateOptions{
			Dir:           repo,
			Target:        wt,
			Home:          home,
			Lease:         []string{"pkg/**"},
			Judge:         "Mul handles overflow",
			RecordVerdict: func(r JudgeVerdictRecord) error { recorded = append(recorded, r); return nil },
		})
		if !errors.Is(err, ErrFailed) || rep.OK {
			t.Fatalf("expected ErrFailed, got err=%v rep=%+v", err, rep)
		}
		if rep.JudgeModel != "claude-opus-5-5-high" || rep.JudgeVerdict != "FAIL" || !hasFinding(rep, "judge", "overflow not handled") {
			t.Errorf("expected judge finding and FAIL verdict, got %+v", rep)
		}
		if len(recorded) != 1 || recorded[0].Verdict != "FAIL" {
			t.Errorf("expected 1 FAIL verdict record, got %+v", recorded)
		}
	})

	t.Run("missing-pool refusal when agy models empty or agy absent", func(t *testing.T) {
		home := t.TempDir()
		binDir := filepath.Join(t.TempDir(), "bin")
		writeFakeGateAgy(t, binDir, "", healthyUsage, `{"verdict":"PASS","reason":"unused"}`)
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		called := false
		_, err := Gate(ctx, GateOptions{
			Dir:           repo,
			Target:        wt,
			Home:          home,
			Lease:         []string{"pkg/**"},
			Judge:         "Mul exists",
			RecordVerdict: func(JudgeVerdictRecord) error { called = true; return nil },
		})
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("empty agy models: expected ErrRefused, got %v", err)
		}
		if called {
			t.Errorf("RecordVerdict should not run on ErrRefused")
		}

		t.Setenv("PATH", filepath.Join(t.TempDir(), "no-agy-here"))
		if _, err := Gate(ctx, GateOptions{Dir: repo, Target: wt, Home: home, Judge: "Mul exists"}); !errors.Is(err, ErrRefused) {
			t.Fatalf("missing agy binary: expected ErrRefused, got %v", err)
		}
	})

	t.Run("quota <30%/20% falls back to Gemini", func(t *testing.T) {
		for _, tc := range []struct {
			name, five, week string
		}{
			{"five hour < 30%", "25%", "50%"},
			{"weekly < 20%", "50%", "15%"},
		} {
			home := t.TempDir()
			binDir := filepath.Join(t.TempDir(), "bin")
			lowUsage := "Gemini Models\tWeekly Limit Remaining\t80%\tx\n" +
				"Gemini Models\tFive Hour Limit Remaining\t90%\tx\n" +
				"Claude and GPT models\tWeekly Limit Remaining\t" + tc.week + "\tx\n" +
				"Claude and GPT models\tFive Hour Limit Remaining\t" + tc.five + "\tx\n"
			writeFakeGateAgy(t, binDir, modelsText, lowUsage, `{"verdict":"PASS","reason":"gemini fallback ok"}`)
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			var recorded []JudgeVerdictRecord
			rep, err := Gate(ctx, GateOptions{
				Dir:           repo,
				Target:        wt,
				Home:          home,
				Lease:         []string{"pkg/**"},
				Judge:         "Mul exists",
				RecordVerdict: func(r JudgeVerdictRecord) error { recorded = append(recorded, r); return nil },
			})
			if err != nil || !rep.OK {
				t.Fatalf("%s: Gate failed: err=%v rep=%+v", tc.name, err, rep)
			}
			if rep.JudgeModel != "gemini-4-carbon-high" || rep.JudgeVerdict != "PASS" || !strings.Contains(rep.JudgeFallback, "Gemini") {
				t.Errorf("%s: expected Gemini fallback, got %+v", tc.name, rep)
			}
			if len(recorded) != 1 || recorded[0].Model != "gemini-4-carbon-high" || recorded[0].FallbackReason == "" {
				t.Errorf("%s: unexpected verdict record: %+v", tc.name, recorded)
			}
		}
	})

	t.Run("Land forwards judge options", func(t *testing.T) {
		wtLand := filepath.Join(t.TempDir(), "wt-land-judge")
		runGit(t, repo, "worktree", "add", "-b", "branch-judge-land", wtLand, "main")
		writeFile(t, filepath.Join(wtLand, "sub.go"), "package main\n\nfunc Sub(a, b int) int { return a - b }\n")
		runGit(t, wtLand, "add", "sub.go")
		runGit(t, wtLand, "commit", "-m", "add Sub")
		runGit(t, repo, "worktree", "remove", wtLand)

		home := t.TempDir()
		var gotReq JudgeRequest
		var recorded []JudgeVerdictRecord
		lRep, err := Land(ctx, LandOptions{
			Dir:        repo,
			Branch:     "branch-judge-land",
			Home:       home,
			Lease:      []string{"sub.go"},
			Judge:      "Sub returns a - b",
			JudgeModel: "explicit-judge",
			JudgeRunner: func(_ context.Context, req JudgeRequest) (JudgeResult, error) {
				gotReq = req
				return JudgeResult{Model: req.ExplicitModel, Verdict: "PASS", Reason: "ok"}, nil
			},
			RecordVerdict: func(r JudgeVerdictRecord) error { recorded = append(recorded, r); return nil },
		})
		if err != nil || !lRep.OK || lRep.Gate == nil || lRep.Gate.JudgeVerdict != "PASS" {
			t.Fatalf("Land with judge failed: err=%v rep=%+v", err, lRep)
		}
		if gotReq.Criterion != "Sub returns a - b" || gotReq.ExplicitModel != "explicit-judge" || len(recorded) != 1 {
			t.Errorf("Land did not forward judge options: req=%+v recorded=%+v", gotReq, recorded)
		}
	})

	t.Run("nonzero exit with empty stdout surfaces stderr not empty judge response", func(t *testing.T) {
		home := t.TempDir()
		binDir := filepath.Join(t.TempDir(), "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\n" +
			"if [ \"$1\" = \"models\" ]; then\n" +
			"  printf '%s' " + shSingleQuote(modelsText) + "\n" +
			"  exit 0\n" +
			"fi\n" +
			"if [ \"$1\" = \"-p\" ] && [ \"$2\" = \"/usage\" ]; then\n" +
			"  printf '%s' " + shSingleQuote(healthyUsage) + "\n" +
			"  exit 0\n" +
			"fi\n" +
			"if [ \"$1\" = \"-p\" ]; then\n" +
			"  echo 'Error: --json-schema failed' >&2\n" +
			"  exit 1\n" +
			"fi\n" +
			"exit 1\n"
		if err := os.WriteFile(filepath.Join(binDir, "agy"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		rep, err := Gate(ctx, GateOptions{
			Dir:    repo,
			Target: wt,
			Home:   home,
			Lease:  []string{"pkg/**"},
			Judge:  "Mul exists",
		})
		if !errors.Is(err, ErrFailed) || rep.OK {
			t.Fatalf("expected ErrFailed, got err=%v rep=%+v", err, rep)
		}
		if !hasFinding(rep, "judge", "Error: --json-schema failed") {
			t.Errorf("expected stderr message in judge finding, got %+v", rep.Findings)
		}
		if hasFinding(rep, "judge", "empty judge response") {
			t.Errorf("stderr should not be masked by empty judge response: %+v", rep.Findings)
		}
	})

	t.Run("stale verify.stamp on main notes head mismatch instead of ok=true", func(t *testing.T) {
		repo2 := initTestGoRepo(t)
		wt2 := addWorktreeBranch(t, repo2, "feat-stale-stamp")
		writeFile(t, filepath.Join(wt2, "pkg", "mul.go"), "package pkg\n\nfunc Mul(a, b int) int { return a * b }\n")
		runGit(t, wt2, "add", "pkg/mul.go")
		runGit(t, wt2, "commit", "-m", "add Mul")

		mainHead := runGit(t, repo2, "rev-parse", "HEAD")
		wtHead := runGit(t, wt2, "rev-parse", "HEAD")
		if mainHead == "" || wtHead == "" || mainHead == wtHead {
			t.Fatalf("expected distinct heads, main=%q wt=%q", mainHead, wtHead)
		}
		if _, err := WriteStamp(repo2, Stamp{
			TreeSHA256: "deadbeef",
			UpdatedAt:  "2026-11-19T00:00:00Z",
			Branch:     "main",
			Head:       mainHead,
			OK:         true,
		}); err != nil {
			t.Fatalf("WriteStamp: %v", err)
		}

		home := t.TempDir()
		binDir := filepath.Join(t.TempDir(), "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		wantNote := "verify.stamp: missing for head " + wtHead + " (found stamp for head=" + mainHead + " ok=true)"
		script := "#!/bin/sh\n" +
			"if [ \"$1\" = \"models\" ]; then\n" +
			"  printf '%s' " + shSingleQuote(modelsText) + "\n" +
			"  exit 0\n" +
			"fi\n" +
			"if [ \"$1\" = \"-p\" ] && [ \"$2\" = \"/usage\" ]; then\n" +
			"  printf '%s' " + shSingleQuote(healthyUsage) + "\n" +
			"  exit 0\n" +
			"fi\n" +
			"if [ \"$1\" = \"-p\" ]; then\n" +
			"  case \"$2\" in *\"" + wantNote + "\"*) ;; *) echo 'missing head-mismatch note in prompt' >&2; exit 44 ;; esac\n" +
			"  case \"$2\" in *\"ok=true treeSha256=\"*) echo 'stale main stamp reported as ok=true' >&2; exit 45 ;; esac\n" +
			"  printf '%s\\n' " + shSingleQuote(`{"conversation_id":"c1","status":"SUCCESS","structured_output":{"verdict":"PASS","reason":"checked"}}`) + "\n" +
			"  exit 0\n" +
			"fi\n" +
			"exit 1\n"
		if err := os.WriteFile(filepath.Join(binDir, "agy"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		rep, err := Gate(ctx, GateOptions{
			Dir:    repo2,
			Target: wt2,
			Home:   home,
			Lease:  []string{"pkg/**"},
			Judge:  "Mul returns a * b",
		})
		if err != nil || !rep.OK {
			t.Fatalf("Gate with stale main stamp failed: err=%v rep=%+v", err, rep)
		}

		var gotReq JudgeRequest
		rep2, err := Gate(ctx, GateOptions{
			Dir:    repo2,
			Target: wt2,
			Home:   home,
			Lease:  []string{"pkg/**"},
			Judge:  "Mul returns a * b",
			JudgeRunner: func(_ context.Context, req JudgeRequest) (JudgeResult, error) {
				gotReq = req
				return JudgeResult{Model: "claude-opus-5-5-high", Verdict: "PASS", Reason: "ok"}, nil
			},
		})
		if err != nil || !rep2.OK {
			t.Fatalf("Gate JudgeRunner failed: err=%v rep=%+v", err, rep2)
		}
		prompt := buildGateJudgePrompt(gotReq)
		if gotReq.StampSummary != wantNote || !strings.Contains(prompt, wantNote) || strings.Contains(prompt, "ok=true treeSha256=") {
			t.Fatalf("verify-stamp fence leaked stale main stamp as ok=true: summary=%q prompt=%s", gotReq.StampSummary, prompt)
		}
	})
}

func TestGatePackageDeps(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == "modernc.org/sqlite" || strings.Contains(p, "internal/metrics") {
				t.Errorf("%s imports forbidden package %q", e.Name(), p)
			}
		}
	}
}
