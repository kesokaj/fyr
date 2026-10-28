package dream

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func day(n int) time.Time { return now.Add(-time.Duration(n) * 24 * time.Hour) }

// Over the cap the dated kinds go together, newest first, whatever their
// kind, so what is sent is the oldest news; Through stops just before the
// oldest dropped (ADR-069, sandbox finding 3).
func TestFitDropsDatedNewestFirst(t *testing.T) {
	text := strings.Repeat("x", 1000)
	c := Collected{Name: "p", Inputs: []Input{
		{Kind: kindMemory, Label: "memory/a.md", Text: "m"},
		{Kind: kindTranscript, Label: "t4", Text: text, Time: day(1)},
		{Kind: kindTrail, Label: "r3", Text: text, Time: day(2)},
		{Kind: kindFeedback, Label: "f2", Text: text, Time: day(3)},
		{Kind: kindTranscript, Label: "t1", Text: text, Time: day(4)},
	}}
	keep := Collected{Name: "p", Inputs: []Input{c.Inputs[0], c.Inputs[3], c.Inputs[4]}}
	limit := len(prompt(keep)) + 300
	if err := c.fit(limit); err != nil {
		t.Fatal(err)
	}
	if got := labels(c); got != "memory:memory/a.md\nfeedback:f2\ntranscript:t1\n" {
		t.Errorf("kept:\n%s", got)
	}
	kb := limit / 1024
	want := fmt.Sprintf("transcript t4 (dropped to fit the %d KB cap)|trail r3 (dropped to fit the %d KB cap)", kb, kb)
	if got := strings.Join(c.Dropped, "|"); got != want {
		t.Errorf("dropped %q, want %q", got, want)
	}
	if !c.Through.Equal(day(2).Add(-time.Nanosecond)) {
		t.Errorf("Through = %v, want just before r3's %v", c.Through, day(2))
	}
}

// A dated input larger than what the cap leaves after memory and the skills
// is dropped first and named, and holds nothing back, though it is under the
// cap alone (choices D33); the news around it is sent.
func TestFitTooBigDoesNotHoldWatermark(t *testing.T) {
	c := Collected{Name: "p", Inputs: []Input{
		{Kind: kindMemory, Label: "memory/a.md", Text: strings.Repeat("m", 3000)},
		{Kind: kindSkill, Label: "skill golang", Text: "golang: Go"},
		{Kind: kindTrail, Label: "older", Text: "o", Time: day(3)},
		{Kind: kindTrail, Label: "huge", Text: strings.Repeat("h", 3000), Time: day(2)},
		{Kind: kindTrail, Label: "newer", Text: "n", Time: day(1)},
	}}
	limit := len(prompt(c)) - 1000
	if len(block(c.Inputs[3])) >= limit {
		t.Fatalf("the huge input (%d) must be under the cap alone (%d)", len(block(c.Inputs[3])), limit)
	}
	if err := c.fit(limit); err != nil {
		t.Fatal(err)
	}
	if got := labels(c); got != "memory:memory/a.md\nskill:skill golang\ntrail:older\ntrail:newer\n" {
		t.Errorf("kept:\n%s", got)
	}
	want := fmt.Sprintf("trail huge (too big for the %d KB cap beside memory and skills)", limit/1024)
	if got := strings.Join(c.Dropped, "|"); got != want {
		t.Errorf("dropped %q, want %q", got, want)
	}
	if !c.Through.IsZero() {
		t.Errorf("Through = %v, want zero", c.Through)
	}
}

// The last dated input, dropped because the left-out line beside it leaves
// no room, holds nothing back and is named as not sent again: a run that
// sends no news still advances.
func TestFitLastDatedDoesNotHoldWatermark(t *testing.T) {
	c := Collected{Name: "p", Dropped: []string{"feedback.jsonl: " + strings.Repeat("e", 1500)},
		Inputs: []Input{{Kind: kindMemory, Label: "memory/a.md", Text: "m"}}}
	room := 4096 - len(head(c)) - len(block(c.Inputs[0]))
	c.Inputs = append(c.Inputs, Input{Kind: kindTranscript, Label: "only", Text: strings.Repeat("t", room-200), Time: day(1)})
	if len(block(c.Inputs[1])) > room {
		t.Fatal("the input must fit the room the cap leaves after memory")
	}
	if err := c.fit(4096); err != nil {
		t.Fatal(err)
	}
	if len(c.Inputs) != 1 || c.Dropped[1] != "transcript only (dropped to fit the 4 KB cap; not sent again)" {
		t.Errorf("inputs %s, dropped %q", labels(c), c.Dropped)
	}
	if !c.Through.IsZero() {
		t.Errorf("Through = %v, want zero", c.Through)
	}
}

