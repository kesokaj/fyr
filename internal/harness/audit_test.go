package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// The audit of 2026-09-18 (`reviews/2026-09-18-review-harness-merged.md`):
// each test here pins one finding.

// Finding 6: only a canonical vX.Y.Z is a release; a non-canonical spelling
// compared equal to the running version and reached the download.
func TestIsReleaseVersionIsCanonical(t *testing.T) {
	for v, want := range map[string]bool{
		"v1.2.3": true, "v0.0.0": true, "v10.20.30": true,
		"v1.02.2": false, "v01.2.2": false, "v1.2.02": false,
		"v1.2": false, "1.2.3": false, "v1.2.3-rc1": false, "v-1.2.3": false,
		"v99999999999999999999.0.0": false, // overflows int: releaseParts would read it as 0
	} {
		if got := IsReleaseVersion(v); got != want {
			t.Errorf("IsReleaseVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestNonCanonicalTagNeverReachesTheDownload(t *testing.T) {
	r := newRelease(t, "v9.9.9")
	r.env(t, true)
	t.Setenv("GH_TAG", "v1.02.2")
	home := t.TempDir()
	dest := installedBinary(t, home)
	before, _ := os.ReadFile(dest)
	var out bytes.Buffer
	err := UpdateFromRelease(home, "v1.0.0", false, nil, &out)
	if err == nil || !strings.Contains(err.Error(), "not a vX.Y.Z release") {
		t.Fatalf("a non-canonical tag was not refused before the download: err=%v\n%s", err, out.String())
	}
	if after, _ := os.ReadFile(dest); !bytes.Equal(before, after) {
		t.Errorf("the installed binary changed")
	}
	if left, _ := filepath.Glob(filepath.Join(home, StateDir, "release-*")); len(left) != 0 {
		t.Errorf("a download directory exists: %v", left)
	}
}

// Finding 7: the BUG-079 note is about the shell that runs `fyr` from
// the real home; a --home elsewhere is a scratch or test place and gets no
// advice to relink anything. And "point it at the installed binary" is only
// offered for a file the caller owns.
func TestNoteRunningElsewhereOnlyForTheRealHome(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(self, filepath.Join(elsewhere, "fyr")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", elsewhere)
	home := t.TempDir()
	binAt(t, filepath.Join(home, ".local", "bin"))

	t.Setenv("HOME", t.TempDir()) // the real home is somewhere else
	var out bytes.Buffer
	NoteRunningElsewhere(home, &out)
	if out.Len() != 0 {
		t.Errorf("a --home that is not the real home got the note:\n%s", out.String())
	}

	t.Setenv("HOME", home)
	out.Reset()
	NoteRunningElsewhere(home, &out)
	if !strings.Contains(out.String(), "not from the installed binary") {
		t.Errorf("the real home got no note:\n%s", out.String())
	}
}

// Finding 1: adopt keeps group/other write bits that install strips.
func TestAdoptStripsGroupAndOtherWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root's umask makes mode assertions meaningless")
	}
	repo := newRepo(t)
	home := t.TempDir()
	src := filepath.Join(t.TempDir(), "widget")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: widget\ndescription: a widget\n---\nbody\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(src, "SKILL.md"), 0o666); err != nil {
		t.Fatal(err)
	}
	dest, err := Adopt(repo, home, src, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		t.Errorf("adopted file is group- or other-writable: %o", info.Mode().Perm())
	}
}

// Finding 14 and the 0% body: Diff never uses the system temp dir. Since
// IN24 it writes no temp file at all (the plan reaches diff on stdin), so an
// unusable temp dir changes nothing and the runtime dir gains no file.
func TestDiffUsesTheHomeNotTmp(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &out); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(home, AgyRoot, "skills", "alpha", "SKILL.md")
	if err := os.WriteFile(victim, []byte("drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
	// diff inspects; it never tightens the runtime dir (security round 2).
	if err := os.Chmod(filepath.Join(home, StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Diff(home, files, &out); err != nil {
		t.Fatalf("Diff with an unusable system temp dir: %v", err)
	}
	if !strings.Contains(out.String(), "-drifted") || !strings.Contains(out.String(), "skills/alpha/SKILL.md") {
		t.Errorf("Diff printed no hunk for the drifted file:\n%s", out.String())
	}
	if left, _ := filepath.Glob(filepath.Join(home, StateDir, "fyr-diff-*")); len(left) != 0 {
		t.Errorf("Diff left temp files: %v", left)
	}
	if info, _ := os.Stat(filepath.Join(home, StateDir)); info.Mode().Perm() != 0o755 {
		t.Errorf("diff changed the runtime dir's mode to %o", info.Mode().Perm())
	}
}

// Finding 15: cell must not cut a rune in half.
func TestCellIsRuneSafe(t *testing.T) {
	s := strings.Repeat("é", 40) + " " + strings.Repeat("ü", 40)
	for max := 5; max < 120; max++ {
		got := cell(s, max)
		if !utf8.ValidString(got) {
			t.Fatalf("cell(…, %d) produced invalid UTF-8: %q", max, got)
		}
	}
}

// Finding 5, second round: the runtime directory holds the brain store's
// global feedback and proposals; it is 0700 after every install, whatever it
// was before.
func TestInstallMakesTheConfigDirPrivate(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root's umask makes mode assertions meaningless")
	}
	repo := newRepo(t)
	for _, pre := range []string{"absent", "0755"} {
		t.Run(pre, func(t *testing.T) {
			home := t.TempDir()
			cfg := filepath.Join(home, StateDir)
			if pre == "0755" {
				if err := os.MkdirAll(cfg, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			files, err := Plan(DirSource(repo), nil)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := Install(DirSource(repo), home, files, false, &out); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0o077 != 0 {
				t.Errorf("after install, %s is %o, want 0700", cfg, info.Mode().Perm())
			}
			line := "mode    ~/.config/fyr (0755 -> 0700)"
			if has := strings.Contains(out.String(), line); has != (pre == "0755") {
				t.Errorf("mode line printed=%v for a %s dir:\n%s", has, pre, out.String())
			}
		})
	}

	t.Run("dry run names it and leaves it", func(t *testing.T) {
		home := t.TempDir()
		cfg := filepath.Join(home, StateDir)
		if err := os.MkdirAll(cfg, 0o755); err != nil {
			t.Fatal(err)
		}
		files, err := Plan(DirSource(repo), nil)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := Install(DirSource(repo), home, files, true, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "mode    ~/.config/fyr (0755 -> 0700)") {
			t.Errorf("dry run did not name the mode change:\n%s", out.String())
		}
		if info, _ := os.Stat(cfg); info.Mode().Perm() != 0o755 {
			t.Errorf("dry run changed the mode to %o", info.Mode().Perm())
		}
	})

	t.Run("a symlinked runtime dir is left alone", func(t *testing.T) {
		home := t.TempDir()
		target := filepath.Join(t.TempDir(), "elsewhere")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(home, StateDir)); err != nil {
			t.Fatal(err)
		}
		files, err := Plan(DirSource(repo), nil)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := Install(DirSource(repo), home, files, false, &out); err != nil {
			t.Fatal(err)
		}
		if info, _ := os.Stat(target); info.Mode().Perm() != 0o755 {
			t.Errorf("install chmodded the target of a symlinked runtime dir to %o", info.Mode().Perm())
		}
		if strings.Contains(out.String(), "mode    ~/.config/fyr") {
			t.Errorf("install claimed a mode change through a symlink:\n%s", out.String())
		}
		if !strings.Contains(out.String(), "note: ~/.config/fyr is a symlink; its target's mode is yours to set (0700 keeps the brain store's global feedback private)") {
			t.Errorf("install did not name the symlink it left alone:\n%s", out.String())
		}
	})
}

// The reviewer's low: a lexical compare of the home path misses a symlinked
// $HOME; identity is os.SameFile here as everywhere else.
func TestNoteRunningElsewhereThroughASymlinkedHome(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(self, filepath.Join(elsewhere, "fyr")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", elsewhere)
	home := t.TempDir()
	binAt(t, filepath.Join(home, ".local", "bin"))
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link) // the shell's home is the link; --home is the target
	var out bytes.Buffer
	NoteRunningElsewhere(home, &out)
	if !strings.Contains(out.String(), "not from the installed binary") {
		t.Errorf("a symlinked $HOME suppressed the note:\n%s", out.String())
	}
}
