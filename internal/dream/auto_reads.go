package dream

import (
	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

// AgySession is the dream's process check for `fyr auto`'s G5. It errs toward
// no session: an entry it cannot read is skipped, so a caller that must not
// run beside a session treats its error as a stop and says that it can miss
// one (R3).
func AgySession(goos string, uid int) (bool, error) { return agySession(goos, uid) }

// Target is one project `--all` knows: Skip says why it is left out, and is
// empty for a registered, migrated checkout.
type Target struct{ Root, Skip string }

// Targets is --all's list for `fyr auto`'s G6, read from a copy of the brain
// store, so a dry run and a real run take one path and neither opens the live
// store for it.
func Targets(home string) ([]Target, error) {
	tmp, cleanup, err := brainCopy(home)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	o := Options{Home: home, Projects: search.ListProjects,
		Scopes: func(string) ([]string, error) { return brain.Scopes(tmp) }}
	projects, skips, err := o.targets()
	if err != nil {
		return nil, err
	}
	var out []Target
	for _, p := range projects {
		out = append(out, Target{Root: p.Root, Skip: skipReason(p, tmp)})
	}
	for _, s := range skips {
		out = append(out, Target{Root: s.root, Skip: s.why})
	}
	return out, nil
}
