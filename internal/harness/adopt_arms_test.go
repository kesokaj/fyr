package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The arms of Adopt that its split moves into helpers, pinned before the
// split (PLAN-2026-10-27-funclen Phase 5, D13). Exempt (D12): the two
// filepath.Abs errors, which need getcwd to fail; the second Stat of dest
// failing other than not-exist, which needs dest to change after the first;
// and the restore after a failed second rename, which needs that rename to
// fail after the first one into the same directories succeeded.

// adoptSource writes a skill directory named name outside any repo.
func adoptSource(t *testing.T, name, body string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), name)
	writeFile(t, filepath.Join(src, "SKILL.md"), "---\nname: "+name+"\ndescription: d\n---\n"+body)
	return src
}

// lockedDir makes dir/locked/f with locked read-only, so dir cannot be
// removed; the mode is restored for the temp-dir cleanup.
func lockedDir(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root removes a read-only directory's entries")
	}
	locked := filepath.Join(dir, "locked")
	writeFile(t, filepath.Join(locked, "f"), "x")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
}

func TestAdoptRefusesAnInvalidName(t *testing.T) {
	repo := newRepo(t)
	src := adoptSource(t, "bad name", "")
	if _, err := Adopt(repo, t.TempDir(), src, false); err == nil || !strings.Contains(err.Error(), "must be a single local path segment") {
		t.Errorf("err = %v, want the name refused", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a refused name reached the staging step: %v", err)
	}
}

// A dest that cannot be stat'ed for a reason other than absence stops adopt
// before anything is staged.
func TestAdoptDestStatError(t *testing.T) {
	repo := newRepo(t)
	if err := os.RemoveAll(filepath.Join(repo, "skills")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "skills"), "a file where skills/ belongs\n")
	_, err := Adopt(repo, t.TempDir(), adoptSource(t, "gamma", ""), false)
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("err = %v, want ENOTDIR", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("adopt staged a copy after the stat failed: %v", err)
	}
}

func TestAdoptScratchNotADirectory(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, ".agents"), "a file where .agents/ belongs\n")
	_, err := Adopt(repo, t.TempDir(), adoptSource(t, "gamma", ""), false)
	var pe *fs.PathError
	if !errors.As(err, &pe) || pe.Op != "mkdir" || pe.Path != filepath.Join(repo, ".agents") {
		t.Errorf("err = %v, want the scratch mkdir error", err)
	}
}

// A staging directory left by an earlier run that cannot be removed stops
// adopt before the copy.
func TestAdoptStagingNotRemovable(t *testing.T) {
	repo := newRepo(t)
	lockedDir(t, filepath.Join(repo, ".agents", "scratch", fmt.Sprintf("adopt-gamma-%d", os.Getpid())))
	_, err := Adopt(repo, t.TempDir(), adoptSource(t, "gamma", ""), false)
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("err = %v, want the staging removal refused", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "skills", "gamma")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the skill was adopted past the failed removal: %v", err)
	}
}

// A backup left by an earlier run that cannot be removed stops a forced
// adopt with the existing skill untouched.
func TestAdoptBackupNotRemovable(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	if _, err := Adopt(repo, home, adoptSource(t, "gamma", "old body\n"), false); err != nil {
		t.Fatal(err)
	}
	lockedDir(t, filepath.Join(repo, ".agents", "scratch", fmt.Sprintf("adopt-gamma-%d.previous", os.Getpid())))
	_, err := Adopt(repo, home, adoptSource(t, "gamma", "new body\n"), true)
	if !errors.Is(err, fs.ErrPermission) || strings.Contains(err.Error(), "could not move") {
		t.Errorf("err = %v, want the backup removal refused", err)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "skills", "gamma", "SKILL.md")); !strings.Contains(string(got), "old body") {
		t.Errorf("the existing skill changed: %q", got)
	}
}

// A rename into an unwritable skills/ with no previous skill of that name
// fails and keeps the prepared copy where the message says.
func TestAdoptRenameFailsWithNoPrevious(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root renames into a read-only directory")
	}
	repo := newRepo(t)
	skills := filepath.Join(repo, "skills")
	if err := os.Chmod(skills, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(skills, 0o755) })
	_, err := Adopt(repo, t.TempDir(), adoptSource(t, "gamma", ""), false)
	staging := filepath.Join(repo, ".agents", "scratch", fmt.Sprintf("adopt-gamma-%d", os.Getpid()))
	want := "there was no previous skill of that name and the prepared copy is at " + staging
	if err == nil || !strings.Contains(err.Error(), want) || !errors.Is(err, fs.ErrPermission) {
		t.Errorf("err = %v, want %q", err, want)
	}
	if _, err := os.Stat(filepath.Join(staging, "SKILL.md")); err != nil {
		t.Errorf("the prepared copy is gone: %v", err)
	}
}
