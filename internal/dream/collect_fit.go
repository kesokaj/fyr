package dream

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/trail"
)

const (
	minTranscriptChunkRoom = 512
	leftOutSlackReserve    = 128
	minSIMTrimText         = 1024
	minTrimTranscriptBytes = 4096
)

// datedKinds are the kinds that carry news. Over the cap they are dropped
// together, newest first, and two inputs of the same time in this order
// (ADR-069).
var datedKinds = []string{kindTranscript, kindTrail, kindFeedback}

// fit drops inputs until the prompt is under limit: the metrics summary,
// then the dated kinds newest first, then SIM optimizer memory, then skills
// (D21, ADR-055a, ADR-069, ADR-533).
// What a capped run sends is the oldest news, and Through is set just
// before the oldest dated input dropped, so the next run offers it again.
// A drop holds Through only while an input strictly older than it is kept:
// a dated input too big to be sent beside memory and the skills (choices
// D33), and one of the oldest time left, are dropped for good and named,
// since holding the watermark for either would stop it there on every run.
// A project whose memory alone is over the limit fails.
//
// The prompt's size is counted from its parts once and then kept by
// subtracting each dropped block and re-measuring the short left-out line,
// never by rendering the prompt again (audit 2026-10-25, SD-S1).
func (c *Collected) fit(limit int) error {
	return newFitter(c, limit).run()
}

func (f *fitter) run() error {
	f.dropKind(kindMetrics)
	for f.over() {
		i, how := f.datedVictim()
		if i < 0 {
			break
		}
		f.dropDated(i, how)
	}
	f.dropKind(kindSIM)
	f.dropKind(kindSkill)
	if f.over() {
		return errors.New("memory over cap")
	}
	return nil
}

// fitter is one fit's running count: each input's block size, the prompt's
// size, and room, what the cap leaves after the head, memory and skills.
// over is the size check; a test measures by rendering instead.
type fitter struct {
	c           *Collected
	limit, size int
	room        int
	sizes       []int
	over        func() bool
}

func newFitter(c *Collected, limit int) *fitter {
	f := &fitter{c: c, limit: limit, sizes: make([]int, len(c.Inputs))}
	f.over = func() bool { return f.size > f.limit }
	f.size = len(head(*c)) + len(leftOut(c.Dropped))
	f.room = limit - len(head(*c)) - leftOutSlackReserve
	for i, in := range c.Inputs {
		f.sizes[i] = len(block(in))
		f.size += f.sizes[i]
		if in.Kind == kindMemory || in.Kind == kindSkill || in.Kind == kindSIM {
			f.room -= f.sizes[i]
		}
	}
	return f
}

// dropKind drops inputs of an undated kind while the prompt is over the cap.
func (f *fitter) dropKind(kind string) {
	for f.over() {
		i := -1
		for j, in := range f.c.Inputs {
			if in.Kind == kind {
				i = j
				break
			}
		}
		if i < 0 {
			return
		}
		f.drop(i, fmt.Sprintf("dropped to fit the %d KB cap", f.limit/1024))
	}
}

// How a dated input is dropped.
const (
	heldBack   = iota // offered again: Through stops just before it
	tooBig            // too big to send beside memory and the skills
	oldestLeft        // of the oldest time left: nothing older is kept
)

// datedVictim is the next dated input to drop, or -1, and how: the newest
// too big for room, else the newest, held back only when an input strictly
// older than it stays. A time watermark cannot split a group of inputs of
// one time, so the oldest group is never held back (code review, Phase 4).
func (f *fitter) datedVictim() (i, how int) {
	newest, big := -1, -1
	var oldest time.Time
	for j, in := range f.c.Inputs {
		if datedRank(in.Kind) < 0 {
			continue
		}
		if !in.Time.IsZero() && (oldest.IsZero() || in.Time.Before(oldest)) {
			oldest = in.Time
		}
		newest = f.newerOf(j, newest)
		if f.sizes[j] > f.room {
			big = f.newerOf(j, big)
		}
	}
	if big >= 0 {
		return big, tooBig
	}
	if newest >= 0 && !f.c.Inputs[newest].Time.IsZero() && f.c.Inputs[newest].Time.After(oldest) {
		return newest, heldBack
	}
	return newest, oldestLeft
}

