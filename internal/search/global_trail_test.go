package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const trailLine = `{"kind":"bug","id":"BUG-001"}` + "\n"

// trailRepo is a migrated checkout: its trail is .agents/trail.jsonl, with no
// .agents/docs.
func trailRepo(t *testing.T) string {
	t.Helper()
	root := newRepo(t)
	writeFile(t, root, ".agents/trail.jsonl", trailLine)
	commitAll(t, root, "trail")
	return root
}

func projectRoots(t *testing.T, home string) []string {
	t.Helper()
	ps, err := ListProjects(home)
	if err != nil {
		t.Fatal(err)
	}
	var roots []string
	for _, p := range ps {
		roots = append(roots, p.Root)
	}
	return roots
}

// A migrated checkout registers for --global on its trail.jsonl, the one
// condition beside the marker (PLAN.md D9): before this it had no
// .agents/docs, so no consumer of the projects table ever saw it.
func TestTrailRegistersForGlobal(t *testing.T) {
	home := t.TempDir()
	root := trailRepo(t)
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	if got := projectRoots(t, home); len(got) != 0 {
		t.Fatalf("a trail with no marker registered: %v", got)
	}
	markRegistered(t, home, root)
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	if got := projectRoots(t, home); len(got) != 1 || got[0] != root {
		t.Fatalf("projects %v, want the migrated checkout", got)
	}
	out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"demo"}})
	if strings.Contains(out, "dropped") {
		t.Errorf("--global dropped a migrated checkout:\n%s", out)
	}
	if got := projectRoots(t, home); len(got) != 1 {
		t.Errorf("projects after --global: %v", got)
	}
}

// A file-era tree carries no registration: a marker over .agents/docs and no
// trail.jsonl never joins, whatever it holds.
func TestDocsTreeDoesNotRegister(t *testing.T) {
	for _, docs := range []string{".agents/docs/plans/p.md", "docs/plans/p.md"} {
		home := t.TempDir()
		root := newRepo(t)
		writeFile(t, root, docs, "# plan\n")
		commitAll(t, root, "docs")
		markRegistered(t, home, root)
		mustSearch(t, Options{Dir: root, Home: home, Words: []string{"plan"}})
		if got := projectRoots(t, home); len(got) != 0 {
			t.Errorf("%s: registered %v", docs, got)
		}
	}
}

// A trail.jsonl that is not a regular file is no trail: a directory or a
// symlink at the name neither registers a checkout nor keeps one registered.
func TestTrailMustBeARegularFile(t *testing.T) {
	plant := map[string]func(t *testing.T, path string){
		"directory": func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, path string) {
			target := filepath.Join(t.TempDir(), "elsewhere.jsonl")
			if err := os.WriteFile(target, []byte(trailLine), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, put := range plant {
		t.Run("joins: "+name, func(t *testing.T) {
			home := t.TempDir()
			root := newRepo(t)
			writeFile(t, root, ".agents/keep", "x\n")
			put(t, filepath.Join(root, trailExport))
			markRegistered(t, home, root)
			mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
			if got := projectRoots(t, home); len(got) != 0 {
				t.Errorf("registered %v", got)
			}
		})
		t.Run("stays: "+name, func(t *testing.T) {
			home := t.TempDir()
			root := trailRepo(t)
			markRegistered(t, home, root)
			mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
			path := filepath.Join(root, trailExport)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			put(t, path)
			out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"demo"}})
			if !strings.Contains(out, "note: dropped "+root+" (no longer has .agents/trail.jsonl)") {
				t.Errorf("not dropped:\n%s", out)
			}
			if got := projectRoots(t, home); len(got) != 0 {
				t.Errorf("projects %v", got)
			}
		})
	}
}

