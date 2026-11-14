package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func appendConfig(t *testing.T, git, s string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(git, "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

// TestCloneTakesNoTemplate: an operator's init.templateDir may hold links,
// which GitIntact would read as the session's (P12b's round 2).
func TestCloneTakesNoTemplate(t *testing.T) {
	r := newRig(t)
	tpl := filepath.Join(r.tmp, "tpl")
	if err := os.MkdirAll(filepath.Join(tpl, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/true", filepath.Join(tpl, "hooks", "pre-commit")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(r.real, ".gitconfig"), "[init]\n\ttemplateDir = "+tpl+"\n", 0o644)
	b := r.prepared(r.config("template"))
	if !b.GitIntact() {
		t.Errorf("reasons %v\n%s", b.Reasons(), r.out.String())
	}
}

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
			if err := os.Symlink("/other/.git/refs/heads/main", filepath.Join(refsHeads(git), "w")); err != nil {
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
			put(t, filepath.Join(refsHeads(git), "big"), strings.Repeat("0", maxRefFile+1), 0o644)
		}, "refs/heads/big"},
		{"an include", func(t *testing.T, git string) {
			appendConfig(t, git, "[include]\n\tpath = big\n")
		}, "include.path"},
		{"a conditional include", func(t *testing.T, git string) {
			appendConfig(t, git, "[includeIf \"gitdir:/\"]\n\tpath = big\n")
		}, "includeif."},
		{"a worktree config", func(t *testing.T, git string) {
			appendConfig(t, git, "[extensions]\n\tworktreeConfig = true\n")
		}, "extensions.worktreeconfig"},
		{"uploadpack packObjectsHook", func(t *testing.T, git string) {
			appendConfig(t, git, "[uploadpack]\n\tpackObjectsHook = /bin/true\n")
		}, "uploadpack."},
		{"core.hooksPath", func(t *testing.T, git string) {
			appendConfig(t, git, "[core]\n\thooksPath = /bad/hooks\n")
		}, "core.hookspath"},
		{"core.fsmonitor", func(t *testing.T, git string) {
			appendConfig(t, git, "[core]\n\tfsmonitor = /bin/true\n")
		}, "core.fsmonitor"},
		{"core.sshCommand", func(t *testing.T, git string) {
			appendConfig(t, git, "[core]\n\tsshCommand = /bin/true\n")
		}, "core.sshcommand"},
		{"pack windowMemory", func(t *testing.T, git string) {
			appendConfig(t, git, "[pack]\n\twindowMemory = 1g\n")
		}, "pack."},
		{"filter clean", func(t *testing.T, git string) {
			appendConfig(t, git, "[filter \"lfs\"]\n\tclean = /bin/true\n")
		}, "filter."},
		{"a config git cannot read", func(t *testing.T, git string) {
			appendConfig(t, git, "[broken\n")
		}, "config"},
		{"a config too big", func(t *testing.T, git string) {
			put(t, filepath.Join(git, "config"), strings.Repeat("#", maxConfigFile+1), 0o644)
		}, "config"},
		{"a reftable too big", func(t *testing.T, git string) {
			if err := os.MkdirAll(filepath.Join(git, "reftable"), 0o755); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(git, "reftable", "0001.ref"), strings.Repeat("0", maxConfigFile+1), 0o644)
		}, "reftable/0001.ref"},
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
			if refsHeads(filepath.Join(b.App(), ".git")) != filepath.Join(b.App(), ".git", "refs", "heads") {
				c.want = strings.Replace(c.want, "refs/heads/", "refs/", 1)
			}
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

// refsHeads is where a planted loose ref goes: refs/heads, or refs itself
// when the clone uses the reftable format, where refs/heads is a stub file
// (a git whose system config sets feature.experimental, as this Mac's does).
func refsHeads(git string) string {
	if fi, err := os.Stat(filepath.Join(git, "refs", "heads")); err == nil && fi.IsDir() {
		return filepath.Join(git, "refs", "heads")
	}
	return filepath.Join(git, "refs")
}
