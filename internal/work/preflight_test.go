package work

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// refusalCases are TestRunRefusals' rows: each setup returns the Kickoff
// file and the error the refusal names.
var refusalCases = []struct {
	name  string
	setup func(r *rig, o *Options) string // returns the Kickoff file
	want  string
}{
	{"not-a-checkout", func(r *rig, o *Options) string {
		o.Root = filepath.Join(r.tmp, "plain")
		mkdir(r.t, o.Root)
		return r.kickoff("a", "src/a.txt")
	}, "is not a git checkout"},
	{"untracked", func(r *rig, o *Options) string {
		put(r.t, filepath.Join(r.lead, "new.txt"), "x")
		return r.kickoff("a", "src/a.txt")
	}, "uncommitted or untracked"},
	{"dirty-trail", func(r *rig, o *Options) string {
		f, _ := os.OpenFile(filepath.Join(r.lead, trailRel), os.O_WRONLY|os.O_APPEND, 0)
		f.WriteString("\n")
		f.Close()
		return r.kickoff("a", "src/a.txt")
	}, "uncommitted or untracked"},
	{"no-files-line", func(r *rig, o *Options) string {
		p := filepath.Join(r.kick, "a.md")
		put(r.t, p, "Project: x\n")
		return p
	}, "no Files: line"},
	{"bad-name", func(r *rig, o *Options) string { return r.kickoff("Fix_A", "src/a.txt") }, "the name"},
	{"branch-exists", func(r *rig, o *Options) string {
		r.git("-C", r.lead, "branch", "work/a")
		return r.kickoff("a", "src/a.txt")
	}, "already exists"},
	{"no-identity", func(r *rig, o *Options) string {
		r.git("-C", r.lead, "config", "--unset", "user.email")
		return r.kickoff("a", "src/a.txt")
	}, "no git user.name and user.email"},
	{"root-in-checkout", func(r *rig, o *Options) string {
		o.Home = filepath.Join(r.lead, ".agents", "scratch", "home")
		return r.kickoff("a", "src/a.txt")
	}, "inside a git checkout"},
	{"no-login", func(r *rig, o *Options) string {
		os.Remove(filepath.Join(r.real, sandbox.TokenRel))
		return r.kickoff("a", "src/a.txt")
	}, "no Antigravity login"},
	{"no-agy", func(r *rig, o *Options) string {
		git, err := exec.LookPath("git")
		if err != nil {
			r.t.Fatal(err)
		}
		only := filepath.Join(r.tmp, "git-only")
		mkdir(r.t, only)
		os.Symlink(git, filepath.Join(only, "git"))
		r.t.Setenv("PATH", only)
		return r.kickoff("a", "src/a.txt")
	}, "agy not found"},
	{"short-timeout", func(r *rig, o *Options) string {
		o.Timeout = 9 * time.Minute
		return r.kickoff("a", "src/a.txt")
	}, "at least 10m"},
	{"model-flag", func(r *rig, o *Options) string {
		o.Model = "--mode=auto"
		return r.kickoff("a", "src/a.txt")
	}, "--model"},
	{"part-minute", func(r *rig, o *Options) string {
		o.Timeout = 10*time.Minute + 30*time.Second
		return r.kickoff("a", "src/a.txt")
	}, "whole minutes"},
}

// TestRunRefusals is one test per refusal of Phase 2 (1-4 and 7-10, and
// the git identity D25 needs): each is an error before anything is made,
// so no directory exists under the sandbox root.
func TestRunRefusals(t *testing.T) {
	for _, c := range refusalCases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			// The git identity must come from the checkout alone.
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(r.real, ".config"))
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			o := r.opts()
			file := c.setup(r, &o)
			_, err := runWith(o, file)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
			if _, err := os.Lstat(sandbox.Root(o.Home, "work")); !os.IsNotExist(err) {
				t.Errorf("a refusal made the sandbox root: %v", err)
			}
			if r.out.Len() != 0 {
				t.Errorf("a refusal printed %q", r.out.String())
			}
		})
	}
}
