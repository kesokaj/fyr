package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// globalHome makes a home whose ~/.config/fyr still holds the old stores: a
// feedback line, a retro line and a proposal, which search never reads.
func globalHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, harness.StateDir)
	writeFile(t, dir, "feedback.jsonl", `{"rule_ref":"scratch","note":"the operator corrected a jackalope mistake"}`+"\n")
	writeFile(t, dir, "retros.jsonl", `{"kind":"retro","summary":"a quetzal pattern recurred"}`+"\n")
	writeFile(t, dir, "proposals/2026-09-29-x.md", "# Proposal\n\nadd a rule about the ocelot\n")
	return home
}

// --global reads no file in ~/.config/fyr, inside a checkout or outside one:
// the old stores reach the brain store by `fyr record import --global` alone
// (ADR-087), and the global index holds no file.
func TestGlobalReadsNoConfigFile(t *testing.T) {
	home := globalHome(t)
	dir := trailRepo(t)
	for _, in := range []string{t.TempDir(), dir} {
		for _, word := range []string{"jackalope", "quetzal", "ocelot"} {
			out, err := runSearch(t, Options{Dir: in, Home: home, Global: true, Words: []string{word}})
			if err != nil {
				t.Fatalf("--global in %s: %v", in, err)
			}
			if strings.Contains(out, "config") || strings.Contains(out, word) && !strings.Contains(out, "0 hits") {
				t.Errorf("%s answered from a file:\n%s", word, out)
			}
		}
	}
	if n := countStore(t, globalStore(t, home), `SELECT count(*) FROM files`); n != 0 {
		t.Errorf("the global index holds %d files, want none", n)
	}
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
