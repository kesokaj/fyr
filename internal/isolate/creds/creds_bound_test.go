package creds

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScanBoundsAFileStatSizeHides: a /proc file stats as regular with size 0,
// so the size check passes it, and a link a session plants reaches one on the
// host: the read itself must stop at maxScan (the v5.5.0 gate, finding S1).
func TestScanBoundsAFileStatSizeHides(t *testing.T) {
	t.Parallel()
	const target = "/proc/self/status"
	st, err := os.Stat(target)
	if err != nil || st.Size() != 0 {
		t.Skipf("%s is not a size-0 file here: %v", target, err)
	}
	opts := Options{MaxScan: 4}
	dir := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "innocent")); err != nil {
		t.Fatal(err)
	}
	f := ScanWith(dir, opts)
	got, ok := f["innocent"]
	if !ok {
		t.Fatalf("scan %v, want a finding for the link", f)
	}
	if !strings.Contains(got.problem, "larger than") {
		t.Errorf("problem %q, want the size refusal", got.problem)
	}
}

// TestReadBoundedStopsAtTheBound: the read itself stops, whatever the size
// says: the refusal alone would also pass an unbounded read of a file that
// ends.
func TestReadBoundedStopsAtTheBound(t *testing.T) {
	t.Parallel()
	const bound int64 = 4
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := readBoundedLimit(p, bound)
	if err != nil || len(b) != 5 {
		t.Errorf("read %d bytes, %v, want 5", len(b), err)
	}
}
