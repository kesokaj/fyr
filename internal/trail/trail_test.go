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
	os.MkdirAll(filepath.Join(root, "notes", "handoffs"), 0o755)
	os.WriteFile(filepath.Join(root, "notes", "handoffs", "h.md"), []byte("h\n"), 0o644)
	git("add", "notes")
	git("-c", "gpg.program="+prog, "-c", "user.signingkey=x", "commit", "-q", "-S", "-m", "signed")
	git("config", "gpg.program", prog)
	git("config", "log.showSignature", "true")
	// The fixture must trip the marker with the very format the code asks
	// for, minus the hardening (silence is not evidence).
	if out, err := exec.Command("git", "-C", root, "log", "--format=%ct", "--name-only", "--no-renames", "--", "notes").CombinedOutput(); err != nil {
		t.Fatalf("plain git log: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the fixture does not run gpg.program on a plain git log, so it proves nothing: %v", err)
	}
	os.Remove(marker)
	return root, marker
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
	docs := filepath.Join(root, "notes", "handoffs")
	os.MkdirAll(docs, 0o755)
	os.WriteFile(filepath.Join(docs, "kept.md"), []byte("kept\n"), 0o644)
	os.WriteFile(filepath.Join(docs, "edited.md"), []byte("before\n"), 0o644)
	git("init", "-q")
	git("add", "notes")
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
	if out, err := exec.Command("git", "-C", root, "status", "--porcelain", "--", "notes").CombinedOutput(); err != nil {
		t.Fatalf("plain git status: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the fixture does not run the clean filter on a plain git status, so it proves nothing: %v", err)
	}
	os.Remove(marker)
	return root, marker
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
// any checkout, the policy search applies (audit 2026-10-25, SD17).
func TestPartialCloneIsReadOnlyOnANewGit(t *testing.T) {
	root, _ := filterRepo(t)
	if out, err := exec.Command("git", "-C", root, "config", "extensions.partialClone", "origin").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	saved := lazyFetchSafe
	t.Cleanup(func() { lazyFetchSafe = saved })

	lazyFetchSafe = func() bool { return false }
	if Readable(root) {
		t.Fatal("a partial clone reads as readable on an old git")
	}
	if out, err := Log(root, "--oneline"); err == nil {
		t.Fatalf("Log ran in a partial clone on an old git: %q", out)
	}

	lazyFetchSafe = func() bool { return true }
	if !Readable(root) {
		t.Fatal("a partial clone reads as unreadable on a git that honours GIT_NO_LAZY_FETCH")
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
	sub := filepath.Join(root, "notes")
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

// Both marker names count, the old one kept for trees from before the
// rename; any other base name does not (plan fyr-check D20).
func TestNoIndexMarkers(t *testing.T) {
	for p, want := range map[string]bool{
		".fyr-noindex":               true,
		"vendor/.sh-agent-noindex":   true,
		"a/b/.fyr-noindex":           true,
		"fyr-noindex":                false,
		".fyr-noindex/x.md":          false,
		"docs/.sh-agent-noindex.bak": false,
	} {
		if got := IsNoIndexMarker(p); got != want {
			t.Errorf("IsNoIndexMarker(%q) = %v, want %v", p, got, want)
		}
	}
}

// textconvRepo is a repository whose tracked .gitattributes routes f.txt
// through a textconv driver its own config defines; the driver writes
// marker. Two commits change f.txt.
func textconvRepo(t *testing.T) (root, marker string) {
	t.Helper()
	root = t.TempDir()
	marker = filepath.Join(t.TempDir(), "textconv-ran")
	prog := filepath.Join(t.TempDir(), "fake-conv")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\ntouch '"+marker+"'\ncat \"$1\"\n"), 0o755); err != nil {
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
	os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("f.txt diff=probe\n"), 0o644)
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("one\n"), 0o644)
	git("add", "-A")
	git("commit", "-q", "-m", "one")
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("two\n"), 0o644)
	git("commit", "-q", "-a", "-m", "two")
	git("config", "diff.probe.textconv", prog)
	// The fixture must trip the marker with the very arguments the code
	// passes, minus the hardening (silence is not evidence).
	for _, args := range [][]string{{"log", "-G", "two", "--format=%H", "--", "f.txt"}, {"log", "-p", "-1", "--", "f.txt"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("plain git %v: %v\n%s", args, err, out)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("the fixture does not run its driver on a plain git %v, so it proves nothing: %v", args, err)
		}
		os.Remove(marker)
	}
	return root, marker
}

// Log never starts a textconv program the checkout picks, for either
// content diff a caller asks for (security review 2026-10-25, finding 1:
// fyr check's -G).
func TestLogRunsNoTextconv(t *testing.T) {
	root, marker := textconvRepo(t)
	for _, args := range [][]string{{"-G", "two", "--format=%H", "--", "f.txt"}, {"-p", "-1", "--", "f.txt"}} {
		out, err := Log(root, args...)
		if err != nil || len(out) == 0 {
			t.Fatalf("Log %v: %v, %q", args, err, out)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("Log %v ran the checkout's textconv driver", args)
		}
	}
}
