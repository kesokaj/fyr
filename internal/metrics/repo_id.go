package metrics

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

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
