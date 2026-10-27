package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A write open of a name the agent hard-linked to another file is refused
// before anything is truncated, so the other link keeps its bytes (IN49 of
// the 2026-10-27 audit, plan D15). A fresh name and a file of one link open
// and truncate as before.
func TestOpenRegularRefusesAHardLink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "operator-file")
	put(t, victim, "keep me", 0o600)
	log := filepath.Join(dir, "check.log")
	if err := os.Link(victim, log); err != nil {
		t.Fatal(err)
	}
	if f, err := openRegular(log, os.O_CREATE|os.O_WRONLY|os.O_TRUNC); err == nil {
		f.Close()
		t.Error("a hard-linked check.log was opened for writing")
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep me" {
		t.Errorf("the other link holds %q: truncated through the hard link", b)
	}
	single := filepath.Join(dir, "out.txt")
	put(t, single, "old output", 0o600)
	f, err := openRegular(single, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("new")
	f.Close()
	if b, _ := os.ReadFile(single); string(b) != "new" {
		t.Errorf("a file of one link holds %q, want it truncated and rewritten", b)
	}
	f, err = openRegular(filepath.Join(dir, "fresh.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	// Reading a hard-linked file harms nothing and stays allowed.
	if f, err := openRegular(log, os.O_RDONLY); err != nil {
		t.Errorf("a read of a hard-linked file: %v", err)
	} else {
		f.Close()
	}
}

// A conversation database over the scan cap is not read, and says so,
// instead of passing or silently failing check f (IN51, plan D16).
func TestInvokedSkipsAnOversizedDatabase(t *testing.T) {
	sb := t.TempDir()
	conv := filepath.Join(sb, ".gemini", "antigravity-cli", "conversations")
	put(t, filepath.Join(conv, "big.db"), "SQLite format 3\x00{\"TypeName\":\"reviewer\"}\x00", 0o600)
	orig := maxScan
	maxScan = 8
	t.Cleanup(func() { maxScan = orig })
	ok, notes := invokedNotes(sb, "reviewer")
	if ok {
		t.Error("a database over the cap was read")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "big.db") || !strings.Contains(notes[0], "not read") {
		t.Errorf("notes %q, want one naming big.db", notes)
	}
}
