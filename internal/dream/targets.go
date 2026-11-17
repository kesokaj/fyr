package dream

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// skipped is a root --all leaves out before collecting, and why.
type skipped struct{ root, why string }

// targets is --project's one workspace (git checkout, non-git directory, or
// search.GlobalWorkspace), or for --all every workspace discovered from the
// search index, Antigravity conversations, brain scopes, and HarnessRepo,
// deduplicated per checkout/directory (ADR-524).
func (o Options) targets() ([]search.Project, []skipped, error) {
	if o.Project != "" {
		return o.projectTarget()
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
	if o.HarnessRepo != "" {
		clean := filepath.Clean(o.HarnessRepo)
		projects = append(projects, search.Project{Root: clean, Name: filepath.Base(clean)})
	}
	out, skips := o.dedupeTargets(projects)
	return out, skips, nil
}

func (o Options) projectTarget() ([]search.Project, []skipped, error) {
	if o.Project == search.GlobalWorkspace {
		return []search.Project{{Root: search.GlobalWorkspace, Name: search.GlobalWorkspace}}, nil, nil
	}
	abs, err := filepath.Abs(o.Project)
	if err != nil {
		return nil, nil, err
	}
	if main, _, err := trail.Roots(abs); err == nil {
		return []search.Project{{Root: main, Name: filepath.Base(main)}}, nil, nil
	} else if !errors.Is(err, trail.ErrNotCheckout) {
		return nil, nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("%s is not a directory", abs)
	}
	clean := filepath.Clean(abs)
	return []search.Project{{Root: clean, Name: filepath.Base(clean)}}, nil, nil
}

// dedupeTargets normalizes git subdirectories to their main checkout, deduplicates
// projects by projectKey, and separates gone/invalid entries into skips.
func (o Options) dedupeTargets(projects []search.Project) ([]search.Project, []skipped) {
	// One checkout, however reached (a linked worktree's path, a symlinked
	// parent), is one target: its records would otherwise be sent twice
	// (security review of the dream over records, finding 6).
	seen := map[string]bool{}
	var out []search.Project
	var skips []skipped
	for _, p := range projects {
		if p.Root == "" {
			continue
		}
		p = normalizeTarget(p)
		k := projectKey(p.Root)
		if seen[k] {
			continue
		}
		seen[k] = true
		if p.Name == "" {
			p.Name = filepath.Base(p.Root)
		}
		if why := absent(p); why != "" {
			skips = append(skips, skipped{p.Root, why})
			continue
		}
		out = append(out, p)
	}
	return out, skips
}

func normalizeTarget(p search.Project) search.Project {
	if p.Root == "" || p.Root == search.GlobalWorkspace {
		return p
	}
	if main, _, err := trail.Roots(p.Root); err == nil && !samePath(p.Root, main) {
		return search.Project{Root: main, Name: filepath.Base(main)}
	}
	return p
}

func samePath(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	ra, errA := filepath.EvalSymlinks(ca)
	rb, errB := filepath.EvalSymlinks(cb)
	return errA == nil && errB == nil && ra == rb
}
