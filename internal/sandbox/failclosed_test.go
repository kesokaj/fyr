//go:build linux || darwin

package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestExitChecksProcsUnreadable: the error reaches the box as `procs`.
func TestExitChecksProcsUnreadable(t *testing.T) {
	r := newRig(t)
	b := r.prepared(r.config("procs"))
	old := procDir
	t.Cleanup(func() { procDir = old })
	procDir = filepath.Join(t.TempDir(), "missing")
	b.ExitChecks()
	if !b.Has(ReasonProcs) {
		t.Errorf("reasons %v, want %s\n%s", b.Reasons(), ReasonProcs, r.out.String())
	}
}

// TestScanCredsUnreadableRoot is item 49's creds.go read: a tree that cannot
// be opened is a finding, never an empty scan.
func TestScanCredsUnreadableRoot(t *testing.T) {
	if left := newCreds(nil, scanCreds(filepath.Join(t.TempDir(), "gone"))); len(left) != 1 || !strings.Contains(left[0], "unreadable") {
		t.Errorf("newCreds of an unreadable root: %v", left)
	}
}

// TestAfterUnreadableErrFile is item 49's after.go read: err.txt is where a
// denial is counted, so one that cannot be read after a session fails the
// box in the graded direction rather than reading as no denial.
func TestAfterUnreadableErrFile(t *testing.T) {
	for _, c := range []struct {
		name    string
		mutate  func(path string) error
		started bool
		want    string
	}{
		{"removed", os.Remove, true, ReasonReplaced},
		{"a link", func(p string) error {
			if err := os.Remove(p); err != nil {
				return err
			}
			return os.Symlink("/etc/hostname", p)
		}, true, ReasonReplaced},
		{"no session ran", os.Remove, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			b := r.prepared(r.config("errtxt"))
			errTxt := filepath.Join(b.Dir(), "err.txt")
			put(t, errTxt, "fine\n", 0o600)
			if err := c.mutate(errTxt); err != nil {
				t.Fatal(err)
			}
			b.started = c.started
			b.After()
			if c.want == "" {
				if len(b.Reasons()) != 0 {
					t.Errorf("reasons %v", b.Reasons())
				}
				return
			}
			if !b.Has(c.want) {
				t.Errorf("reasons %v, want %s", b.Reasons(), c.want)
			}
		})
	}
}

// TestCountLinesScanError: a line over the scanner's buffer is an error, not
// the count so far, so a denial past it is not lost.
func TestCountLinesScanError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	put(t, p, strings.Repeat("x", 17<<20)+"\nauto-denied\n", 0o600)
	if _, err := countLines(p, "auto-denied"); err == nil {
		t.Error("an unreadable line passed")
	}
}

// TestIntegrityReasons is PLAN.md item 51: the one list eval's ingest and
// work read.
func TestIntegrityReasons(t *testing.T) {
	for _, r := range IntegrityReasons {
		if !Compromised([]string{"x", r}) {
			t.Errorf("%s does not read as compromised", r)
		}
	}
	for _, r := range []string{ReasonExists, ReasonProcs, ReasonReplaced, ReasonWrite} {
		if !slices.Contains(IntegrityReasons, r) {
			t.Errorf("%s left the list", r)
		}
	}
	if Compromised([]string{ReasonDenied, ReasonCredentials, ReasonRealConfig}) {
		t.Error("a graded reason whose sessions are sound reads as compromised")
	}
}
