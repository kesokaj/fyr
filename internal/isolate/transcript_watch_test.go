package isolate

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTranscriptWatchAbortsIdleAndErrorLoop(t *testing.T) {
	oldInterval := transcriptPollInterval
	transcriptPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { transcriptPollInterval = oldInterval })

	tok := func(r *rig) string { return filepath.Join(r.real, TokenRel) }

	t.Run("idle_timeout_aborts_early", func(t *testing.T) {
		r := newRig(t)
		agy := filepath.Join(r.bin, "agy-idle")
		put(t, agy, "#!/bin/bash\nsleep 30\n", 0o755)

		c := r.config("idle-abort")
		c.Timeout = 10 * time.Second
		c.IdleTimeout = 120 * time.Millisecond
		b := r.prepared(c)

		start := time.Now()
		rc := b.Session(context.Background(), agy, tok(r), "p", "out.txt")
		elapsed := time.Since(start)
		if rc == 0 {
			t.Fatalf("rc = %d, want non-zero on idle abort", rc)
		}
		if elapsed > 3*time.Second {
			t.Fatalf("idle abort took %v, expected < 3s", elapsed)
		}
		if b.TimedOut() {
			t.Errorf("TimedOut() = true, want false for idle abort")
		}
		if !strings.Contains(strings.ToLower(r.out.String()), "idle") {
			t.Errorf("expected idle abort note, got:\n%s", r.out.String())
		}
	})

	t.Run("artifact_and_transcript_progress_keeps_alive", func(t *testing.T) {
		r := newRig(t)
		agy := filepath.Join(r.bin, "agy-progress")
		script := `#!/bin/bash
mkdir -p "$PWD/.agents/scratch/agents" "$HOME/.gemini/antigravity-cli/brain/conv-ok/.system_generated/logs"
for i in 1 2 3 4 5; do
  echo "update $i" >> "$PWD/.agents/scratch/agents/reviewer.md"
  sleep 0.06
done
printf '{"step_index":1,"status":"done","tool_calls":[{"name":"read_file","args":{"Path":"a.go"}}]}\n' >> "$HOME/.gemini/antigravity-cli/brain/conv-ok/.system_generated/logs/transcript.jsonl"
sleep 0.05
`
		put(t, agy, script, 0o755)

		c := r.config("keep-alive")
		c.Timeout = 5 * time.Second
		c.IdleTimeout = 150 * time.Millisecond
		c.TestUnconfined = true
		b := r.prepared(c)

		start := time.Now()
		if rc := b.Session(context.Background(), agy, tok(r), "p", "out.txt"); rc != 0 {
			t.Fatalf("rc = %d after %v, want 0 while progress continues\n%s", rc, time.Since(start), r.out.String())
		}
	})

	t.Run("error_loop_aborts_after_three_identical_failures", func(t *testing.T) {
		r := newRig(t)
		agy := filepath.Join(r.bin, "agy-loop")
		script := `#!/bin/bash
logdir="$HOME/.gemini/antigravity-cli/brain/conv-loop/.system_generated/logs"
mkdir -p "$logdir"
printf '{"step_index":1,"status":"error","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test","Wait":5}}]}\n' >> "$logdir/transcript.jsonl"
printf '{"step_index":2,"status":"failed","tool_calls":[{"name":"run_command","args":{"Wait":5,"CommandLine":"go test"}}]}\n' >> "$logdir/transcript.jsonl"
printf '{"step_index":3,"status":"ERROR","tool_calls":[{"name":"run_command","args":{"CommandLine": "go test", "Wait": 5}}]}\n' >> "$logdir/transcript.jsonl"
sleep 30
`
		put(t, agy, script, 0o755)

		var mu sync.Mutex
		var events []ACPEvent
		c := r.config("error-loop")
		c.Timeout = 10 * time.Second
		c.OnEvent = func(ev ACPEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}
		b := r.prepared(c)

		start := time.Now()
		rc := b.Session(context.Background(), agy, tok(r), "p", "out.txt")
		elapsed := time.Since(start)
		if rc == 0 {
			t.Fatalf("rc = %d, want non-zero on error loop", rc)
		}
		if elapsed > 3*time.Second {
			t.Fatalf("error loop abort took %v, expected < 3s", elapsed)
		}
		if !strings.Contains(strings.ToLower(r.out.String()), "error loop") {
			t.Errorf("expected error loop note, got:\n%s", r.out.String())
		}
		mu.Lock()
		evCount := len(events)
		mu.Unlock()
		if evCount < 3 {
			t.Errorf("OnEvent saw %d events, want >= 3", evCount)
		}
	})

	t.Run("interleaved_success_resets_error_streak", func(t *testing.T) {
		r := newRig(t)
		agy := filepath.Join(r.bin, "agy-reset")
		script := `#!/bin/bash
logdir="$HOME/.gemini/antigravity-cli/brain/conv-reset/.system_generated/logs"
mkdir -p "$logdir"
printf '{"step_index":1,"status":"error","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test"}}]}\n' >> "$logdir/transcript.jsonl"
printf '{"step_index":2,"status":"error","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test"}}]}\n' >> "$logdir/transcript.jsonl"
printf '{"step_index":3,"status":"done","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test"}}]}\n' >> "$logdir/transcript.jsonl"
printf '{"step_index":4,"status":"error","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test"}}]}\n' >> "$logdir/transcript.jsonl"
printf '{"step_index":5,"status":"error","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test"}}]}\n' >> "$logdir/transcript.jsonl"
sleep 0.06
`
		put(t, agy, script, 0o755)

		c := r.config("reset-streak")
		c.Timeout = 5 * time.Second
		c.OnEvent = func(ACPEvent) {}
		b := r.prepared(c)

		if rc := b.Session(context.Background(), agy, tok(r), "p", "out.txt"); rc != 0 {
			t.Fatalf("rc = %d, want 0 when error streak is reset\n%s", rc, r.out.String())
		}
	})
}
