package harness

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The arms of Install and Plan that their splits move into helpers, pinned
// before the splits (PLAN-2026-10-27-funclen Phase 5, D13). Exempt (D12):
// saveOnError's dry-run return, which no caller reaches (every call sits on
// a path a dry run never takes); the failed chmod of an unchanged file's
// mode, which needs a file the installing user does not own; and Plan's
// d.Info error, which needs the entry removed between ReadDir and Info.

// installed plans and installs repo into a fresh home.
func installed(t *testing.T, repo string) (string, []File) {
	t.Helper()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	return home, files
}

// readOnlyConfigDir makes home's ~/.config/fyr unwritable, which install's
// mode correction leaves alone (no group or other bits).
func readOnlyConfigDir(t *testing.T, home string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	dir := filepath.Join(home, StateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
}

func TestInstallUnreadableManifest(t *testing.T) {
	repo := newRepo(t)
	home, files := installed(t, repo)
	writeFile(t, statePath(home), "not json\n")
	err := Install(DirSource(repo), home, files, false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "parsing "+statePath(home)) {
		t.Errorf("err = %v, want the manifest's parse error", err)
	}
}

// A failed write whose manifest cannot be saved either names both.
func TestInstallWriteErrorManifestUnsaved(t *testing.T) {
	repo := newRepo(t)
	home, files := installed(t, repo)
	notes := filepath.Join(home, AgyRoot, "skills", "alpha", "references", "notes.md")
	if err := os.Remove(notes); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(notes, "inside"), "a directory where a file is deployed\n")
	readOnlyConfigDir(t, home)
	err := Install(DirSource(repo), home, files, false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "writing "+notes) || !strings.Contains(err.Error(), "(and the manifest could not be saved: ") {
		t.Errorf("err = %v, want the write error and the unsaved manifest", err)
	}
}

// A dry run names a stale file's removal and leaves it on disk.
func TestInstallDryRunKeepsStale(t *testing.T) {
	repo := newRepo(t)
	home, files := installed(t, repo)
	var kept []File
	var stale string
	for _, f := range files {
		if strings.HasSuffix(f.Rel, "notes.md") {
			stale = f.Rel
			continue
		}
		kept = append(kept, f)
	}
	var out bytes.Buffer
	if err := Install(DirSource(repo), home, kept, true, &out); err != nil {
		t.Fatal(err)
	}
	if want := "remove  ~/" + stale + " (no longer in the source)\n"; !strings.Contains(out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, out.String())
	}
	if !strings.Contains(out.String(), "[dry-run] 0 written, ") || !strings.Contains(out.String(), ", 1 removed, ") {
		t.Errorf("summary is not the dry run's:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, stale)); err != nil {
		t.Errorf("a dry run removed the stale file: %v", err)
	}
}

// A checkout whose state git cannot report is recorded as such, never as a
// bare commit.
func TestInstallStatusUnknown(t *testing.T) {
	repo := newRepo(t)
	gitInit(t, repo)
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "fixture")
	short := gitIn(t, repo, "rev-parse", "--short", "HEAD")
	writeFile(t, filepath.Join(repo, ".git", "index"), "garbage\n")
	home, _ := installed(t, repo)
	st, _, err := LoadState(home)
	if err != nil {
		t.Fatal(err)
	}
	if st.Commit != short+"+status-unknown" {
		t.Errorf("commit = %q, want %q", st.Commit, short+"+status-unknown")
	}
}

func TestInstallManifestUnsaved(t *testing.T) {
	repo := newRepo(t)
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	readOnlyConfigDir(t, home)
	var out bytes.Buffer
	err = Install(DirSource(repo), home, files, false, &out)
	if err == nil || !strings.HasPrefix(err.Error(), "saving manifest: ") {
		t.Errorf("err = %v, want the manifest's save error", err)
	}
	if strings.Contains(out.String(), "files managed") {
		t.Errorf("a failed save printed the summary:\n%s", out.String())
	}
}

// A skill file or directory Plan cannot read fails the plan, naming the
// skill.
func TestPlanSkillUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-0 file")
	}
	for _, c := range []struct{ name, rel string }{
		{"file", "data.txt"},
		{"directory", "locked"},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo := newRepo(t)
			p := filepath.Join(repo, "skills", "alpha", c.rel)
			if c.name == "directory" {
				writeFile(t, filepath.Join(p, "f"), "x")
			} else {
				writeFile(t, p, "x")
			}
			if err := os.Chmod(p, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(p, 0o755) })
			_, err := Plan(DirSource(repo), nil)
			if err == nil || !strings.HasPrefix(err.Error(), "skill alpha: ") || !errors.Is(err, fs.ErrPermission) {
				t.Errorf("err = %v, want the skill named and the permission error", err)
			}
		})
	}
}
