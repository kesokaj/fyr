package dream

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeManager puts a recording systemctl or launchctl first on PATH; code is
// its exit status. The real user manager is never reached (R6).
func fakeManager(t *testing.T, name string, code int) (calls string) {
	t.Helper()
	bin := t.TempDir()
	calls = filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\necho \"$*\" >> \"" + calls + "\"\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	return calls
}

func timerHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	// The test's home counts as the real one, so the bare unit names apply.
	prev := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = prev })
	write(t, filepath.Join(home, ".local", "bin", "fyr"), "#!/bin/sh\n", fresh)
	return home
}

func callsOf(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestRemoveTimerSaysWhatItRemoved: the caller prints "removed" only when a
// unit of this home was on disk and is gone (the v2.1.0 gate: a removal that
// found nothing reported success).
func TestRemoveTimerSaysWhatItRemoved(t *testing.T) {
	fakeManager(t, "systemctl", 0)
	home := timerHome(t)
	if err := InstallTimer(home, "/usr/bin:/bin", "linux"); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveTimer(home, "linux"); err != nil || !removed {
		t.Errorf("an installed timer: removed=%v err=%v, want true", removed, err)
	}
	if removed, err := RemoveTimer(home, "linux"); err != nil || removed {
		t.Errorf("nothing installed: removed=%v err=%v, want false", removed, err)
	}
}
