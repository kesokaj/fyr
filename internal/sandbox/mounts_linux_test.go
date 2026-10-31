package sandbox

import (
	"context"
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
	nbsp := "36 35 0:50 /a\u00a0b /s/box/app/nb rw - fuse.x x rw"
	fakeMounts(t, mountLine("/"), mountLine("/s/box/app/mnt"), mountLine(`/s/box/home/with\040space`), mountLine("/s/box2/app"), nbsp, mountLine("/s/box/app/\u00a0n"))
	got, err := mountsUnder("/s/box")
	if err != nil || strings.Join(got, ",") != "/s/box/app/mnt,/s/box/home/with space,/s/box/app/nb,/s/box/app/\u00a0n" {
		t.Errorf("got %q, %v", got, err)
	}
}

// TestMountChains: every mount above a root, in the table's order.
func TestMountChains(t *testing.T) {
	fakeMounts(t, "1 0 0:1 / / rw - x x rw", "2 1 0:2 / /s rw - x x rw", "4 1 0:4 / /s/ab rw - x x rw")
	got, err := mountChains("/s/a", "/s/ab/c", "/t")
	if err != nil || got["/s/a"] != "1 /\n2 /s" || got["/s/ab/c"] != "1 /\n2 /s\n4 /s/ab" || got["/t"] != "1 /" {
		t.Errorf("%q, %v", got, err)
	}
}

// TestIntactParentMount is the checkpoint's security finding S2: a file
// system mounted above the sandbox leaves its paths resolving as approved.
func TestIntactParentMount(t *testing.T) {
	root := "1 0 0:1 / / rw - x x rw"
	fakeMounts(t, root)
	r := newRig(t)
	b := r.prepared(r.config("parent"))
	if !b.Intact() {
		t.Fatalf("no new mount, yet %v", b.Reasons())
	}
	fakeMounts(t, root, "99 1 0:9 / "+filepath.Dir(b.resolved[b.dir])+" rw - fuse.x x rw")
	if b.Intact() || !b.Has(ReasonReplaced) {
		t.Errorf("reasons %v, want %s", b.Reasons(), ReasonReplaced)
	}
	// Above the covering mount: the longest prefix alone stays the same.
	dir := filepath.Dir(b.resolved[b.dir])
	cover := "50 1 0:5 / " + dir + " rw - x x rw"
	fakeMounts(t, root, cover)
	r2 := r.box(r.config("ancestor"))
	if !r2.Make() || !prepare(context.Background(), r2) || !r2.Intact() {
		t.Fatalf("prepare: %v", r2.Reasons())
	}
	fakeMounts(t, root, cover, "98 1 0:8 / "+filepath.Dir(dir)+" rw - x x rw")
	if r2.Intact() || !r2.Has(ReasonReplaced) {
		t.Errorf("a mount above the covering mount: reasons %v", r2.Reasons())
	}
	if !strings.Contains(r.out.String(), "above") {
		t.Errorf("the note: %s", r.out.String())
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
