package isolate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var transcriptPollInterval = 500 * time.Millisecond

const (
	maxTranscriptRead = 8 << 20
	errorLoopCap      = 3
)

type transcriptStep struct {
	StepIndex int                  `json:"step_index"`
	Status    string               `json:"status"`
	ToolCalls []transcriptToolCall `json:"tool_calls"`
}

type transcriptToolCall struct {
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
	Status string          `json:"status"`
}

type failRun struct {
	sig   string
	count int
}

type transcriptTailer struct {
	sbDir     string
	appDir    string
	onEvent   func(ACPEvent)
	offsets   map[string]int64
	sizes     map[string]int64
	artMtimes map[string]time.Time
	failRuns  map[string]failRun
	stepIdx   int
}

func newTranscriptTailer(sbDir, appDir string, onEvent func(ACPEvent)) *transcriptTailer {
	return &transcriptTailer{
		sbDir:     sbDir,
		appDir:    appDir,
		onEvent:   onEvent,
		offsets:   map[string]int64{},
		sizes:     map[string]int64{},
		artMtimes: map[string]time.Time{},
		failRuns:  map[string]failRun{},
	}
}

// watchTranscript polls transcript.jsonl and .agents/scratch/agents/*.md during
// non-ACP sessions to stream ACPEvent notifications and cancel early on idle
// stalls or >= 3 consecutive identical failing tool calls.
func (b *Box) watchTranscript(ctx context.Context, cancel context.CancelFunc) (stop func()) {
	if b.c.OnEvent == nil && b.c.IdleTimeout <= 0 {
		return func() {}
	}
	tailer := newTranscriptTailer(b.sb, b.app, b.c.OnEvent)
	_ = tailer.pollArtifacts()
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go b.runWatchLoop(ctx, cancel, tailer, done, &wg)
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
			_, _ = tailer.pollOnce()
		})
	}
}

func (b *Box) runWatchLoop(
	ctx context.Context, cancel context.CancelFunc,
	tailer *transcriptTailer, done <-chan struct{}, wg *sync.WaitGroup,
) {
	defer wg.Done()
	ticker := time.NewTicker(watchTickInterval(b.c.IdleTimeout))
	defer ticker.Stop()
	lastProgress := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if reason, progressed := b.watchStep(tailer, lastProgress); reason != "" {
				b.Note("%s", reason)
				if cancel != nil {
					cancel()
				}
				return
			} else if progressed {
				lastProgress = time.Now()
			}
		}
	}
}

func (b *Box) watchStep(tailer *transcriptTailer, lastProgress time.Time) (string, bool) {
	progressed, loopMsg := tailer.pollOnce()
	if loopMsg != "" {
		return loopMsg, progressed
	}
	if progressed {
		return "", true
	}
	if b.c.IdleTimeout > 0 && time.Since(lastProgress) >= b.c.IdleTimeout {
		return fmt.Sprintf("agy idle for %s with no transcript or artifact progress; aborting", b.c.IdleTimeout), false
	}
	return "", false
}

func watchTickInterval(idle time.Duration) time.Duration {
	d := transcriptPollInterval
	if idle > 0 && idle/2 < d {
		d = max(10*time.Millisecond, idle/2)
	}
	return d
}

func (t *transcriptTailer) pollOnce() (bool, string) {
	progressed := t.pollArtifacts()
	var loopMsg string
	root, err := os.OpenRoot(t.sbDir)
	if err != nil {
		return progressed, ""
	}
	defer root.Close()
	brainRel := filepath.Join(".gemini", "antigravity-cli", "brain")
	dir, err := root.Open(brainRel)
	if err != nil {
		return progressed, ""
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return progressed, ""
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		convID := ent.Name()
		rel := filepath.Join(brainRel, convID, ".system_generated", "logs", "transcript.jsonl")
		grew, msg := t.readTranscript(root, convID, rel)
		if grew {
			progressed = true
		}
		if msg != "" && loopMsg == "" {
			loopMsg = msg
		}
	}
	return progressed, loopMsg
}

