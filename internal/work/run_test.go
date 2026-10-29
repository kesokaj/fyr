package work

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// TestRunPass is one good worker end to end: the stamp line first, the
// clone with no origin on its branch, the operator's identity, the branch
// fetched into the lead, the records the strict export the lead's own fyr
// reads after the merge, and the sandbox removed.
func TestRunPass(t *testing.T) {
	r := newRig(t)
	r.on("change", "")
	res := r.run(r.kickoff("fix-a", "src/a.txt, pkg/"))
	wantReasons(t, res)
	if res.Verdict != "PASS" || res.Commits != 2 || res.Head == "" || res.AgyExit != 0 {
		t.Fatalf("result %+v\n%s", res, r.out.String())
	}
	stamp := fmt.Sprintf("2026-10-29-180000-%d", os.Getpid())
	lines := strings.Split(r.out.String(), "\n")
	if want := "work: stamp " + stamp + "  1 workers  " + filepath.Join(sandbox.Root(r.home, "work"), stamp) + "/"; lines[0] != want {
		t.Errorf("first line %q, want %q", lines[0], want)
	}
	if got := r.git("-C", r.lead, "rev-parse", "work/fix-a"); got != res.Head {
		t.Errorf("work/fix-a in the lead is %s, want %s", got, res.Head)
	}
	wantWorkerSaw(t, r)
	if res.Dir != "" {
		t.Errorf("a fetched PASS kept its sandbox at %s", res.Dir)
	}
	if _, err := os.Lstat(filepath.Join(sandbox.Root(r.home, "work"), stamp, "fix-a")); !os.IsNotExist(err) {
		t.Errorf("the sandbox is still there: %v", err)
	}
	if res.Handoff == nil || !strings.HasPrefix(res.Handoff.ID, "HANDOFF-") || len(res.Changes) != 1 || res.Changes[0].Title != "Changed src/a.txt" {
		t.Fatalf("records: handoff %+v, changes %+v", res.Handoff, res.Changes)
	}
	wantContract(t, r, res)
	// The merged trail is what the lead's next fyr record call parses.
	r.git("-C", r.lead, "merge", "-q", "--no-ff", "-m", "merge", "work/fix-a")
	list := exec.Command(realFyr(t), "record", "list", "--home", filepath.Join(r.tmp, "lead-home"))
	list.Dir = r.lead
	out, err := list.CombinedOutput()
	if err != nil || !strings.Contains(string(out), res.Handoff.ID) || !strings.Contains(string(out), r.guide) {
		t.Errorf("record list after the merge: %v\n%s", err, out)
	}
}

// wantWorkerSaw: agy ran on the worker's branch in a clone with no remote,
// as the operator, with the Kickoff and the print timeout, and the commit is
// the operator's.
func wantWorkerSaw(t *testing.T, r *rig) {
	t.Helper()
	if got := strings.TrimSpace(r.read("branch")); got != "work/fix-a" {
		t.Errorf("agy ran on %q", got)
	}
	if got := r.read("remotes"); got != "" {
		t.Errorf("the clone kept remotes %q (D14)", got)
	}
	env := r.read("env")
	for _, kv := range []string{"GIT_AUTHOR_NAME=Lead Person", "GIT_COMMITTER_EMAIL=lead@localhost"} {
		if !strings.Contains(env, kv+"\n") {
			t.Errorf("agy env lacks %s (D25)", kv)
		}
	}
	args := r.read("args")
	if !strings.Contains(args, "Files: src/a.txt, pkg/") || !strings.Contains(args, "--print-timeout\n5m\n") || strings.Contains(args, "--model") {
		t.Errorf("agy args:\n%s", args)
	}
	if author := r.git("-C", r.lead, "log", "-1", "--format=%an <%ae>", "work/fix-a"); author != "Lead Person <lead@localhost>" {
		t.Errorf("the worker committed as %s", author)
	}
}

