package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/isolate/acp"
)

func TestWatchTranscriptIdleAndErrorLoop(t *testing.T) {
	t.Parallel()

	t.Run("idle_timeout_cancels", func(t *testing.T) {
		t.Parallel()
		sbHome := t.TempDir()
		appDir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var mu sync.Mutex
		var note string
		stop := WatchTranscript(ctx, cancel, TranscriptWatchConfig{
			SandboxHome:  sbHome,
			AppDir:       appDir,
			IdleTimeout:  80 * time.Millisecond,
			PollInterval: 15 * time.Millisecond,
			Note: func(format string, a ...any) {
				mu.Lock()
				note = fmt.Sprintf(format, a...)
				mu.Unlock()
			},
		})
		defer stop()
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("idle timeout did not cancel context")
		}
		mu.Lock()
		got := note
		mu.Unlock()
		if !strings.Contains(strings.ToLower(got), "idle") {
			t.Errorf("note = %q, want idle message", got)
		}
	})

	t.Run("error_loop_emits_events_and_cancels", func(t *testing.T) {
		t.Parallel()
		sbHome := t.TempDir()
		appDir := t.TempDir()
		logPath := filepath.Join(sbHome, ".gemini", "antigravity-cli", "brain", "conv-1", ".system_generated", "logs", "transcript.jsonl")
		content := strings.Join([]string{
			`{"step_index":1,"status":"error","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test","Wait":5}}]}`,
			`{"step_index":2,"status":"failed","tool_calls":[{"name":"run_command","args":{"Wait":5,"CommandLine":"go test"}}]}`,
			`{"step_index":3,"status":"ERROR","tool_calls":[{"name":"run_command","args":{"CommandLine": "go test", "Wait": 5}}]}`,
		}, "\n") + "\n"
		writeTestFile(t, logPath, content, 0o600)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var mu sync.Mutex
		var events []acp.Event
		var note string
		stop := WatchTranscript(ctx, cancel, TranscriptWatchConfig{
			SandboxHome:  sbHome,
			AppDir:       appDir,
			PollInterval: 15 * time.Millisecond,
			OnEvent: func(ev acp.Event) {
				mu.Lock()
				events = append(events, ev)
				mu.Unlock()
			},
			Note: func(format string, a ...any) {
				mu.Lock()
				note = fmt.Sprintf(format, a...)
				mu.Unlock()
			},
		})
		defer stop()
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("error loop did not cancel context")
		}
		mu.Lock()
		evCount, gotNote := len(events), note
		mu.Unlock()
		if evCount < 3 || !strings.Contains(strings.ToLower(gotNote), "error loop") {
			t.Fatalf("evCount=%d note=%q, want >=3 events and error loop note", evCount, gotNote)
		}
	})

	t.Run("interleaved_success_resets_streak_and_artifact_progress", func(t *testing.T) {
		t.Parallel()
		sbHome := t.TempDir()
		appDir := t.TempDir()
		artPath := filepath.Join(appDir, ".agents", "scratch", "agents", "reviewer.md")
		writeTestFile(t, artPath, "v1\n", 0o600)

		tailer := newTranscriptTailer(sbHome, appDir, nil)
		if !tailer.pollArtifacts() {
			t.Fatal("initial pollArtifacts = false, want true")
		}
		if tailer.pollArtifacts() {
			t.Fatal("unchanged pollArtifacts = true, want false")
		}
		future := time.Now().Add(2 * time.Second)
		if err := os.Chtimes(artPath, future, future); err != nil {
			t.Fatal(err)
		}
		if !tailer.pollArtifacts() {
			t.Fatal("updated artifact pollArtifacts = false, want true")
		}

		logPath := filepath.Join(sbHome, ".gemini", "antigravity-cli", "brain", "conv-2", ".system_generated", "logs", "transcript.jsonl")
		lines := strings.Join([]string{
			`{"step_index":1,"status":"error","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test"}}]}`,
			`{"step_index":2,"status":"error","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test"}}]}`,
			`{"step_index":3,"status":"done","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test"}}]}`,
			`{"step_index":4,"status":"error","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test"}}]}`,
			`{"step_index":5,"status":"error","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test"}}]}`,
		}, "\n") + "\n"
		writeTestFile(t, logPath, lines, 0o600)
		progressed, loopMsg := tailer.pollOnce()
		if !progressed || loopMsg != "" {
			t.Fatalf("pollOnce = (progressed=%v, loopMsg=%q), want (true, empty)", progressed, loopMsg)
		}
	})
}

func TestCanonicalToolArgs(t *testing.T) {
	t.Parallel()
	if got := canonicalToolArgs(json.RawMessage(` null `)); got != "" {
		t.Errorf("null = %q, want empty", got)
	}
	a := canonicalToolArgs(json.RawMessage(`{"b":2, "a":1}`))
	b := canonicalToolArgs(json.RawMessage(`{"a":1,"b":2}`))
	if a != b || a != `{"a":1,"b":2}` {
		t.Errorf("canonicalToolArgs mismatch: a=%q b=%q", a, b)
	}
}
