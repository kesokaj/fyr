package work

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// TestRunSeveralAtOnce: two workers with disjoint scopes run at the same
// time (each waits until the other runs), both pass and are fetched, the
// pid file counts both, and the blocks print in file order though the
// second ends first.
func TestRunSeveralAtOnce(t *testing.T) {
	r := newRig(t)
	r.on("peers", "")
	r.on("together", "20")
	r.on("file-one", "src/a.txt")
	r.on("file-two", "pkg/b.go")
	r.on("slow-one", "1") // two ends first; one still prints first
	res, err := Run(context.Background(), r.opts(), r.kickoff("one", "src/"), r.kickoff("two", "pkg/b.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Name != "one" || res[1].Name != "two" {
		t.Fatalf("results %+v", res)
	}
	for _, x := range res {
		wantReasons(t, x)
		if x.Head == "" || r.git("-C", r.lead, "rev-parse", "work/"+x.Name) != x.Head {
			t.Errorf("work/%s not fetched: %+v", x.Name, x)
		}
	}
	for _, n := range []string{"one", "two"} {
		if _, err := os.Lstat(filepath.Join(r.ctl, "met-"+n)); err != nil {
			t.Errorf("the worker %s never ran beside the other", n)
		}
	}
	// The worker up first reads the pid file while both run; the other may
	// read it after the first ended.
	if want := fmt.Sprintf("%d 2\n", os.Getpid()); r.read("pid-one") != want && r.read("pid-two") != want {
		t.Errorf("pid file %q and %q, want one %q", r.read("pid-one"), r.read("pid-two"), want)
	}
	out := r.out.String()
	if !strings.Contains(out, "  2 workers  ") {
		t.Errorf("stamp line:\n%s", out)
	}
	if i, j := strings.Index(out, "\nwork: one  PASS"), strings.Index(out, "\nwork: two  PASS"); i < 0 || j < i {
		t.Errorf("the blocks are not in file order:\n%s", out)
	}
	wantRunClosed(t, r)
}

// wantRunClosed: once a run ends, its pid file and its emptied stamp
// directory are gone.
func wantRunClosed(t *testing.T, r *rig) {
	t.Helper()
	if m, _ := filepath.Glob(filepath.Join(sandbox.Root(r.home, "work"), "*", pidName)); len(m) > 0 {
		t.Errorf("pid files left: %v", m)
	}
}

// TestRunSeveralWaits: past --max-workers a file waits for a slot, so with
// one slot the two workers never overlap; both still pass.
func TestRunSeveralWaits(t *testing.T) {
	r := newRig(t)
	fastPoll(t)
	r.on("peers", "")
	r.on("together", "2") // each waits 2s for the other, which never comes
	r.on("apart", "")
	r.on("file-two", "pkg/b.go")
	o := r.opts()
	o.MaxWorkers = 1
	res, err := Run(context.Background(), o, r.kickoff("one", "src/a.txt"), r.kickoff("two", "pkg/"))
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range res {
		wantReasons(t, x)
	}
	wantApart(t, r)
	if want := fmt.Sprintf("%d 1\n", os.Getpid()); r.read("pid-two") != want {
		t.Errorf("pid file %q, want %q", r.read("pid-two"), want)
	}
	wantRunClosed(t, r)
}

// TestRunCountsOtherRuns: a live run's pid file, held by another process,
// counts against the limit machine-wide (D30): with one of two slots taken
// the run's two workers take turns; a stale pid file counts nothing.
func TestRunCountsOtherRuns(t *testing.T) {
	r := newRig(t)
	fastPoll(t)
	holder(t, r.home, 1)
	stale := filepath.Join(sandbox.Root(r.home, "work"), "2026-10-29-170000-1234", pidName)
	put(t, stale, "1234 8\n")
	r.on("peers", "")
	r.on("together", "2")
	r.on("apart", "")
	r.on("file-two", "pkg/b.go")
	o := r.opts()
	o.MaxWorkers = 2
	res, err := Run(context.Background(), o, r.kickoff("one", "src/a.txt"), r.kickoff("two", "pkg/"))
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range res {
		wantReasons(t, x)
	}
	wantApart(t, r)
}

// wantApart: neither worker ever ran beside the other; each waited at the
// barrier for one that did not come.
func wantApart(t *testing.T, r *rig) {
	t.Helper()
	for _, n := range []string{"one", "two"} {
		if _, err := os.Lstat(filepath.Join(r.ctl, "met-"+n)); err == nil {
			t.Errorf("the worker %s ran beside the other", n)
		}
	}
}

// TestRunSeveralRefusals: refusals 4 (a name twice), 5 (the machine at its
// limit, naming the live run's stamp) and 6 (shared scope, naming both files
// and both entries), and --max-workers out of range; each before anything is
// made under the work root, beside the holder's own directory.
func TestRunSeveralRefusals(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(r *rig, o *Options) []string
		want  string
	}{
		{"name-twice", func(r *rig, o *Options) []string {
			other := filepath.Join(r.tmp, "other")
			mkdir(r.t, other)
			p := filepath.Join(other, "one.md")
			put(r.t, p, "Files: pkg/\n")
			return []string{r.kickoff("one", "src/"), p}
		}, "share the name one"},
		{"same-file", func(r *rig, o *Options) []string {
			return []string{r.kickoff("one", "src/a.txt, README.md"), r.kickoff("two", "pkg/, src/a.txt")}
		}, "one.md and " + "@KICK@/two.md share scope: src/a.txt and src/a.txt"},
		{"dir-prefix", func(r *rig, o *Options) []string {
			return []string{r.kickoff("one", "pkg/b.go"), r.kickoff("two", "pkg/")}
		}, "share scope: pkg/b.go and pkg/"},
		{"dir-prefix-first", func(r *rig, o *Options) []string {
			return []string{r.kickoff("one", "pkg/"), r.kickoff("two", "pkg/b.go")}
		}, "share scope: pkg/ and pkg/b.go"},
		{"same-record", func(r *rig, o *Options) []string {
			return []string{r.kickoff("one", "src/, "+r.guide), r.kickoff("two", "pkg/, "+r.guide)}
		}, "share scope: " + "@GUIDE@ and @GUIDE@"},
		{"at-limit", func(r *rig, o *Options) []string {
			o.MaxWorkers = 2
			return []string{r.kickoff("one", "src/")}
		}, "the machine already runs 2 workers, at --max-workers 2: @STAMP@"},
		{"max-9", func(r *rig, o *Options) []string {
			o.MaxWorkers = 9
			return []string{r.kickoff("one", "src/")}
		}, "--max-workers 9: give 1 to 8"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			stamp := holder(t, r.home, 2)
			o := r.opts()
			files := c.setup(r, &o)
			_, err := Run(context.Background(), o, files...)
			want := strings.NewReplacer("@KICK@", r.kick, "@GUIDE@", r.guide, "@STAMP@", stamp).Replace(c.want)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err %v, want %q", err, want)
			}
			if m, _ := filepath.Glob(filepath.Join(sandbox.Root(o.Home, "work"), "*")); len(m) != 1 || filepath.Base(m[0]) != stamp {
				t.Errorf("a refusal made %v", m)
			}
			if r.out.Len() != 0 {
				t.Errorf("a refusal printed %q", r.out.String())
			}
		})
	}
}

