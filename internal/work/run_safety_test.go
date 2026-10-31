package work

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/sandbox"
)

func runWith(o Options, file string) (Result, error) {
	return first(Run(context.Background(), o, file))
}

// first is a one-file run's result.
func first(res []Result, err error) (Result, error) {
	if err != nil {
		return Result{}, err
	}
	return res[0], nil
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
	res, err := first(Run(ctx, r.opts(), r.kickoff("stop", "src/a.txt")))
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

// TestRunDraftNames is D138: a draft is copied only under a name the lead
// can put on a command line as it is, ^[a-z0-9][a-z0-9._-]{0,80}\.md$; any
// other is skipped and named in the note.
func TestRunDraftNames(t *testing.T) {
	long := "a" + strings.Repeat("b", 80) + ".md"
	good := []string{"0.md", "bug-1.2_x.md", long}
	bad := []string{"Bug.md", "bug draft.md", "x;id.md", "$(id).md", "`id`.md", "a'b.md", ".hidden.md",
		"-flag.md", "_x.md", "bug.txt", "bug.md.sh", ".md", "a" + strings.Repeat("b", 81) + ".md", "bug\tx.md", "ü.md"}
	r := newRig(t)
	r.on("draft-names", strings.Join(append(append([]string{}, good...), bad...), "\n")+"\n")
	res := r.run(r.kickoff("names", "src/a.txt"))
	wantReasons(t, res)
	got, _ := os.ReadDir(filepath.Join(r.lead, filepath.FromSlash(res.DraftsDir)))
	var names []string
	for _, e := range got {
		names = append(names, e.Name())
	}
	sort.Strings(good)
	if strings.Join(names, ",") != strings.Join(good, ",") || res.Drafts != len(good) {
		t.Errorf("copied %q (%d), want %q", names, res.Drafts, good)
	}
	notes := strings.Join(res.Notes, "\n")
	for _, n := range bad {
		if !strings.Contains(notes, sandbox.Printable(n)) {
			t.Errorf("the note does not name the skipped %q: %s", n, notes)
		}
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
// as the other workers of one run leave it, is used; only
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

// TestRunLeadReflogMoved is BUG-198: a lead that commits during the run and
// resets back leaves HEAD at the base and the tree clean, and the reflog
// still shows it, so the worker is not fetched and the note names the
// first entry the run added.
func TestRunLeadReflogMoved(t *testing.T) {
	r := newRig(t)
	r.on("lead-reset", r.lead)
	res := r.run(r.kickoff("reflog", "src/a.txt"))
	wantReasons(t, res, ReasonLeadChanged)
	if res.Head != "" || r.git("-C", r.lead, "branch", "--list", "work/*") != "" {
		t.Errorf("the worker was fetched: %+v", res)
	}
	notes := strings.Join(res.Notes, "\n")
	if !strings.Contains(notes, "HEAD reflog moved during the run: 2 new entries") || !strings.Contains(notes, `"commit: record D5 for p3"`) {
		t.Errorf("the note does not name the reflog's first new entry:\n%s", notes)
	}
}

// TestRunLeadNoReflog: a checkout that keeps no HEAD reflog is refused
// before anything is made, since step 6's guard reads it (BUG-198).
func TestRunLeadNoReflog(t *testing.T) {
	r := newRig(t)
	r.git("-C", r.lead, "config", "core.logAllRefUpdates", "false")
	r.git("-C", r.lead, "reflog", "expire", "--expire=all", "--all")
	_, err := runWith(r.opts(), r.kickoff("noreflog", "src/a.txt"))
	if err == nil || !strings.Contains(err.Error(), "keeps no HEAD reflog") {
		t.Fatalf("err %v", err)
	}
	if _, serr := os.Stat(sandbox.Root(r.home, "work")); !os.IsNotExist(serr) {
		t.Errorf("the work root was made: %v", serr)
	}
}

// TestLeadGitHistoryOptions: every git call in the lead after the session
// reads history without replace refs, grafts or the commit-graph, any of
// which can rewire a commit's parents, and runs no hook (item 42).
func TestLeadGitHistoryOptions(t *testing.T) {
	cmd := leadGit("/x", "rev-list", "HEAD")
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{"--no-replace-objects", "-c core.hooksPath=/dev/null", "-c core.commitGraph=false"} {
		if !strings.Contains(args, want) {
			t.Errorf("no %q in %q", want, args)
		}
	}
	if !slices.Contains(cmd.Env, "GIT_GRAFT_FILE="+os.DevNull) {
		t.Errorf("no GIT_GRAFT_FILE in the environment")
	}
}
