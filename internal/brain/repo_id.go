package brain

import "github.com/kesokaj/fyr/internal/trail"

// NormalizeRepoID returns a canonical repository identifier via
// trail.NormalizeRepoID.
func NormalizeRepoID(originURL, absRoot string) string {
	return trail.NormalizeRepoID(originURL, absRoot)
}

// ResolveRepoID inspects root's git checkout via trail.ResolveRepoID and
// returns its canonical repo_id. Empty or global workspace roots return "".
func ResolveRepoID(root string) string {
	return trail.ResolveRepoID(root)
}
