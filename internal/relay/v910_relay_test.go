package relay

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/gate"
	"github.com/kesokaj/fyr/internal/models"
)

func TestV910RelayPoolAwareQuotaAndAttribution(t *testing.T) {
	t.Run("RunAgySession parses worker model and session id from JSON stdout", func(t *testing.T) {
		dir := t.TempDir()
		fakeAgy := filepath.Join(dir, "fake-agy.sh")
		script := "#!/bin/sh\nprintf '{\"conversation_id\":\"conv-agy-42\",\"model\":\"claude-opus-5-5-high\",\"status\":\"SUCCESS\"}\\n'\n"
		if err := os.WriteFile(fakeAgy, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		res, err := RunAgySession(context.Background(), SessionRequest{
			RepoDir: dir, AgyPath: fakeAgy, Model: "gemini-4-carbon-high", Prompt: "run phase", Timeout: 10 * time.Second,
		})
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("RunAgySession failed: err=%v res=%+v", err, res)
		}
		if res.WorkerModel != "claude-opus-5-5-high" || res.WorkerSessionID != "conv-agy-42" {
			t.Fatalf("RunAgySession attribution = (%q, %q), want (claude-opus-5-5-high, conv-agy-42)", res.WorkerModel, res.WorkerSessionID)
		}
	})

	t.Run("phase targeting claude passes when gemini is low and claude is healthy, threading RealHome and worker attribution", func(t *testing.T) {
		repo, home := initRelayRepo(t)
		realHome := t.TempDir()
		ts := time.Now().UTC().Format(time.RFC3339)
		if err := models.SaveCatalog(home, models.Catalog{
			UpdatedAt:      ts,
			QuotaUpdatedAt: ts,
			Pools: []models.Pool{
				{Name: "Gemini Models", Five: 5, Week: 5},
				{Name: "Claude and GPT models", Five: 75, Week: 65},
			},
		}); err != nil {
			t.Fatal(err)
		}
		planBody := "# Plan — Claude Phase\n\n#### Phase 1 — Add alpha\nModel: claude-opus-5-5-high\n`Files:` `alpha.go`\n\n- [ ] Create alpha.go\n"
		planID := addPlanT(t, home, repo, "PLAN-2026-11-23-claude-ok", planBody)
		runner := func(_ context.Context, req SessionRequest) (SessionResult, error) {
			writeFileT(t, filepath.Join(req.RepoDir, "alpha.go"), "package main\nfunc alpha() {}\n")
			runGitT(t, req.RepoDir, "add", "alpha.go")
			runGitT(t, req.RepoDir, "commit", "-m", "add alpha")
			return SessionResult{Stdout: `{"conversation_id":"conv-relay-1","model":"claude-opus-5-5-high","status":"SUCCESS"}`}, nil
		}
		var gotJudgeOpts gate.GateOptions
		judgeFn := func(_ context.Context, opts gate.GateOptions) (gate.GateReport, error) {
			gotJudgeOpts = opts
			return gate.GateReport{OK: true, JudgeModel: "gemini-4-carbon-high", JudgeVerdict: "PASS"}, nil
		}
		rep, err := Run(context.Background(), Options{
			Dir: repo, Home: home, RealHome: realHome, PlanID: planID, MaxPhases: 1, RequireQuota: true,
			SessionRunner: runner, VerifyFn: passVerify, JudgeFn: judgeFn,
		})
		if err != nil || !rep.OK || rep.Status != StatusOK {
			t.Fatalf("claude phase with healthy claude pool failed: rep=%+v err=%v", rep, err)
		}
		if len(rep.Phases) != 1 || rep.Phases[0].WorkerModel != "claude-opus-5-5-high" || rep.Phases[0].WorkerSessionID != "conv-relay-1" {
			t.Fatalf("PhaseResult worker attribution = %+v", rep.Phases)
		}
		if gotJudgeOpts.RealHome != realHome || gotJudgeOpts.WorkerModel != "claude-opus-5-5-high" || gotJudgeOpts.WorkerSessionID != "conv-relay-1" {
			t.Fatalf("JudgeFn GateOptions = (RealHome=%q WorkerModel=%q WorkerSessionID=%q), want (%q, claude-opus-5-5-high, conv-relay-1)",
				gotJudgeOpts.RealHome, gotJudgeOpts.WorkerModel, gotJudgeOpts.WorkerSessionID, realHome)
		}
	})

	t.Run("phase targeting claude halts BUDGET_FLOOR when claude is low even if gemini is healthy", func(t *testing.T) {
		repo, home := initRelayRepo(t)
		ts := time.Now().UTC().Format(time.RFC3339)
		if err := models.SaveCatalog(home, models.Catalog{
			UpdatedAt:      ts,
			QuotaUpdatedAt: ts,
			Pools: []models.Pool{
				{Name: "Gemini Models", Five: 90, Week: 90},
				{Name: "Claude and GPT models", Five: 10, Week: 50},
			},
		}); err != nil {
			t.Fatal(err)
		}
		planBody := "# Plan — Claude Low\n\n#### Phase 1 — Add alpha\nModel: claude-opus-5-5-high\n`Files:` `alpha.go`\n\n- [ ] Create alpha.go\n"
		planID := addPlanT(t, home, repo, "PLAN-2026-11-23-claude-low", planBody)
		calls := 0
		rep, err := Run(context.Background(), Options{
			Dir: repo, Home: home, PlanID: planID, MaxPhases: 1,
			SessionRunner: func(context.Context, SessionRequest) (SessionResult, error) { calls++; return SessionResult{}, nil },
			VerifyFn:      passVerify, JudgeFn: passJudge,
		})
		if !errors.Is(err, ErrHalted) || rep.HaltReason != HaltBudgetFloor || calls != 0 {
			t.Fatalf("claude low must halt BUDGET_FLOOR with 0 session calls: rep=%+v calls=%d err=%v", rep, calls, err)
		}
		if !strings.Contains(rep.QuotaNote, "Claude and GPT models") {
			t.Errorf("QuotaNote = %q, want Claude pool name", rep.QuotaNote)
		}
	})

	t.Run("EvaluatePoolsFloorForModel table covers gemini, anthropic, and gpt pools", func(t *testing.T) {
		pools := []models.Pool{
			{Name: "Gemini Models", Five: 10, Week: 80},
			{Name: "Claude and GPT models", Five: 70, Week: 60},
		}
		for _, tc := range []struct {
			model       string
			require     bool
			wantOK      bool
			wantNoteSub string
		}{
			{"", true, false, "Gemini Models below floor"},
			{"gemini-4-carbon-high", true, false, "Gemini Models below floor"},
			{"claude-opus-5-5-high", true, true, "Claude and GPT models 5h=70%"},
			{"gpt-5-codex", true, true, "Claude and GPT models 5h=70%"},
		} {
			got := EvaluatePoolsFloorForModel(pools, tc.model, tc.require)
			if got.OK != tc.wantOK || !strings.Contains(got.Note, tc.wantNoteSub) {
				t.Errorf("EvaluatePoolsFloorForModel(%q) = {OK:%v Note:%q}, want {OK:%v sub:%q}",
					tc.model, got.OK, got.Note, tc.wantOK, tc.wantNoteSub)
			}
		}
		// Missing Claude pool with requireQuota=true halts, optional skips.
		gemOnly := []models.Pool{{Name: "Gemini Models", Five: 90, Week: 90}}
		if got := EvaluatePoolsFloorForModel(gemOnly, "claude-opus-5-5-high", true); got.OK || !strings.Contains(got.Note, "Anthropic/Claude pool missing") {
			t.Errorf("missing claude pool required: %+v", got)
		}
		if got := EvaluatePoolsFloorForModel(gemOnly, "claude-opus-5-5-high", false); !got.OK || !strings.Contains(got.Note, "Anthropic/Claude pool absent") {
			t.Errorf("missing claude pool optional: %+v", got)
		}
	})
}
