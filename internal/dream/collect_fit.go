package dream

import (
	"errors"
	"fmt"
	"time"
)

// datedKinds are the kinds that carry news. Over the cap they are dropped
// together, newest first, and two inputs of the same time in this order
// (ADR-069).
var datedKinds = []string{kindTranscript, kindTrail, kindFeedback}

// fit drops inputs until the prompt is under limit: the metrics summary,
// then the dated kinds newest first, then skills (D21, ADR-055a, ADR-069).
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
	f.room = limit - len(head(*c))
	for i, in := range c.Inputs {
		f.sizes[i] = len(block(in))
		f.size += f.sizes[i]
		if in.Kind == kindMemory || in.Kind == kindSkill {
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
		if newest < 0 || in.Time.Before(oldest) {
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
	if newest >= 0 && f.c.Inputs[newest].Time.After(oldest) {
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
		f.drop(i, fmt.Sprintf("too big for the %d KB cap beside memory and skills", f.limit/1024))
	case oldestLeft:
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
