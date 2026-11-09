package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var transcriptPollInterval = 500 * time.Millisecond

const maxTranscriptRead = 8 << 20

type transcriptStep struct {
	StepIndex int                  `json:"step_index"`
	Status    string               `json:"status"`
	ToolCalls []transcriptToolCall `json:"tool_calls"`
}

type transcriptToolCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type transcriptTailer struct {
	sbDir   string
	onEvent func(ACPEvent)
	offsets map[string]int64
	stepIdx int
}

// watchTranscript polls `<b.sb>/.gemini/antigravity-cli/brain/*/.system_generated/logs/transcript.jsonl`
// every 500 ms during non-ACP Docker and Host runs and emits ACPEvent notifications
// to b.c.OnEvent (ADR-513, D7).
func (b *Box) watchTranscript(ctx context.Context) (stop func()) {
	if b.c.OnEvent == nil {
		return func() {}
	}
	tailer := &transcriptTailer{
		sbDir:   b.sb,
		onEvent: b.c.OnEvent,
		offsets: map[string]int64{},
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(transcriptPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				tailer.pollOnce()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
			tailer.pollOnce()
		})
	}
}

func (t *transcriptTailer) pollOnce() {
	root, err := os.OpenRoot(t.sbDir)
	if err != nil {
		return
	}
	defer root.Close()
	brainRel := filepath.Join(".gemini", "antigravity-cli", "brain")
	dir, err := root.Open(brainRel)
	if err != nil {
		return
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		convID := ent.Name()
		rel := filepath.Join(brainRel, convID, ".system_generated", "logs", "transcript.jsonl")
		t.readTranscript(root, convID, rel)
	}
}

func (t *transcriptTailer) readTranscript(root *os.Root, convID, rel string) {
	fi, err := root.Lstat(rel)
	if err != nil || !fi.Mode().IsRegular() {
		return
	}
	off := t.offsets[convID]
	if fi.Size() <= off {
		return
	}
	f, err := root.Open(rel)
	if err != nil {
		return
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return
	}
	if off > 0 {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return
		}
	}
	buf, err := io.ReadAll(io.LimitReader(f, maxTranscriptRead))
	if err != nil || len(buf) == 0 {
		return
	}
	lastNL := bytes.LastIndexByte(buf, '\n')
	if lastNL < 0 {
		if len(buf) == maxTranscriptRead {
			t.offsets[convID] = off + int64(len(buf))
		}
		return
	}
	complete := buf[:lastNL+1]
	t.offsets[convID] = off + int64(len(complete))
	for _, line := range bytes.Split(complete, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) > 0 {
			t.emitLine(convID, line)
		}
	}
}

func (t *transcriptTailer) emitLine(convID string, raw []byte) {
	var step transcriptStep
	if err := json.Unmarshal(raw, &step); err != nil || len(step.ToolCalls) == 0 {
		return
	}
	status := strings.ToLower(step.Status)
	if status == "" {
		status = "done"
	}
	for _, tc := range step.ToolCalls {
		if tc.Name == "" {
			continue
		}
		t.stepIdx++
		ev := buildToolEvent(convID, t.stepIdx, acpUpdateDetail{
			SessionUpdate: "tool_call",
			Name:          tc.Name,
			Status:        status,
			RawInput:      tc.Args,
		})
		t.onEvent(ev)
	}
}
