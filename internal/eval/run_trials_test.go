package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// TestRunTrials: Runs 3 over two rows runs six trials, serially, rows in
// the order given and trials innermost, each in its own sandbox
// <stamp>/<row>/<i> under one stamp, each a session of its own.
func TestRunTrials(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	o.Runs = 3
	res, err := Run(context.Background(), o, []Row{named("a", "p", "true"), named("b", "p", "false")})
	if err != nil {
		t.Fatal(err)
	}
	r.clean(res)
	if len(res) != 6 {
		t.Fatalf("got %d results, want 6: %+v", len(res), res)
	}
	stampDir := filepath.Join(r.home, harness.StateDir, "eval", "2026-10-26-080000")
	for i, x := range res {
		row, trial, status := "a", i%3+1, "PASS"
		if i >= 3 {
			row, status = "b", "FAIL"
		}
		run := fmt.Sprintf("2026-10-26-080000/%s/%d", row, trial)
		dir := filepath.Join(stampDir, row, fmt.Sprint(trial))
		if x.Row != row || x.Trial != trial || x.Run != run || x.Dir != dir || x.Status != status {
			t.Errorf("result %d: %s#%d %s %s %s; want %s#%d %s %s %s", i, x.Row, x.Trial, x.Run, x.Dir, x.Status, row, trial, run, dir, status)
		}
		if _, err := os.Stat(filepath.Join(dir, "out.txt")); err != nil {
			t.Errorf("trial %s: no session output: %v", run, err)
		}
	}
	if n := strings.TrimSpace(r.read("agy.n")); n != "6" {
		t.Errorf("agy ran %s times, want 6", n)
	}
	if got := strings.Count(r.out.String(), "eval: a  PASS"); got != 3 {
		t.Errorf("%d PASS lines for a, want 3:\n%s", got, r.out.String())
	}
}

// TestRunRunsRefused: a trial count outside 1 to MaxRuns is refused before
// anything is made; 0 is one trial.
func TestRunRunsRefused(t *testing.T) {
	r := newRig(t)
	for _, n := range []int{-1, MaxRuns + 1} {
		o := r.opts()
		o.Runs = n
		if res, err := Run(context.Background(), o, []Row{named("a", "p", "true")}); err == nil || !strings.Contains(err.Error(), "want 1 to 25") || res != nil {
			t.Errorf("runs %d: %v %v, want a refusal", n, res, err)
		}
	}
	if _, err := os.Stat(filepath.Join(r.home, harness.StateDir, "eval")); !os.IsNotExist(err) {
		t.Errorf("a refused run made the sandbox root: %v", err)
	}
	x := one(t, r.run(named("a", "p", "true")))
	if x.Trial != 1 || filepath.Base(x.Dir) != "1" {
		t.Errorf("Runs 0: trial %d in %s, want one trial in .../a/1", x.Trial, x.Dir)
	}
}