func (t *transcriptTailer) pollArtifacts() bool {
	if t.appDir == "" {
		return false
	}
	root, err := os.OpenRoot(t.appDir)
	if err != nil {
		return false
	}
	defer root.Close()
	relDir := filepath.Join(".agents", "scratch", "agents")
	dir, err := root.Open(relDir)
	if err != nil {
		return false
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return false
	}
	changed := false
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
			continue
		}
		fi, err := root.Lstat(filepath.Join(relDir, ent.Name()))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		mt := fi.ModTime()
		if prev, seen := t.artMtimes[ent.Name()]; !seen || mt.After(prev) {
			t.artMtimes[ent.Name()] = mt
			changed = true
		}
	}
	return changed
}

func (t *transcriptTailer) readTranscript(root *os.Root, convID, rel string) (bool, string) {
	fi, err := root.Lstat(rel)
	if err != nil || !fi.Mode().IsRegular() {
		return false, ""
	}
	grew := fi.Size() > t.sizes[convID]
	if grew {
		t.sizes[convID] = fi.Size()
	}
	off := t.offsets[convID]
	if fi.Size() <= off {
		return grew, ""
	}
	complete, ok := readTranscriptChunk(root, rel, off, t.offsets, convID)
	if !ok {
		return grew, ""
	}
	var loopMsg string
	for _, line := range bytes.Split(complete, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if msg := t.emitLine(convID, line); msg != "" && loopMsg == "" {
			loopMsg = msg
		}
	}
	return grew, loopMsg
}

func readTranscriptChunk(root *os.Root, rel string, off int64, offsets map[string]int64, convID string) ([]byte, bool) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, false
	}
	if off > 0 {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return nil, false
		}
	}
	buf, err := io.ReadAll(io.LimitReader(f, maxTranscriptRead))
	if err != nil || len(buf) == 0 {
		return nil, false
	}
	lastNL := bytes.LastIndexByte(buf, '\n')
	if lastNL < 0 {
		if len(buf) == maxTranscriptRead {
			offsets[convID] = off + int64(len(buf))
		}
		return nil, false
	}
	complete := buf[:lastNL+1]
	offsets[convID] = off + int64(len(complete))
	return complete, true
}

func (t *transcriptTailer) emitLine(convID string, raw []byte) string {
	var step transcriptStep
	if err := json.Unmarshal(raw, &step); err != nil || len(step.ToolCalls) == 0 {
		return ""
	}
	stepStatus := strings.ToLower(strings.TrimSpace(step.Status))
	var loopMsg string
	for _, tc := range step.ToolCalls {
		if tc.Name == "" {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(tc.Status))
		if status == "" {
			status = stepStatus
		}
		if status == "" {
			status = "done"
		}
		t.stepIdx++
		if t.onEvent != nil {
			t.onEvent(buildToolEvent(convID, t.stepIdx, acpUpdateDetail{
				SessionUpdate: "tool_call",
				Name:          tc.Name,
				Status:        status,
				RawInput:      tc.Args,
			}))
		}
		if msg := t.trackToolStatus(convID, tc.Name, tc.Args, status); msg != "" && loopMsg == "" {
			loopMsg = msg
		}
	}
	return loopMsg
}

func (t *transcriptTailer) trackToolStatus(convID, name string, args json.RawMessage, status string) string {
	if status != "error" && status != "failed" {
		delete(t.failRuns, convID)
		return ""
	}
	sig := name + "\x00" + canonicalToolArgs(args)
	run := t.failRuns[convID]
	if run.sig == sig {
		run.count++
	} else {
		run = failRun{sig: sig, count: 1}
	}
	t.failRuns[convID] = run
	if run.count >= errorLoopCap {
		return fmt.Sprintf("agy stuck in error loop (%s failed %d consecutive times); aborting", name, run.count)
	}
	return ""
}

func canonicalToolArgs(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var v any
	if err := json.Unmarshal(trimmed, &v); err == nil {
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	}
	var buf bytes.Buffer
	if json.Compact(&buf, trimmed) == nil {
		return buf.String()
	}
	return string(trimmed)
}
