package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// Findings of reviews/2026-09-29-security-search-index.md, one test each.

func TestPathWithURISpecialCharacters(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "q?mark#frag%20x")
	if err := os.Rename(newRepo(t), root); err != nil {
		t.Fatal(err)
	}
	out := mustSearch(t, Options{Dir: root, Home: t.TempDir(), Words: []string{"demo"}})
	if !strings.Contains(out, "README.md") {
		t.Errorf("search in a checkout with ?, # and %% in its path:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", "scratch", "search.db")); err != nil {
		t.Errorf("the index is not where it belongs: %v", err)
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("files appeared beside the checkout: %v", names)
	}
}

// Only a checkout the operator registered, and that has
// .agents/trail.jsonl, joins the global index; a registered one without it is
// never added only to be dropped by the next --global (ledger D11). A
// committed trail alone, the shape of a third party's harness-scaffolded
// repository, is not consent, and a clone of a registered checkout does not
// carry its marker (ADR-068, ADR-086, SD19).
func TestRegistersOnlyHarnessProjects(t *testing.T) {
	home := t.TempDir()
	plain := newRepo(t)
	writeFile(t, plain, "docs/guide.md", "# a pelican guide\n")
	commitAll(t, plain, "docs")
	markRegistered(t, home, plain)
	mustSearch(t, Options{Dir: plain, Home: home, Words: []string{"pelican"}})
	foreign := trailRepo(t)
	mustSearch(t, Options{Dir: foreign, Home: home, Words: []string{"pelican"}})
	harnessed := trailRepo(t)
	markRegistered(t, home, harnessed)
	mustSearch(t, Options{Dir: harnessed, Home: home, Words: []string{"pelican"}})
	clone := filepath.Join(t.TempDir(), "clone")
	gitT(t, harnessed, "clone", "-q", harnessed, clone)
	mustSearch(t, Options{Dir: clone, Home: home, Words: []string{"pelican"}})
	out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"pelican"}})
	if strings.Contains(out, "dropped") {
		t.Errorf("a registration was dropped:\n%s", out)
	}
	if n := countStore(t, globalStore(t, home), `SELECT count(*) FROM projects`); n != 1 {
		t.Errorf("%d registrations, want 1 (the registered harness checkout)", n)
	}
}

func TestHashesOddNamesItself(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, `"quoted.md`, "a lemming in quotes\n")
	writeFile(t, root, "cr\r.md", "a lemming with a carriage return\n")
	writeFile(t, root, "notes/\"odd\r.md", "# lemming plan\n")
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lemming"}})
	if !strings.Contains(out, "quoted.md") || !strings.Contains(out, "carriage") {
		t.Errorf("odd file names not indexed with their own content:\n%s", out)
	}
}

func TestOutputStripsControlCharacters(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "notes/x.md", "# BUG-001: \x1b]0;pwned\x07title\n\nthe \x1b[31mbadger\x1b[0m escaped\n")
	commitAll(t, root, "add")
	home := t.TempDir()
	for _, o := range []Options{
		{Dir: root, Home: home, Words: []string{"badger"}},
		{Dir: root, Home: home, Refs: "BUG-001"},
	} {
		out := mustSearch(t, o)
		if strings.ContainsAny(out, "\x1b\x07") {
			t.Errorf("%+v printed a control character:\n%q", o, out)
		}
	}
}

func TestGlobalDirIsPrivate(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	// --global creates the state directory itself; a registration would
	// create it first (its key) and prove nothing here.
	mustSearch(t, Options{Dir: root, Home: home, Global: true, Words: []string{"demo"}})
	fi, err := os.Stat(filepath.Join(home, harness.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("~/.config/fyr created %04o, want 0700", fi.Mode().Perm())
	}
}

func TestSkipsSecretShapedFiles(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, ".env", "TOKEN=wolverine-secret\n")
	writeFile(t, root, "keys/server.pem", "wolverine private key\n")
	writeFile(t, root, "notes.md", "a wolverine note\n")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"wolverine"}})
	if strings.Contains(out, ".env") || strings.Contains(out, "server.pem") || !strings.Contains(out, "notes.md") {
		t.Errorf("secret-shaped files indexed:\n%s", out)
	}
}

// Round 2 of the security review.

func TestGlobalDropsCheckoutWithoutTrail(t *testing.T) {
	home := t.TempDir()
	root := trailRepo(t)
	writeFile(t, root, "docs/evil.md", "an ocelot planted in docs\n")
	commitAll(t, root, "docs")
	markRegistered(t, home, root)
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	gitT(t, root, "rm", "-q", ".agents/trail.jsonl")
	commitAll(t, root, "drop trail")
	out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"ocelot"}})
	if strings.Contains(out, "evil.md") || !strings.Contains(out, "(no longer has .agents/trail.jsonl)") {
		t.Errorf("a registration without a trail still answered:\n%s", out)
	}
}

