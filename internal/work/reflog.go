package work

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// reflogWindow is how many of a ref's newest reflog entries the guard
// reads after the run to find the one it recorded at the start.
const reflogWindow = 50

// refEntry is one guarded ref and its newest reflog entry at the start.
type refEntry struct{ ref, entry string }

// reflog is a ref's newest reflog entries in the lead, newest first, each
// "<sha> <ref>@{<date>} <subject>": the date is the entry's own, so a line
// names the same entry before and after the run.
func reflog(root, ref string, n int) ([]string, error) {
	out, err := leadGit(root, "reflog", "show", "-n", fmt.Sprint(n), "--date=iso-strict", "--format=%H %gd %gs", ref, "--").Output()
	if err != nil {
		return nil, fmt.Errorf("git reflog of %s in %s: %v", ref, root, err)
	}
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// guardedRefs is HEAD and the branch it points to: another worktree of the
// checkout moves that branch, and back, through its own reflog alone (the
// P4 security review, finding 3). A detached HEAD is HEAD alone.
func guardedRefs(root string) ([]string, error) {
	out, err := leadGit(root, "symbolic-ref", "-q", "HEAD").Output()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return []string{"HEAD", strings.TrimSpace(string(out))}, nil
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return []string{"HEAD"}, nil
	}
	return nil, fmt.Errorf("git symbolic-ref HEAD in %s: %v", root, err)
}

// reflogStart is refusal 23 (BUG-198): the newest reflog entry of each
// guarded ref, which step 6's guard compares. A checkout that keeps no
// reflog would let a commit and a reset during the run pass unseen.
func reflogStart(root string) ([]refEntry, error) {
	refs, err := guardedRefs(root)
	if err != nil {
		return nil, err
	}
	var start []refEntry
	for _, ref := range refs {
		es, err := reflog(root, ref, 1)
		if err != nil {
			return nil, err
		}
		if len(es) == 0 {
			return nil, fmt.Errorf("%s keeps no %s reflog (core.logAllRefUpdates); the run compares it to see the lead's commits and resets (BUG-198): set core.logAllRefUpdates true and commit once", root, ref)
		}
		start = append(start, refEntry{ref, es[0]})
	}
	return start, nil
}

// reflogMoved is "" when each guarded ref's newest reflog entry is still
// the one recorded at the start, else the note for the first that moved:
// how many entries the run added and the first of them, the lead's first
// move. Expiry drops only entries older than gc.reflogExpire, so it reads
// as a move only when it drops the recorded entry itself, which fails
// closed.
func reflogMoved(root string, start []refEntry) string {
	for _, s := range start {
		if why := refMoved(root, s); why != "" {
			return why
		}
	}
	return ""
}

func refMoved(root string, s refEntry) string {
	es, err := reflog(root, s.ref, reflogWindow)
	if err != nil {
		return fmt.Sprintf("the lead checkout's %s reflog could not be read: %v", s.ref, err)
	}
	for i, e := range es {
		if e != s.entry {
			continue
		}
		if i == 0 {
			return ""
		}
		return fmt.Sprintf("the lead checkout's %s reflog moved during the run: %d new entries, the first %s", s.ref, i, entry(es[i-1]))
	}
	if len(es) == 0 {
		return fmt.Sprintf("the lead checkout's %s reflog lost every entry during the run", s.ref)
	}
	return fmt.Sprintf("the lead checkout's %s reflog moved during the run: over %d new entries, the newest %s", s.ref, len(es)-1, entry(es[0]))
}

// entry is one reflog line for a note: the short sha, the selector and the
// subject, quoted and cut, since the lead's agent wrote it.
func entry(e string) string {
	sha, rest, _ := strings.Cut(e, " ")
	sel, subj, _ := strings.Cut(rest, " ")
	return fmt.Sprintf("%s %s %.128q", short(sha), sel, subj)
}