// newerOf is j when input j drops before input best (or best is -1), else
// best.
func (f *fitter) newerOf(j, best int) int {
	if best < 0 || newer(f.c.Inputs[j], f.c.Inputs[best]) {
		return j
	}
	return best
}

// dropDated drops dated input i; one held back moves Through to just
// before it.
func (f *fitter) dropDated(i, how int) {
	in := f.c.Inputs[i]
	switch how {
	case tooBig:
		if f.chunkOversized(i) {
			return
		}
		f.drop(i, fmt.Sprintf("too big for the %d KB cap beside memory and skills", f.limit/1024))
	case oldestLeft:
		if in.Kind == kindTranscript && len(in.Text) >= minTrimTranscriptBytes && f.over() {
			need := f.size - f.limit
			if f.trimSIMBlock(need, need) && !f.over() {
				return
			}
		}
		f.drop(i, fmt.Sprintf("dropped to fit the %d KB cap; not sent again", f.limit/1024))
	default:
		f.drop(i, fmt.Sprintf("dropped to fit the %d KB cap", f.limit/1024))
		if t := in.Time.Add(-time.Nanosecond); f.c.Through.IsZero() || t.Before(f.c.Through) {
			f.c.Through = t
		}
	}
}

// drop removes input i and names it, as `<kind> <label> (<why>)`, on the
// left-out line.
func (f *fitter) drop(i int, why string) {
	in := f.c.Inputs[i]
	before := len(leftOut(f.c.Dropped))
	f.c.Dropped = append(f.c.Dropped, fmt.Sprintf("%s %s (%s)", in.Kind, in.Label, why))
	f.size += len(leftOut(f.c.Dropped)) - before - f.sizes[i]
	if in.Kind == kindSIM || in.Kind == kindSkill {
		f.room += f.sizes[i]
	}
	f.c.Inputs = append(f.c.Inputs[:i], f.c.Inputs[i+1:]...)
	f.sizes = append(f.sizes[:i], f.sizes[i+1:]...)
}

// datedRank is kind's place in datedKinds, or -1 for an undated kind.
func datedRank(kind string) int {
	for i, k := range datedKinds {
		if k == kind {
			return i
		}
	}
	return -1
}

// newer reports whether a is dropped before b: later; of the same time,
// earlier in datedKinds; of the same kind too, the later label, so a rerun
// over the same inputs drops the same ones.
func newer(a, b Input) bool {
	if a.Time.IsZero() != b.Time.IsZero() {
		return a.Time.IsZero()
	}
	if !a.Time.Equal(b.Time) {
		return a.Time.After(b.Time)
	}
	if ra, rb := datedRank(a.Kind), datedRank(b.Kind); ra != rb {
		return ra < rb
	}
	return a.Label > b.Label
}

// watermark is what a successful run stores for its project: Through when
// the cap held it back, else the run's start (ADR-069).
func watermark(c Collected, t0 time.Time) time.Time {
	if !c.Through.IsZero() && c.Through.Before(t0) {
		return c.Through
	}
	return t0
}

// chunkOversized compacts an oversized transcript into bounded Head +
// Decision/Outcome + Tail windows inside f.room, falling back to trimSIMBlock
// when kindSIM consumes the transcript's room, and returning false only when
// f.room cannot hold even a minimal window.
func (f *fitter) chunkOversized(i int) bool {
	in := f.c.Inputs[i]
	if in.Kind != kindTranscript {
		return false
	}
	avail := f.room - len(leftOut(f.c.Dropped))
	if len(in.Text) >= minTrimTranscriptBytes && (f.room < minTranscriptChunkRoom || avail < minTranscriptChunkRoom) {
		minNeed := max(minTranscriptChunkRoom-f.room, minTranscriptChunkRoom-avail)
		if f.trimSIMBlock(minNeed+leftOutSlackReserve, minNeed) {
			avail = f.room - len(leftOut(f.c.Dropped))
		}
	}
	if f.room < minTranscriptChunkRoom || avail < minTranscriptChunkRoom {
		return false
	}
	wrap := len(block(Input{Kind: in.Kind, Label: in.Label, Time: in.Time}))
	budget := avail - wrap - 32
	if budget < 256 {
		return false
	}
	cand := in
	cand.Text = chunkTranscriptWindows(in.Text, budget)
	newSize := len(block(cand))
	if newSize > avail {
		shrunk := budget - (newSize - avail) - 32
		if shrunk < 128 {
			return false
		}
		cand.Text = chunkTranscriptWindows(in.Text, shrunk)
		newSize = len(block(cand))
	}
	if newSize > avail || newSize > f.room || newSize >= f.sizes[i] {
		return false
	}
	f.c.Inputs[i] = cand
	f.size += newSize - f.sizes[i]
	f.sizes[i] = newSize
	return true
}

