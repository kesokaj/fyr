package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeMounts(t *testing.T, lines ...string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mountinfo")
	put(t, p, strings.Join(lines, "\n")+"\n", 0o600)
	old := mountInfo
	mountInfo = p
	t.Cleanup(func() { mountInfo = old })
}

func mountLine(point string) string {
	return "36 35 0:50 / " + point + " rw,nosuid - fuse.x x rw"
}

// TestMountsUnderReadsTheRealTable proves the parser against the kernel's
// own file: /proc is a mount on every Linux box.
func TestMountsUnderReadsTheRealTable(t *testing.T) {
	found, err := mountsUnder("/proc")
	if err != nil || len(found) == 0 || found[0] != "/proc" {
		t.Errorf("mountsUnder(/proc): %v, %v", found, err)
	}
	if found, err := mountsUnder(filepath.Join(t.TempDir(), "x")); err != nil || len(found) != 0 {
		t.Errorf("an unmounted path: %v, %v", found, err)
	}
}

func TestMountsUnder(t *testing.T) {
	fakeMounts(t, mountLine("/"), mountLine("/s/box/app/mnt"), mountLine(`/s/box/home/with\040space`), mountLine("/s/box2/app"))
	got, err := mountsUnder("/s/box")
	if err != nil || strings.Join(got, ",") != "/s/box/app/mnt,/s/box/home/with space" {
		t.Errorf("got %q, %v", got, err)
	}
}

// TestIntactMount is PLAN.md item 50: a file system mounted under the clone
// or the sandbox home fails the box sandbox replaced, and an unreadable
// table fails it too.
func TestIntactMount(t *testing.T) {
	for _, under := range []string{"app", "home"} {
		t.Run(under, func(t *testing.T) {
			r := newRig(t)
			b := r.prepared(r.config("mount"))
			if !b.Intact() {
				t.Fatalf("a fresh sandbox is not intact: %v", b.Reasons())
			}
			fakeMounts(t, mountLine("/"), mountLine(filepath.Join(b.resolved[b.dir], under, "mnt")))
			if b.Intact() || !b.Has(ReasonReplaced) {
				t.Errorf("reasons %v, want %s", b.Reasons(), ReasonReplaced)
			}
			if b.Remove() {
				t.Error("a sandbox with a mount under it was removed")
			}
			if _, err := os.Stat(b.Dir()); err != nil {
				t.Error(err)
			}
		})
	}
	t.Run("unreadable", func(t *testing.T) {
		r := newRig(t)
		b := r.prepared(r.config("mount"))
		old := mountInfo
		mountInfo = filepath.Join(t.TempDir(), "gone")
		t.Cleanup(func() { mountInfo = old })
		if b.Intact() || !b.Has(ReasonReplaced) {
			t.Errorf("reasons %v, want %s", b.Reasons(), ReasonReplaced)
		}
	})
}
