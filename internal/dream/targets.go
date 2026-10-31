package dream

import (
	"path/filepath"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/memory"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// notRegistered is why --all leaves out a checkout the operator never
// opted in, and how they opt it in (audit-choices D17: the dry run's skip
// lines are the re-registration listing).
const notRegistered = "not registered (fyr index --register)"

// skipped is a root --all leaves out before collecting, and why.
type skipped struct{ root, why string }

// targets is --project's one checkout, or for --all every project the file
// index registered and every project scope the brain store holds, each
// checkout once, whose checkout carries this home's registration marker
// (ADR-068, ADR-068a); the rest come back as skipped, each once. Run has
// already refused both and neither. --project needs no marker: naming the
// directory is the operator's opt-in (audit-choices D16).
func (o Options) targets() ([]search.Project, []skipped, error) {
	if o.Project != "" {
		abs, err := filepath.Abs(o.Project)
		if err != nil {
			return nil, nil, err
		}
		root, err := memory.Root(abs)
		if err != nil {
			return nil, nil, err
		}
		return []search.Project{{Root: root, Name: filepath.Base(root)}}, nil, nil
	}
	projects, err := o.Projects(o.Home)
	if err != nil {
		return nil, nil, err
	}
	scopesOf := o.Scopes
	if scopesOf == nil {
		scopesOf = brain.Scopes
	}
	scopes, err := scopesOf(o.Home)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range scopes {
		projects = append(projects, search.Project{Root: s, Name: filepath.Base(s)})
	}
	out, skips := o.registeredOnly(projects)
	return out, skips, nil
}

// registeredOnly splits projects into the checkouts registered under this
// home and the rest, each checkout once.
func (o Options) registeredOnly(projects []search.Project) ([]search.Project, []skipped) {
	// One checkout, however reached (a linked worktree's path, a symlinked
	// parent), is one target: its records would otherwise be sent twice
	// (security review of the dream over records, finding 6).
	seen := map[string]bool{}
	stateDir := filepath.Join(o.Home, harness.StateDir)
	var out []search.Project
	var skips []skipped
	for _, p := range projects {
		k := projectKey(p.Root)
		if seen[k] {
			continue
		}
		seen[k] = true
		if trail.Registered(stateDir, p.Root) {
			out = append(out, p)
			continue
		}
		// A root that is gone, or no checkout, says so: registering cannot
		// help it.
		why := absent(p)
		if why == "" {
			why = notRegistered
		}
		skips = append(skips, skipped{p.Root, why})
	}
	return out, skips
}