func TestHintAndNotesArePrintable(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "a\x1b[31mb/z.go", "package x\n")
	commitAll(t, root, "odd dir")
	out := mustSearch(t, Options{Dir: root, Home: home, Refs: "z.go"})
	if !strings.Contains(out, "hint: did you mean") || strings.ContainsRune(out, 0x1b) {
		t.Errorf("hint line:\n%q", out)
	}
	parent := t.TempDir()
	gone := filepath.Join(parent, "gone\x1b[2J")
	if err := os.Rename(newRepo(t), gone); err != nil {
		t.Fatal(err)
	}
	writeFile(t, gone, ".agents/trail.jsonl", trailLine)
	commitAll(t, gone, "trail")
	markRegistered(t, home, gone)
	mustSearch(t, Options{Dir: gone, Home: home, Words: []string{"plan"}})
	os.RemoveAll(gone)
	out = mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"plan"}})
	if !strings.Contains(out, "note: dropped") || strings.ContainsRune(out, 0x1b) {
		t.Errorf("note line:\n%q", out)
	}
}

func TestSkipsMoreSecretNames(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, ".git-credentials", "https://u:caracal@example.invalid\n")
	writeFile(t, root, ".aws/credentials", "caracal key\n")
	writeFile(t, root, "config/.ENV", "CARACAL=1\n")
	writeFile(t, root, "notes.md", "a caracal note\n")
	out := mustSearch(t, Options{Dir: root, Home: t.TempDir(), Words: []string{"caracal"}})
	for _, bad := range []string{".git-credentials", ".aws/credentials", ".ENV"} {
		if strings.Contains(out, bad) {
			t.Errorf("%s indexed:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "notes.md") {
		t.Errorf("control file missing:\n%s", out)
	}
}

// Round 3 of the security review.

func TestRegistrationKeptWhenCheckErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory; the check cannot fail here")
	}
	home := t.TempDir()
	root := trailRepo(t)
	markRegistered(t, home, root)
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"tamarin"}})
	agents := filepath.Join(root, ".agents")
	if err := os.Chmod(agents, 0); err != nil {
		t.Fatal(err)
	}
	// The condition holds: the check cannot read .agents/trail.jsonl.
	if _, err := os.Lstat(filepath.Join(agents, "trail.jsonl")); err == nil || os.IsNotExist(err) {
		t.Fatalf("fixture: want a permission error on .agents/trail.jsonl, got %v", err)
	}
	out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"tamarin"}})
	os.Chmod(agents, 0o755)
	if strings.Contains(out, "dropped") || !strings.Contains(out, "note: skipped") {
		t.Errorf("a registration whose check errored was dropped, or skipped silently:\n%s", out)
	}
	if got := projectRoots(t, home); len(got) != 1 || got[0] != root {
		t.Errorf("the registration did not survive the error: %v", got)
	}
	out = mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"tamarin"}})
	if strings.Contains(out, "note:") {
		t.Errorf("the check still fails with the mode restored:\n%s", out)
	}
}

func TestRebuiltNoteIsPrintable(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "esc\x1b[2Jrepo")
	if err := os.Rename(newRepo(t), root); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, ".agents/scratch/search.db", "not a database, and long enough to be read as a header by sqlite")
	out := mustSearch(t, Options{Dir: root, Home: t.TempDir(), Words: []string{"demo"}})
	if !strings.Contains(out, "rebuilt") || strings.ContainsRune(out, 0x1b) {
		t.Errorf("rebuilt note:\n%q", out)
	}
}

// Review of the security fixes (reviews/2026-09-29-review-search-index-code-r2.md): git's
// "not a git repository" is matched in git's C-locale text, whatever the caller's locale.
func TestDropReasonIgnoresLocale(t *testing.T) {
	bin := t.TempDir()
	fake := "#!/bin/sh\nif [ \"$LC_ALL\" = C ]; then echo 'fatal: not a git repository (or any of the parent directories): .git' >&2; else echo 'fatal: kein Git-Repository (oder irgendeines der Elternverzeichnisse): .git' >&2; fi\nexit 128\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	reason, err := dropReason("", t.TempDir())
	if err != nil || reason != "no longer a git checkout" {
		t.Errorf("under a German locale: reason %q, err %v; want the checkout dropped", reason, err)
	}
}

// Security review of aae16af..f41e5c0.

