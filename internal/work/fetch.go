package work

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

// maxNamed is how many paths or IDs one note names.
const maxNamed = 20

// collect is steps 6-8. A worker a safety reason failed, or whose session
// never started, gets none of them (D38); a timed-out, killed or otherwise
// failed one is fetched (D26).
func (w *worker) collect() {
	if !w.ran || w.unsafe() || !w.b.Intact() {
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
	w.scope()
	w.collected = w.drafts()
}

// leadGit is git in the lead's checkout for the steps after the session,
// which may have written the checkout's .git: no replace refs and no grafts,
// so the scope check reads the history a push sends, and no hooks (the
// security review of Phase 2, finding 4 and round 2). The rest of the
// lead's .git/config is not checked: a session that rewrites it runs as the
// operator's uid, the threat model eval accepts (GUIDE-testing § 4).
func leadGit(root string, args ...string) *exec.Cmd {
	cmd := trail.Command(root, append([]string{"--no-replace-objects", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Env = append(cmd.Env, "GIT_GRAFT_FILE="+os.DevNull)
	return cmd
}

// leadUnchanged is D35: the lead's HEAD is still the base and its tracked
// and unignored tree still clean, so the fetch and the drafts land in the
// checkout the worker was cut from. It does not see .git or ignored paths,
// and trail.Dirty's own git calls run without leadGit's options: leadGit
// and the drafts' os.Root narrow what a session can do through those.
func (w *worker) leadUnchanged() bool {
	if h, err := leadGit(w.o.Root, "rev-parse", "--verify", "-q", "HEAD^{commit}").Output(); err != nil || strings.TrimSpace(string(h)) != w.p.base {
		w.b.Note("the lead checkout's HEAD moved from %s; nothing is fetched or copied (D35)", short(w.p.base))
		return false
	}
	if dirty, err := trail.Dirty(w.o.Root); err != nil || dirty {
		w.b.Note("the lead checkout has changes since the start; nothing is fetched or copied (D35)")
		return false
	}
	return true
}

// fetch is step 6: the clone's branch into the lead's checkout, by path,
// with no tags, and every object checked, since the clone is the agent's.
func (w *worker) fetch() bool {
	ref := "refs/heads/" + w.p.k.Branch()
	out, err := leadGit(w.o.Root, "-c", "fetch.fsckObjects=true", "fetch", "-q", "--no-tags",
		"--no-recurse-submodules", "--no-write-fetch-head", w.b.App(), ref+":"+ref).CombinedOutput()
	if err != nil {
		w.b.Note("git fetch of %s: %v: %s", w.p.k.Branch(), err, strings.TrimSpace(string(out)))
		return false
	}
	h, err := leadGit(w.o.Root, "rev-parse", "--verify", "-q", ref+"^{commit}").Output()
	w.res.Head = strings.TrimSpace(string(h))
	return err == nil
}

// scope is step 7 (D18, D36): the branch descends from the base, every path
// any of its commits changed is in Files: (a path added and removed again
// still counts: a non-squash merge carries both commits, security review
// finding 2), and every record that existed at the base and changed or
// vanished by the branch's tip is named there; then the records it added.
func (w *worker) scope() {
	root, ref := w.o.Root, "refs/heads/"+w.p.k.Branch()
	if n, err := leadGit(root, "rev-list", "--count", w.p.base+".."+ref).Output(); err == nil {
		w.res.Commits, _ = strconv.Atoi(strings.TrimSpace(string(n)))
	}
	if leadGit(root, "merge-base", "--is-ancestor", w.p.base, ref).Run() != nil {
		w.b.Note("%s does not descend from %s", w.p.k.Branch(), short(w.p.base))
		w.b.Fail(ReasonRewrote)
		return
	}
	if w.res.Commits == 0 {
		w.b.Fail(ReasonNoCommits)
		return
	}
	paths, err := changedPaths(root, w.p.base+".."+ref)
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
	outside = append(outside, w.records(root, ref)...)
	if len(outside) > 0 {
		sort.Strings(outside)
		w.b.Note("changed outside its Files (D18): %s", named(outside))
		w.b.Fail(ReasonOutside)
	}
}

// changedPaths is every path a commit of rng changed against each of its
// parents, once each.
func changedPaths(root, rng string) ([]string, error) {
	revs, err := leadGit(root, "rev-list", rng).Output()
	if err != nil {
		return nil, err
	}
	cmd := leadGit(root, "diff-tree", "--stdin", "-r", "-m", "--root", "--no-renames", "--no-ext-diff", "--name-only", "-z", "--no-commit-id")
	cmd.Stdin = bytes.NewReader(revs)
	names, err := cmd.Output()
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

// records compares the trail at the base and on the branch: it returns the
// changed or vanished records Files: does not name, and fills the handoff
// and change records the branch added.
func (w *worker) records(root, ref string) (outside []string) {
	before, err := trailAt(root, w.p.base)
	if err == nil {
		var after map[string]Record
		if after, err = trailAt(root, ref); err == nil {
			for id, r := range before {
				if a, ok := after[id]; (!ok || a != r) && !w.p.k.names(id) {
					outside = append(outside, id)
				}
			}
			w.added(before, after)
			return outside
		}
	}
	w.b.Note("%s: %v", trailRel, err)
	w.b.Fail(ReasonNoHandoff)
	return []string{trailRel}
}

// added fills the result's handoff and change records: the IDs on the
// branch and not at the base.
func (w *worker) added(before, after map[string]Record) {
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
