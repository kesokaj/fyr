package auto

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/isolate"
)

// TestSweepLineKeepsVerdict pins BUG-506: long FAIL reasons before
// "; branch <b> kept" are trimmed to maxBeforeBranch so the verdict, kept
// branch, and trailing "; runner ..." always fit within lineLimit.
func TestSweepLineKeepsVerdict(t *testing.T) {
	longReason := "no commits (" + strings.Repeat("x", lineLimit) + ")"
	for _, c := range []struct {
		name, prefix string
		res          isolate.WorkerResult
		code         int
	}{
		{"fail", "FAIL (no commits", isolate.WorkerResult{Verdict: "FAIL", Reasons: []string{longReason}}, 1},
		{"pass", "PASS", isolate.WorkerResult{Verdict: "PASS"}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			root := idleDreamProject(t, home)
			writeConfig(t, home, validConfig)
			var out bytes.Buffer
			o := driftOpts(home, root, &out)
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
			if err := Run(o, false); exit(err) != c.code {
				t.Fatalf("exit %d, want %d: %v\n%s", exit(err), c.code, err, out.String())
			}
			st, _, _ := loadState(home)
			got := st.Projects[root].Verdict
			if branch == "" || !strings.Contains(got, "; "+c.prefix) {
				t.Fatalf("verdict %q did not contain %q", got, c.prefix)
			}
			tail := "; branch " + branch + " kept; runner isolate"
			if !strings.HasSuffix(got, tail) {
				t.Errorf("project verdict lost %q:\n%s", tail, got)
			}
			if c.name == "fail" && len([]rune(got)) >= lineLimit {
				t.Errorf("capped FAIL verdict exceeded lineLimit (%d): %d", lineLimit, len([]rune(got)))
			}
			if log := logOf(t, home); !strings.Contains(log, c.prefix) || !strings.Contains(log, tail) {
				t.Errorf("auto.log lost %q / %q:\n%s", c.prefix, tail, log)
			}
		})
	}
}
