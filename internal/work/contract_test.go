package work

import (
	"fmt"
	"strings"
	"testing"
)

const base40, head40 = "1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "2222222bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// handoffOf is a handoff whose three sections hold open, next and kick
// lines under their headings, with a Context section the contract skips.
func handoffOf(open, next, kick int) *Record {
	var b strings.Builder
	b.WriteString("# Handoff\n\n## Context\n\nskipped\n\n")
	for _, s := range []struct {
		head string
		n    int
	}{{"## Kickoff", kick}, {"## Open / In Progress", open}, {"## Next Steps", next}} {
		b.WriteString(s.head + "\n")
		for i := 0; i < s.n; i++ {
			fmt.Fprintf(&b, "%s line %d\n", s.head[3:], i+1)
		}
		b.WriteString("\n")
	}
	return &Record{ID: "HANDOFF-1", Kind: "handoff", Body: b.String()}
}

func fetchedResult() Result {
	return Result{Name: "fix-a", Verdict: "PASS", AgyExit: 0, Seconds: 12, Model: "gemini-3.8-flash-low", Base: base40, Head: head40, Commits: 2,
		Handoff: handoffOf(2, 2, 2), Changes: []Record{{ID: "CHANGE-1", Title: "Did a"}}, Drafts: 1, DraftsDir: ".agents/scratch/work/s/fix-a/"}
}

// TestBlockLines is each contract line of § Interfaces: the result line
// (an unfetched worker with no model prints model default),
// the handoff and its sections in print order, a change line, the drafts
// line; and an unfetched worker's line names its kept sandbox.
func TestBlockLines(t *testing.T) {
	got := strings.Join(fetchedResult().Block(), "\n")
	want := strings.Join([]string{
		"work: fix-a  PASS  agy exit 0  12s  model gemini-3.8-flash-low  work/fix-a 1111111..2222222 (2 commits)  runner host (--runner host)",
		"work: fix-a: handoff HANDOFF-1",
		"work: fix-a | ## Open / In Progress", "work: fix-a | Open / In Progress line 1", "work: fix-a | Open / In Progress line 2",
		"work: fix-a | ## Next Steps", "work: fix-a | Next Steps line 1", "work: fix-a | Next Steps line 2",
		"work: fix-a | ## Kickoff", "work: fix-a | Kickoff line 1", "work: fix-a | Kickoff line 2",
		"work: fix-a: change CHANGE-1  Did a",
		"work: fix-a: drafts .agents/scratch/work/s/fix-a/ (1 files)",
	}, "\n")
	if got != want {
		t.Errorf("block:\n%s\nwant:\n%s", got, want)
	}
	r := fetchedResult()
	r.Changes, r.Drafts = nil, 0
	if b := strings.Join(r.Block(), "\n"); !strings.HasSuffix(b, "\nwork: fix-a: no change record") {
		t.Errorf("no change record:\n%s", b)
	}
	u := Result{Name: "x", Verdict: "FAIL", Reasons: []string{"real config changed", "killed"}, AgyExit: -1, Seconds: 3,
		Base: base40, Dir: "/h/.config/fyr/work/s/x", Handoff: handoffOf(1, 1, 1), Notes: []string{"work: x: a note"}}
	if b := u.Block(); strings.Join(b, "\n") != "work: x  FAIL (real config changed; killed)  agy exit -1  3s  model default  work/x not fetched  kept /h/.config/fyr/work/s/x  runner host (--runner host)\nwork: x: a note" {
		t.Errorf("unfetched block:\n%s", strings.Join(b, "\n"))
	}
	d := fetchedResult()
	d.Runner = RunnerDocker
	if got, want := d.Block()[0], "work: fix-a  PASS  agy exit 0  12s  model gemini-3.8-flash-low  work/fix-a 1111111..2222222 (2 commits)  runner docker"; got != want {
		t.Errorf("docker result line:\n%s\nwant:\n%s", got, want)
	}
	h := fetchedResult()
	h.Runner, h.RunnerReason = RunnerHost, "--runner docker: docker not found on PATH"
	if got, want := h.Block()[0], "work: fix-a  PASS  agy exit 0  12s  model gemini-3.8-flash-low  work/fix-a 1111111..2222222 (2 commits)  runner host (docker not found on PATH)"; got != want {
		t.Errorf("host reason result line:\n%s\nwant:\n%s", got, want)
	}
}

