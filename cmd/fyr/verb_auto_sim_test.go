package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/metrics"
)

func runSIM(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	err := run(args, &out)
	return out.String(), exitOf(err)
}

func TestAutoRefreshDailyDigestSyncsSIM(t *testing.T) {
	home := t.TempDir()
	repo := recordRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "rule.md"), []byte("v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, repo, "add", "rule.md")
	runGitCmd(t, repo, "commit", "-q", "-m", "apply harness tweak")
	shaOut, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse: %v\n%s", err, shaOut)
	}
	origSHA := strings.TrimSpace(string(shaOut))
	runGitCmd(t, repo, "revert", "--no-edit", origSHA)

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	rec, err := brain.RecordSIMAttempt(home, brain.SIMRecordInput{
		RootCause:   "Digest refresh sync test",
		What:        "Tweak rule.md",
		CommitSHA:   origSHA,
		SourceKind:  "auto_harness_edit",
		EvalVerdict: "kept",
		FieldStatus: "pending_data",
		Provenance:  "derived",
		Now:         now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := refreshDailyDigest(s, home, repo, now); err != nil {
		t.Fatalf("refreshDailyDigest: %v", err)
	}
	det, err := brain.GetSIMProblem(home, rec.ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(det.Attempts) != 1 || det.Attempts[0].FieldStatus != "reverted" {
		t.Errorf("attempt field_status after refreshDailyDigest = %+v, want reverted", det.Attempts)
	}
}

func TestEvalModelFlag(t *testing.T) {
	t.Run("validation and explicit model", func(t *testing.T) {
		r := newEvalRig(t, evalSuite, true)
		for name, tc := range map[string]struct {
			args []string
			want string
		}{
			"list with model":  {[]string{"--list", "--model", "gemini-4-carbon-high"}, "--list takes no --model"},
			"invalid chars":    {[]string{"--model", "bad model!", "ok"}, "invalid --model"},
			"empty model":      {[]string{"--model", "", "ok"}, "invalid --model"},
			"interactive-only": {[]string{"--model", "gemini-orchestrator", "ok"}, "interactive-only"},
		} {
			t.Run(name, func(t *testing.T) {
				out, code := r.eval(tc.args...)
				if code != 2 || !strings.Contains(out, tc.want) {
					t.Errorf("eval %v: exit %d out=%q; want exit 2 containing %q", tc.args, code, out, tc.want)
				}
			})
		}

		var capturedModel string
		origRun := evalRun
		evalRun = func(ctx context.Context, o eval.Options, rows []eval.Row) ([]eval.Result, error) {
			capturedModel = o.Model
			return origRun(ctx, o, rows)
		}
		defer func() { evalRun = origRun }()

		out, code := r.eval("--model", "gemini-4-carbon-high", "ok")
		if code != 0 {
			t.Fatalf("eval --model ok: exit %d\n%s", code, out)
		}
		if capturedModel != "gemini-4-carbon-high" {
			t.Errorf("eval.Options.Model = %q, want gemini-4-carbon-high", capturedModel)
		}
		if got := metrics.LatestTrialModel(r.home, "ok", 0); got != "gemini-4-carbon-high" {
			t.Errorf("LatestTrialModel with --model = %q, want gemini-4-carbon-high", got)
		}
	})

	t.Run("default model from session", func(t *testing.T) {
		r2 := newEvalRig(t, evalSuite, true)
		r2.templateModel("1", "gemini-session-m1", "C1")
		out, code := r2.eval("ok")
		if code != 0 {
			t.Fatalf("eval default model: exit %d\n%s", code, out)
		}
		trials := exported(t, r2.home, "trials")
		if len(trials) != 1 || str(trials[0]["policy_model"]) != "gemini-session-m1" {
			t.Errorf("trials row policy_model = %v, want gemini-session-m1", trials)
		}
		if got := metrics.LatestTrialModel(r2.home, "ok", 0); got != "gemini-session-m1" {
			t.Errorf("LatestTrialModel from session = %q, want gemini-session-m1", got)
		}
	})
}

func TestVerbAutoSIMListShowRecordMerge(t *testing.T) {
	home := t.TempDir()
	out, code := runSIM(t, "auto", "sim", "list", "--json", "--home", home)
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty list --json: exit %d out=%q", code, out)
	}
	if out, code = runSIM(t, "auto", "sim", "list", "--home", home); code != 0 || strings.TrimSpace(out) != "(none)" {
		t.Fatalf("empty list text: exit %d out=%q", code, out)
	}

	out, code = runSIM(t, "auto", "sim", "record",
		"--root-cause", "Hook missed bash read bypass",
		"--mechanism", "structural",
		"--signal", "bash_read_bypass",
		"--what", "Block cat on tracked Go files in pre-tool hook",
		"--files", "internal/search/hook.go,rules/search.md",
		"--commit", "1234567",
		"--proposer-model", "gemini-4-carbon-high",
		"--policy-model", "claude-opus-5-5-high",
		"--fixed-rows", "row-a,row-b",
		"--json", "--home", home)
	if code != 0 {
		t.Fatalf("record flags: exit %d out=%q", code, out)
	}
	var r1 brain.SIMRecordResult
	if err := json.Unmarshal([]byte(out), &r1); err != nil || r1.ProblemID != "SIM-P001" || !r1.CreatedProblem || r1.AttemptID < 1 {
		t.Fatalf("record1 json: %+v err=%v", r1, err)
	}

	stdin = strings.NewReader(`{"root_cause":"Duplicate hook phrasing","what":"Tighten rule wording","signal_id":"bash_read_bypass","files":["rules/search.md"],"policy_model":"gemini-4-carbon-high","eval_verdict":"kept","provenance":"derived"}`)
	defer func() { stdin = os.Stdin }()
	out, code = runSIM(t, "auto", "--home", home, "sim", "record", "--json-in", "--json")
	stdin = os.Stdin
	if code != 0 {
		t.Fatalf("record --json-in: exit %d out=%q", code, out)
	}
	var r2 brain.SIMRecordResult
	if err := json.Unmarshal([]byte(out), &r2); err != nil || r2.ProblemID != "SIM-P002" {
		t.Fatalf("record2 json: %+v err=%v", r2, err)
	}

	out, code = runSIM(t, "auto", "sim", "list", "--signal", "bash_read_bypass", "--path", "internal/search", "--limit", "1", "--json", "--home", home)
	if code != 0 {
		t.Fatalf("list filtered: exit %d out=%q", code, out)
	}
	var listed []brain.SIMProblemSummary
	if err := json.Unmarshal([]byte(out), &listed); err != nil || len(listed) != 1 || listed[0].Problem.ID != "SIM-P001" {
		t.Fatalf("list filtered json: %+v err=%v", listed, err)
	}
	out, code = runSIM(t, "auto", "sim", "list", "--status", "active", "--home", home)
	if code != 0 || !strings.Contains(out, "SIM-P001") || !strings.Contains(out, "claude-opus-5-5-high") {
		t.Fatalf("list text: exit %d out=%q", code, out)
	}

	out, code = runSIM(t, "auto", "sim", "show", "SIM-P001", "--json", "--home", home)
	if code != 0 {
		t.Fatalf("show json: exit %d out=%q", code, out)
	}
	var detail brain.SIMProblemDetail
	if err := json.Unmarshal([]byte(out), &detail); err != nil || detail.Problem.ID != "SIM-P001" || len(detail.Attempts) != 1 || detail.Attempts[0].ProposerModel != "gemini-4-carbon-high" {
		t.Fatalf("show detail: %+v err=%v", detail, err)
	}
	if out, code = runSIM(t, "auto", "sim", "show", "SIM-P999", "--home", home); code != 1 || !strings.Contains(out, "sim: problem SIM-P999 not found") {
		t.Fatalf("show missing: exit %d out=%q", code, out)
	}

	out, code = runSIM(t, "auto", "sim", "merge", "SIM-P002", "SIM-P001", "--json", "--home", home)
	if code != 0 {
		t.Fatalf("merge: exit %d out=%q", code, out)
	}
	var mr brain.SIMMergeResult
	if err := json.Unmarshal([]byte(out), &mr); err != nil || mr.OldID != "SIM-P002" || mr.TargetID != "SIM-P001" {
		t.Fatalf("merge json: %+v err=%v", mr, err)
	}
	out, code = runSIM(t, "auto", "sim", "show", "SIM-P002", "--json", "--home", home)
	if code != 0 {
		t.Fatalf("show alias: exit %d out=%q", code, out)
	}
	var resolved brain.SIMProblemDetail
	if err := json.Unmarshal([]byte(out), &resolved); err != nil || resolved.Problem.ID != "SIM-P001" || len(resolved.Aliases) != 1 || resolved.Aliases[0] != "SIM-P002" || len(resolved.Attempts) != 2 {
		t.Fatalf("show alias detail: %+v err=%v", resolved, err)
	}
	if out, code = runSIM(t, "auto", "sim", "merge", "SIM-P001", "SIM-P002", "--home", home); code != 1 || !strings.Contains(out, "merge cycle") {
		t.Fatalf("merge cycle: exit %d out=%q", code, out)
	}
	if out, code = runSIM(t, "auto", "sim", "merge", "SIM-P001", "SIM-P002", "--provenance", "derived", "--home", home); code != 1 || !strings.Contains(out, "sim: operator-provenance problem SIM-P001") {
		t.Fatalf("merge operator immutability: exit %d out=%q", code, out)
	}
	if out, code = runSIM(t, "auto", "sim", "record", "--problem", "SIM-P001", "--status", "resolved", "--what", "derived edit", "--provenance", "derived", "--home", home); code != 1 || !strings.Contains(out, "sim: operator-provenance problem SIM-P001") {
		t.Fatalf("record operator immutability: exit %d out=%q", code, out)
	}
}

func TestVerbAutoSIMUsageGuardAndWritesState(t *testing.T) {
	home := t.TempDir()
	out, code := runSIM(t, "auto", "sim", "record", "--what", "--home", "--root-cause", "Flag value edge", "--home", home)
	if code != 0 || !strings.Contains(out, "SIM-P001") {
		t.Fatalf("value flag --home: exit %d out=%q", code, out)
	}
	det, err := brain.GetSIMProblem(home, "SIM-P001")
	if err != nil || len(det.Attempts) != 1 || det.Attempts[0].What != "--home" {
		t.Fatalf("value flag recorded what=%+v err=%v", det.Attempts, err)
	}

	badCases := map[string][]string{
		"bare sim":           {"auto", "sim"},
		"unknown sub":        {"auto", "sim", "bogus"},
		"list bad flag":      {"auto", "sim", "list", "--nosuch"},
		"auto run sim flag":  {"auto", "run", "--what", "foo"},
		"list limit 0":       {"auto", "sim", "list", "--limit", "0"},
		"list bad status":    {"auto", "sim", "list", "--status", "bogus"},
		"list extra arg":     {"auto", "sim", "list", "extra"},
		"show missing id":    {"auto", "sim", "show"},
		"record no what":     {"auto", "sim", "record", "--root-cause", "x"},
		"record no target":   {"auto", "sim", "record", "--what", "x"},
		"record bad verdict": {"auto", "sim", "record", "--root-cause", "x", "--what", "y", "--verdict", "bogus"},
		"record bad commit":  {"auto", "sim", "record", "--root-cause", "x", "--what", "y", "--commit", "zzz"},
		"merge one arg":      {"auto", "sim", "merge", "SIM-P001"},
		"merge bad prov":     {"auto", "sim", "merge", "SIM-P001", "SIM-P002", "--provenance", "bogus"},
	}
	for name, args := range badCases {
		t.Run(name, func(t *testing.T) {
			out, code := runSIM(t, append(append([]string{}, args...), "--home", home)...)
			if code != 2 || strings.TrimSpace(out) == "" {
				t.Errorf("%v: exit %d out=%q; want exit 2 with message", args, code, out)
			}
		})
	}
	stdin = strings.NewReader(`{"root_cause":"x","what":"y","unknown_key":1}`)
	out, code = runSIM(t, "auto", "sim", "record", "--json-in", "--home", home)
	stdin = os.Stdin
	if code != 2 || strings.TrimSpace(out) == "" {
		t.Errorf("unknown --json-in key: exit %d out=%q; want exit 2", code, out)
	}

	devHome := guardHome(t, "")
	if out, code = runSIM(t, "auto", "sim", "-h"); code != 0 || !strings.Contains(out, "fyr auto sim") {
		t.Errorf("auto sim -h: exit %d out=%q", code, out)
	}
	for _, args := range [][]string{
		{"auto", "sim", "list"},
		{"auto", "sim", "show", "SIM-P001"},
		{"auto", "sim", "record", "--root-cause", "x", "--what", "y"},
		{"auto", "sim", "merge", "SIM-P001", "SIM-P002"},
	} {
		var buf bytes.Buffer
		err := run(args, &buf)
		if !isGuardRefusal(err, buf.String()) {
			t.Errorf("%v without --home: err=%v out=%q; want BUG-507 refusal", args, err, buf.String())
		}
	}
	if _, err := os.Stat(brain.Path(devHome)); !os.IsNotExist(err) {
		t.Errorf("realhome_guard created brain.db: %v", err)
	}

	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"sim", "record", "--root-cause", "x", "--what", "y"}, true},
		{[]string{"sim", "merge", "SIM-P001", "SIM-P002"}, true},
		{[]string{"sim", "list"}, false},
		{[]string{"sim", "show", "SIM-P001"}, false},
		{[]string{"sim"}, false},
	} {
		c := newCtx("auto", verbs["auto"], &bytes.Buffer{})
		if stop, err := c.parse(verbs["auto"], append(tc.args, "--home", home)); stop || err != nil {
			t.Fatalf("parse %v: stop=%v err=%v", tc.args, stop, err)
		}
		if got := c.writesState(); got != tc.want {
			t.Errorf("writesState(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}

	v := vocabulary()
	simFlags, ok := v["auto"].Subverbs["sim"]
	if !ok {
		t.Fatalf("vocabulary missing auto sim subverb: %+v", v["auto"].Subverbs)
	}
	for _, f := range []string{
		"signal", "path", "status", "limit", "json", "json-in", "problem",
		"root-cause", "mechanism", "what", "files", "proposal", "commit",
		"parent", "harness", "proposer-model", "policy-model", "source",
		"verdict", "field-status", "predicted-rows", "predicted-signals",
		"fixed-rows", "missed-rows", "regressed-rows", "provenance",
	} {
		if !simFlags[f] {
			t.Errorf("vocabulary auto sim missing flag --%s", f)
		}
	}

	// Hardening: renderAutoSIMShow sanitizes ANSI escapes in timestamps,
	// and eval --model rejects leading hyphen and >64-byte model IDs.
	var showBuf bytes.Buffer
	_ = renderAutoSIMShow(&showBuf, brain.SIMProblemDetail{
		Problem:  brain.SIMProblem{ID: "SIM-P001", Status: "active", RootCause: "rc", OpenedAt: "2026\x1b[31m", UpdatedAt: "2026\x1b[0m"},
		Attempts: []brain.SIMAttempt{{ID: 1, CreatedAt: "2026\x1b[2J", SourceKind: "operator_commit", What: "w"}},
	}, false)
	if strings.Contains(showBuf.String(), "\x1b") {
		t.Errorf("renderAutoSIMShow emitted raw ESC byte: %q", showBuf.String())
	}
	for _, badModel := range []string{"-leading-hyphen", "--flag", strings.Repeat("m", 65)} {
		var evalOut bytes.Buffer
		err := run([]string{"eval", "--model", badModel, "--home", home, "smoke"}, &evalOut)
		if exitOf(err) != 2 {
			t.Errorf("eval --model %q: exit %d, want 2 (out=%q)", badModel, exitOf(err), evalOut.String())
		}
	}
}
