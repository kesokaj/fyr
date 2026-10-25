package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The state directory from before the rename moves to ~/.config/fyr once,
// in a verb that writes under it, and only when the new one is absent and
// the old one is a plain directory (plan fyr D13, F3).
func TestMigrateStateDir(t *testing.T) {
	write := func(t *testing.T, p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			return "<" + err.Error() + ">"
		}
		return string(b)
	}

	t.Run("old present, new absent: renamed", func(t *testing.T) {
		home := t.TempDir()
		old, cur := filepath.Join(home, OldStateDir), filepath.Join(home, StateDir)
		write(t, filepath.Join(old, "state.json"), `{"files":[]}`)
		write(t, filepath.Join(old, "feedback.jsonl"), "one\n")
		var out bytes.Buffer
		if err := MigrateStateDir(home, &out); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != "migrated ~/.config/sh-agent → ~/.config/fyr\n" {
			t.Errorf("output %q", got)
		}
		if _, err := os.Lstat(old); !os.IsNotExist(err) {
			t.Errorf("old dir still there: %v", err)
		}
		if got := read(filepath.Join(cur, "feedback.jsonl")); got != "one\n" {
			t.Errorf("feedback.jsonl after the move: %q", got)
		}
		// A second run has nothing to do and says nothing.
		out.Reset()
		if err := MigrateStateDir(home, &out); err != nil || out.Len() != 0 {
			t.Errorf("second run: err %v, output %q", err, out.String())
		}
	})

	t.Run("both present: neither touched, one line", func(t *testing.T) {
		home := t.TempDir()
		old, cur := filepath.Join(home, OldStateDir), filepath.Join(home, StateDir)
		write(t, filepath.Join(old, "state.json"), "old")
		write(t, filepath.Join(cur, "state.json"), "new")
		var out bytes.Buffer
		if err := MigrateStateDir(home, &out); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, "~/.config/sh-agent") || !strings.Contains(got, "fyr reads ~/.config/fyr") {
			t.Errorf("output %q", got)
		}
		if read(filepath.Join(old, "state.json")) != "old" || read(filepath.Join(cur, "state.json")) != "new" {
			t.Error("a directory changed")
		}
	})

	t.Run("old a symlink: refused, nothing moved", func(t *testing.T) {
		home := t.TempDir()
		target := filepath.Join(t.TempDir(), "dotfiles-state")
		write(t, filepath.Join(target, "state.json"), "linked")
		old := filepath.Join(home, OldStateDir)
		if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, old); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		err := MigrateStateDir(home, &out)
		if err == nil || !strings.Contains(err.Error(), "symlink") || !strings.Contains(err.Error(), "~/.config/fyr") {
			t.Fatalf("err %v, want a refusal naming the link and ~/.config/fyr", err)
		}
		if fi, err := os.Lstat(old); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("the link changed: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(home, StateDir)); !os.IsNotExist(err) {
			t.Errorf("~/.config/fyr was created: %v", err)
		}
		if read(filepath.Join(target, "state.json")) != "linked" {
			t.Error("the link's target changed")
		}
	})

	t.Run("neither: nothing", func(t *testing.T) {
		home := t.TempDir()
		var out bytes.Buffer
		if err := MigrateStateDir(home, &out); err != nil || out.Len() != 0 {
			t.Errorf("err %v, output %q", err, out.String())
		}
		if _, err := os.Lstat(filepath.Join(home, StateDir)); !os.IsNotExist(err) {
			t.Errorf("~/.config/fyr was created: %v", err)
		}
	})
}

// A read-only verb never migrates: it names the old directory and reads
// nothing from it (plan fyr D13).
func TestStateDirNotice(t *testing.T) {
	home := t.TempDir()
	if got := StateDirNotice(home); got != "" {
		t.Errorf("neither dir: %q", got)
	}
	old := filepath.Join(home, OldStateDir)
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := StateDirNotice(home); !strings.Contains(got, "run fyr install to migrate ~/.config/sh-agent") {
		t.Errorf("old only: %q", got)
	}
	if _, err := os.Lstat(old); err != nil {
		t.Errorf("the notice moved the old dir: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, StateDir)); !os.IsNotExist(err) {
		t.Errorf("the notice created ~/.config/fyr: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, StateDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := StateDirNotice(home); !strings.Contains(got, "fyr reads ~/.config/fyr") {
		t.Errorf("both: %q", got)
	}
}