// TestBlockCutOrder is D39: over 39 lines (under 40, D85) the Kickoff
// section goes first,
// then Open / In Progress, then the change lines, and Next Steps last, each
// from its end; a heading left alone goes with it; the cut line counts what
// went.
func TestBlockCutOrder(t *testing.T) {
	for _, c := range []struct {
		open, next, kick int
		keep             []string // headings still printed
		cut              int
	}{
		{2, 2, 2, []string{"## Open", "## Next", "## Kickoff"}, 0},
		{10, 10, 12, []string{"## Open", "## Next", "## Kickoff"}, 0}, // 2+1+1 fixed, 11+11+13: 39
		{10, 10, 13, []string{"## Open", "## Next", "## Kickoff"}, 2}, // 40: the cut line and one more
		{20, 15, 10, []string{"## Open", "## Next"}, 14},              // the Kickoff whole, then three of Open
		{3, 33, 3, []string{"## Next"}, 8},                            // Open's heading would be left alone
		{3, 60, 3, []string{"## Next"}, 4 + 4 + 1 + 26},               // then the change line, then Next from its end
	} {
		r := fetchedResult()
		r.Handoff = handoffOf(c.open, c.next, c.kick)
		b := r.Block()
		joined := strings.Join(b, "\n")
		if len(b) > 39 {
			t.Errorf("%v: %d lines, over 39", c, len(b))
		}
		for _, h := range []string{"## Open", "## Next", "## Kickoff"} {
			if in := strings.Contains(joined, "\nwork: fix-a | "+h); in != contains(c.keep, h) {
				t.Errorf("%v: %s printed %v", c, h, in)
			}
		}
		cutLine := fmt.Sprintf("work: fix-a: cut %d lines; read the handoff after the merge", c.cut)
		if c.cut == 0 && strings.Contains(joined, ": cut ") || c.cut > 0 && b[len(b)-1] != cutLine {
			t.Errorf("%v: last line %q, want %q", c, b[len(b)-1], cutLine)
		}
		if c.cut > 0 && len(b) < 38 {
			t.Errorf("%v: %d lines after a cut, want 39 or one less", c, len(b))
		}
		if !strings.Contains(joined, "Next Steps line 1") {
			t.Errorf("%v: the first Next Steps line was cut", c)
		}
	}
}

// TestBlockBounds: at most maxNotes notes, the change lines cut before
// Next Steps, every line printable and at most maxLineSize bytes.
func TestBlockBounds(t *testing.T) {
	r := fetchedResult()
	for i := 0; i < 15; i++ {
		r.Notes = append(r.Notes, fmt.Sprintf("work: fix-a: note %d", i))
	}
	for i := 0; i < 50; i++ {
		r.Changes = append(r.Changes, Record{ID: fmt.Sprintf("CHANGE-%d", i), Title: "t"})
	}
	r.Handoff = handoffOf(1, 1, 1)
	r.Handoff.Body = strings.Replace(r.Handoff.Body, "Next Steps line 1", "Next\x1b[2J"+strings.Repeat("é", 800), 1)
	b := r.Block()
	if len(b) != 39 {
		t.Fatalf("%d lines, want 39", len(b))
	}
	joined := strings.Join(b, "\n")
	if strings.Contains(joined, "note 10") || !strings.Contains(joined, "note 9") || strings.Contains(joined, "\x1b") {
		t.Errorf("notes or a control character:\n%s", joined)
	}
	for _, l := range b {
		if len(l) > maxLineSize+len(" [cut]") {
			t.Errorf("a line of %d bytes", len(l))
		}
	}
	if !strings.Contains(joined, "work: fix-a | Next?[2J") || !strings.Contains(joined, "é [cut]") {
		t.Errorf("the long line was not clipped on a rune:\n%s", joined)
	}
	if !strings.HasSuffix(joined, "read the handoff after the merge") {
		t.Errorf("no cut line:\n%s", joined)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
