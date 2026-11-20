package models

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
)

const (
	DefaultBenchMax  = 3
	BenchTimeout     = 45 * time.Second
	benchExpectedSum = 44
	benchJSONSchema  = `{"type":"object","properties":{"sum":{"type":"integer"},"go_func":{"type":"string"},"ok":{"type":"boolean"}},"required":["sum","go_func","ok"],"additionalProperties":false}`
	BenchPrompt      = "Do NOT call any tools, read files, or run commands. Evaluate `xs := []int{3, 4, 2, 5, 6}; s := 0; for i, v := range xs { if v%2 == 0 { s += v * (i + 1) } }` (set `sum` to `s`), write `func ClampScore(v int) int` clamping `v` to `[0, 100]` in `go_func`, and set `ok` to `true`. Respond ONLY with JSON `{\"sum\": <int>, \"go_func\": \"...\", \"ok\": true}`."
)

type BenchProbeFunc func(ctx context.Context, home, agyPath, modelID, prompt string, timeout time.Duration) (string, error)

var BenchRunnerFunc BenchProbeFunc = RunBenchProbe

type BenchTrialRecord struct {
	Run, Model, Status, Cause string
	StartedAt                 time.Time
	DurationMS                int64
}

type BenchResult struct {
	Model      string          `json:"model"`
	Status     string          `json:"status"`
	Cause      string          `json:"cause,omitempty"`
	DurationMS int64           `json:"duration_ms"`
	Score      *ScoreBreakdown `json:"score,omitempty"`
}

type BenchReport struct {
	Probed  int                 `json:"probed"`
	Passed  int                 `json:"passed"`
	Failed  int                 `json:"failed"`
	Results []BenchResult       `json:"results"`
	Picks   map[string]RolePick `json:"picks,omitempty"`
}

func UsesDefaultBenchRunner() bool {
	return BenchRunnerFunc != nil && reflect.ValueOf(BenchRunnerFunc).Pointer() == reflect.ValueOf(RunBenchProbe).Pointer()
}

func RunBenchProbe(ctx context.Context, home, agyPath, modelID, prompt string, timeout time.Duration) (string, error) {
	if strings.TrimSpace(agyPath) == "" {
		return "", errors.New("bench probe: agy not found on PATH")
	}
	if timeout <= 0 {
		timeout = BenchTimeout
	}
	return runAgyProbe(ctx, home, agyPath, timeout, 5*time.Second,
		"-p", prompt, "--disable-slash-commands", "--output-format", "json", "--json-schema", benchJSONSchema, "--model", modelID)
}

func SelectBenchTargets(cat Catalog, explicitModel string, maxModels int) ([]CatalogEntry, error) {
	if maxModels <= 0 {
		maxModels = DefaultBenchMax
	}
	if target := strings.ToLower(strings.TrimSpace(explicitModel)); target != "" {
		for _, e := range cat.Entries {
			ApplyModelProfile(&e)
			if strings.EqualFold(e.ID, target) {
				if e.Status == "gone" || e.InteractiveOnly || e.Mode == "orchestrator" {
					return nil, fmt.Errorf("model %q is gone or interactive-only and cannot run headless --bench", explicitModel)
				}
				return []CatalogEntry{e}, nil
			}
		}
		return nil, fmt.Errorf("no model %q in catalog", explicitModel)
	}
	var cands []CatalogEntry
	for _, e := range cat.Entries {
		ApplyModelProfile(&e)
		if isAutoPickEligible(e) && HasZeroEvidence(e.Evidence) {
			cands = append(cands, e)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if (cands[i].Status == "new") != (cands[j].Status == "new") {
			return cands[i].Status == "new"
		}
		ra, ta := defaultEntryRoleTier(cands[i])
		rb, tb := defaultEntryRoleTier(cands[j])
		return betterScoredCandidate(cands[i], ScoreCandidate(cands[i], ra, ta, 0, cat.Pools), cands[j], ScoreCandidate(cands[j], rb, tb, 0, cat.Pools))
	})
	return cands[:min(len(cands), maxModels)], nil
}

func RunBench(ctx context.Context, home, agyPath string, targets []CatalogEntry, record func(BenchTrialRecord) error) (BenchReport, error) {
	if BenchRunnerFunc == nil {
		return BenchReport{}, errors.New("bench runner unavailable")
	}
	rep := BenchReport{Results: make([]BenchResult, 0, len(targets))}
	stamp := time.Now().UTC().Format("20060102-150405.000")
	for i, t := range targets {
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		started := time.Now().UTC()
		raw, pErr := BenchRunnerFunc(ctx, home, agyPath, t.ID, BenchPrompt, BenchTimeout)
		durMS := max(time.Since(started).Milliseconds(), 1)
		status, cause := classifyBenchOutcome(raw, pErr)
		if record != nil {
			if err := record(BenchTrialRecord{Run: fmt.Sprintf("bench/%s/%d/%s", stamp, i+1, t.ID), Model: t.ID, Status: status, Cause: cause, StartedAt: started, DurationMS: durMS}); err != nil {
				return rep, err
			}
		}
		rep.Probed++
		if status == "pass" {
			rep.Passed++
		} else {
			rep.Failed++
		}
		rep.Results = append(rep.Results, BenchResult{Model: t.ID, Status: status, Cause: cause, DurationMS: durMS})
	}
	return rep, nil
}

