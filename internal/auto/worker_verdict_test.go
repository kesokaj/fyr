package auto

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/isolate"
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
		res           isolate.WorkerResult
		code          int
	}{
		{"fail", "FAIL (no commits)", isolate.WorkerResult{Verdict: "FAIL", Reasons: []string{"no commits"}}, 1},
		{"pass", "PASS", isolate.WorkerResult{Verdict: "PASS"}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			root := idleDocDriftProject(t)
			writeConfig(t, home, validConfig)
			var out bytes.Buffer
			o := driftOpts(home, root, &out)
			// Deterministic long preflight refusal with isolate.BuildLine so
			// docker fallback reaches the lineLimit cut without relying on host
			// Landlock or an Antigravity login token.
			o.IsolateAvailable = func() error {
				return errors.New("landlock disabled; build it: " + isolate.BuildLine + " " + strings.Repeat("x", 120))
			}
			o.DockerPreflight = func(isolate.WorkerOptions) error { return nil }
			var branch string
			o.Work = func(_ context.Context, _ isolate.WorkerOptions, paths ...string) ([]isolate.WorkerResult, error) {
				name := strings.TrimSuffix(filepath.Base(paths[0]), ".md")
				branch = "work/" + name
				if c.res.Verdict == "PASS" {
					git(t, root, "branch", branch)
				}
				res := c.res
				res.Name = name
				return []isolate.WorkerResult{res}, nil
			}
			o.Apply = func(string, string, string) (bool, error) { return false, nil }
			if err := Run(o, false); exit(err) != c.code {
				t.Fatalf("exit %d, want %d: %v\n%s", exit(err), c.code, err, out.String())
			}
			st, _, _ := loadState(home)
			got := st.Projects[root].Verdict
			if branch == "" || !strings.HasSuffix(got, "...") || !strings.Contains(got, "runner docker (--runner isolate: landlock disabled") {
				t.Fatalf("precondition: a worker ran with fallback docker and its line reached the %d-rune cut:\n%s", lineLimit, got)
			}
			want := c.verdict + "; branch " + branch + " kept; runner docker ("
			if !strings.Contains(got, want) {
				t.Errorf("project verdict lost %q:\n%s", want, got)
			}
			if log := logOf(t, home); !strings.Contains(log, want) {
				t.Errorf("auto.log lost %q:\n%s", want, log)
			}
		})
	}
}
