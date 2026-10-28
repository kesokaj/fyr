package dream

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"unicode"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
)

// The branch (plan D12): each project's call runs in the operator's own
// Antigravity home, --home, when the oracle shows every tool needs review
// there, so the conversation stays where /resume lists it (R3); otherwise in
// the run's private home, built on its first private project and reused by
// the later ones. The oracle is asked again for every project: the settings
// may change between two projects of one run.

// branch is where one project's call runs.
type branch struct {
	env   []string // childEnv with the branch's HOME
	where string   // the home, as agyAuthHint names it
}

const (
	visibleWhere = "the operator's Antigravity home (--home)"
	privateWhere = "the dream job's private HOME"
)

// branchFor asks the oracle for p, logs the branch, and returns where p's
// call runs; false when the private home cannot be built, which fails p
// and not the run.
func (r *dreamRun) branchFor(p search.Project) (branch, bool) {
	env := os.Environ()
	ok, why := visibleHome(r.argv[0], r.o.Home, r.work, env)
	if ok {
		r.logf(p.Root + " agy home visible")
		return branch{childEnv(env, r.o.Home), visibleWhere}, true
	}
	r.logf(p.Root + " agy home private (" + logReason(why) + ")")
	if r.private == "" {
		dir, cleanup, err := agyHome(r.conf, runtime.GOOS)
		if err != nil {
			r.failed++
			r.logf(p.Root + " fail " + scrub(err.Error(), r.o.Home))
			return branch{}, false
		}
		r.private, r.dropPrivate = dir, cleanup
	}
	return branch{childEnv(env, r.private), privateWhere}, true
}

// logReason is the oracle's why as a log line takes it: it may quote a rule
// from the operator's settings, so it is redacted, folded to one line and
// stripped of control characters; paths stay as written (a scratch --home
// shown as ~ would read as the operator's own).
func logReason(why string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, oneLine(redact.Redact(why)))
}

// removePrivate removes the run's private home, if a project built one.
func (r *dreamRun) removePrivate() {
	if r.dropPrivate != nil {
		r.dropPrivate()
	}
}

// dryBranch is the branch line a dry run prints for a project it would
// dream over: it asks the oracle as the run does, and for the private
// branch checks the login the private home would copy. Before the first
// real run there is no dream-work to ask from, and the dry run makes none.
func (o Options) dryBranch() string {
	work := harness.DreamWork(o.Home)
	if _, err := os.Lstat(work); errors.Is(err, fs.ErrNotExist) {
		return "agy home not asked: no dream-work yet; a real run makes it, then asks the oracle"
	}
	ok, why := visibleHome(command()[0], o.Home, work, os.Environ())
	if ok {
		return "agy home visible (HOME " + o.Home + ")"
	}
	line := "agy home private (" + logReason(why) + ")"
	if _, _, err := findLogin(runtime.GOOS); err != nil {
		return line + ", would fail: " + err.Error()
	}
	return line + ": a copy of the login, with no grants"
}
