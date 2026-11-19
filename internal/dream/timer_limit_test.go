package dream

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A wedged manager cannot hang a removal: the call ends at the limit, and a
// strict timer reports it.
func TestRemoveManagerCallHasALimit(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	prev := managerLimit
	managerLimit = 50 * time.Millisecond
	t.Cleanup(func() { managerLimit = prev })
	timer := Timer{Unit: "fyr-limit", Plist: "com.fyr.limit", RemoveVerb: "verb", StrictStop: true}
	home := t.TempDir()
	unit, _ := timer.Names(home)
	dir := filepath.Join(home, ".config", "systemd", "user")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, unit+".timer"), []byte("x"), 0o644)
	start := time.Now()
	if _, err := timer.Remove(home, "linux"); err == nil {
		t.Error("a timed-out stop went unsaid")
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestInstallLimitAndRemoveReloadStrict(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nif [ \"$2\" = \"daemon-reload\" ]; then exit 1; fi\nif [ \"$2\" = \"enable\" ]; then exec sleep 60; fi\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	prev := managerLimit
	managerLimit = 50 * time.Millisecond
	t.Cleanup(func() { managerLimit = prev })

	home := timerHome(t)
	start := time.Now()
	if err := installTimer(home, "/usr/bin:/bin", "linux"); err == nil {
		t.Error("expected install error from failing/timed-out systemctl")
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("install took %v", d)
	}

	// Remove with failing daemon-reload surfaces error when strict=true, ignores when false.
	timer := Timer{Unit: "fyr-reload", Plist: "com.fyr.reload", RemoveVerb: "verb", StrictStop: true}
	unit, _ := timer.Names(home)
	dir := filepath.Join(home, ".config", "systemd", "user")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, unit+".timer"), []byte("x"), 0o644)
	if _, err := timer.Remove(home, "linux"); err == nil {
		t.Error("strict remove ignored daemon-reload failure")
	}
	timer.StrictStop = false
	os.WriteFile(filepath.Join(dir, unit+".timer"), []byte("x"), 0o644)
	if ok, err := timer.Remove(home, "linux"); err != nil || !ok {
		t.Errorf("non-strict remove = (%v, %v), want (true, nil)", ok, err)
	}
}
