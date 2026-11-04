package auto

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/work"
)

// TestSweepLineKeepsVerdict pins BUG-506: the real docker preflight's
// refusal on a development build carries the image build line, so the
// runner's reason is about 300 runes, and the project line, cut at
// lineLimit, lost the worker's verdict and branch when the runner came
// first. The verdict and branch come first and the runner last, so a cut
// takes only the end of the runner's reason.
func TestSweepLineKeepsVerdict(t *testing.T) {
	for _, c := range []struct {
		name, verdict string
		res           work.Result
		code          int
	}{
		{"fail", "FAIL (no commits)", work.Result{Verdict: "FAIL", Reasons: []string{"no commits"}}, 1},
		{"pass", "PASS", work.Result{Verdict: "PASS"}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			root := idleDocDriftProject(t)
			writeConfig(t, home, validConfig)
			var out bytes.Buffer
			o := driftOpts(home, root, &out)
			// The real preflight: Version "" refuses as a development build
			// before any docker call or Keychain read.
			o.DockerPreflight = nil
			var branch string
			o.Work = func(_ context.Context, _ work.Options, paths ...string) ([]work.Result, error) {
				name := strings.TrimSuffix(filepath.Base(paths[0]), ".md")
				branch = "work/" + name
				if c.res.Verdict == "PASS" {
					git(t, root, "branch", branch)
				}
				res := c.res
				res.Name = name
				return []work.Result{res}, nil
			}
			o.Apply = func(string, string, string) (bool, error) { return false, nil }
			if err := Run(o, false); exit(err) != c.code {
				t.Fatalf("exit %d, want %d: %v\n%s", exit(err), c.code, err, out.String())
			}
			st, _, _ := loadState(home)
			got := st.Projects[root].Verdict
			if branch == "" || !strings.HasSuffix(got, "...") || !strings.Contains(got, "runner host (this fyr is a development build") {
				t.Fatalf("precondition: a worker ran on the host and its line reached the %d-rune cut:\n%s", lineLimit, got)
			}
			want := c.verdict + "; branch " + branch + " kept; runner host ("
			if !strings.Contains(got, want) {
				t.Errorf("project verdict lost %q:\n%s", want, got)
			}
			if log := logOf(t, home); !strings.Contains(log, want) {
				t.Errorf("auto.log lost %q:\n%s", want, log)
			}
		})
	}
}
