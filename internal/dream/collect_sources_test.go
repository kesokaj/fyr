package dream

import (
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
)

func TestAddOptionalPlanAndHandoffSkipsSeenNewest(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	root := t.TempDir()
	run(t, root, "git", "init", "-q")

	sinceT := now.Add(-2 * time.Hour)
	cutoffT := now
	addHandoff := func(at time.Time, id, title, body string) {
		t.Helper()
		o := brain.Options{Home: home, Dir: root, Now: func() time.Time { return at }}
		if _, err := brain.Add(o, brain.AddInput{
			Kind: "handoff", ID: id, Title: title, Body: strings.NewReader(body),
		}); err != nil {
			t.Fatalf("add handoff %s: %v", id, err)
		}
	}

	addHandoff(sinceT.Add(-3*time.Hour), "HANDOFF-2026-09-30-1500-oldest", "Oldest handoff", "oldest context")
	addHandoff(sinceT.Add(-1*time.Hour), "HANDOFF-2026-09-30-1700-middle", "Middle handoff", "middle context")
	addHandoff(sinceT.Add(30*time.Minute), "HANDOFF-2026-09-30-1830-newest", "Newest handoff", "newest context")

	handoffs, err := brain.List(brain.Options{Home: home, Dir: root}, brain.ListFilter{Kind: "handoff"})
	if err != nil {
		t.Fatalf("brain.List handoffs: %v", err)
	}
	if len(handoffs) != 3 {
		t.Fatalf("len(handoffs) = %d, want 3", len(handoffs))
	}

	t.Run("newest seen falls through to next unseen handoff", func(t *testing.T) {
		c := Collected{
			Root: root, Name: "proj", Changed: true,
			Inputs: []Input{
				{Kind: kindTrail, Label: handoffs[0].ID, Text: handoffs[0].Title + "\n" + handoffs[0].Body, Time: sinceT.Add(30 * time.Minute)},
			},
		}
		addOptionalPlanAndHandoff(home, root, sinceT, cutoffT, &c, c.adder(sinceT))

		var got []string
		for _, in := range c.Inputs {
			if in.Kind == kindTrail {
				got = append(got, in.Label)
			}
		}
		if len(got) != 2 || got[0] != handoffs[0].ID || got[1] != handoffs[1].ID {
			t.Fatalf("trail labels = %v, want [%s %s]", got, handoffs[0].ID, handoffs[1].ID)
		}
	})

	t.Run("no handoffs seen adds only newest", func(t *testing.T) {
		c := Collected{Root: root, Name: "proj", Changed: true}
		addOptionalPlanAndHandoff(home, root, sinceT, cutoffT, &c, c.adder(sinceT))

		var got []string
		for _, in := range c.Inputs {
			if in.Kind == kindTrail {
				got = append(got, in.Label)
			}
		}
		if len(got) != 1 || got[0] != handoffs[0].ID {
			t.Fatalf("trail labels = %v, want [%s]", got, handoffs[0].ID)
		}
	})

	t.Run("all handoffs seen adds nothing", func(t *testing.T) {
		c := Collected{Root: root, Name: "proj", Changed: true}
		for _, h := range handoffs {
			c.Inputs = append(c.Inputs, Input{Kind: kindTrail, Label: h.ID, Text: h.Body})
		}
		addOptionalPlanAndHandoff(home, root, sinceT, cutoffT, &c, c.adder(sinceT))

		if len(c.Inputs) != 3 {
			t.Fatalf("len(Inputs) = %d, want 3 (no duplicate handoffs)", len(c.Inputs))
		}
	})
}
