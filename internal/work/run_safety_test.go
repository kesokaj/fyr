package work

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/sandbox"
)

func runWith(o Options, file string) (Result, error) {
	return Run(context.Background(), o, file)
}

// TestRunSafetyKeepsSandbox is D38 and D35: a worker a safety reason
// failed is not fetched, its drafts are not copied, its handoff is not
// printed, and its sandbox is kept and named.
func TestRunSafetyKeepsSandbox(t *testing.T) {
	for _, c := range []struct {
		name, ctl string
		val       func(r *rig) string
		want      string
	}{
		{"lead", "lead", func(r *rig) string { return r.lead }, ReasonLeadChanged},
		{"real-config", "realconfig", func(r *rig) string { return filepath.Join(r.real, ".gemini", "config", "rules", "x.md") }, sandbox.ReasonRealConfig},
		{"leak", "leak", func(*rig) string { return "gh" + "o_" + strings.Repeat("C", 36) }, sandbox.ReasonCredentials},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.on(c.ctl, c.val(r))
			r.on("drafts", "")
			res := r.run(r.kickoff(c.name, "src/a.txt"))
			wantReasons(t, res, c.want)
			if res.Head != "" || r.git("-C", r.lead, "branch", "--list", "work/*") != "" {
				t.Errorf("an unsafe worker was fetched: %+v", res)
			}
			if _, err := os.Stat(filepath.Join(r.lead, ".agents", "scratch", "work")); !os.IsNotExist(err) {
				t.Errorf("an unsafe worker's drafts were copied: %v", err)
			}
			if st, err := os.Stat(res.Dir); err != nil || !st.IsDir() {
				t.Errorf("the sandbox was not kept: %v", err)
			}
			block := strings.Join(res.Block(), "\n")
			if strings.Contains(block, "handoff") || !strings.Contains(block, "not fetched  kept "+res.Dir) {
				t.Errorf("the block:\n%s", block)
			}
		})
	}
}

// TestRunSkipsStores is D45 through a worker: a session that writes the
// real home's brain.db passes, as the dream timer and the operator's own
// sessions do.
func TestRunSkipsStores(t *testing.T) {
	r := newRig(t)
	store := filepath.Join(r.real, harness.StateDir, "brain.db")
	put(t, store, "store")
	r.on("store", store)
	wantReasons(t, r.run(r.kickoff("stores", "src/a.txt")))
}

// shortTimeouts lets a test run a worker with a timeout of seconds.
func shortTimeouts(t *testing.T) {
	min, unit := minTimeout, timeoutUnit
	minTimeout, timeoutUnit = time.Second, time.Second
	t.Cleanup(func() { minTimeout, timeoutUnit = min, unit })
}

