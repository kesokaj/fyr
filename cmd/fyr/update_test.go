package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// `update --vendor bogus` checked the vendor only after it had fetched and
// pulled the checkout, exiting 1; on --from-zip it exited 1 and under
// --dry-run not at all (the release gate's review of v2.6.2..13c5bde,
// BUG-171). The flag is gone with the second vendor (plan fyr Phase 2), and
// any --vendor, a once-valid value too, is a usage error: exit 2 before update
// does anything, on every path.
func TestUpdateRefusesTheVendorFlagFirst(t *testing.T) {
	repo, marker := gitFixture(t)
	// run() resolves --repo as a harness checkout, which needs skills/.
	if err := os.MkdirAll(filepath.Join(repo, "skills", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills", "x", "SKILL.md"), []byte("---\nname: x\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "--quiet", "-m", "skills"}, {"push", "--quiet", "origin", "HEAD:refs/heads/main"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(gitCleanEnv(), "GIT_CONFIG_GLOBAL="+filepath.Join(filepath.Dir(repo), "gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	advanceOrigin(t, repo)
	head := func() string {
		out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	before := head()
	for name, args := range map[string][]string{
		"checkout": {"update", "--repo", repo, "--vendor", "agy"},
		"dry run":  {"update", "--dry-run", "--repo", repo, "--vendor", "agy"},
		"zip":      {"update", "--from-zip", filepath.Join(t.TempDir(), "x.zip"), "--vendor", "agy"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(append(args, "--home", t.TempDir()), &out)
			var code exitCode
			if !errors.As(err, &code) || code != 2 || !strings.Contains(out.String(), "flag provided but not defined: -vendor") {
				t.Errorf("err = %v, want exit 2 naming the flag\n%s", err, out.String())
			}
		})
	}
	if got := head(); got != before {
		t.Errorf("the checkout moved from %s to %s before the flag was refused", before, got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("an install ran")
	}
}

// TestUpdateNeverInstallsDirtyTree is BUG-046: `update` deploys the working
// tree, not the commit it fetched, so a dirty checkout must not be installed
// on EITHER path. Pull already refused it when behind; the up-to-date path
// had no guard, and an hourly timer put a session's uncommitted edits into
// the operator's real home. --check only reports, so it still works dirty.
func TestUpdateNeverInstallsDirtyTree(t *testing.T) {
	installed := func(marker string) bool {
		_, err := os.Stat(marker)
		return err == nil
	}
	dirty := func(t *testing.T, repo string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, "context", "AGENTS.md"), []byte("# edited, not committed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	behind := advanceOrigin

	t.Run("clean and up to date installs", func(t *testing.T) {
		repo, marker := gitFixture(t)
		var out bytes.Buffer
		if err := update(repo, t.TempDir(), false, "", &out); err != nil {
			t.Fatalf("clean update: %v\n%s", err, out.String())
		}
		if !installed(marker) {
			t.Error("a clean checkout should have been installed")
		}
	})

	t.Run("dirty and up to date refuses", func(t *testing.T) {
		repo, marker := gitFixture(t)
		dirty(t, repo)
		var out bytes.Buffer
		err := update(repo, t.TempDir(), false, "", &out)
		if err == nil {
			t.Fatal("a dirty checkout must not be installed")
		}
		if !strings.Contains(err.Error(), "uncommitted changes") {
			t.Errorf("error should name the uncommitted changes, got %v", err)
		}
		if installed(marker) {
			t.Error("nothing should have been installed from a dirty tree")
		}
	})

	t.Run("dirty and behind refuses without pulling", func(t *testing.T) {
		repo, marker := gitFixture(t)
		before := harness.Inspect(repo).Commit
		behind(t, repo)
		dirty(t, repo)
		var out bytes.Buffer
		err := update(repo, t.TempDir(), false, "", &out)
		if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
			t.Fatalf("dirty and behind must refuse, got %v", err)
		}
		if installed(marker) {
			t.Error("nothing should have been installed")
		}
		if after := harness.Inspect(repo).Commit; after != before {
			t.Errorf("the tree moved from %s to %s; a refused update pulls nothing", before, after)
		}
	})

	// --check and --dry-run resolve the same guard as the real run: a dirty
	// checkout is refused, exit 1, whether or not it is behind; a clean one
	// keeps the documented exit 2 when behind (audit 2026-09-30, IN4).
	t.Run("dirty and behind with --check refuses like the real run", func(t *testing.T) {
		repo, marker := gitFixture(t)
		behind(t, repo)
		dirty(t, repo)
		var out bytes.Buffer
		err := update(repo, t.TempDir(), true, "", &out)
		var code exitCode
		if err == nil || errors.As(err, &code) || !strings.Contains(err.Error(), "uncommitted changes") {
			t.Fatalf("--check on a dirty checkout must refuse with the real run's message, got %v", err)
		}
		if installed(marker) {
			t.Error("a check installed something")
		}
	})

	t.Run("clean and behind with --check exits 2", func(t *testing.T) {
		repo, _ := gitFixture(t)
		behind(t, repo)
		var out bytes.Buffer
		err := update(repo, t.TempDir(), true, "", &out)
		var code exitCode
		if !errors.As(err, &code) || code != 2 {
			t.Fatalf("--check clean and behind: %v, want exit 2", err)
		}
	})

	t.Run("clean and behind pulls then installs", func(t *testing.T) {
		repo, marker := gitFixture(t)
		before := harness.Inspect(repo).Commit
		behind(t, repo)
		var out bytes.Buffer
		if err := update(repo, t.TempDir(), false, "", &out); err != nil {
			t.Fatalf("clean and behind: %v\n%s", err, out.String())
		}
		if after := harness.Inspect(repo).Commit; after == before {
			t.Error("a clean checkout behind upstream should have been fast-forwarded")
		}
		if !installed(marker) {
			t.Error("it should have been installed after the pull")
		}
	})

	// Until 2026-09-30 --check reported "up to date" on a dirty tree and
	// exited 0, a promise the real run refuses; a read-only mode resolves
	// every guard the real run does (plan audit-fix D32, IN4).
	t.Run("--check on a dirty tree refuses like the real run and installs nothing", func(t *testing.T) {
		repo, marker := gitFixture(t)
		dirty(t, repo)
		var out bytes.Buffer
		err := update(repo, t.TempDir(), true, "", &out)
		if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
			t.Fatalf("--check on a dirty tree must refuse with the real run's message, got %v", err)
		}
		if !strings.Contains(out.String(), "up to date") {
			t.Errorf("--check should still report the state first, got %q", out.String())
		}
		if installed(marker) {
			t.Error("--check installs nothing")
		}
	})
}

// TestReadSettingsProblems covers the reader's refusals, including the open
// failure the verifier reported as untested. A problem is named without a
// byte of the file's contents.
// TestUpdateRefusesWhenStatusIsUnknown closes the coverage gap the round-2
// verifier named: `update`'s StatusUnknown branch had no cmd-level test.
// "git could not answer" must refuse, not deploy — a checkout whose state
// cannot be read is not a clean one.
func TestUpdateRefusesWhenStatusIsUnknown(t *testing.T) {
	repo, marker := gitFixture(t)
	// Break git's view of the tree while leaving the checkout recognisable:
	// the objects are gone, so `status` fails where `rev-parse` may not.
	if err := os.RemoveAll(filepath.Join(repo, ".git", "objects")); err != nil {
		t.Fatal(err)
	}
	if info := harness.Inspect(repo); !info.StatusUnknown && !info.Dirty {
		t.Skip("this git reports a readable status without its object store; the branch is covered in internal/harness")
	}
	var out bytes.Buffer
	err := update(repo, t.TempDir(), false, "", &out)
	if err == nil {
		t.Fatal("a checkout whose status git cannot report must not be installed")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("nothing should have been installed")
	}
}

// The checkout path of update has the same shape: Reinstall writes
// ~/.local/bin/fyr, and the process that ran update — a copy at a
// system path, or a `go run` build — is not that file (BUG-079).
func TestCheckoutUpdateNamesTheBinaryItRanFrom(t *testing.T) {
	repo, _ := gitFixture(t)
	home := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	byName := filepath.Join(elsewhere, "fyr")
	if err := os.Symlink(self, byName); err != nil {
		t.Fatal(err)
	}
	// Appended, so git and go stay reachable; no earlier entry holds THIS
	// executable under the name, so the report is the appended one.
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+elsewhere)
	t.Setenv("HOME", home) // the note is for the real home only
	installed := filepath.Join(home, harness.BinPath)
	// The fixture's built binary writes a marker, not ~/.local/bin/fyr;
	// the note requires an installed file to be behind, so plant one — a
	// different file from this process, as after any real install.
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := "note: this update ran from " + byName + ", not from the installed binary " + installed

	var out bytes.Buffer
	if err := update(repo, home, true, "", &out); err != nil {
		t.Fatalf("update --check: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "not from the installed binary") {
		t.Errorf("--check, which changes nothing, described a replacement:\n%s", out.String())
	}
	out.Reset()
	if err := update(repo, home, false, "", &out); err != nil {
		t.Fatalf("update: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), want) {
		t.Errorf("checkout update did not name the binary it ran from:\nwant %q\n%s", want, out.String())
	}
}
