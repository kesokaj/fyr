package isolate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRegularRefusesAHardLink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	victim := filepath.Join(dir, "operator-file")
	put(t, victim, "keep me", 0o600)
	log := filepath.Join(dir, "check.log")
	if err := os.Link(victim, log); err != nil {
		t.Fatal(err)
	}
	if f, err := OpenRegular(log, os.O_CREATE|os.O_WRONLY|os.O_TRUNC); err == nil {
		f.Close()
		t.Error("a hard-linked check.log was opened for writing")
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep me" {
		t.Errorf("the other link holds %q: truncated through the hard link", b)
	}
	single := filepath.Join(dir, "out.txt")
	put(t, single, "old output", 0o600)
	f, err := OpenRegular(single, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("new")
	f.Close()
	if b, _ := os.ReadFile(single); string(b) != "new" {
		t.Errorf("a file of one link holds %q, want it truncated and rewritten", b)
	}
	f, err = OpenRegular(filepath.Join(dir, "fresh.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if f, err := OpenRegular(log, os.O_RDONLY); err != nil {
		t.Errorf("a read of a hard-linked file: %v", err)
	} else {
		f.Close()
	}
}