// TestRunTimedOut: agy past --timeout is killed as eval kills it; the
// branch it committed is fetched and the worker fails timed out.
func TestRunTimedOut(t *testing.T) {
	shortTimeouts(t)
	r := newRig(t)
	r.on("hang", "")
	o := r.opts()
	o.Timeout = 2 * time.Second
	res, err := runWith(o, r.kickoff("slow", "src/a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	wantReasons(t, res, ReasonTimedOut)
	if res.Head == "" || res.Commits != 2 || res.Handoff == nil {
		t.Errorf("the timed-out branch was not fetched whole: %+v", res)
	}
}

// TestRunKilled: the context's end (a signal, later --kill) fails the
// worker killed, and its partial branch is fetched (D26).
func TestRunKilled(t *testing.T) {
	r := newRig(t)
	r.on("hang", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for i := 0; i < 600; i++ {
			if _, err := os.Stat(filepath.Join(r.ctl, "started")); err == nil {
				cancel()
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	res, err := Run(ctx, r.opts(), r.kickoff("stop", "src/a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	wantReasons(t, res, ReasonKilled)
	if res.Head == "" || res.Commits != 2 {
		t.Errorf("the killed worker's branch was not fetched: %+v", res)
	}
}

// TestRunDrafts is D21: the clone's draft records are copied into the
// lead's scratch, regular files of at most 1 MiB only, 0600.
func TestRunDrafts(t *testing.T) {
	r := newRig(t)
	r.on("drafts", "")
	res := r.run(r.kickoff("drafts", "src/a.txt"))
	wantReasons(t, res)
	dir := filepath.Join(r.lead, filepath.FromSlash(res.DraftsDir))
	got, _ := os.ReadDir(dir)
	var names []string
	for _, e := range got {
		names = append(names, e.Name())
		if st, err := os.Lstat(filepath.Join(dir, e.Name())); err != nil || st.Mode() != 0o600 {
			t.Errorf("%s: %v %v, want a 0600 file", e.Name(), st.Mode(), err)
		}
	}
	if strings.Join(names, ",") != "adr-draft.md,bug-draft.md" || res.Drafts != 2 {
		t.Errorf("copied %v (%d), want the two regular drafts", names, res.Drafts)
	}
	notes := strings.Join(res.Notes, "\n")
	for _, skipped := range []string{"big.md", "hard.md", "link.md", "pipe.md", "sub"} {
		if !strings.Contains(notes, skipped) {
			t.Errorf("the note does not name the skipped %s: %s", skipped, notes)
		}
	}
	if !strings.Contains(strings.Join(res.Block(), "\n"), "work: drafts: drafts "+res.DraftsDir+" (2 files)") {
		t.Errorf("the block lacks the drafts line:\n%s", strings.Join(res.Block(), "\n"))
	}
	if dirty := r.git("-C", r.lead, "status", "--porcelain"); dirty != "" {
		t.Errorf("the copy dirtied the lead's tree: %s", dirty)
	}
}

// TestRunUnfetchedKept: a worker whose branch was never fetched keeps its
// sandbox, which holds all that is left of the work: a setup that failed
// before agy, and a branch the session deleted.
func TestRunUnfetchedKept(t *testing.T) {
	for _, c := range []struct{ ctl, want string }{
		{"install-fail", sandbox.ReasonInstall},
		{"token-block", sandbox.ReasonToken},
		{"delete-branch", ReasonNoCommits},
	} {
		r := newRig(t)
		r.on(c.ctl, "")
		res := r.run(r.kickoff("gone", "src/a.txt"))
		wantReasons(t, res, c.want)
		if res.Head != "" {
			t.Errorf("%s: fetched %s", c.ctl, res.Head)
		}
		if st, err := os.Stat(res.Dir); err != nil || !st.IsDir() {
			t.Errorf("%s: the sandbox was not kept: %v", c.ctl, err)
		}
	}
}

// TestRunDraftsCap: at most 50 drafts are copied, the rest named.
func TestRunDraftsCap(t *testing.T) {
	r := newRig(t)
	r.on("many-drafts", "")
	res := r.run(r.kickoff("many", "src/a.txt"))
	wantReasons(t, res)
	if got, _ := os.ReadDir(filepath.Join(r.lead, filepath.FromSlash(res.DraftsDir))); res.Drafts != maxDrafts || len(got) != maxDrafts {
		t.Errorf("copied %d (%d on disk), want %d", res.Drafts, len(got), maxDrafts)
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "d51.md") {
		t.Errorf("the 51st draft is not named: %q", res.Notes)
	}
}

// TestRunDraftsNotRedirected: the drafts destination is made fresh inside
// the lead's checkout, never through a link the session planted there (the
// security review of Phase 2, finding 1), and a copy that cannot be made
// keeps the sandbox, which still holds the drafts (the review, finding 4).
func TestRunDraftsNotRedirected(t *testing.T) {
	for _, plant := range []string{"name", "namedir", "work"} {
		t.Run(plant, func(t *testing.T) {
			r := newRig(t)
			outside := filepath.Join(r.tmp, "outside")
			mkdir(t, outside)
			r.on("drafts", "")
			r.on("plant", plant)
			r.on("leadpath", r.lead)
			r.on("outdir", outside)
			res := r.run(r.kickoff("redirect", "src/a.txt"))
			if got, _ := os.ReadDir(outside); len(got) != 0 {
				t.Errorf("drafts written through the planted link: %v", got)
			}
			if res.Drafts != 0 || res.Dir == "" || !strings.Contains(strings.Join(res.Notes, "\n"), "drafts") {
				t.Errorf("drafts %d, kept %q, notes %q", res.Drafts, res.Dir, res.Notes)
			}
		})
	}
}

// TestRunEveryCommitInScope: a path outside Files: that one commit adds and
// a later one removes fails the scope check, which reads every commit, not
// only the net diff (security review, finding 2).
// A graft the session wrote into the lead's .git/info, hiding those
// commits, is not followed (security review round 2).
func TestRunEveryCommitInScope(t *testing.T) {
	for _, graft := range []bool{false, true} {
		r := newRig(t)
		r.on("add-remove", "")
		if graft {
			r.on("graft", "")
			r.on("leadpath", r.lead)
		}
		res := r.run(r.kickoff("addrm", "src/a.txt"))
		wantReasons(t, res, ReasonOutside)
		if !strings.Contains(strings.Join(res.Notes, "\n"), "outside.txt") {
			t.Errorf("graft %v: the note does not name outside.txt: %q", graft, res.Notes)
		}
	}
}

// TestRunDraftsStampExists: a <stamp> directory that is a real directory,
// as Phase 4's other workers of one invocation leave it, is used; only
// <name> must be new.
func TestRunDraftsStampExists(t *testing.T) {
	r := newRig(t)
	r.on("drafts", "")
	r.on("plant", "stamp")
	r.on("leadpath", r.lead)
	res := r.run(r.kickoff("stamped", "src/a.txt"))
	wantReasons(t, res)
	if res.Drafts != 2 || res.Dir != "" {
		t.Errorf("drafts %d, kept %q, notes %q", res.Drafts, res.Dir, res.Notes)
	}
}

// TestRunLeadHooksOff: a hook the session planted in the lead's .git does
// not run when fyr fetches into the lead (security review, finding 4).
func TestRunLeadHooksOff(t *testing.T) {
	r := newRig(t)
	marker := filepath.Join(r.tmp, "hook-ran")
	r.on("hook", marker)
	r.on("leadpath", r.lead)
	r.run(r.kickoff("hook", "src/a.txt"))
	if _, err := os.Stat(marker); err == nil {
		t.Error("the planted reference-transaction hook ran")
	}
}

// TestRunRelativePaths: a relative Root and Home are made absolute once,
// so the fetch and the drafts copy read the same clone (security review,
// finding 8).
func TestRunRelativePaths(t *testing.T) {
	r := newRig(t)
	t.Chdir(r.tmp)
	o := r.opts()
	o.Root, o.Home = "lead", "home"
	res, err := runWith(o, r.kickoff("rel", "src/a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	wantReasons(t, res)
}
