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
