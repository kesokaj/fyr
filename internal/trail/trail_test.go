package trail

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// signedRepo is a repository whose own config makes `git log` run a program:
// log.showSignature plus gpg.program, and one commit signed by that program.
// The program writes marker when git asks it to verify.
func signedRepo(t *testing.T) (root, marker string) {
	t.Helper()
	root = t.TempDir()
	marker = filepath.Join(t.TempDir(), "gpg-ran")
	prog := filepath.Join(t.TempDir(), "fake-gpg")
	// The program drains stdin before answering: git writes the payload to
	// it, and a program that exits first can fail the signing under load
	// ("gpg failed to sign", security review round 2).
	script := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = \"--verify\" ] && { cat >/dev/null; touch '" + marker + "'; exit 0; }; done\n" +
		"cat >/dev/null\necho '[GNUPG:] SIG_CREATED D 1 8 00 0 0' >&2\nprintf -- '-----BEGIN PGP SIGNATURE-----\\n\\nx\\n-----END PGP SIGNATURE-----\\n'\n"
	if err := os.WriteFile(prog, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	os.MkdirAll(filepath.Join(root, ".agents", "docs", "handoffs"), 0o755)
	os.WriteFile(filepath.Join(root, ".agents", "docs", "handoffs", "h.md"), []byte("h\n"), 0o644)
	git("add", ".agents")
	git("-c", "gpg.program="+prog, "-c", "user.signingkey=x", "commit", "-q", "-S", "-m", "signed")
	git("config", "gpg.program", prog)
	git("config", "log.showSignature", "true")
	// The fixture must trip the marker with the very format the code asks
	// for, minus the hardening (silence is not evidence).
	if out, err := exec.Command("git", "-C", root, "log", "--format=%ct", "--name-only", "--no-renames", "--", ".agents/docs").CombinedOutput(); err != nil {
		t.Fatalf("plain git log: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the fixture does not run gpg.program on a plain git log, so it proves nothing: %v", err)
	}
	os.Remove(marker)
	return root, marker
}

// Dates runs git log in project roots taken from transcripts: a repository's
// own config must not make it start a program (security review 2026-10-08).
func TestDatesRunsNoSignatureProgram(t *testing.T) {
	root, marker := signedRepo(t)
	dates, _ := Dates(root, ".agents/docs")
	if len(dates) == 0 {
		t.Fatal("Dates read nothing; the test would pass vacuously")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git log ran the repository's gpg.program")
	}
}

// Log is the one way the metrics store and the dreamer run git log in a
// project root (internal/metrics commitsIn uses it too).
func TestLogRunsNoSignatureProgram(t *testing.T) {
	root, marker := signedRepo(t)
	out, err := Log(root, "--format=%H %ct", "--all")
	if err != nil || len(out) == 0 {
		t.Fatalf("Log: %v, %q", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git log ran the repository's gpg.program")
	}
}

// filterRepo is a repository whose .gitattributes routes every .md file
// through a clean filter its own config defines; the filter writes marker.
// One tracked record is then edited, one left as committed, one added
// untracked.
func filterRepo(t *testing.T) (root, marker string) {
	t.Helper()
	root = t.TempDir()
	marker = filepath.Join(t.TempDir(), "filter-ran")
	prog := filepath.Join(t.TempDir(), "fake-clean")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\ntouch '"+marker+"'\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	docs := filepath.Join(root, ".agents", "docs", "handoffs")
	os.MkdirAll(docs, 0o755)
	os.WriteFile(filepath.Join(docs, "kept.md"), []byte("kept\n"), 0o644)
	os.WriteFile(filepath.Join(docs, "edited.md"), []byte("before\n"), 0o644)
	git("init", "-q")
	git("add", ".agents")
	git("commit", "-q", "-m", "records")
	os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.md filter=x\n"), 0o644)
	git("config", "filter.x.clean", prog)
	os.WriteFile(filepath.Join(docs, "edited.md"), []byte("after, longer\n"), 0o644)
	os.WriteFile(filepath.Join(docs, "new.md"), []byte("new\n"), 0o644)
	// kept.md's content and size are unchanged but its mtime moves: status
	// cannot settle it from the index's stat data and must hash it, through
	// the filter, every time (a size change alone is decided without one).
	later := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(docs, "kept.md"), later, later)
	// The fixture must trip the marker with a plain status (silence is not evidence).
	if out, err := exec.Command("git", "-C", root, "status", "--porcelain", "--", ".agents/docs").CombinedOutput(); err != nil {
		t.Fatalf("plain git status: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the fixture does not run the clean filter on a plain git status, so it proves nothing: %v", err)
	}
	os.Remove(marker)
	return root, marker
}

// Dates runs no program the repository's config defines (no clean filter,
// security review round 2), and still tells an edited record from one as
// committed.
func TestDatesRunsNoCleanFilter(t *testing.T) {
	root, marker := filterRepo(t)
	dates, modified := Dates(root, ".agents/docs")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Dates ran the repository's clean filter")
	}
	kept, edited := ".agents/docs/handoffs/kept.md", ".agents/docs/handoffs/edited.md"
	if _, ok := dates[kept]; !ok || modified[kept] {
		t.Errorf("the record as committed: dated %v, modified %v; want dated and not modified", ok, modified[kept])
	}
	if !modified[edited] {
		t.Error("the edited record is not reported modified")
	}
	if _, ok := dates[".agents/docs/handoffs/new.md"]; ok {
		t.Error("an untracked record got a commit date")
	}
}

