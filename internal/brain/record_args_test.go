package brain

import (
	"strings"
	"testing"
)

// The argument checks of Add, Edit and List, and the empty queries of
// Search and Refs, no other test reaches, pinned before the functions were
// split (PLAN-2026-10-27-funclen, Phase 2, D13).

func TestRecordVerbArgumentRefusals(t *testing.T) {
	o := opts(repo(t), t.TempDir())
	g := o
	g.Global = true
	for name, err := range map[string]error{
		"add unknown kind": func() error {
			_, err := Add(o, AddInput{Kind: "nope", Title: "t", Body: strings.NewReader("b")})
			return err
		}(),
		"edit nothing":      Edit(o, EditInput{ID: "BUG-001"}),
		"edit empty status": Edit(o, EditInput{ID: "BUG-001", Status: str("")}),
		"edit bad title":    Edit(o, EditInput{ID: "BUG-001", Title: str("two\nlines")}),
		"list unknown kind": func() error {
			_, err := List(o, ListFilter{Kind: "nope"})
			return err
		}(),
		"list all global": func() error {
			_, err := List(g, ListFilter{All: true})
			return err
		}(),
	} {
		want := map[string]string{
			"add unknown kind":  `unknown kind "nope"`,
			"edit nothing":      "needs at least one of them",
			"edit empty status": "--status needs a value",
			"edit bad title":    "--title must be one line",
			"list unknown kind": `unknown kind "nope"`,
			"list all global":   "--all already covers every scope",
		}[name]
		if !IsUsage(err) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want a usage error containing %q", name, err, want)
		}
	}
}

// A query with no word in it finds nothing, and is no error.
func TestSearchAndRefsWithNoWords(t *testing.T) {
	o := opts(repo(t), t.TempDir())
	add(t, o, "bug", "a bug", "body")
	if hits, err := Search(o, []string{"/"}, "", 10); err != nil || len(hits) != 0 {
		t.Errorf("Search of no word: %v %v", hits, err)
	}
	if hits, err := Refs(o, "/", "", 10); err != nil || len(hits) != 0 {
		t.Errorf("Refs of no word: %v %v", hits, err)
	}
}
