package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// globalHome makes a home whose ~/.config/fyr holds a feedback line, a
// retro line and a proposal.
func globalHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, harness.StateDir)
	writeFile(t, dir, "feedback.jsonl", `{"rule_ref":"scratch","note":"the operator corrected a jackalope mistake"}`+"\n")
	writeFile(t, dir, "retros.jsonl", `{"kind":"retro","summary":"a quetzal pattern recurred"}`+"\n")
	writeFile(t, dir, "proposals/2026-09-29-x.md", "# Proposal\n\nadd a rule about the ocelot\n")
	return home
}

// A first run that is --global registers its own registered checkout too.
func TestGlobalRunRegistersItsCheckout(t *testing.T) {
	home := globalHome(t)
	c := trailRepo(t)
	markRegistered(t, home, c)
	mustSearch(t, Options{Dir: c, Home: home, Global: true, Words: []string{"egret"}})
	if got := projectRoots(t, home); len(got) != 1 || got[0] != c {
		t.Errorf("projects %v, want the checkout", got)
	}
}

func TestGlobalRunsOutsideACheckout(t *testing.T) {
	home := globalHome(t)
	out, err := runSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"jackalope"}})
	if err != nil {
		t.Fatalf("--global outside a checkout: %v", err)
	}
	if !strings.Contains(out, "~config:feedback.jsonl:1  feedback") {
		t.Errorf("feedback line not found:\n%s", out)
	}
}

func TestGlobalIndexesFeedbackRetrosProposals(t *testing.T) {
	home := globalHome(t)
	dir := t.TempDir()
	for word, want := range map[string]string{
		"jackalope": "~config:feedback.jsonl:1  feedback",
		"quetzal":   "~config:retros.jsonl:1  retro",
		"ocelot":    "~config:proposals/2026-09-29-x.md:3  proposal",
	} {
		out := mustSearch(t, Options{Dir: dir, Home: home, Global: true, Words: []string{word}})
		if !strings.Contains(out, want) {
			t.Errorf("%s: want %q:\n%s", word, want, out)
		}
	}
	out := mustSearch(t, Options{Dir: dir, Home: home, Global: true, Words: []string{"ocelot"}, Kind: "feedback"})
	if strings.Contains(out, "proposal") {
		t.Errorf("--kind feedback let a proposal through:\n%s", out)
	}
	// An edited log is re-read on the next query.
	writeFile(t, filepath.Join(home, harness.StateDir), "feedback.jsonl", `{"note":"now a kinkajou"}`+"\n")
	out = mustSearch(t, Options{Dir: dir, Home: home, Global: true, Words: []string{"kinkajou"}})
	if !strings.Contains(out, "feedback.jsonl:1") {
		t.Errorf("edited feedback log not re-read:\n%s", out)
	}
	if out := mustSearch(t, Options{Dir: dir, Home: home, Global: true, Words: []string{"jackalope"}}); strings.Contains(out, "feedback.jsonl") {
		t.Errorf("old feedback text still answered:\n%s", out)
	}
}

func TestGlobalDropsVanishedProject(t *testing.T) {
	home := globalHome(t)
	a := trailRepo(t)
	markRegistered(t, home, a)
	mustSearch(t, Options{Dir: a, Home: home, Words: []string{"kiwi"}})
	if err := os.RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"kiwi"}})
	if !strings.Contains(out, "note: dropped "+a+" (no longer a git checkout)") {
		t.Errorf("vanished project:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"kiwi"}})
	if strings.Contains(out, "dropped") {
		t.Errorf("dropped twice:\n%s", out)
	}
}

func TestGlobalRebuildDropsRegistry(t *testing.T) {
	home := globalHome(t)
	a := trailRepo(t)
	markRegistered(t, home, a)
	mustSearch(t, Options{Dir: a, Home: home, Words: []string{"demo"}})
	var out strings.Builder
	if err := Index(Options{Dir: t.TempDir(), Home: home, Global: true, Rebuild: true}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "note: 1 project registrations dropped; each registered one returns on its next search") {
		t.Errorf("rebuild note:\n%s", out.String())
	}
}

// TestConfigViewListsProposalsByName: the proposals directory is listed
// with ReadDir and matched by suffix, never through a glob pattern built
// from the config path, which a `[` in the path would turn into a class
// (BUG-109's class; the golang skill's rule).
func TestConfigViewListsProposalsByName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "c[1]", harness.StateDir)
	if err := os.MkdirAll(filepath.Join(dir, "proposals"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"feedback.jsonl", "retros.jsonl", "proposals/2026-09-30-retro.md", "proposals/notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := configView(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.path)
	}
	got := strings.Join(names, " ")
	if !strings.Contains(got, "proposals/2026-09-30-retro.md") || strings.Contains(got, "notes.txt") {
		t.Errorf("proposals should be listed by name, .md only, got %q", got)
	}
}