// Every git call into a project root carries GIT_NO_LAZY_FETCH=1: a partial
// clone would otherwise fetch through its own core.sshCommand.
func TestCommandsNeverLazyFetch(t *testing.T) {
	cmd := Command("/r", "log")
	found := false
	for _, e := range cmd.Env {
		found = found || e == "GIT_NO_LAZY_FETCH=1"
	}
	if !found {
		t.Fatal("a hardened git command can lazy-fetch")
	}
	if got := strings.Join(cmd.Args[1:], " "); got != "--no-optional-locks -c core.fsmonitor=false -c log.showSignature=false -C /r log" {
		t.Errorf("args %q", got)
	}
}

// A partial clone is not read on a git older than 2.44: that git ignores
// GIT_NO_LAZY_FETCH and would fetch through the repository's own
// core.sshCommand (security review round 3). On a newer git it is read like
// any checkout, the policy search applies: refusing it there dated the
// dreamer's whole trail by mtime (audit 2026-10-25, SD17).
func TestPartialCloneIsReadOnlyOnANewGit(t *testing.T) {
	root, _ := filterRepo(t)
	if out, err := exec.Command("git", "-C", root, "config", "extensions.partialClone", "origin").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	saved := lazyFetchSafe
	t.Cleanup(func() { lazyFetchSafe = saved })

	lazyFetchSafe = func() bool { return false }
	if dates, _ := Dates(root, ".agents/docs"); len(dates) != 0 {
		t.Fatalf("a partial clone was read on an old git: %v", dates)
	}
	if out, err := Log(root, "--oneline"); err == nil {
		t.Fatalf("Log ran in a partial clone on an old git: %q", out)
	}

	lazyFetchSafe = func() bool { return true }
	dates, modified := Dates(root, ".agents/docs")
	if len(dates) == 0 {
		t.Fatal("a partial clone on a git that honours GIT_NO_LAZY_FETCH got no commit dates")
	}
	if kept := ".agents/docs/handoffs/kept.md"; modified[kept] {
		t.Errorf("%s, unedited, reads as modified in a partial clone", kept)
	}
	if edited := ".agents/docs/handoffs/edited.md"; !modified[edited] {
		t.Errorf("%s, edited, is not reported modified in a partial clone", edited)
	}
	if _, err := Log(root, "--oneline"); err != nil {
		t.Fatalf("Log refused a partial clone on a new git: %v", err)
	}
	// A plain checkout is read whatever the git.
	lazyFetchSafe = func() bool { return false }
	plain, _ := filterRepo(t)
	if !Readable(plain) {
		t.Error("a checkout that is no partial clone reads as unreadable")
	}
}

