package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestGitDirClean is PLAN.md item 45, finding S1: the host's upload-pack
// reads the clone's .git, so nothing in it may lead elsewhere. Each case
// names what must be reported.
func TestGitDirClean(t *testing.T) {
	for _, c := range []struct {
		name  string
		plant func(t *testing.T, git string)
		want  string
	}{
		{"clean", func(*testing.T, string) {}, ""},
		{"alternates", func(t *testing.T, git string) {
			put(t, filepath.Join(git, "objects", "info", "alternates"), "/other/.git/objects\n", 0o644)
		}, "objects/info/alternates"},
		{"commondir", func(t *testing.T, git string) { put(t, filepath.Join(git, "commondir"), "/other\n", 0o644) }, "commondir"},
		{"gitdir", func(t *testing.T, git string) { put(t, filepath.Join(git, "gitdir"), "/other\n", 0o644) }, "gitdir"},
		{"a linked ref", func(t *testing.T, git string) {
			if err := os.Symlink("/other/.git/refs/heads/main", filepath.Join(git, "refs", "heads", "w")); err != nil {
				t.Fatal(err)
			}
		}, "refs/heads/w"},
		{"a FIFO at config", func(t *testing.T, git string) {
			p := filepath.Join(git, "config")
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(p, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "config"},
		{"a ref too big", func(t *testing.T, git string) {
			put(t, filepath.Join(git, "refs", "heads", "big"), strings.Repeat("0", maxRefFile+1), 0o644)
		}, "refs/heads/big"},
		{"a config too big", func(t *testing.T, git string) {
			put(t, filepath.Join(git, "config"), strings.Repeat("#", maxConfigFile+1), 0o644)
		}, "config"},
		{"a HEAD too big", func(t *testing.T, git string) {
			put(t, filepath.Join(git, "HEAD"), strings.Repeat("0", maxRefFile+1), 0o644)
		}, "HEAD"},
		{"a .git file", func(t *testing.T, git string) {
			if err := os.RemoveAll(git); err != nil {
				t.Fatal(err)
			}
			put(t, git, "gitdir: /other/.git\n", 0o644)
		}, ".git is not a directory"},
		{"a linked .git", func(t *testing.T, git string) {
			if err := os.RemoveAll(git); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/other/.git", git); err != nil {
				t.Fatal(err)
			}
		}, ".git is not a directory"},
		{"no .git", func(t *testing.T, git string) {
			if err := os.RemoveAll(git); err != nil {
				t.Fatal(err)
			}
		}, ".git is not a directory"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			b := r.prepared(r.config("gitdir"))
			c.plant(t, filepath.Join(b.App(), ".git"))
			got := b.GitIntact()
			if c.want == "" {
				if !got || len(b.Reasons()) != 0 {
					t.Fatalf("a clean clone: %v %v\n%s", got, b.Reasons(), r.out.String())
				}
				return
			}
			if got || !b.Has(ReasonReplaced) {
				t.Fatalf("GitIntact %v, reasons %v", got, b.Reasons())
			}
			if !strings.Contains(r.out.String(), c.want) {
				t.Errorf("the note does not name %s:\n%s", c.want, r.out.String())
			}
		})
	}
}
