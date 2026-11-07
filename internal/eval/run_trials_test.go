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
	stampDir := filepath.Join(ProjectSandboxRoot(r.home, r.root), "2026-10-26-080000")
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
	for _, i := range []string{"1", "2", "3"} {
		if got := strings.Count(r.out.String(), "eval: a#"+i+"  PASS"); got != 1 {
			t.Errorf("%d PASS lines for a#%s, want 1:\n%s", got, i, r.out.String())
		}
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

// TestRunParallel: Parallel 2 over 3 trials runs 2 trials concurrently (meeting
// a barrier of 2 while capping peak concurrency at 2) and prints notes and
// verdict lines in trial order (1, 2, 3) even when trial 2 finishes before
// trial 1; a Parallel outside 1 to MaxParallel is refused before anything is
// made (Item 23 of PLAN-2026-11-02-1806-fyr-speed).
func TestRunParallel(t *testing.T) {
	r := newRig(t)
	for _, n := range []int{-1, MaxParallel + 1} {
		o := r.opts()
		o.Parallel = n
		if res, err := Run(context.Background(), o, []Row{named("a", "p", "true")}); err == nil || !strings.Contains(err.Error(), "want 1 to 25") || res != nil {
			t.Errorf("parallel %d: %v %v, want a refusal", n, res, err)
		}
	}
	r.on("barrier", "2")
	r.on("after.1", "2")
	o := r.opts()
	o.Runs = 3
	o.Parallel = 2
	row := Row{ID: "a", Prompt: "p", Check: "true", Grants: "wide: needs bash"}
	res, err := Run(context.Background(), o, []Row{row})
	if err != nil {
		t.Fatal(err)
	}
	r.clean(res)
	if len(res) != 3 {
		t.Fatalf("got %d results, want 3: %+v", len(res), res)
	}
	if peak := strings.TrimSpace(r.read("peak")); peak != "2" {
		t.Errorf("peak concurrent agy sessions = %q, want \"2\"", peak)
	}
	order := strings.Fields(r.read("finish.order"))
	if len(order) != 3 || order[0] != "2" {
		t.Errorf("finish.order = %v, want trial 2 finishing first", order)
	}
	for i, x := range res {
		wantTrial := i + 1
		if x.Row != "a" || x.Trial != wantTrial || x.Status != StatusPass {
			t.Errorf("res[%d] = %+v, want a#%d PASS", i, x, wantTrial)
		}
	}
	out := r.out.String()
	i1 := strings.Index(out, "eval: a#1  PASS")
	i2 := strings.Index(out, "eval: a#2  PASS")
	i3 := strings.Index(out, "eval: a#3  PASS")
	if i1 < 0 || i2 <= i1 || i3 <= i2 {
		t.Errorf("output not in trial order (indices %d, %d, %d):\n%s", i1, i2, i3, out)
	}
}
