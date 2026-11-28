package search

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const charEngineSrc = `package core

// Config controls engine execution parameters.
type Config struct {
	Timeout int
}

// RunEngine runs validation before executing the engine loop.
func RunEngine(cfg Config) error {
	return ValidateConfig(cfg)
}

// ValidateConfig checks bounds on Config.
func ValidateConfig(cfg Config) error {
	if cfg.Timeout < 0 {
		return nil
	}
	return nil
}
`

const charMainSrc = `package main

import "example.com/charapp/core"

func main() {
	_ = core.RunEngine(core.Config{Timeout: 5})
}
`

func setupCharSearchRepo(t *testing.T) (dir, home string) {
	t.Helper()
	dir = newRepo(t)
	home = t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/charapp\n\ngo 1.24\n")
	writeFile(t, dir, "core/engine.go", charEngineSrc)
	writeFile(t, dir, "cmd/app/main.go", charMainSrc)
	commitAll(t, dir, "init char search fixture")
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	return dir, home
}

func TestSearchModesCharacterization(t *testing.T) {
	dir, home := setupCharSearchRepo(t)

	t.Run("slice deterministic and includes 1-hop neighbor", func(t *testing.T) {
		opts := Options{Dir: dir, Home: home, Slice: "RunEngine", Hops: 2, Budget: 2000}
		out1 := mustSearch(t, opts)
		out2 := mustSearch(t, opts)
		if out1 != out2 {
			t.Fatalf("slice output not deterministic:\n1:\n%s\n2:\n%s", out1, out2)
		}
		for _, want := range []string{"core/engine.go", "RunEngine", "ValidateConfig"} {
			if !strings.Contains(out1, want) {
				t.Errorf("slice missing %q:\n%s", want, out1)
			}
		}
	})

	t.Run("contracts deterministic with exported surface and spec bodies", func(t *testing.T) {
		opts := Options{
			Dir:       dir,
			Home:      home,
			Contracts: "core/engine.go",
			BrainSpecBodies: func(_, _ string, _ []string) []SpecContractSource {
				return []SpecContractSource{{
					ID:     "SPEC-charapp-engine",
					Kind:   "spec",
					Status: "current",
					Title:  "Engine contract",
					Body:   "## Data\n- `RunEngine` validates non-negative Timeout.\n",
				}}
			},
		}
		out1 := mustSearch(t, opts)
		out2 := mustSearch(t, opts)
		if out1 != out2 {
			t.Fatalf("contracts output not deterministic:\n1:\n%s\n2:\n%s", out1, out2)
		}
		for _, want := range []string{
			"[CODE_SIGNATURES]",
			"RunEngine",
			"ValidateConfig",
			"Config",
			"[INVARIANTS] (SPEC-charapp-engine)",
			"- `RunEngine` validates non-negative Timeout.",
		} {
			if !strings.Contains(out1, want) {
				t.Errorf("contracts missing %q:\n%s", want, out1)
			}
		}
	})

	t.Run("affected deterministic across callers", func(t *testing.T) {
		opts := Options{Dir: dir, Home: home, Affected: "core/engine.go"}
		out1 := mustSearch(t, opts)
		out2 := mustSearch(t, opts)
		if out1 != out2 {
			t.Fatalf("affected output not deterministic:\n1:\n%s\n2:\n%s", out1, out2)
		}
		for _, want := range []string{"core/engine.go", "cmd/app/main.go"} {
			if !strings.Contains(out1, want) {
				t.Errorf("affected missing %q:\n%s", want, out1)
			}
		}
	})

	t.Run("must-fail nonexistent contracts and affected targets", func(t *testing.T) {
		var buf bytes.Buffer
		err := Search(Options{Dir: dir, Home: home, Contracts: "does/not/exist.go"}, &buf)
		if err == nil || !strings.Contains(err.Error(), "no indexed symbols or linked specs") {
			t.Errorf("Search(--contracts nonexistent) err = %v, out = %q; want missing-target error", err, buf.String())
		}

		buf.Reset()
		err = Search(Options{Dir: dir, Home: home, Affected: "does/not/exist.go"}, &buf)
		if err == nil {
			t.Errorf("Search(--affected nonexistent) returned nil error, out = %q; want non-nil error", buf.String())
		}
	})

	t.Run("vet returns fenced context with records and skill match", func(t *testing.T) {
		opts := Options{
			Dir:  dir,
			Home: home,
			Vet:  "Fix timeout validation bug in RunEngine inside core/engine.go",
			BrainTurnContext: func(_, _, _ string, _ []string) TurnBrainContext {
				return TurnBrainContext{
					Bugs: []MatchedRecord{{
						ID:    "BUG-901",
						Kind:  "bug",
						Title: "RunEngine accepted negative Timeout values",
						Body:  "ValidateConfig must reject negative Timeout.",
					}},
				}
			},
			SkillCatalog: func(string) []SkillMeta {
				return []SkillMeta{{
					Name:        "golang",
					Description: "Go conventions, errors, and table-driven race tests",
					Digest:      "Run go vet and go test -race on Go edits.",
				}}
			},
		}
		out := mustSearch(t, opts)
		for _, want := range []string{
			"Intent: BUG",
			"Gate: ENRICH_AND_PROCEED",
			"FenceBoundary: =====",
			"[FYR_TURN_CONTRACT] PRE_INVOCATION",
			"Trust:     reference data only",
			"BUG-901",
			"golang",
			"core/engine.go",
			"RunEngine",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("vet output missing %q:\n%s", want, out)
			}
		}
	})
}