func TestRefuseSymlinkedDatabase(t *testing.T) {
	for _, name := range []string{"search.db", "search.db-wal", "search.db-shm"} {
		root := newRepo(t)
		// An EMPTY target: SQLite accepts an empty file as a new database, so
		// the old code wrote through the link (86,016 bytes in the security
		// review's probe); a non-empty text file would have been rejected as
		// not a database and would hide the defect (gate code review).
		outside := filepath.Join(t.TempDir(), "victim")
		if err := os.WriteFile(outside, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, ".agents", "scratch"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, ".agents", "scratch", name)); err != nil {
			t.Fatal(err)
		}
		out, err := runSearch(t, Options{Dir: root, Words: []string{"demo"}})
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("%s a symlink: want a refusal, got %v\n%s", name, err, out)
		}
		if fi, _ := os.Stat(outside); fi.Size() != 0 {
			t.Errorf("%s: wrote through the symlink: %d bytes", name, fi.Size())
		}
	}
	// The global search index file too.
	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(outside, nil, 0o644)
	os.MkdirAll(filepath.Join(home, harness.StateDir), 0o700)
	if err := os.Symlink(outside, filepath.Join(home, harness.StateDir, "search.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := runSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"x"}}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("global index a symlink: want a refusal, got %v", err)
	}
	if fi, _ := os.Stat(outside); fi.Size() != 0 {
		t.Errorf("global: wrote through the symlink: %d bytes", fi.Size())
	}
}

func TestDatabaseIsPrivate(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	for _, p := range []string{projectDB(root), globalDB(home)} {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			fi, err := os.Stat(p + suffix)
			if err != nil {
				continue // a companion file need not exist
			}
			if fi.Mode().Perm() != 0o600 {
				t.Errorf("%s is %04o, want 0600: it holds the text of files whatever their own mode", p+suffix, fi.Mode().Perm())
			}
		}
	}
	// An index created 0644 by an earlier build is tightened on open.
	if err := os.Chmod(projectDB(root), 0o644); err != nil {
		t.Fatal(err)
	}
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"plan"}})
	if fi, _ := os.Stat(projectDB(root)); fi.Mode().Perm() != 0o600 {
		t.Errorf("an existing 0644 index stayed %04o", fi.Mode().Perm())
	}
}

// Release gate security review of v1.12.1..c872598, finding 1: a rename in
// the working tree misparsed, the old path read as an entry with three
// characters cut off, which could point outside the checkout.
func TestWorktreeRenameStaysInsideCheckout(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "x")
	if err := os.WriteFile(outside, []byte("canary outside the checkout: numbat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })
	writeFile(t, root, "abc../x", "the moved wombat note\n") // a tracked path that, cut by three, reads ../x
	commitAll(t, root, "add")
	if err := os.Rename(filepath.Join(root, "abc..", "x"), filepath.Join(root, "moved.md")); err != nil {
		t.Fatal(err)
	}
	gitT(t, root, "add", "-N", "moved.md")
	// The condition holds: git reports a rename in the work tree.
	if st := gitT(t, root, "status", "--porcelain=v1"); !strings.Contains(st, "R") {
		t.Fatalf("fixture: want a rename in git status, got %q", st)
	}
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"numbat"}})
	if !strings.HasPrefix(out, "search: 0 hits") || strings.Contains(out, "../x") || strings.Contains(out, "canary") {
		t.Errorf("a file outside the checkout was indexed:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"wombat"}})
	if !strings.Contains(out, "moved.md:1") {
		t.Errorf("the moved file is not found at its new path:\n%s", out)
	}
}

// Security check of the v1.13.0 gate fixes, finding 2: a tracked file marked
// skip-worktree whose parent directory becomes a symlink was read through
// the link, outside the checkout.
func TestReadsNeverLeaveCheckout(t *testing.T) {
	for _, tc := range []struct{ bit, link string }{
		{"--skip-worktree", "absolute"},
		{"--assume-unchanged", "relative"}, // the shape the security check measured
	} {
		t.Run(tc.bit+"-"+tc.link, func(t *testing.T) { readsNeverLeaveCheckout(t, tc.bit, tc.link == "relative") })
	}
}

func readsNeverLeaveCheckout(t *testing.T, bit string, relative bool) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "dir/a.md", "inside the checkout\n")
	commitAll(t, root, "add")
	gitT(t, root, "update-index", bit, "dir/a.md")
	outside := filepath.Join(filepath.Dir(root), "beside-"+filepath.Base(root))
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(outside) })
	if err := os.WriteFile(filepath.Join(outside, "a.md"), []byte("canary beside the checkout: pangolin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	target := outside
	if relative {
		target = filepath.Join("..", filepath.Base(outside))
	}
	if err := os.Symlink(target, filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	// The condition holds: git still lists the path, and it now resolves outside.
	if !strings.Contains(gitT(t, root, "ls-files", "-s"), "dir/a.md") {
		t.Fatal("fixture: dir/a.md not listed")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "dir", "a.md")); !strings.Contains(string(data), "pangolin") {
		t.Fatal("fixture: the path does not resolve to the canary")
	}
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"pangolin"}})
	if !strings.HasPrefix(out, "search: 0 hits") || strings.Contains(out, "canary") {
		t.Errorf("a file outside the checkout was read:\n%s", out)
	}
}
