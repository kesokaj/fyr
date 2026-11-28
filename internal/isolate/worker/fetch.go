package worker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// maxNamed is how many paths or IDs one note names.
const maxNamed = 20

// collect is steps 6-8, step 8 being the drafts and the records (ADR-501).
// A worker a safety reason failed, or whose session never started, gets none
// of them (D38); a timed-out, killed or otherwise failed one is fetched (D26).
func (w *worker) collect(ctx context.Context) {
	if w.o.DraftsOnly {
		if !w.ran || w.unsafe() || !w.b.Intact() {
			return
		}
		drafts := w.drafts()
		w.closeLeadDir()
		w.collected = drafts
		return
	}
	if !w.ran || w.unsafe() || !w.b.Intact() || !w.b.GitIntact() {
		return
	}
	if !w.leadUnchanged() {
		w.b.Fail(ReasonLeadChanged)
		return
	}
	if !w.fetch() {
		w.b.Fail(ReasonNoCommits)
		return
	}
	w.scope(ctx)
	drafts := w.drafts()
	w.closeLeadDir()
	w.collected = drafts
}

// leadGit is git in the lead's checkout for the steps after the session,
// which may have written the checkout's .git: no replace refs, no grafts
// and no commit-graph, any of which can rewire a commit's parents, so the
// scope check reads the history a push sends, and no hooks.
func leadGit(root string, args ...string) *exec.Cmd {
	cmd := trail.Command(root, append([]string{"--no-replace-objects", "-c", "core.hooksPath=/dev/null", "-c", "core.commitGraph=false"}, args...)...)
	cmd.Env = append(cmd.Env, "GIT_GRAFT_FILE="+os.DevNull)
	return cmd
}

// leadUnchanged is D35: the lead's HEAD is still the base, the reflogs of
// HEAD and its branch have no entry the run added (BUG-198), and its tracked
// and unignored tree is still clean.
func (w *worker) leadUnchanged() bool {
	if h, err := leadGit(w.o.Root, "rev-parse", "--verify", "-q", "HEAD^{commit}").Output(); err != nil || strings.TrimSpace(string(h)) != w.p.base {
		w.b.Note("the lead checkout's HEAD moved from %s; nothing is fetched or copied (D35)", short(w.p.base))
		return false
	}
	if why := reflogMoved(w.o.Root, w.p.reflog); why != "" {
		w.b.Note("%s; nothing is fetched or copied (BUG-198)", why)
		return false
	}
	if dirty, err := trail.Dirty(w.o.Root); err != nil || dirty {
		w.b.Note("the lead checkout has changes since the start; nothing is fetched or copied (D35)")
		return false
	}
	return true
}

// fetchLimit bounds step 6.
const fetchLimit = 10 * time.Minute

// maxFetchOutput bounds what a fetch's output keeps; the rest is discarded.
const maxFetchOutput = 64 << 10

// runBounded is CombinedOutput with a deadline and a bound on the output.
func runBounded(cmd *exec.Cmd, limit time.Duration) ([]byte, error) {
	out := trail.NewCappedBuffer(maxFetchOutput, nil)
	cmd.Stdout, cmd.Stderr = out, out
	err := runGroup(cmd, limit)
	return out.Bytes(), err
}

// runGroup runs cmd in its own process group, which is killed whole at the
// deadline.
func runGroup(cmd *exec.Cmd, limit time.Duration) error {
	setWorkerCmdSetsid(cmd)
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	pgid := cmd.Process.Pid
	var late atomic.Bool
	t := time.AfterFunc(limit, func() {
		late.Store(true)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	})
	err := cmd.Wait()
	t.Stop()
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	if late.Load() {
		return fmt.Errorf("timed out after %s", limit)
	}
	return err
}

// fetch is step 6: the clone's branch into the lead's checkout, by path,
// with no tags, and every object checked.
func (w *worker) fetch() bool {
	ref := "refs/heads/" + w.p.k.Branch()
	out, err := runBounded(fetchCmd(w.o.Root, filepath.Join(w.b.App(), ".git"), ref), fetchLimit)
	if err != nil {
		w.b.Note("git fetch of %s: %v: %s", w.p.k.Branch(), err, strings.TrimSpace(string(out)))
		return false
	}
	h, err := leadGit(w.o.Root, "rev-parse", "--verify", "-q", ref+"^{commit}").Output()
	w.res.Head = strings.TrimSpace(string(h))
	return err == nil
}