// Search reads no docs root: a project's docs/ and .agents/docs are its text
// like any other file, never a trail. A heading, a status and an unticked box
// there make no record, and a plan in them is never open (PLAN.md D4).
func TestDocsTreeIsPlainText(t *testing.T) {
	for _, dir := range []string{".agents/docs", "docs"} {
		home := t.TempDir()
		root := newRepo(t)
		writeFile(t, root, dir+"/bugs/BUG-001-lynx.md", "---\nid: BUG-001\nstatus: open\n---\n# BUG-001: the lynx\n")
		writeFile(t, root, dir+"/plans/p.md", "# the lynx plan\n- [ ] left\n")
		commitAll(t, root, "docs")
		out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lynx"}})
		for _, want := range []string{dir + "/bugs/BUG-001-lynx.md:5  doc\n", dir + "/plans/p.md:1  doc\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: want %q:\n%s", dir, want, out)
			}
		}
		if out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lynx"}, Kind: "bug"}); strings.Contains(out, "BUG-001-lynx") {
			t.Errorf("%s: a file answered --kind bug:\n%s", dir, out)
		}
		if out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lynx"}, Kind: "trail"}); strings.Contains(out, "plans/p.md") {
			t.Errorf("%s: a file answered --kind trail:\n%s", dir, out)
		}
		if out := mustSearch(t, Options{Dir: root, Home: home, Open: true}); strings.TrimSpace(out) != "" {
			t.Errorf("%s: --open read a file:\n%s", dir, out)
		}
		if out := mustSearch(t, Options{Dir: root, Home: home, Refs: "BUG-001"}); strings.Contains(out, "no record") || !strings.Contains(out, "referenced by 1:") {
			t.Errorf("%s: --refs:\n%s", dir, out)
		}
	}
}

// The global index holds ~/.config/fyr only: a registered checkout's records
// answer --global from the brain store, and none of its files do.
func TestGlobalIndexHoldsNoCheckoutFiles(t *testing.T) {
	home := globalHome(t)
	root := trailRepo(t)
	writeFile(t, root, ".agents/docs/plans/p.md", "# the heron plan\n")
	commitAll(t, root, "leftover")
	markRegistered(t, home, root)
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"heron"}})
	out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"heron"}})
	if strings.Contains(out, "plans/p.md") {
		t.Errorf("--global answered from a checkout's file:\n%s", out)
	}
	if n := countStore(t, globalStore(t, home), `SELECT count(*) FROM files WHERE project != '~config'`); n != 0 {
		t.Errorf("%d checkout files in the global index", n)
	}
}

// A search from a linked worktree registers the main root: the row is one
// checkout's, whichever worktree ran, so the dream's target and its watermark
// never move to a worktree (ADR-086; P10a's security review, finding 1).
func TestWorktreeRegistersTheMainRoot(t *testing.T) {
	home := t.TempDir()
	root := trailRepo(t)
	markRegistered(t, home, root)
	wt := filepath.Join(t.TempDir(), "wt")
	gitT(t, root, "worktree", "add", "-q", wt)
	mustSearch(t, Options{Dir: wt, Home: home, Words: []string{"demo"}})
	if got := projectRoots(t, home); len(got) != 1 || got[0] != root {
		t.Fatalf("projects %v, want only the main root %s", got, root)
	}
	if out := mustSearch(t, Options{Dir: wt, Home: home, Global: true, Words: []string{"demo"}}); strings.Contains(out, "dropped") {
		t.Errorf("--global dropped it:\n%s", out)
	}
}

// .agents itself must be a real directory, as brain's guardExportPath asks: a
// symlink or a file there is no trail, and is a drop, never a skip each time.
func TestTrailNeedsARealAgentsDirectory(t *testing.T) {
	for name, put := range map[string]func(t *testing.T, agents string){
		"symlink": func(t *testing.T, agents string) {
			target := t.TempDir()
			if err := os.WriteFile(filepath.Join(target, "trail.jsonl"), []byte(trailLine), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, agents); err != nil {
				t.Fatal(err)
			}
		},
		"file": func(t *testing.T, agents string) {
			if err := os.WriteFile(agents, []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			root := trailRepo(t)
			markRegistered(t, home, root)
			mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
			agents := filepath.Join(root, ".agents")
			if err := os.RemoveAll(agents); err != nil {
				t.Fatal(err)
			}
			put(t, agents)
			out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"demo"}})
			if !strings.Contains(out, "note: dropped "+root+" (no longer has .agents/trail.jsonl)") {
				t.Errorf("not dropped:\n%s", out)
			}
		})
	}
}

