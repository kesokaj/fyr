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

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/models"
)

func day(n int) time.Time { return now.Add(-time.Duration(n) * 24 * time.Hour) }

// Over the cap the dated kinds go together, newest first, whatever their
// kind, so what is sent is the oldest news; Through stops just before the
// oldest dropped (ADR-069, sandbox finding 3).
func TestFitDropsDatedNewestFirst(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	const limit = 8192
	c := Collected{Name: "p", Dropped: []string{"feedback.jsonl: " + strings.Repeat("e", 1500)},
		Inputs: []Input{{Kind: kindMemory, Label: "memory/a.md", Text: "m"}}}
	room := limit - len(head(c)) - len(block(c.Inputs[0]))
	c.Inputs = append(c.Inputs, Input{Kind: kindTranscript, Label: "only", Text: strings.Repeat("t", room-200), Time: day(1)})
	if len(block(c.Inputs[1])) > room {
		t.Fatal("the input must fit the room the cap leaves after memory")
	}
	if err := c.fit(limit); err != nil {
		t.Fatal(err)
	}
	wantDropped := fmt.Sprintf("transcript only (dropped to fit the %d KB cap; not sent again)", limit/1024)
	if len(c.Inputs) != 1 || c.Dropped[1] != wantDropped {
		t.Errorf("inputs %s, dropped %q", labels(c), c.Dropped)
	}
	if !c.Through.IsZero() {
		t.Errorf("Through = %v, want zero", c.Through)
	}
}

