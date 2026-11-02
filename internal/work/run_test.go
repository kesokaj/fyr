package work

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// The merged trail is what the lead's next fyr record sync + list parses.
	r.git("-C", r.lead, "merge", "-q", "--no-ff", "-m", "merge", "work/fix-a")
	leadHome := filepath.Join(r.tmp, "lead-home")
	syncCmd := exec.Command(realFyr(t), "record", "sync", "--home", leadHome)
	syncCmd.Dir = r.lead
	if out, err := syncCmd.CombinedOutput(); err != nil {
		t.Fatalf("record sync after the merge: %v\n%s", err, out)
	}
	list := exec.Command(realFyr(t), "record", "list", "--home", leadHome)
	list.Dir = r.lead
	out, err := list.CombinedOutput()
	if err != nil || !strings.Contains(string(out), res.Handoff.ID) || !strings.Contains(string(out), r.guide) {
		t.Errorf("record list after the merge: %v\n%s", err, out)
	}
}

// TestRunWorkerWithoutTrailExport verifies that in a DB-only checkout (no
// .agents/trail.jsonl at HEAD), a worker's sandbox brain.db is seeded from the
// lead's brain.db, a worker calling fyr record add without fyr record export
// passes ExitChecks and has its handoff and change records collected from its
// sandbox brain.db, and an ungranted edit to a base record fails with
// ReasonOutside (D9).
func TestRunWorkerWithoutTrailExport(t *testing.T) {
	r := newRig(t)
	r.git("-C", r.lead, "rm", "-q", ".agents/trail.jsonl")
	r.git("-C", r.lead, "commit", "-qm", "db-only")
	r.on("change", "")
	r.on("noexport", "")
	res := r.run(r.kickoff("fix-a", "src/a.txt"))
	wantReasons(t, res)
	if res.Verdict != "PASS" || res.Commits != 1 || res.Handoff == nil || len(res.Changes) != 1 {
		t.Fatalf("result %+v\n%s", res, r.out.String())
	}

	// An ungranted edit to a seeded base record in the sandbox brain.db fails
	// with ReasonOutside even when .agents/trail.jsonl is absent in git.
	r2 := newRig(t)
	r2.git("-C", r2.lead, "rm", "-q", ".agents/trail.jsonl")
	r2.git("-C", r2.lead, "commit", "-qm", "db-only")
	r2.on("edit-record", r2.guide)
	r2.on("noexport", "")
	res2 := r2.run(r2.kickoff("fix-b", "src/a.txt"))
	wantReasons(t, res2, ReasonOutside)
	if !strings.Contains(r2.out.String(), r2.guide) {
		t.Errorf("ungranted base record edit did not name %s:\n%s", r2.guide, r2.out.String())
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

// TestRunLinks: a symlink or a gitlink on the branch fails the worker even
// where its Files: names the path, since the squash would carry a path
// that reaches outside the checkout into the lead's tree (the v5.0.0
// gate's security review). A regular file at the same path is the
// control.
func TestRunLinks(t *testing.T) {
	for _, c := range []struct {
		name, ctl, val string
		ignored        bool
	}{
		{"file", "", "", false},
		{"symlink", "symlink", "/etc/passwd", false},
		{"symlink-in-tree", "symlink", "a.txt", false},
		{"gitlink", "gitlink", "", false},
		// The base's .gitmodules tells git to ignore the path: diff-tree
		// would drop it from both checks without --ignore-submodules=none.
		{"gitlink-ignored", "gitlink", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			if c.ignored {
				put(t, filepath.Join(r.lead, ".gitmodules"), "[submodule \"x\"]\n\tpath = src/link\n\turl = ./x\n\tignore = all\n")
				r.git("-C", r.lead, "add", ".gitmodules")
				r.git("-C", r.lead, "commit", "-qm", "gitmodules")
			}
			if c.ctl != "" {
				r.on(c.ctl, c.val)
			}
			res := r.run(r.kickoff("links", "src/a.txt, src/link"))
			if c.ctl == "" {
				wantReasons(t, res)
				return
			}
			wantReasons(t, res, ReasonOutside)
			if !strings.Contains(strings.Join(res.Notes, "\n"), "symlink or submodule on the branch, whatever its Files: src/link") {
				t.Errorf("no note naming src/link: %q", res.Notes)
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

// TestRunAddedRecordKinds is D138: a branch may add handoff and change
// records only; one of any other kind, stored through fyr record or
// written into the trail by hand, fails the worker as outside its Files,
// named with its kind, so the squash never carries it to the lead.
func TestRunAddedRecordKinds(t *testing.T) {
	for _, c := range []struct{ ctl, kind string }{
		{"add-kind", "change"},
		{"add-kind", "plan"}, {"add-kind", "review"}, {"add-kind", "testing"}, {"add-kind", "bug"},
		{"add-kind", "adr"}, {"add-kind", "retro"}, {"add-kind", "learning"}, {"add-kind", "spec"},
		{"add-kind", "deploy"}, {"add-kind", "guide"},
		{"raw-kind", "memory"}, {"raw-kind", "feedback"}, {"raw-kind", "proposal"},
		{"raw-kind", "nonsense"}, {"raw-kind", ""},
	} {
		t.Run(c.ctl+"-"+c.kind, func(t *testing.T) {
			r := newRig(t)
			r.on(c.ctl, c.kind)
			res := r.run(r.kickoff("kind", "src/a.txt"))
			if _, err := os.Stat(filepath.Join(r.ctl, "add-kind-failed")); err == nil {
				t.Fatalf("fyr record add %s failed in the worker: %s", c.kind, r.read("add-kind-err"))
			}
			if c.kind == "change" {
				wantReasons(t, res)
				return
			}
			wantReasons(t, res, ReasonOutside)
			if want := fmt.Sprintf("kind %q", c.kind); !strings.Contains(strings.Join(res.Notes, "\n"), want) {
				t.Errorf("no note naming %s: %q", want, res.Notes)
			}
			if res.Handoff == nil {
				t.Errorf("the handoff is not printed beside the refusal")
			}
		})
	}
}

// TestRunAddedRecordBounded: a refused added record is named by its ID and
// kind quoted and cut to 128 characters each, so a worker-written ID is
// never printed raw nor carries a body into the lead's notes.
func TestRunAddedRecordBounded(t *testing.T) {
	r := newRig(t)
	id, kind := "RAW-"+strings.Repeat("x", 4000)+"-end", strings.Repeat("k", 300)+"-end"
	r.on("raw-id", id)
	r.on("raw-kind", kind)
	res := r.run(r.kickoff("long", "src/a.txt"))
	wantReasons(t, res, ReasonOutside)
	notes := strings.Join(res.Notes, "\n")
	want := fmt.Sprintf("%q (added, kind %q)", id[:128], kind[:128])
	if !strings.Contains(notes, want) || strings.Contains(notes, "-end") {
		t.Errorf("the refused record is not named %s, bounded: %.300s", want, notes)
	}
}

// TestRunAddedRecordDates is D139 item 2: an added handoff or change
// record, written into the trail by hand, whose date or updated falls
// outside the worker's window fails it as outside its Files, so a
// worker's record never outranks the lead's own as the newest (the
// security review of D138, finding 1); so does an updated that is not
// the UTC form fyr record writes, since the store sorts it as text. The
// in-window case is the control: the same hand-written line passes when
// its dates are now's.
func TestRunAddedRecordDates(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name, kind, date, updated, field string
	}{
		{"in-window", "change", "", "", ""},
		{"future-date", "handoff", "2099-12-31", "", "date"},
		{"past-date", "handoff", "2000-01-01", "", "date"},
		{"past-updated", "change", "", "2000-01-01T00:00:00Z", "updated"},
		{"future-updated", "change", "", now.Add(time.Hour).UTC().Format(time.RFC3339), "updated"},
		// In the window as instants, but sorted as text after every Z stamp
		// the lead's fyr writes that day (the security review of D139).
		{"offset-updated", "change", "", now.In(time.FixedZone("", 14*3600)).Format(time.RFC3339), "updated"},
		{"fraction-updated", "change", "", now.UTC().Truncate(time.Second).Add(500 * time.Millisecond).Format(time.RFC3339Nano), "updated"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.on("raw-kind", c.kind)
			r.on("raw-id", strings.ToUpper(c.kind)+"-2026-10-30-0000-raw")
			if c.kind == "handoff" {
				r.on("nohandoff", "")
			}
			if c.date != "" {
				r.on("raw-date", c.date)
			}
			if c.updated != "" {
				r.on("raw-updated", c.updated)
			}
			res := r.run(r.kickoff("dated", "src/a.txt"))
			if c.field == "" {
				wantReasons(t, res)
				if len(res.Changes) != 1 {
					t.Errorf("the in-window change is not returned: %+v", res.Changes)
				}
				return
			}
			wantReasons(t, res, ReasonOutside)
			if !strings.Contains(strings.Join(res.Notes, "\n"), "(added, "+c.field+" ") {
				t.Errorf("no note naming the %s: %q", c.field, res.Notes)
			}
		})
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

// TestWorkArgsUnchangedByAuto: Options.Auto is additive. Without it, work's
// agy argv, its refusal of a bare Files: line and its drafts name rule are
// unchanged; with it, the agy argv stays the same, a bare Files: line is a
// drafts-only scope that fails any tree change, and auto-drafts.jsonl is
// copied.
func TestWorkArgsUnchangedByAuto(t *testing.T) {
	r := newRig(t)
	r.on("draft-names", "auto-drafts.jsonl\nok.md\n")
	if _, err := runWith(r.opts(), r.kickoff("empty-no-auto", "")); err == nil || !strings.Contains(err.Error(), "an empty Files: entry") {
		t.Fatalf("without Auto, bare Files: = %v", err)
	}
	res := r.run(r.kickoff("plain", "src/a.txt"))
	wantReasons(t, res)
	plainArgs := r.read("args")
	if !strings.HasPrefix(plainArgs, "-p\n") || !strings.HasSuffix(plainArgs, "\n--mode\naccept-edits\n--print-timeout\n5m\n") || strings.Contains(plainArgs, "--model") {
		t.Fatalf("plain agy argv = %q", plainArgs)
	}
	if res.Drafts != 1 || !strings.Contains(strings.Join(res.Notes, "\n"), "auto-drafts.jsonl") {
		t.Errorf("without Auto, drafts = %d, notes = %v", res.Drafts, res.Notes)
	}

	r = newRig(t)
	r.on("nocommit", "")
	r.on("draft-names", "auto-drafts.jsonl\n")
	o := r.opts()
	o.Auto = true
	res, err := runWith(o, r.kickoff("auto-drafts", ""))
	if err != nil {
		t.Fatalf("with Auto, bare Files: refused: %v", err)
	}
	wantReasons(t, res)
	autoArgs := r.read("args")
	if !strings.HasPrefix(autoArgs, "-p\n") || !strings.HasSuffix(autoArgs, "\n--mode\naccept-edits\n--print-timeout\n5m\n") || strings.Contains(autoArgs, "--model") {
		t.Fatalf("auto agy argv = %q", autoArgs)
	}
	if res.Drafts != 1 {
		t.Errorf("with Auto, drafts = %d, want 1", res.Drafts)
	}

	r = newRig(t)
	o = r.opts()
	o.Auto = true
	res, err = runWith(o, r.kickoff("auto-outside", ""))
	if err != nil {
		t.Fatal(err)
	}
	wantReasons(t, res, ReasonOutside)
}

func TestAutoFilesLine(t *testing.T) {
	if _, err := filesLine("Files:\n", false); err == nil {
		t.Error("filesLine accepted empty Files: with auto=false")
	}
	got, err := filesLine("Files:\n", true)
	if err != nil || len(got) != 0 {
		t.Errorf("filesLine(auto=true) = %v, %v", got, err)
	}
}

func TestAutoDraftsCopy(t *testing.T) {
	r := newRig(t)
	r.on("nocommit", "")
	r.on("draft-names", "auto-drafts.jsonl\n")
	o := r.opts()
	o.Auto = true
	res, err := runWith(o, r.kickoff("auto-copy", ""))
	if err != nil || res.Drafts != 1 {
		t.Fatalf("err=%v drafts=%d", err, res.Drafts)
	}
}

func TestDockerPreflight(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	o.Version = ""
	if err := DockerPreflight(o); err == nil || !strings.Contains(err.Error(), "development build") {
		t.Errorf("DockerPreflight on devel build = %v", err)
	}
}