// A search never deletes a companion file a clone tracks; an untracked one
// needs no ignore rule of its own, so a .gitignore naming search.db alone
// still works (P10a's reviews: security finding 5, round 2's finding 1).
func TestRefusesTrackedCompanion(t *testing.T) {
	for _, name := range []string{"search.db-wal", "search.db-shm", "search.db-journal"} {
		root := newRepo(t)
		rel := ".agents/scratch/" + name
		writeFile(t, root, rel, strings.Repeat("\x00", 64))
		gitT(t, root, "add", "-f", rel)
		commitAll(t, root, "tracked")
		_, err := runSearch(t, Options{Dir: root, Home: t.TempDir(), Words: []string{"demo"}})
		if err == nil || !strings.Contains(err.Error(), rel+" is tracked") {
			t.Errorf("%s: %v, want the refusal", name, err)
		}
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
}

func TestIgnoringSearchDBAloneIsEnough(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, ".gitignore", ".agents/scratch/search.db\n")
	commitAll(t, root, "narrow ignore")
	if _, err := runSearch(t, Options{Dir: root, Home: t.TempDir(), Words: []string{"demo"}}); err != nil {
		t.Errorf("a .gitignore naming search.db alone: %v", err)
	}
}

// The marker is read on the checkout that ran, and the row's trail on the
// row's root: a worktree of an unmarked repository inside a registered
// checkout joins nothing, and only the main root's trail counts.
func TestRegistrationReadsTheRightRoot(t *testing.T) {
	t.Run("unmarked bare repository inside a registered checkout", func(t *testing.T) {
		home := t.TempDir()
		outer := trailRepo(t)
		markRegistered(t, home, outer)
		bare := filepath.Join(outer, "repo.git")
		gitT(t, outer, "clone", "-q", "--bare", outer, bare)
		wt := filepath.Join(outer, "wt")
		gitT(t, bare, "worktree", "add", "-q", wt)
		mustSearch(t, Options{Dir: wt, Home: home, Words: []string{"demo"}})
		if got := projectRoots(t, home); len(got) != 0 {
			t.Errorf("projects %v, want none", got)
		}
	})
	t.Run("a worktree's own trail does not stand in for main's", func(t *testing.T) {
		home := t.TempDir()
		root := trailRepo(t)
		markRegistered(t, home, root)
		wt := filepath.Join(t.TempDir(), "wt")
		gitT(t, root, "worktree", "add", "-q", wt)
		if err := os.Remove(filepath.Join(root, trailExport)); err != nil {
			t.Fatal(err)
		}
		mustSearch(t, Options{Dir: wt, Home: home, Words: []string{"demo"}})
		if got := projectRoots(t, home); len(got) != 0 {
			t.Errorf("projects %v, want none", got)
		}
	})
	t.Run("a worktree of a bare repository is its own root", func(t *testing.T) {
		src := trailRepo(t)
		base := t.TempDir()
		bare := filepath.Join(base, "repo.git")
		gitT(t, src, "clone", "-q", "--bare", src, bare)
		wt := filepath.Join(base, "wt")
		gitT(t, bare, "worktree", "add", "-q", wt)
		if r, err := filepath.EvalSymlinks(wt); err == nil {
			wt = r
		}
		if got := RegistryRoot(wt); got != wt {
			t.Errorf("RegistryRoot = %s, want the worktree %s", got, wt)
		}
	})
}
