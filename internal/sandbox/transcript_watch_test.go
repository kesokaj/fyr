package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWatchTranscriptNilCallback(t *testing.T) {
	b := &Box{sb: t.TempDir()}
	stop := b.watchTranscript(context.Background())
	stop()
}

func TestWatchTranscriptEmitsToolAndSubagentEvents(t *testing.T) {
	oldInterval := transcriptPollInterval
	transcriptPollInterval = 15 * time.Millisecond
	defer func() { transcriptPollInterval = oldInterval }()

	sb := t.TempDir()
	logDir := filepath.Join(sb, ".gemini", "antigravity-cli", "brain", "conv-1234", ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(logDir, "transcript.jsonl")

	var mu sync.Mutex
	var events []ACPEvent
	b := &Box{
		sb: sb,
		c: Config{
			OnEvent: func(ev ACPEvent) {
				mu.Lock()
				events = append(events, ev)
				mu.Unlock()
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := b.watchTranscript(ctx)

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Write line 1 (tool_call with doubly-quoted string in args) + partial line 2 (no newline yet).
	line1 := `{"step_index":1,"status":"DONE","tool_calls":[{"name":"run_command","args":{"CommandLine":"\"go test ./...\""}}]}` + "\n"
	line2Part1 := `{"step_index":2,"status":"DONE","tool_calls":[{"name":"invoke_subagent",`
	if _, err := f.WriteString(line1 + line2Part1); err != nil {
		t.Fatal(err)
	}

	// Wait for line 1 to be picked up while partial line 2 waits for its newline.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Complete line 2 and stop immediately to verify stop()'s final flush.
	line2Part2 := `"args":{"TypeName":"\"reviewer\"","Role":"\"Code Reviewer\""}}]}` + "\n"
	if _, err := f.WriteString(line2Part2); err != nil {
		t.Fatal(err)
	}
	stop()

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("got %d events %+v, want 2", len(events), events)
	}
	if events[0].Kind != "tool_call" || events[0].ToolName != "run_command" || events[0].Summary != "go test ./..." || events[0].StepIndex != 1 {
		t.Errorf("events[0] = %+v", events[0])
	}
	if events[1].Kind != "subagent" || events[1].ToolName != "invoke_subagent" || events[1].Summary != "reviewer (Code Reviewer)" || events[1].StepIndex != 2 {
		t.Errorf("events[1] = %+v", events[1])
	}
}

func TestWatchTranscriptSkipsSymlink(t *testing.T) {
	sb := t.TempDir()
	outsideDir := t.TempDir()
	realFile := filepath.Join(outsideDir, "transcript.jsonl")
	if err := os.WriteFile(realFile, []byte(`{"step_index":1,"status":"DONE","tool_calls":[{"name":"read_file","args":{"Path":"x.go"}}]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Case 1: leaf transcript.jsonl is a symlink to outsideFile.
	logDir := filepath.Join(sb, ".gemini", "antigravity-cli", "brain", "conv-sym", ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realFile, filepath.Join(logDir, "transcript.jsonl")); err != nil {
		t.Fatal(err)
	}
	// Case 2: intermediate directory (logs) is a symlink to outsideDir.
	sysDir := filepath.Join(sb, ".gemini", "antigravity-cli", "brain", "conv-dir-sym", ".system_generated")
	if err := os.MkdirAll(sysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(sysDir, "logs")); err != nil {
		t.Fatal(err)
	}

	var got []ACPEvent
	tailer := &transcriptTailer{
		sbDir:   sb,
		onEvent: func(ev ACPEvent) { got = append(got, ev) },
		offsets: map[string]int64{},
	}
	tailer.pollOnce()
	if len(got) != 0 {
		t.Fatalf("symlink transcript emitted events: %+v", got)
	}
}
