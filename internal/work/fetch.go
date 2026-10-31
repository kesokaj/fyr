package work

import (
	"bytes"
	"errors"
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

	"github.com/kesokaj/fyr/internal/trail"
)

// maxNamed is how many paths or IDs one note names.
const maxNamed = 20

// collect is steps 6-8. A worker a safety reason failed, or whose session
// never started, gets none of them (D38); a timed-out, killed or otherwise
// failed one is fetched (D26).
func (w *worker) collect() {
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
	w.scope()
	w.collected = w.drafts()
}

// leadGit is git in the lead's checkout for the steps after the session,
// which may have written the checkout's .git: no replace refs, no grafts
// and no commit-graph, any of which can rewire a commit's parents, so the
// scope check reads the history a push sends, and no hooks (the security
// review of Phase 2, finding 4 and round 2; PLAN.md item 42). The rest of the
// lead's .git/config is not checked: a session that rewrites it runs as the
// operator's uid, the threat model eval accepts (GUIDE-testing § 4).
func leadGit(root string, args ...string) *exec.Cmd {
	cmd := trail.Command(root, append([]string{"--no-replace-objects", "-c", "core.hooksPath=/dev/null", "-c", "core.commitGraph=false"}, args...)...)
	cmd.Env = append(cmd.Env, "GIT_GRAFT_FILE="+os.DevNull)
	return cmd
}

// leadUnchanged is D35: the lead's HEAD is still the base, the reflogs of
// HEAD and its branch have no entry the run added (a commit and a reset
// back leave HEAD at the base, BUG-198), and its tracked and unignored tree
// is still clean, so the fetch and the drafts land in the checkout the
// worker was cut from. It does not see .git or ignored paths, and
// trail.Dirty's own git calls run without leadGit's options: leadGit and
// the drafts' os.Root narrow what a session can do through those.
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

// fetchLimit bounds step 6. It holds the collect lock every other worker
// waits on, and no session's work takes this long to copy (PLAN.md item 45,
// finding S3).
const fetchLimit = 10 * time.Minute

// maxFetchOutput bounds what a fetch's output keeps; the rest is discarded.
const maxFetchOutput = 64 << 10

// cappedWriter keeps the first maxFetchOutput bytes.
// Its buffer is a named field: embedded, its ReadFrom would be io.Copy's way
// around Write.
type cappedWriter struct{ kept bytes.Buffer }

