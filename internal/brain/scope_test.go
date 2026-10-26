package brain

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Outside a git checkout the project scope refuses; --global still works.
func TestOutsideACheckout(t *testing.T) {
	home := t.TempDir()
	o := opts(t.TempDir(), home)
	if _, err := List(o, ListFilter{}); !IsRefusal(err) {
		t.Errorf("list outside a checkout: %v, want a refusal", err)
	}
	o.Global = true
	if _, err := Add(o, AddInput{Kind: "learning", Title: "t", Body: strings.NewReader("b")}); err != nil {
		t.Errorf("add --global outside a checkout: %v", err)
	}
}

// A linked worktree shares the main worktree's scope (memory.Root's rule),
// but its export is its own checkout's file, the one its branch commits.
func TestLinkedWorktreeExportsToItsOwnCheckout(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, dir, "worktree", "add", "-q", "-b", "side", wt)
	id := add(t, opts(wt, home), "learning", "from the worktree", "b")
	if _, err := os.Stat(trailFile(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a write in the linked worktree wrote the main worktree's trail.jsonl")
	}
	if !strings.Contains(trailText(t, wt), id) {
		t.Errorf("the linked worktree's trail.jsonl lacks %s", id)
	}
	recs, _, err := Get(opts(wt, home), []string{id})
	if err != nil || recs[0].Scope != dir {
		r, _ := filepath.EvalSymlinks(dir)
		if err != nil || recs[0].Scope != r {
			t.Errorf("scope %v (%v), want the main worktree %s", recs, err, dir)
		}
	}
}

// list --all covers every scope; --project needs --global.
func TestListScopes(t *testing.T) {
	home := t.TempDir()
	a, b := repo(t), repo(t)
	add(t, opts(a, home), "learning", "in a", "x")
	add(t, opts(b, home), "learning", "in b", "x")
	g := opts(a, home)
	g.Global = true
	add(t, g, "feedback", "global fb", "x")
	only, err := List(opts(a, home), ListFilter{})
	if err != nil || len(only) != 1 || only[0].Title != "in a" {
		t.Errorf("project list: %+v %v", only, err)
	}
	all, err := List(opts(a, home), ListFilter{All: true})
	if err != nil || len(all) != 3 {
		t.Errorf("--all: %+v %v", all, err)
	}
	if line := FormatLine(all[0], true); !strings.Contains(line, "[") {
		t.Errorf("--all line names no scope: %q", line)
	}
	if _, err := List(opts(a, home), ListFilter{Project: a}); !IsUsage(err) {
		t.Errorf("--project without --global: %v, want usage", err)
	}
}

// Search ranks records by BM25 over title and body in the project and
// global scopes; --global covers every scope.
func TestSearchRecords(t *testing.T) {
	home := t.TempDir()
	a, b := repo(t), repo(t)
	add(t, opts(a, home), "bug", "trailer co-authored slipped in", "Rule: no trailer.")
	add(t, opts(b, home), "bug", "other project trailer", "x")
	g := opts(a, home)
	g.Global = true
	add(t, g, "feedback", "trailer feedback", "no co-authored lines")
	hits, err := Search(opts(a, home), []string{"trailer", "co-authored"}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "BUG-001" || hits[0].Label != "BUG-001" {
		t.Fatalf("project search: %+v", hits)
	}
	if hits[1].Label != "global:"+hits[1].ID {
		t.Errorf("a global hit is not labelled: %q", hits[1].Label)
	}
	if !strings.Contains(hits[0].Snippet, "»") && !strings.Contains(hits[0].Title, "trailer") {
		t.Errorf("snippet %q", hits[0].Snippet)
	}
	all, err := Search(g, []string{"trailer"}, "", 10)
	if err != nil || len(all) != 3 {
		t.Errorf("--global: %+v %v", all, err)
	}
	if k, err := Search(opts(a, home), []string{"trailer"}, "feedback", 10); err != nil || len(k) != 1 {
		t.Errorf("--kind feedback: %+v %v", k, err)
	}
	if k, err := Search(opts(a, home), []string{"trailer"}, "code", 10); err != nil || len(k) != 0 {
		t.Errorf("--kind code: %+v %v", k, err)
	}
}