// TestRunSeveralKilled: the context's end fails every running worker
// killed with its branch fetched, and a worker still waiting for a slot
// never starts and fails killed too.
func TestRunSeveralKilled(t *testing.T) {
	r := newRig(t)
	r.on("hang", "")
	r.on("file-two", "pkg/b.go")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for i := 0; i < 600; i++ {
			_, e1 := os.Stat(filepath.Join(r.ctl, "started-one"))
			_, e2 := os.Stat(filepath.Join(r.ctl, "started-two"))
			if e1 == nil && e2 == nil {
				cancel()
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	o := r.opts()
	o.MaxWorkers = 2
	res, err := Run(ctx, o, r.kickoff("one", "src/"), r.kickoff("two", "pkg/"), r.kickoff("three", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range res[:2] {
		wantReasons(t, x, ReasonKilled)
		if x.Head == "" || x.Commits != 2 {
			t.Errorf("the killed worker %s was not fetched: %+v", x.Name, x)
		}
	}
	wantReasons(t, res[2], ReasonKilled)
	if res[2].Head != "" || res[2].Dir != "" || !strings.Contains(strings.Join(res[2].Notes, "\n"), "never started") {
		t.Errorf("the waiting worker: %+v", res[2])
	}
	if _, err := os.Lstat(filepath.Join(r.ctl, "started-three")); err == nil {
		t.Error("the waiting worker started after the end")
	}
	wantRunClosed(t, r)
}

// fastPoll makes a waiting run recount the other runs often.
func fastPoll(t *testing.T) {
	old := slotPoll
	slotPoll = 50 * time.Millisecond
	t.Cleanup(func() { slotPoll = old })
}

// TestRunDeadContextStartsNothing: a run whose context has already ended
// starts no waiting file, though slots are free: each fails killed, never
// started, with no sandbox (the Phase 4 review's dispatch finding).
func TestRunDeadContextStartsNothing(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := Run(ctx, r.opts(), r.kickoff("one", "src/"), r.kickoff("two", "pkg/"))
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range res {
		wantReasons(t, x, ReasonKilled)
		if x.Dir != "" || !strings.Contains(strings.Join(x.Notes, "\n"), "never started") {
			t.Errorf("%s started: %+v", x.Name, x)
		}
	}
	if m, _ := filepath.Glob(filepath.Join(sandbox.Root(r.home, "work"), "*", "*")); len(m) > 0 {
		t.Errorf("sandboxes made: %v", m)
	}
}
