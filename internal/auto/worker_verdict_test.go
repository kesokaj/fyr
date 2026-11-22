package auto

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
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

func TestFinishWorkerTamperSaveWarnings(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses 0500 directory permissions")
	}
	home := t.TempDir()
	root := idleProject(t)
	writeConfig(t, home, validConfig)
	if err := ensureDir(home); err != nil {
		t.Fatal(err)
	}
	if err := saveState(home, State{Date: "2026-11-01", ModelRuns: 1}); err != nil {
		t.Fatal(err)
	}
	stateDir, autoDir := filepath.Join(home, harness.StateDir), dir(home)
	t.Cleanup(func() {
		_ = os.Chmod(stateDir, 0o700)
		_ = os.Chmod(autoDir, 0o700)
	})
	if err := os.Chmod(autoDir, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	r := &run{o: Options{Home: home}, cfg: defaults(), st: State{Date: "2026-11-01", ModelRuns: 1}}
	v, fs := r.finishWorker(context.Background(), root, &Project{}, Task{Class: ClassMemoryConsolidation}, Kickoff{Name: "k", Branch: "work/k"},
		isolate.RunnerIsolate, isolate.RunnerIsolate, "", nil, nil, "", checkoutSnapshot{}, nil,
		isolate.WorkerResult{Reasons: []string{isolate.ReasonRealConfig}}, nil, time.Time{})
	if !strings.Contains(v, "could not disable config:") || !strings.Contains(v, "could not save state:") {
		t.Errorf("config tamper verdict missing save warnings: %q", v)
	}
	if len(fs) != 1 || !strings.Contains(fs[0].Text, "could not disable config:") || !strings.Contains(fs[0].Text, "could not save state:") {
		t.Errorf("config tamper finding missing save warnings: %+v", fs)
	}

	home2 := t.TempDir()
	writeConfig(t, home2, validConfig)
	if err := ensureDir(home2); err != nil {
		t.Fatal(err)
	}
	if err := saveState(home2, State{Date: "2026-11-01", ModelRuns: 1}); err != nil {
		t.Fatal(err)
	}
	cfgBytes, cfgErr := readBounded(ConfigPath(home2), configLimit)
	autoDir2 := dir(home2)
	t.Cleanup(func() { _ = os.Chmod(autoDir2, 0o700) })
	if err := os.Chmod(autoDir2, 0o500); err != nil {
		t.Fatal(err)
	}
	r2 := &run{o: Options{Home: home2}, cfg: defaults(), st: State{Date: "2026-11-01", ModelRuns: 1}}
	v2, fs2 := r2.finishWorker(context.Background(), root, &Project{}, Task{Class: ClassMemoryConsolidation}, Kickoff{Name: "k", Branch: "work/k"},
		isolate.RunnerIsolate, isolate.RunnerIsolate, "", cfgBytes, cfgErr, "mismatch", checkoutSnapshot{}, nil,
		isolate.WorkerResult{Verdict: "PASS"}, nil, time.Time{})
	if !strings.Contains(v2, "FAIL (runs.json changed during the run; warning: could not restore state:") {
		t.Errorf("state tamper verdict missing restore warning: %q", v2)
	}
	if len(fs2) != 1 || !strings.Contains(fs2[0].Text, "warning: could not restore state:") {
		t.Errorf("state tamper finding missing restore warning: %+v", fs2)
	}
}
