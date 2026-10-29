package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The records baseline that cannot be written fails the records step (step
// 3, eval's own; the other prepare arms are internal/sandbox's tests). The
// row is built as Run builds one, its directory made by the test.
func TestPrepareRecordsBaselineRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	r := newRig(t)
	o := r.opts()
	o.Fixture = r.root
	dir := filepath.Join(r.home, "eval", "p")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	w := newRow(o, named("p", "say ok", "true"), dir, "2026-10-26-0800")
	t.Cleanup(w.b.ExitChecks) // closes the held root
	sb := w.b.Home()
	if err := os.MkdirAll(filepath.Join(sb, ".gemini", "antigravity-cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sb, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sb, 0o755) })
	if w.prepare(context.Background()) || strings.Join(w.b.Reasons(), "; ") != ReasonRecords {
		t.Errorf("reasons %v, want [%s]", w.b.Reasons(), ReasonRecords)
	}
}