// The metrics summary and the skills are undated: dropping them holds
// nothing back.
func TestFitUndatedDoNotHoldWatermark(t *testing.T) {
	c := Collected{Name: "p", Inputs: []Input{
		{Kind: kindMemory, Label: "memory/a.md", Text: "m"},
		{Kind: kindSkill, Label: "skill golang", Text: strings.Repeat("s", 900)},
		{Kind: kindMetrics, Label: "metrics", Text: strings.Repeat("9", 900)},
	}}
	limit := len(prompt(c)) - 1500
	if err := c.fit(limit); err != nil {
		t.Fatal(err)
	}
	kb := limit / 1024
	want := fmt.Sprintf("metrics metrics (dropped to fit the %d KB cap)|skill skill golang (dropped to fit the %d KB cap)", kb, kb)
	if got := strings.Join(c.Dropped, "|"); got != want {
		t.Errorf("dropped %q, want %q", got, want)
	}
	if !c.Through.IsZero() {
		t.Errorf("Through = %v, want zero", c.Through)
	}
}

func TestWatermark(t *testing.T) {
	for _, tc := range []struct {
		through, want time.Time
	}{
		{time.Time{}, now},
		{day(2), day(2)},
		{now.Add(time.Hour), now},
	} {
		if got := watermark(Collected{Through: tc.through}, now); !got.Equal(tc.want) {
			t.Errorf("watermark(Through %v) = %v, want %v", tc.through, got, tc.want)
		}
	}
}

// Nine dated inputs of ~40 KB, between one and two times the cap and each
// under it: two runs send all nine between them (choices E2E 5; at ade98e8
// the first sent six and the second made no call).
func TestTwoRunsSendEveryCappedInput(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	pad := strings.Repeat("lorem ipsum dolor sit amet ", 1500)
	for d := 2; d <= 10; d++ {
		write(t, filepath.Join(f.root, ".agents", "docs", "changes", fmt.Sprintf("c%02d.md", d)), fmt.Sprintf("MARK-%d-DAYS-AGO %s\n", d, pad), day(d))
	}
	rec := fakeAgy(t, `{"proposals":[],"skills":[]}`, 0)
	marks := map[string]bool{}
	mark := regexp.MustCompile(`MARK-[0-9]+-DAYS-AGO`)
	for run := 1; run <= 2; run++ {
		os.Remove(filepath.Join(rec, "stdin"))
		var out bytes.Buffer
		o := runOpts(f, &out)
		o.Now = func() time.Time { return now.Add(time.Duration(run) * time.Minute) }
		if err := Run(o); err != nil {
			t.Fatalf("run %d: %v\n%s", run, err, out.String())
		}
		stdin, err := os.ReadFile(filepath.Join(rec, "stdin"))
		if err != nil {
			t.Fatalf("run %d made no call: %v\n%s", run, err, read(t, filepath.Join(f.home, harness.StateDir, "dream.log")))
		}
		for _, m := range mark.FindAllString(string(stdin), -1) {
			marks[m] = true
		}
	}
	var got []string
	for m := range marks {
		got = append(got, m)
	}
	sort.Strings(got)
	if len(got) != 9 {
		t.Errorf("the two runs sent %d of 9: %v", len(got), got)
	}
}

// A dated input with no time cannot place the watermark: dropping it holds
// nothing back, or the next run would read from the year 1.
func TestFitUntimedDatedHoldsNothing(t *testing.T) {
	c := Collected{Name: "p", Inputs: []Input{
		{Kind: kindMemory, Label: "memory/a.md", Text: "m"},
		{Kind: kindTrail, Label: "a", Text: strings.Repeat("a", 1000)},
		{Kind: kindTrail, Label: "b", Text: strings.Repeat("b", 1000)},
	}}
	if err := c.fit(len(prompt(c)) - 500); err != nil {
		t.Fatal(err)
	}
	if len(c.Inputs) != 2 || !c.Through.IsZero() {
		t.Errorf("inputs %s, Through %v, want one trail kept and Through zero", labels(c), c.Through)
	}
}