func FinalizeBenchReport(w io.Writer, rep *BenchReport, cat Catalog, writeText bool) {
	for i := range rep.Results {
		for j := range cat.Entries {
			if cat.Entries[j].ID == rep.Results[i].Model {
				rep.Results[i].Score = cat.Entries[j].Score
			}
		}
	}
	rep.Picks = cat.Picks
	if !writeText {
		return
	}
	if rep.Probed == 0 {
		fmt.Fprintln(w, "bench: 0 unprobed active models")
		return
	}
	for _, r := range rep.Results {
		line := fmt.Sprintf("bench %-28s  %-4s  %dms", r.Model, strings.ToUpper(r.Status), r.DurationMS)
		if r.Cause != "" {
			line += " (" + r.Cause + ")"
		}
		if r.Score != nil {
			line += "  " + formatScoreSummary(*r.Score)
		}
		fmt.Fprintln(w, line)
	}
}

func classifyBenchOutcome(raw string, pErr error) (string, string) {
	if pErr != nil {
		if low := strings.ToLower(pErr.Error()); errors.Is(pErr, context.DeadlineExceeded) || strings.Contains(low, "no answer in") || strings.Contains(low, "timed out") {
			return "fail", "timeout"
		}
		return "fail", "probe_error"
	}
	if why, ok := VerifyBenchResponse(raw); !ok {
		return "fail", why
	}
	return "pass", ""
}

func VerifyBenchResponse(raw string) (string, bool) {
	inner, envCause, okEnv := unwrapBenchPayload(strings.TrimSpace(raw))
	if !okEnv {
		return envCause, false
	}
	var payload struct {
		Sum    int    `json:"sum"`
		GoFunc string `json:"go_func"`
		OK     bool   `json:"ok"`
	}
	dec := json.NewDecoder(bytes.NewReader(inner))
	dec.DisallowUnknownFields()
	if dec.Decode(&payload) != nil {
		return "invalid_json", false
	}
	if _, err := dec.Token(); err != io.EOF {
		return "invalid_json", false
	}
	if !payload.OK {
		return "not_ok", false
	}
	if payload.Sum != benchExpectedSum {
		return "wrong_sum", false
	}
	if !validClampScoreAST(payload.GoFunc) {
		return "invalid_go_ast", false
	}
	return "", true
}

func unwrapBenchPayload(trimmed string) ([]byte, string, bool) {
	if trimmed == "" {
		return nil, "empty_output", false
	}
	var env struct {
		Status, Error, Response string
		StructuredOutput        json.RawMessage `json:"structured_output"`
		StructuredCamel         json.RawMessage `json:"structuredOutput"`
		DeniedActions           []any           `json:"denied_actions"`
		DeniedCamel             []any           `json:"deniedActions"`
	}
	if json.Unmarshal([]byte(trimmed), &env) == nil {
		so := bytes.TrimSpace(env.StructuredOutput)
		if len(so) == 0 || string(so) == "null" {
			so = bytes.TrimSpace(env.StructuredCamel)
		}
		if env.Status != "" || env.Error != "" || env.Response != "" || (len(so) > 0 && string(so) != "null") || len(env.DeniedActions) > 0 || len(env.DeniedCamel) > 0 {
			if len(env.DeniedActions) > 0 || len(env.DeniedCamel) > 0 {
				return nil, "tool_attempted", false
			}
			if env.Error != "" || (env.Status != "" && !strings.EqualFold(env.Status, "SUCCESS")) {
				return nil, "agy_error", false
			}
			if len(so) > 0 && string(so) != "null" {
				return so, "", true
			}
			trimmed = strings.TrimSpace(env.Response)
		}
	}
	if strings.HasPrefix(trimmed, "```") {
		if nl := strings.IndexByte(trimmed, '\n'); nl >= 0 {
			trimmed = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(trimmed[nl+1:]), "```"))
		}
	}
	return []byte(trimmed), "", true
}

func validClampScoreAST(goFunc string) bool {
	f, err := parser.ParseFile(token.NewFileSet(), "bench.go", "package bench\n\n"+strings.TrimSpace(goFunc)+"\n", 0)
	if err != nil || len(f.Decls) != 1 {
		return false
	}
	fd, ok := f.Decls[0].(*ast.FuncDecl)
	if !ok || fd.Recv != nil || fd.Name.Name != "ClampScore" || fd.Body == nil || len(fd.Body.List) == 0 ||
		fd.Type.Params == nil || fd.Type.Params.NumFields() != 1 || fd.Type.Results == nil || fd.Type.Results.NumFields() != 1 {
		return false
	}
	pIdent, okP := fd.Type.Params.List[0].Type.(*ast.Ident)
	rIdent, okR := fd.Type.Results.List[0].Type.(*ast.Ident)
	return okP && okR && pIdent.Name == "int" && rIdent.Name == "int"
}