// Toplevel answers a parent of the directory or the directory itself: a
// repository's core.worktree cannot point a session at another project.
func TestToplevelIsAParent(t *testing.T) {
	root, _ := filterRepo(t)
	sub := filepath.Join(root, ".agents", "docs")
	if got := Toplevel(sub); got != root {
		t.Errorf("Toplevel(sub) = %q, want %q", got, root)
	}
	elsewhere := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "config", "core.worktree", elsewhere).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := Toplevel(sub); got != filepath.Clean(sub) {
		t.Errorf("with core.worktree elsewhere, Toplevel(sub) = %q, want the directory itself", got)
	}
}

func TestVersionAtLeast(t *testing.T) {
	for out, want := range map[string]bool{
		"git version 2.47.3\n":                 true,
		"git version 2.44.0":                   true,
		"git version 2.43.7":                   false,
		"git version 2.39.5 (Apple Git-154)\n": false,
		"git version 3.0.0":                    true,
		"nonsense":                             false,
		"git version x.y":                      false,
	} {
		if got := versionAtLeast(out, 2, 44); got != want {
			t.Errorf("versionAtLeast(%q) = %v, want %v", out, got, want)
		}
	}
}

// A promisor remote alone makes a repository lazy-fetch, with or without
// extensions.partialClone (release review of v2.6.0, finding 1).
func TestPartialCloneSeesPromisorRemote(t *testing.T) {
	for _, cfg := range [][]string{
		{"extensions.partialClone", "origin"},
		{"remote.origin.promisor", "true"},
		{"remote.other.promisor", "TRUE"},
		{"remote.a b.promisor", "true"},
		{"remote.tab\there.promisor", "yes"},
	} {
		root := t.TempDir()
		for _, args := range [][]string{{"init", "-q"}, {"config", cfg[0], cfg[1]}} {
			if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
		}
		if !PartialClone(root) {
			t.Errorf("%s=%s: not seen as a partial clone", cfg[0], cfg[1])
		}
	}
	root := t.TempDir()
	exec.Command("git", "-C", root, "init", "-q").Run()
	exec.Command("git", "-C", root, "config", "remote.origin.promisor", "false").Run()
	if PartialClone(root) {
		t.Error("remote.origin.promisor=false read as a partial clone")
	}
}

// changed reads files only inside the checkout: a tracked record whose
// directory became a symlink to the outside is changed, never hashed there
// (security review of v2.6.0, part 2, finding 3).
func TestChangedStaysInsideTheCheckout(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	dir := filepath.Join(root, ".agents", "docs", "handoffs")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "h.md"), []byte("same\n"), 0o644)
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "h")
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "h.md"), []byte("same\n"), 0o644) // identical bytes outside
	os.RemoveAll(dir)
	if err := os.Symlink(outside, dir); err != nil {
		t.Skip("no symlinks here")
	}
	diff, ok := changed(root, ".agents/docs")
	if !ok || !diff[".agents/docs/handoffs/h.md"] {
		t.Fatalf("a record behind a symlinked directory read as unchanged (ok=%v): it was hashed outside the checkout", ok)
	}
}

// DocsRoot is the one resolver the other packages share (project review
// 2026-10-09, row 15): `.agents/docs` first, then `docs`, a directory only.
func TestDocsRoot(t *testing.T) {
	for name, tc := range map[string]struct {
		dirs, files []string
		want        string
	}{
		"agents docs":    {[]string{".agents/docs"}, nil, ".agents/docs"},
		"docs":           {[]string{"docs"}, nil, "docs"},
		"both":           {[]string{".agents/docs", "docs"}, nil, ".agents/docs"},
		"none":           {nil, nil, ""},
		"docs is a file": {nil, []string{"docs"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			for _, d := range tc.dirs {
				if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, f := range tc.files {
				if err := os.WriteFile(filepath.Join(root, f), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := DocsRoot(root); got != tc.want {
				t.Errorf("DocsRoot = %q, want %q", got, tc.want)
			}
		})
	}
}