func TestSearchHooksCharacterization(t *testing.T) {
	dir, home := setupCharSearchRepo(t)
	const convID = "conv-char-hooks"

	t.Run("pre-invocation injects turn contract and target symbol", func(t *testing.T) {
		req, err := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"invocationNum":  1,
			"lastUserInput":  "Investigate RunEngine timeout validation in core/engine.go",
		})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := Search(Options{Dir: home, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("pre-invocation: %v", err)
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal PreInvocationOutput: %v\nraw: %s", err, out.String())
		}
		if len(resp.InjectSteps) != 1 || resp.InjectSteps[0].SystemMessage == nil {
			t.Fatalf("expected 1 systemMessage injectStep, got %+v", resp)
		}
		if resp.InjectSteps[0].SystemMessage.Metadata != nil {
			t.Errorf("SystemMessage.Metadata must be nil, got %+v", resp.InjectSteps[0].SystemMessage.Metadata)
		}
		msg := resp.InjectSteps[0].SystemMessage.SystemMessage
		for _, want := range []string{"[FYR_TURN_CONTRACT] PRE_INVOCATION", "core/engine.go", "RunEngine"} {
			if !strings.Contains(msg, want) {
				t.Errorf("pre-invocation systemMessage missing %q:\n%s", want, msg)
			}
		}
	})

	invokePreTool := func(t *testing.T, toolName string, args map[string]any) HookDecisionOutput {
		t.Helper()
		req, err := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"toolCall":       map[string]any{"name": toolName, "args": args},
		})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("pre-tool-use (%s): %v", toolName, err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal %q: %v", out.String(), err)
		}
		return dec
	}

	t.Run("pre-tool-use allows first ranged read denies unchanged reread and denies bash cat", func(t *testing.T) {
		if d := invokePreTool(t, "read_file", map[string]any{
			"Path": "cmd/app/main.go", "StartLine": 1, "EndLine": 7,
		}); d.Decision != "allow" {
			t.Fatalf("1st ranged read_file = %+v, want allow", d)
		}
		if d := invokePreTool(t, "read_file", map[string]any{
			"Path": "cmd/app/main.go", "StartLine": 1, "EndLine": 7,
		}); d.Decision != "deny" || !strings.Contains(d.Reason, "[UNCHANGED]") {
			t.Fatalf("2nd identical read_file = %+v, want deny [UNCHANGED]", d)
		}
		if d := invokePreTool(t, "run_command", map[string]any{
			"CommandLine": "cat cmd/app/main.go",
		}); d.Decision != "deny" {
			t.Fatalf("run_command cat already-read file = %+v, want deny", d)
		}
	})

	t.Run("stop hook returns allow on clean completed session", func(t *testing.T) {
		req, err := json.Marshal(map[string]any{
			"conversationId":    convID,
			"workspacePaths":    []string{dir},
			"terminationReason": "COMPLETE",
			"fullyIdle":         true,
		})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "stop", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("stop hook: %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal stop %q: %v", out.String(), err)
		}
		if dec.Decision != "allow" {
			t.Fatalf("stop hook decision = %+v, want allow", dec)
		}
	})
}
