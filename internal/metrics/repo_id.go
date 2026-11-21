package metrics

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// NormalizeRepoID returns a canonical portable repository identifier via
// trail.NormalizeRepoID.
func NormalizeRepoID(originURL, absRoot string) string {
	return trail.NormalizeRepoID(originURL, absRoot)
}

// ResolveRepoID inspects root's git checkout via trail.ResolveRepoID without
// leaking embedded credentials, skipping deleted Antigravity worktree paths.
func ResolveRepoID(root string) string {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" || trimmed == search.GlobalWorkspace {
		return ""
	}
	absRoot, err := filepath.Abs(trimmed)
	if err != nil {
		absRoot = filepath.Clean(trimmed)
	} else {
		absRoot = filepath.Clean(absRoot)
	}
	if isAgyWorktreePath(absRoot) {
		if _, err := os.Stat(absRoot); err != nil {
			return ""
		}
	}
	return trail.ResolveRepoID(trimmed)
}

func (w *writer) repoID(root string) string {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" || trimmed == search.GlobalWorkspace {
		return ""
	}
	if w.repoIDs == nil {
		w.repoIDs = map[string]string{}
	}
	if id, ok := w.repoIDs[trimmed]; ok {
		return id
	}
	id := ResolveRepoID(trimmed)
	w.repoIDs[trimmed] = id
	return id
}