// scope is step 7 (D18, D36): the branch descends from the base, every path
// any of its commits changed is in Files:, and every record that existed at
// the base and changed or vanished by the branch's tip is named there.
func (w *worker) scope(ctx context.Context) {
	root, ref := w.o.Root, "refs/heads/"+w.p.k.Branch()
	if n, err := branchOutput(leadGit(root, "rev-list", "--count", w.p.base+".."+ref)); err == nil {
		w.res.Commits, _ = strconv.Atoi(strings.TrimSpace(string(n)))
	}
	if _, err := branchOutput(leadGit(root, "merge-base", "--is-ancestor", w.p.base, ref)); err != nil {
		w.b.Note("%s does not descend from %s", w.p.k.Branch(), short(w.p.base))
		w.b.Fail(ReasonRewrote)
		return
	}
	if w.res.Commits == 0 {
		w.b.Fail(ReasonNoCommits)
		return
	}
	paths, err := workerChangedPaths(root, w.p.base+".."+ref)
	if err != nil {
		w.b.Note("the branch's commits: %v", err)
		w.b.Fail(ReasonOutside)
		return
	}
	var outside []string
	for _, p := range paths {
		if p != trailRel && !w.p.k.covers(p) {
			outside = append(outside, p)
		}
	}
	outside = append(outside, w.records(ctx, root, ref)...)
	if len(outside) > 0 {
		sort.Strings(outside)
		w.b.Note("changed outside its Files (D18): %s", named(outside))
		w.b.Fail(ReasonOutside)
	}
	ls, err := links(root, w.p.base, ref)
	switch {
	case err != nil:
		w.b.Note("the branch's links: %v", err)
	case len(ls) > 0:
		w.b.Note("a symlink or submodule on the branch, whatever its Files: %s", named(ls))
	default:
		return
	}
	if !w.b.Has(ReasonOutside) {
		w.b.Fail(ReasonOutside)
	}
}

// links is every path the branch's tip holds as a symlink or a gitlink
// that the base did not hold as the same.
func links(root, base, ref string) ([]string, error) {
	raw, err := branchOutput(leadGit(root, "diff-tree", "-r", "-z", "--no-renames", "--no-ext-diff", "--ignore-submodules=none", base, ref))
	if err != nil {
		return nil, err
	}
	f := strings.Split(string(bytes.TrimRight(raw, "\x00")), "\x00")
	var out []string
	for i := 0; i+1 < len(f); i += 2 {
		if m := strings.Fields(f[i]); len(m) > 1 && (m[1] == "120000" || m[1] == "160000") {
			out = append(out, f[i+1])
		}
	}
	sort.Strings(out)
	return out, nil
}

// workerChangedPaths is every path a commit of rng changed against each of its
// parents, once each, a gitlink that .gitmodules says to ignore included.
func workerChangedPaths(root, rng string) ([]string, error) {
	revs, err := branchOutput(leadGit(root, "rev-list", rng))
	if err != nil {
		return nil, err
	}
	cmd := leadGit(root, "diff-tree", "--stdin", "-r", "-m", "--root", "--no-renames", "--no-ext-diff", "--ignore-submodules=none", "--name-only", "-z", "--no-commit-id")
	cmd.Stdin = bytes.NewReader(revs)
	names, err := branchOutput(cmd)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(string(bytes.TrimRight(names, "\x00")), "\x00") {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// records compares the trail at the base and on the branch.
func (w *worker) records(ctx context.Context, root, ref string) (outside []string) {
	before, err := trailAt(root, w.p.base)
	if err == nil && len(before) == 0 && len(w.baseRecords) > 0 {
		before = w.baseRecords
	}
	if err == nil {
		var after map[string]Record
		if after, err = w.afterRecords(ctx, root, ref); err == nil {
			for id, r := range before {
				a, ok := after[id]
				if (!ok || a != r) && !w.p.k.names(id) {
					outside = append(outside, id)
				}
			}
			return append(outside, w.added(before, after)...)
		}
	}
	w.b.Note("%s: %v", trailRel, err)
	w.b.Fail(ReasonNoHandoff)
	return []string{trailRel}
}

func (w *worker) afterRecords(ctx context.Context, root, ref string) (map[string]Record, error) {
	diff, err := branchOutput(leadGit(root, "diff-tree", "-r", "--name-only", w.p.base, ref, "--", trailRel))
	if err == nil && len(bytes.TrimSpace(diff)) == 0 {
		if _, err := os.Stat(filepath.Join(w.b.Home(), harness.StateDir, "brain.db")); err == nil {
			if recs, err := w.exportBoxRecords(ctx); err == nil {
				return recs, nil
			}
		}
	}
	return trailAt(root, ref)
}

// added fills the result's handoff and change records: the IDs on the
// branch and not at the base.
func (w *worker) added(before, after map[string]Record) (refused []string) {
	win := window{w.start, time.Now(), w.p.zone.loc}
	var ids []string
	for id := range after {
		if _, ok := before[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var handoffs []Record
	for _, id := range ids {
		switch r := after[id]; r.Kind {
		case "handoff":
			handoffs = append(handoffs, r)
		case "change":
			w.res.Changes = append(w.res.Changes, r)
		default:
			refused = append(refused, fmt.Sprintf("%.128q (added, kind %.128q)", id, r.Kind))
			continue
		}
		if why := win.outside(after[id]); why != "" {
			refused = append(refused, fmt.Sprintf("%.128q (added, %s)", id, why))
		}
	}
	switch len(handoffs) {
	case 0:
		w.b.Fail(ReasonNoHandoff)
	case 1:
		w.res.Handoff = &handoffs[0]
	default:
		w.b.Note("the branch adds %d handoffs; the newest is printed", len(handoffs))
		w.b.Fail(ReasonTwoHandoffs)
		w.res.Handoff = &handoffs[len(handoffs)-1]
	}
	return refused
}

// named is a note's list, at most maxNamed entries.
func named(s []string) string {
	if len(s) > maxNamed {
		return strings.Join(s[:maxNamed], ", ") + fmt.Sprintf(" and %d more", len(s)-maxNamed)
	}
	return strings.Join(s, ", ")
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