// wantContract: the handoff line, its sections prefixed, the change line,
// and nothing of the session's other output.
func wantContract(t *testing.T, r *rig, res Result) {
	t.Helper()
	for _, want := range []string{"work: fix-a: handoff " + res.Handoff.ID, "work: fix-a | ## Open / In Progress", "work: fix-a | - open item",
		"work: fix-a | 1. next step", "work: fix-a | ## not a heading inside the fence", "work: fix-a: change " + res.Changes[0].ID + "  Changed src/a.txt"} {
		if !strings.Contains(r.out.String(), "\n"+want+"\n") {
			t.Errorf("the contract lacks the line %q:\n%s", want, r.out.String())
		}
	}
	if strings.Contains(r.out.String(), "## Context") || strings.Contains(r.out.String(), "agy says ok") {
		t.Errorf("the contract printed more than the three sections:\n%s", r.out.String())
	}
}

// TestRunFailReasons is each FAIL reason of a fetched worker: the branch
// is fetched and printed, never a PASS.
func TestRunFailReasons(t *testing.T) {
	for _, c := range []struct {
		name, files string
		ctl         []string
		want        []string
		note        string
	}{
		{"no-commits", "src/a.txt", []string{"nocommit", "nohandoff"}, []string{ReasonNoCommits}, ""},
		{"no-handoff", "src/a.txt", []string{"nohandoff"}, []string{ReasonNoHandoff}, ""},
		{"two-handoffs", "src/a.txt", []string{"twohandoffs"}, []string{ReasonTwoHandoffs}, "adds 2 handoffs"},
		{"outside", "src/a.txt", []string{"outside"}, []string{ReasonOutside}, "outside.txt"},
		{"dir-scope", "src/a.txt/", []string{}, []string{ReasonOutside}, "src/a.txt"},
		{"rewrote", "src/a.txt", []string{"rewrite"}, []string{ReasonRewrote}, "does not descend"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			for _, k := range c.ctl {
				r.on(k, "")
			}
			res := r.run(r.kickoff(c.name, c.files))
			wantReasons(t, res, c.want...)
			if res.Head == "" {
				t.Errorf("not fetched: %s", r.out.String())
			}
			if c.note != "" && !strings.Contains(strings.Join(res.Notes, "\n"), c.note) {
				t.Errorf("no note naming %q: %q", c.note, res.Notes)
			}
			if res.Dir != "" {
				t.Errorf("a fetched FAIL kept its sandbox: %s", res.Dir)
			}
		})
	}
}

// TestRunRecordScope: a record that existed at the base and changed on the
// branch must be named in Files:, by its ID.
func TestRunRecordScope(t *testing.T) {
	for _, named := range []bool{false, true} {
		r := newRig(t)
		r.on("edit-record", r.guide)
		files := "src/a.txt"
		if named {
			files += ", " + r.guide
		}
		res := r.run(r.kickoff("edit", files))
		if named {
			wantReasons(t, res)
			continue
		}
		wantReasons(t, res, ReasonOutside)
		if !strings.Contains(strings.Join(res.Notes, "\n"), r.guide) {
			t.Errorf("the note does not name %s: %q", r.guide, res.Notes)
		}
	}
}

// TestRunNonZeroExit: agy's exit is shown, and a worker that did its work
// and wrote its handoff still passes (§ Interfaces: PASS is the checks).
func TestRunNonZeroExit(t *testing.T) {
	r := newRig(t)
	r.on("exit", "3")
	res := r.run(r.kickoff("exit", "src/a.txt"))
	wantReasons(t, res)
	if res.AgyExit != 3 || !strings.Contains(res.Block()[0], "agy exit 3") {
		t.Errorf("agy exit %d, line %q", res.AgyExit, res.Block()[0])
	}
}

// TestRunModelAndKeep: --model reaches agy, and --keep keeps a PASS's
// sandbox.
func TestRunModelAndKeep(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	o.Model, o.Keep = "gemini-x", true
	res, err := runWith(o, r.kickoff("keep", "src/a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	wantReasons(t, res)
	if !strings.Contains(r.read("args"), "--model\ngemini-x\n") {
		t.Errorf("agy args:\n%s", r.read("args"))
	}
	if st, err := os.Stat(res.Dir); err != nil || !st.IsDir() || !strings.Contains(res.Block()[0], "kept "+res.Dir) {
		t.Errorf("--keep: %v, line %q", err, res.Block()[0])
	}
}
