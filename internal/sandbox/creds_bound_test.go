package sandbox

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
	const target = "/proc/self/status"
	st, err := os.Stat(target)
	if err != nil || st.Size() != 0 {
		t.Skipf("%s is not a size-0 file here: %v", target, err)
	}
	old := maxScan
	maxScan = 4
	t.Cleanup(func() { maxScan = old })
	dir := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "innocent")); err != nil {
		t.Fatal(err)
	}
	f := scanCreds(dir)
	got, ok := f["innocent"]
	if !ok {
		t.Fatalf("scan %v, want a finding for the link", f)
	}
	if !strings.Contains(got.problem, "larger than") {
		t.Errorf("problem %q, want the size refusal", got.problem)
	}
}