// sendRuns fits inputs run after run, each run taking the inputs newer than
// the last watermark, as collect does, until none is left. It returns the
// labels each run sent, and fails a test that stalls (code review, Phase 4:
// a group of one time split by the cap was dropped again every night).
func sendRuns(t *testing.T, inputs []Input, limit int) (sent [][]string, lost []string) {
	t.Helper()
	var since time.Time
	for run := 1; run <= 10; run++ {
		c := Collected{Name: "p", Inputs: []Input{{Kind: kindMemory, Label: "memory/a.md", Text: "m"}}}
		for _, in := range inputs {
			if in.Time.After(since) {
				c.Inputs = append(c.Inputs, in)
			}
		}
		if len(c.Inputs) == 1 {
			return sent, lost
		}
		if err := c.fit(limit); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, in := range c.Inputs[1:] {
			got = append(got, in.Label)
		}
		sent = append(sent, got)
		for _, d := range c.Dropped {
			if strings.HasSuffix(d, "not sent again)") || strings.Contains(d, "(too big") {
				lost = append(lost, d)
			}
		}
		next := watermark(c, now.Add(time.Duration(run)*time.Minute))
		if !next.After(since) {
			t.Fatalf("run %d: the watermark stayed at %v", run, since)
		}
		since = next
	}
	t.Fatalf("still inputs after 10 runs: %v", sent)
	return nil, nil
}

func trailInputs(times ...time.Time) []Input {
	var in []Input
	for i, ts := range times {
		in = append(in, Input{Kind: kindTrail, Label: fmt.Sprintf("c%d", i), Text: strings.Repeat("x", 1000), Time: ts})
	}
	return in
}

// roomFor is a cap with room for memory and n of trailInputs' inputs, and
// the left-out line naming the rest.
func roomFor(n int) int {
	c := Collected{Name: "p", Inputs: append([]Input{{Kind: kindMemory, Label: "memory/a.md", Text: "m"}}, trailInputs(make([]time.Time, n)...)...)}
	return len(prompt(c)) + 400
}

// Nine inputs of one time (one commit) with room for five: a time
// watermark cannot split them, so the run sends five and names the other
// four as not sent again, and the next run has nothing; it never sends the
// same five every night.
func TestFitTieGroupNeverStalls(t *testing.T) {
	ts := day(2)
	sent, lost := sendRuns(t, trailInputs(ts, ts, ts, ts, ts, ts, ts, ts, ts), roomFor(5))
	if len(sent) != 1 || strings.Join(sent[0], " ") != "c0 c1 c2 c3 c4" || len(lost) != 4 {
		t.Errorf("sent %v, lost %v; want one run of c0 to c4 and four named", sent, lost)
	}
}

// An older input, then three of one newer time, with room for two: the
// first run sends the older one and one of the group and holds the other two
// back; the second run has the group of three, sends two and names one. No
// run sends the same inputs again, and every input is sent or named.
func TestFitTieAboveOlderNeverStalls(t *testing.T) {
	sent, lost := sendRuns(t, trailInputs(day(3), day(2), day(2), day(2)), roomFor(2))
	n := len(lost)
	for _, s := range sent {
		n += len(s)
	}
	if len(sent) != 2 || len(sent[0]) != 2 || len(lost) != 1 || n != 5 {
		t.Errorf("sent %v, lost %v; want two runs of two and one named", sent, lost)
	}
}

// Nine inputs of nine times, between one and two times the cap: two runs
// send all nine, none named lost (choices E2E 5).
func TestFitDistinctTimesLoseNothing(t *testing.T) {
	var times []time.Time
	for d := 10; d >= 2; d-- {
		times = append(times, day(d))
	}
	sent, lost := sendRuns(t, trailInputs(times...), roomFor(5))
	if len(sent) != 2 || len(sent[0])+len(sent[1]) != 9 || len(lost) != 0 {
		t.Errorf("sent %v, lost %v; want all nine over two runs", sent, lost)
	}
}

// Two inputs too big for the room both go, newest first, as every dated
// drop does, so the left-out line reads newest first.
func TestFitTooBigNewestFirst(t *testing.T) {
	c := Collected{Name: "p", Inputs: []Input{
		{Kind: kindMemory, Label: "memory/a.md", Text: "m"},
		{Kind: kindTrail, Label: "new", Text: strings.Repeat("n", 3000), Time: day(1)},
		{Kind: kindTrail, Label: "old", Text: strings.Repeat("o", 3000), Time: day(2)},
	}}
	limit := len(prompt(c)) - 4000
	if err := c.fit(limit); err != nil {
		t.Fatal(err)
	}
	kb := limit / 1024
	want := fmt.Sprintf("trail new (too big for the %d KB cap beside memory and skills)|trail old (too big for the %d KB cap beside memory and skills)", kb, kb)
	if got := strings.Join(c.Dropped, "|"); got != want {
		t.Errorf("dropped %q, want %q", got, want)
	}
}
