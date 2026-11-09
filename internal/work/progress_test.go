package work

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

func TestProgressTrackerAndStatusBoard(t *testing.T) {
	home := outsideTemp(t)
	root := outsideTemp(t)

	// Empty status board before any wave
	board, err := FormatStatusBoard(home)
	if err != nil {
		t.Fatalf("FormatStatusBoard empty: %v", err)
	}
	if !strings.Contains(board, "no active or recent waves") {
		t.Fatalf("unexpected empty board: %q", board)
	}

	var prog bytes.Buffer
	statusMD := filepath.Join(outsideTemp(t), "work-wave.md")
	fixedNow := time.Date(2026, 11, 8, 21, 0, 0, 0, time.UTC)
	o := Options{
		Home:     home,
		Root:     root,
		Model:    "gemini-3-flash",
		Progress: &prog,
		StatusMD: statusMD,
		Now:      func() time.Time { return fixedNow },
	}
	stamp := "2026-11-08-210000-1234"
	ps := []prepared{
		{k: Kickoff{Name: "p1", Model: "gemini-4-argon-high", Files: []string{"internal/search/hook.go"}}},
		{k: Kickoff{Name: "p2", Files: []string{"internal/eval/suite.go"}}},
	}

	tr := newProgressTracker(o, stamp, ps)
	tr.onStart("p1")
	fixedNow = fixedNow.Add(15 * time.Second)
	tr.onEvent("p1", sandbox.ACPEvent{
		Kind:      "tool_call",
		StepIndex: 1,
		ToolName:  "read_file",
		Summary:   "internal/search/hook.go",
		Status:    "completed",
	})
	fixedNow = fixedNow.Add(30 * time.Second)
	tr.onEvent("p1", sandbox.ACPEvent{
		Kind:      "subagent",
		StepIndex: 2,
		ToolName:  "invoke_subagent",
		Summary:   "verifier (Test Runner)",
		Status:    "running",
	})
	tr.onFinish("p1", Result{
		Name:    "p1",
		Verdict: "PASS",
		Seconds: 75,
		Commits: 2,
	})

	lines := prog.String()
	for _, want := range []string{
		"work: [p1] ▶ started  model gemini-4-argon-high  branch work/p1  files: internal/search/hook.go",
		"work: [p1] ⚙ step 1 (00:15)  read_file internal/search/hook.go",
		"work: [p1] 🤖 step 2 (00:45)  subagent verifier (Test Runner)",
		"work: [p1] ✓ finished PASS (01:15, 2 steps, 2 commits)",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("progress output missing %q:\n%s", want, lines)
		}
	}

	statusPath := filepath.Join(sandbox.Root(home, "work"), stamp, "status.json")
	raw, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("reading status.json: %v", err)
	}
	var wave WaveStatus
	if err := json.Unmarshal(raw, &wave); err != nil {
		t.Fatalf("unmarshaling status.json: %v", err)
	}
	if wave.Stamp != stamp || len(wave.Workers) != 2 {
		t.Fatalf("unexpected wave: %+v", wave)
	}
	if wave.Workers[0].State != "PASS" || wave.Workers[0].StepCount != 2 || wave.Workers[0].Commits != 2 {
		t.Fatalf("unexpected worker[0]: %+v", wave.Workers[0])
	}
	if len(wave.Workers[0].Subagents) != 1 || wave.Workers[0].Subagents[0] != "verifier (Test Runner)" {
		t.Fatalf("unexpected subagents: %+v", wave.Workers[0].Subagents)
	}
	if wave.Workers[1].State != "queued" || wave.Workers[1].Model != "gemini-3-flash" {
		t.Fatalf("unexpected worker[1]: %+v", wave.Workers[1])
	}

	board, err = FormatStatusBoard(home)
	if err != nil {
		t.Fatalf("FormatStatusBoard: %v", err)
	}
	for _, want := range []string{
		"work: stamp " + stamp,
		"WORKER\tSTATE\tELAPSED\tSTEPS\tCOMMITS\tMODEL\tLAST STEP",
		"p1\tPASS\t01:15\t2\t2\tgemini-4-argon-high\tverifier (Test Runner)",
		"p2\tqueued\t00:00\t0\t0\tgemini-3-flash\t-",
	} {
		if !strings.Contains(board, want) {
			t.Errorf("status board missing %q:\n%s", want, board)
		}
	}

	mdBytes, err := os.ReadFile(statusMD)
	if err != nil {
		t.Fatalf("reading statusMD: %v", err)
	}
	md := string(mdBytes)
	for _, want := range []string{
		"# fyr work — wave " + stamp,
		"```mermaid\nflowchart LR",
		`wave --> w0["p1 (PASS)"]`,
		`w0 --> w0_s0["subagent: verifier (Test Runner)"]`,
		"| `p1` | `PASS` | `gemini-4-argon-high` | `work/p1` | `01:15` | 2 | 2 |",
		"````carousel",
		"<!-- slide -->",
		"### Worker `p1` (`PASS`)",
		"### Worker `p2` (`queued`)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("statusMD missing %q:\n%s", want, md)
		}
	}

	// Symlink target is refused and never overwritten.
	symDir := outsideTemp(t)
	victim := filepath.Join(symDir, "victim.md")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	symPath := filepath.Join(symDir, "link.md")
	if err := os.Symlink(victim, symPath); err != nil {
		t.Fatal(err)
	}
	writeStatusMD(symPath, wave)
	if got, _ := os.ReadFile(victim); string(got) != "keep" {
		t.Fatalf("symlink victim was overwritten: %q", string(got))
	}
}