// trimSIMBlock trims the kindSIM input by at least need bytes (down to a
// bounded minimum prefix) to preserve room for transcripts instead of dropping
// them. When trimming down to minSIMTrimText cannot free at least minFreed
// bytes, kindSIM is left untouched and trimSIMBlock returns false.
func (f *fitter) trimSIMBlock(need, minFreed int) bool {
	if need <= 0 {
		return false
	}
	idx := -1
	for i, in := range f.c.Inputs {
		if in.Kind == kindSIM {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	sim := f.c.Inputs[idx]
	if len(sim.Text) <= minSIMTrimText {
		return false
	}
	target := len(sim.Text) - need - 64
	if target < minSIMTrimText {
		target = minSIMTrimText
	}
	kept := trail.TruncateUTF8(strings.TrimRight(sim.Text, "\n"), target)
	dropped := max(1, len(sim.Text)-len(kept))
	sim.Text = fmt.Sprintf("%s\n[... trimmed %d bytes ...]", kept, dropped)
	newSize := len(block(sim))
	if newSize >= f.sizes[idx] {
		return false
	}
	freed := f.sizes[idx] - newSize
	if freed < minFreed {
		return false
	}
	f.c.Inputs[idx] = sim
	f.sizes[idx] = newSize
	f.size -= freed
	f.room += freed
	return true
}

func chunkTranscriptWindows(raw string, budget int) string {
	raw = strings.TrimRight(raw, "\n")
	if len(raw) <= budget {
		return raw
	}
	markerReserve := 96
	usable := max(96, budget-markerReserve)
	headLen := usable * 35 / 100
	midLen := usable * 30 / 100
	tailLen := usable - headLen - midLen
	headPart := trail.TruncateUTF8(raw, headLen)
	tailPart := sliceUTF8Suffix(raw, tailLen)
	midRegion := ""
	if len(headPart)+len(tailPart) < len(raw) {
		midRegion = raw[len(headPart) : len(raw)-len(tailPart)]
	}
	midPart := extractDecisionWindow(midRegion, midLen)
	kept := len(headPart) + len(midPart) + len(tailPart)
	dropped := max(1, len(raw)-kept)
	if midPart == "" {
		return fmt.Sprintf("%s\n[... truncated %d bytes ...]\n%s", headPart, dropped, tailPart)
	}
	d1 := dropped / 2
	d2 := dropped - d1
	return fmt.Sprintf("%s\n[... truncated %d bytes ...]\n%s\n[... truncated %d bytes ...]\n%s", headPart, d1, midPart, d2, tailPart)
}

func extractDecisionWindow(region string, budget int) string {
	if budget <= 0 || strings.TrimSpace(region) == "" {
		return ""
	}
	var picked []string
	used := 0
	for _, line := range strings.Split(region, "\n") {
		low := strings.ToLower(line)
		if strings.Contains(low, "decision") || strings.Contains(low, "outcome") ||
			strings.Contains(low, "error") || strings.Contains(low, "commit") ||
			strings.Contains(low, "adr-") || strings.Contains(low, "plan-") ||
			strings.Contains(low, "resolved") || strings.Contains(low, "verdict") {
			part := trail.TruncateUTF8(strings.TrimSpace(line), min(160, budget-used))
			if part != "" {
				picked = append(picked, part)
				used += len(part) + 1
				if used >= budget {
					break
				}
			}
		}
	}
	if len(picked) > 0 {
		return trail.TruncateUTF8(strings.Join(picked, "\n"), budget)
	}
	start := len(region)/2 - budget/2
	if start < 0 {
		start = 0
	}
	for start < len(region) && !utf8.RuneStart(region[start]) {
		start++
	}
	return trail.TruncateUTF8(region[start:], budget)
}

func sliceUTF8Suffix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
