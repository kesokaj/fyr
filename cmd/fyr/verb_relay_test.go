package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/gate"
	"github.com/kesokaj/fyr/internal/relay"
)

func TestCLIRelayGuardUsageAndHelp(t *testing.T) {
	// 1. Realhome guard refuses dev build without --home (exit 2).
	guardHome(t, "")
	outStr, _, code := runCLIWithErr("relay", "--plan", "PLAN-1")
	if code != 2 || !strings.Contains(outStr, "BUG-507") {
		t.Fatalf("relay without --home: code=%d out=%q; want exit 2 BUG-507 refusal", code, outStr)
	}

	// 2. Usage errors exit 2.
	home := t.TempDir()
	for name, args := range map[string][]string{
		"missing plan":      {"relay", "--home", home},
		"max-phases 0":      {"relay", "--home", home, "--plan", "PLAN-1", "--max-phases", "0"},
		"max-phases 21":     {"relay", "--home", home, "--plan", "PLAN-1", "--max-phases", "21"},
		"stray positional":  {"relay", "--home", home, "--plan", "PLAN-1", "extra"},
		"unknown verb flag": {"relay", "--home", home, "--plan", "PLAN-1", "--bogus"},
	} {
		t.Run(name, func(t *testing.T) {
			out, _, c := runCLIWithErr(args...)
			if c != 2 {
				t.Errorf("%v: exit=%d, want 2; out=%q", args, c, out)
			}
		})
	}

	// 3. help relay succeeds and documents all flags.
	helpOut, _, code := runCLIWithErr("help", "relay")
	if code != 0 {
		t.Fatalf("help relay exit=%d out=%q", code, helpOut)
	}
	for _, want := range []string{"--plan ID", "--max-phases N", "--judge-model ID", "--dry-run", "--json"} {
		if !strings.Contains(helpOut, want) {
			t.Errorf("help relay missing %q:\n%s", want, helpOut)
		}
	}
}

func TestCLIRelayDryRunPassAndHalt(t *testing.T) {
	repo := initCLITestRepo(t)
	home := t.TempDir()
	planBody := "# Plan — CLI Relay\n\n#### Phase 1 — Add piece\n`Files:` `piece.go`\n\n- [ ] Add piece.go\n"
	planID, err := brain.Add(brain.Options{Home: home, Dir: repo}, brain.AddInput{
		Kind: "plan", ID: "PLAN-2026-11-22-cli", Title: "CLI Relay", Body: strings.NewReader(planBody),
	})
	if err != nil {
		t.Fatalf("add plan: %v", err)
	}

	// 1. --dry-run --json exits 0, invokes nothing, writes nothing.
	called := 0
	prevRunner, prevVerify, prevJudge := relaySessionRunner, relayVerifyFn, relayJudgeFn
	t.Cleanup(func() {
		relaySessionRunner, relayVerifyFn, relayJudgeFn = prevRunner, prevVerify, prevJudge
	})
	relaySessionRunner = func(context.Context, relay.SessionRequest) (relay.SessionResult, error) {
		called++
		return relay.SessionResult{}, nil
	}
	outStr, errStr, code := runCLIWithErr("relay", "--home", home, "--dir", repo, "--plan", planID, "--dry-run", "--json")
	if code != 0 || called != 0 {
		t.Fatalf("dry-run --json: code=%d called=%d out=%s err=%s", code, called, outStr, errStr)
	}
	var dryRep relay.Report
	if err := json.Unmarshal([]byte(outStr), &dryRep); err != nil || dryRep.Status != relay.StatusDryRun || !dryRep.GitClean {
		t.Fatalf("bad dry-run json: err=%v rep=%+v", err, dryRep)
	}
	outStr, _, code = runCLIWithErr("relay", "--home", home, "--dir", repo, "--plan", planID, "--dry-run")
	if code != 0 || !strings.Contains(outStr, "relay: DRY_RUN") {
		t.Fatalf("dry-run text: code=%d out=%q", code, outStr)
	}

	// 2. Halt (zero progress) exits 1 with JSON report.
	relayVerifyFn = func(context.Context, gate.VerifyOptions) (gate.VerifyReport, error) {
		return gate.VerifyReport{OK: true, TreeSHA256: "1234567890abcdef"}, nil
	}
	relayJudgeFn = func(context.Context, gate.GateOptions) (gate.GateReport, error) {
		return gate.GateReport{OK: true, JudgeModel: "claude-test", JudgeVerdict: "PASS"}, nil
	}
	outStr, _, code = runCLIWithErr("relay", "--home", home, "--dir", repo, "--plan", planID, "--json")
	if code != 1 {
		t.Fatalf("zero-progress halt: want exit 1, got %d out=%s", code, outStr)
	}
	var haltRep relay.Report
	if err := json.Unmarshal([]byte(outStr), &haltRep); err != nil || haltRep.HaltReason != relay.HaltZeroProgress {
		t.Fatalf("bad halt json: err=%v rep=%+v", err, haltRep)
	}

	// 3. Passing phase exits 0, ticks plan, subsequent run prints COMPLETE (exit 0).
	relaySessionRunner = func(_ context.Context, req relay.SessionRequest) (relay.SessionResult, error) {
		writeCLITestFile(t, filepath.Join(req.RepoDir, "piece.go"), "package main\nfunc piece() int { return 1 }\n")
		runGitCLI(t, req.RepoDir, "add", "piece.go")
		runGitCLI(t, req.RepoDir, "commit", "-m", "add piece")
		return relay.SessionResult{ExitCode: 0}, nil
	}
	outStr, errStr, code = runCLIWithErr("relay", "--home", home, "--dir", repo, "--plan", planID, "--judge-model", "claude-test")
	if code != 0 || !strings.Contains(outStr, "relay: OK") {
		t.Fatalf("passing relay: code=%d out=%q err=%q", code, outStr, errStr)
	}
	outStr, errStr, code = runCLIWithErr("relay", "--home", home, "--dir", repo, "--plan", planID)
	if code != 0 || !strings.Contains(outStr, "relay: COMPLETE") {
		t.Fatalf("completed plan rerun: code=%d out=%q err=%q", code, outStr, errStr)
	}

	// 4. Dirty tree refusal exits 2.
	plan2ID, err := brain.Add(brain.Options{Home: home, Dir: repo}, brain.AddInput{
		Kind: "plan", ID: "PLAN-2026-11-22-dirty", Title: "Dirty Check", Body: strings.NewReader(planBody),
	})
	if err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(repo, "dirty.txt"), "uncommitted\n")
	_, errStr, code = runCLIWithErr("relay", "--home", home, "--dir", repo, "--plan", plan2ID)
	if code != 2 || !strings.Contains(errStr, "dirty") {
		t.Fatalf("dirty tree refusal: code=%d err=%q", code, errStr)
	}
}