// The metrics summary and the skills are undated: dropping them holds
// nothing back.
func TestFitUndatedDoNotHoldWatermark(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
		when := day(d)
		f.addTrail(t, brain.Options{Home: f.home, Dir: f.root, Now: func() time.Time { return when }}, "change", fmt.Sprintf("c%02d", d), fmt.Sprintf("MARK-%d-DAYS-AGO %s\n", d, pad))
	}
	rec := fakeAgy(t, `{"proposals":[],"skills":[]}`, 0)
	marks := map[string]bool{}
	mark := regexp.MustCompile(`MARK-[0-9]+-DAYS-AGO`)
	for run := 1; run <= 2; run++ {
		os.Remove(filepath.Join(rec, "stdin"))
		var out bytes.Buffer
		o := runOpts(f, &out)
		// The second run comes a day later: --all tries a project once in
		// 24 hours (the cooldown), so the rest waits a day, as it did under
		// the old once-a-night timer.
		o.Now = func() time.Time { return now.Add(time.Duration(run-1)*cooldown + time.Duration(run)*time.Minute) }
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
	t.Parallel()

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
// a group of one time split by the cap was dropped again on every run).
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
// same five on every run.
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
	t.Parallel()

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

func TestFitChunksOversizedTranscriptAndDropsTinyRoom(t *testing.T) {
	t.Parallel()

	huge := "HEAD_USER_REQUEST: refactor dream chunking\n" +
		strings.Repeat("step output filler line abcdefghij\n", 300) +
		"DECISION_MARKER: preserve head, decision/outcome, and tail windows\n" +
		strings.Repeat("more intermediate filler 0123456789\n", 300) +
		"TAIL_OUTCOME_MARKER: verified all tests green"
	ts := day(2)
	c := Collected{Name: "p", Inputs: []Input{
		{Kind: kindMemory, Label: "memory/a.md", Text: "m"},
		{Kind: kindTranscript, Label: "antigravity conv-huge", Text: huge, Time: ts},
	}}
	limit := len(head(c)) + 2400
	if err := c.fit(limit); err != nil {
		t.Fatal(err)
	}
	if len(c.Dropped) != 0 {
		t.Fatalf("expected oversized transcript to be chunked rather than dropped, got Dropped=%v", c.Dropped)
	}
	if len(c.Inputs) != 2 {
		t.Fatalf("len(Inputs) = %d, want 2", len(c.Inputs))
	}
	got := c.Inputs[1]
	if got.Label != "antigravity conv-huge" || !got.Time.Equal(ts) {
		t.Errorf("chunked input lost label/timestamp: label=%q time=%v", got.Label, got.Time)
	}
	for _, marker := range []string{"HEAD_USER_REQUEST", "DECISION_MARKER", "TAIL_OUTCOME_MARKER", "[... truncated "} {
		if !strings.Contains(got.Text, marker) {
			t.Errorf("chunked transcript missing %q:\n%s", marker, got.Text)
		}
	}
	if rendered := prompt(c); len(rendered) > limit {
		t.Errorf("rendered prompt size %d exceeds limit %d", len(rendered), limit)
	}

	// When f.room cannot fit even a minimal window (< 512 B), drop as tooBig.
	cTiny := Collected{Name: "p", Inputs: []Input{
		{Kind: kindMemory, Label: "memory/a.md", Text: "m"},
		{Kind: kindTranscript, Label: "antigravity conv-tiny-room", Text: huge, Time: ts},
	}}
	tinyLimit := len(head(cTiny)) + len(block(cTiny.Inputs[0])) + 300
	if err := cTiny.fit(tinyLimit); err != nil {
		t.Fatal(err)
	}
	if len(cTiny.Inputs) != 1 || len(cTiny.Dropped) != 1 || !strings.Contains(cTiny.Dropped[0], "too big") {
		t.Errorf("expected tooBig drop when room < 512B, got inputs=%v dropped=%v", labels(cTiny), cTiny.Dropped)
	}
}

func TestCommandWithModelAndPrepareAgyCatalogPick(t *testing.T) {
	if got := commandWithModel(""); !equalSlice(got, command()) {
		t.Fatalf("commandWithModel(\"\") = %v, want %v", got, command())
	}
	wantWithModel := []string{"agy", "--input-format", "stream-json", "--output-format", "stream-json", "--mode", "plan", "--model", "gemini-4-flash-medium", "-p", ""}
	if got := commandWithModel("gemini-4-flash-medium"); !equalSlice(got, wantWithModel) {
		t.Fatalf("commandWithModel = %v, want %v", got, wantWithModel)
	}

	emptyHome := t.TempDir()
	if got := (Options{Home: emptyHome}).resolveModel(); got != "" {
		t.Fatalf("resolveModel without catalog = %q, want empty", got)
	}
	if got := (Options{Home: emptyHome, Model: "explicit-model-id"}).resolveModel(); got != "explicit-model-id" {
		t.Fatalf("resolveModel with explicit Model = %q, want explicit-model-id", got)
	}

	catHome := t.TempDir()
	cat := models.BuildCatalog(now, []models.CatalogEntry{
		{ID: "gemini-4-pro-high", Family: "gemini", Tier: "pro", Status: "active"},
		{ID: "gemini-4-flash-medium", Family: "gemini", Tier: "flash", Status: "active"},
	}, nil, nil)
	if err := models.SaveCatalog(catHome, cat); err != nil {
		t.Fatalf("SaveCatalog: %v", err)
	}
	_ = fakeAgy(t, `{"proposals":[],"skills":[]}`, 0)
	r := &dreamRun{o: Options{Home: catHome}}
	if err := r.prepareAgy(); err != nil {
		t.Fatalf("prepareAgy: %v", err)
	}
	if !equalSlice(r.argv, wantWithModel) {
		t.Fatalf("prepareAgy argv = %v, want %v", r.argv, wantWithModel)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFitOldestLeftLeavesSIMIntactWhenTrimCannotFit(t *testing.T) {
	t.Parallel()

	simOrig := "SIM_START\n" + strings.Repeat("sim-entry-line-0123456789\n", 80) // ~2090 B (> minSIMTrimText 1024; max trim ~1020 B)
	trText := strings.Repeat("transcript-body-0123456789\n", 200)                // ~5400 B (>= minTrimTranscriptBytes 4096)
	droppedPre := "prior.jsonl: " + strings.Repeat("d", 4000)

	// Construct oldestLeft case: transcript fits f.room, so it is NOT tooBig,
	// yet total size exceeds limit by ~2500 B — more than trimming kindSIM down
	// to minSIMTrimText can free. Transcript must be dropped and kindSIM must
	// remain completely untrimmed.
	base := Collected{
		Name:    "p",
		Dropped: []string{droppedPre},
		Inputs: []Input{
			{Kind: kindMemory, Label: "memory/a.md", Text: "mem"},
			{Kind: kindSIM, Label: "optimizer-memory", Text: simOrig},
			{Kind: kindTranscript, Label: "antigravity conv-oldest", Text: trText, Time: day(1)},
		},
	}
	totalNoLimit := len(prompt(base))
	limit := totalNoLimit - 2500
	room := limit - len(head(base)) - leftOutSlackReserve - len(block(base.Inputs[0])) - len(block(base.Inputs[1]))
	if len(block(base.Inputs[2])) > room {
		t.Fatalf("fixture setup invalid: transcript (%d) > room (%d), would hit tooBig instead of oldestLeft", len(block(base.Inputs[2])), room)
	}

	c := base
	c.Dropped = append([]string(nil), base.Dropped...)
	c.Inputs = append([]Input(nil), base.Inputs...)
	if err := c.fit(limit); err != nil {
		t.Fatalf("fit: %v", err)
	}
	if rendered := prompt(c); len(rendered) > limit {
		t.Fatalf("rendered prompt %d > limit %d", len(rendered), limit)
	}
	var gotSIM *Input
	for i := range c.Inputs {
		if c.Inputs[i].Kind == kindTranscript {
			t.Fatalf("expected oversized oldestLeft transcript to be dropped, still present")
		}
		if c.Inputs[i].Kind == kindSIM {
			gotSIM = &c.Inputs[i]
		}
	}
	if gotSIM == nil {
		t.Fatalf("kindSIM unexpectedly dropped; inputs=%s dropped=%v", labels(c), c.Dropped)
	}
	if gotSIM.Text != simOrig {
		t.Fatalf("kindSIM was mutated when trimming could not save the transcript: got len=%d (contains trimmed=%v), want exact len=%d",
			len(gotSIM.Text), strings.Contains(gotSIM.Text, "[... trimmed"), len(simOrig))
	}

	// Complementary must-pass case: when overage (400 B) IS smaller than what
	// trimming kindSIM can free (~1020 B), kindSIM IS trimmed and the oldestLeft
	// transcript IS preserved.
	limitFits := totalNoLimit - 400
	cFits := base
	cFits.Dropped = append([]string(nil), base.Dropped...)
	cFits.Inputs = append([]Input(nil), base.Inputs...)
	if err := cFits.fit(limitFits); err != nil {
		t.Fatalf("fit(limitFits): %v", err)
	}
	if rendered := prompt(cFits); len(rendered) > limitFits {
		t.Fatalf("limitFits rendered prompt %d > limit %d", len(rendered), limitFits)
	}
	var keptTranscript, trimmedSIM bool
	for _, in := range cFits.Inputs {
		if in.Kind == kindTranscript {
			keptTranscript = true
		}
		if in.Kind == kindSIM && strings.Contains(in.Text, "[... trimmed") {
			trimmedSIM = true
		}
	}
	if !keptTranscript || !trimmedSIM {
		t.Fatalf("expected oldestLeft transcript kept (%v) via trimmed kindSIM (%v); dropped=%v", keptTranscript, trimmedSIM, cFits.Dropped)
	}
}
