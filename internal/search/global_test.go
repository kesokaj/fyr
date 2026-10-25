package search

import (
	"os"
	"os/exec"
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

func TestGlobalRegistersProject(t *testing.T) {
	home := globalHome(t)
	a := newRepo(t)
	writeFile(t, a, ".agents/docs/bugs/BUG-001-heron.md", "---\nid: BUG-001\nstatus: open\ndate: 2026-09-10\n---\n# BUG-001: the heron failure\n")
	writeFile(t, a, "src/heron.go", "package src // heron in code, not trail\n")
	commitAll(t, a, "add")
	b := newRepo(t)
	// b searches globally before a has ever run: nothing of a yet.
	if out := mustSearch(t, Options{Dir: b, Home: home, Global: true, Words: []string{"heron"}}); strings.Contains(out, "BUG-001") {
		t.Fatalf("unregistered project answered:\n%s", out)
	}
	mustSearch(t, Options{Dir: a, Home: home, Words: []string{"demo"}})
	out := mustSearch(t, Options{Dir: b, Home: home, Global: true, Words: []string{"heron"}})
	name := filepath.Base(a)
	if !strings.Contains(out, name+":.agents/docs/bugs/BUG-001-heron.md:") || !strings.Contains(out, "bug BUG-001 open") {
		t.Errorf("a's bug not found from b:\n%s", out)
	}
	if strings.Contains(out, "src/heron.go") {
		t.Errorf("the global search index holds only trails, not code:\n%s", out)
	}
	// A first run that is --global registers its own checkout too.
	c := newRepo(t)
	writeFile(t, c, ".agents/docs/plans/p.md", "# the egret plan\n- [ ] one\n")
	commitAll(t, c, "add")
	out = mustSearch(t, Options{Dir: c, Home: home, Global: true, Words: []string{"egret"}})
	if !strings.Contains(out, filepath.Base(c)+":.agents/docs/plans/p.md") {
		t.Errorf("a first --global run left its own checkout out:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: b, Home: home, Global: true, Open: true})
	if !strings.Contains(out, "BUG-001") || !strings.Contains(out, "plans/p.md") {
		t.Errorf("--global --open:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: b, Home: home, Global: true, Refs: "BUG-001"})
	if !strings.HasPrefix(out, "BUG-001  "+name+":.agents/docs/bugs/BUG-001-heron.md:1  open") {
		t.Errorf("--global --refs:\n%s", out)
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
	parent := t.TempDir()
	a := filepath.Join(parent, "gone")
	if err := os.Rename(newRepo(t), a); err != nil {
		t.Fatal(err)
	}
	writeFile(t, a, ".agents/docs/bugs/BUG-002-kiwi.md", "---\nid: BUG-002\nstatus: open\n---\n# kiwi\n")
	commitAll(t, a, "add")
	mustSearch(t, Options{Dir: a, Home: home, Words: []string{"kiwi"}})
	if err := os.RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"kiwi"}})
	if !strings.Contains(out, "note: dropped "+a+" (no longer a git checkout)") || strings.Contains(out, "BUG-002") {
		t.Errorf("vanished project:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"kiwi"}})
	if strings.Contains(out, "dropped") {
		t.Errorf("dropped twice:\n%s", out)
	}
}

func TestGlobalNameCollision(t *testing.T) {
	home := globalHome(t)
	var roots []string
	for i := 0; i < 2; i++ {
		p := filepath.Join(t.TempDir(), "app")
		if err := os.Rename(newRepo(t), p); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, ".agents/docs/plans/p.md", "# the stoat plan\n")
		commitAll(t, p, "add")
		mustSearch(t, Options{Dir: p, Home: home, Words: []string{"stoat"}})
		roots = append(roots, p)
	}
	out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"stoat"}})
	if !strings.Contains(out, "app:.agents/docs/plans/p.md") || !strings.Contains(out, "app~2:.agents/docs/plans/p.md") {
		t.Errorf("two projects named app:\n%s", out)
	}
}

func TestGlobalRebuildDropsRegistry(t *testing.T) {
	home := globalHome(t)
	a := newRepo(t)
	writeFile(t, a, ".agents/docs/plans/p.md", "# plan\n") // only a harness project registers
	commitAll(t, a, "trail")
	mustSearch(t, Options{Dir: a, Home: home, Words: []string{"demo"}})
	var out strings.Builder
	if err := Index(Options{Dir: t.TempDir(), Home: home, Global: true, Rebuild: true}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "note: 1 project registrations dropped; each returns on its next search") {
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

// A registered checkout that became a partial clone, on a git too old to
// refuse lazy fetches, is skipped for this query with a note; the global
// search answers from the rest (release review of v2.6.0, finding 2).
func TestGlobalSkipsUnsafePartialClone(t *testing.T) {
	home := globalHome(t)
	a := newRepo(t)
	writeFile(t, a, ".agents/docs/bugs/BUG-003-tern.md", "---\nid: BUG-003\nstatus: open\n---\n# tern\n")
	commitAll(t, a, "add")
	mustSearch(t, Options{Dir: a, Home: home, Words: []string{"tern"}})
	if out, err := exec.Command("git", "-C", a, "config", "remote.origin.promisor", "true").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	saved := lazyFetchSafe
	t.Cleanup(func() { lazyFetchSafe = saved })
	lazyFetchSafe = func() bool { return false }
	out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"jackalope"}})
	if !strings.Contains(out, "note: skipped "+a+" this time") || !strings.Contains(out, "feedback.jsonl") {
		t.Errorf("an unsafe partial clone was not skipped with a note, or the rest went unanswered:\n%s", out)
	}
}