func (c *cappedWriter) Write(p []byte) (int, error) {
	if room := maxFetchOutput - c.kept.Len(); room > 0 {
		c.kept.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// runBounded is CombinedOutput with a deadline and a bound on the output, in
// its own process group, which is killed whole at the deadline: upload-pack
// is cmd's child, and a kill of cmd alone leaves it holding the output pipe.
// The kill after Wait takes what outlived cmd: a live member pins the group's
// id, and with none the kill finds nothing, as models.go's does.
func runBounded(cmd *exec.Cmd, limit time.Duration) ([]byte, error) {
	var out cappedWriter
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pgid := cmd.Process.Pid
	var late atomic.Bool
	t := time.AfterFunc(limit, func() {
		late.Store(true)
		syscall.Kill(-pgid, syscall.SIGKILL)
	})
	err := cmd.Wait()
	t.Stop()
	syscall.Kill(-pgid, syscall.SIGKILL)
	if late.Load() {
		return out.kept.Bytes(), fmt.Errorf("timed out after %s", limit)
	}
	return out.kept.Bytes(), err
}

// lazyFetchSafe is trail.LazyFetchSafe, a variable so a test can take either
// side.
var lazyFetchSafe = trail.LazyFetchSafe

// gitFloor is refusal 25: under the docker runner the host's upload-pack
// reads a clone the container wrote, and a git that does not honour
// GIT_NO_LAZY_FETCH would run the promisor it configures (PLAN.md item 45,
// finding S2). A host session runs as the operator already.
func gitFloor(runner string) error {
	if runner == RunnerDocker && !lazyFetchSafe() {
		return errors.New("--runner docker: this git does not honour GIT_NO_LAZY_FETCH, so a fetch from a clone the container wrote could run its config: upgrade git")
	}
	return nil
}

// hostMemKiB bounds the address space of the git commands that read what a
// docker session wrote, in KiB: 8 GiB, the worker container's own --memory.
var hostMemKiB = 8 << 20

// capMemory runs cmd under that limit under the docker runner, through a sh
// that exits when it cannot set it, so a limit not set fails the command. A
// clone's core.bigFileThreshold makes the host's pack-objects hold a blob
// whole, and fsck in index-pack does so with no config at all, each from a
// file of a few MiB (the v5.5.0 gate). Children inherit it. A host session
// runs as the operator already.
func capMemory(cmd *exec.Cmd, runner string) *exec.Cmd {
	if runner != RunnerDocker {
		return cmd
	}
	args := append([]string{"-c", `ulimit -v "$0" && exec "$@"`, strconv.Itoa(hostMemKiB)}, cmd.Args...)
	c := exec.Command("sh", args...)
	c.Dir, c.Env = cmd.Dir, cmd.Env
	return c
}

// fetch is step 6: the clone's branch into the lead's checkout, by path,
// with no tags, and every object checked, since the clone is the agent's.
// Naming .git, and --strict, close upload-pack's fallback lookups: a .git
// that is no valid repository makes it look at the clone's top level, or at
// .git/.git (P12b's security review, finding S1). A .git changed after
// GitIntact is still honoured.
func (w *worker) fetch() bool {
	ref := "refs/heads/" + w.p.k.Branch()
	out, err := runBounded(capMemory(leadGit(w.o.Root, "-c", "fetch.fsckObjects=true", "fetch", "-q", "--no-tags",
		"--no-recurse-submodules", "--no-write-fetch-head", "--upload-pack", "git upload-pack --strict",
		filepath.Join(w.b.App(), ".git"), ref+":"+ref), w.o.Runner), fetchLimit)
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
// vanished by the branch's tip is named there; then the records it added,
// handoff and change records only (D138).
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
// that the base did not hold as the same: the lead squash-merges the tip,
// and a link reaches outside the checkout through a path its Files: may
// cover (the v5.0.0 gate's security review). --ignore-submodules=none: a
// .gitmodules entry with ignore = all would hide a gitlink (round 2).
func links(root, base, ref string) ([]string, error) {
	raw, err := leadGit(root, "diff-tree", "-r", "-z", "--no-renames", "--no-ext-diff", "--ignore-submodules=none", base, ref).Output()
	if err != nil {
		return nil, err
	}
	// -z raw: ":<old mode> <new mode> <old> <new> <status>" NUL "<path>" NUL.
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

// changedPaths is every path a commit of rng changed against each of its
// parents, once each, a gitlink that .gitmodules says to ignore included.
func changedPaths(root, rng string) ([]string, error) {
	revs, err := leadGit(root, "rev-list", rng).Output()
	if err != nil {
		return nil, err
	}
	cmd := leadGit(root, "diff-tree", "--stdin", "-r", "-m", "--root", "--no-renames", "--no-ext-diff", "--ignore-submodules=none", "--name-only", "-z", "--no-commit-id")
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
// changed or vanished records Files: does not name and the added records
// of a kind added refuses, and fills the handoff and change records the
// branch added.
func (w *worker) records(root, ref string) (outside []string) {
	before, err := trailAt(root, w.p.base, w.o.Runner)
	if err == nil {
		var after map[string]Record
		if after, err = trailAt(root, ref, w.o.Runner); err == nil {
			for id, r := range before {
				if a, ok := after[id]; (!ok || a != r) && !w.p.k.names(id) {
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

// added fills the result's handoff and change records: the IDs on the
// branch and not at the base. It returns the added records of every other
// kind, named with it: those reach the trail only through the lead, a
// worker drafts them, and the squash would carry them past the lead's
// reading (D138; a review or testing record needs its own D-row). It also
// returns a handoff or change record dated outside the worker's window,
// or whose updated is not the UTC form fyr record writes: either would
// rank as newer than the lead's own (D139, D140).
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
			// Both are the worker's text: quoted, and cut to 128 characters.
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
