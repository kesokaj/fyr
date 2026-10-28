package search

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// Why a conversation under the target root is left out of its dream; the
// dream names it by ID with this reason, never by its workspace's path,
// which belongs to a checkout the operator did not register.
const (
	whyNested  = "its workspace is a nested checkout, not this project"
	whyOutside = "it also has a workspace outside this project"
	whyUnread  = "its workspace's checkout could not be read"
	whyDream   = "a fyr dream conversation"
)

// workspaces judges, for one dream target, whether a conversation is the
// target's own (ledger item 77, the Phase 3 security review's finding 3): a
// path prefix alone sent a session in an unregistered checkout nested inside
// a registered one to the model.
type workspaces struct {
	root, main string
	mains      map[string]string // a directory -> its main root, "" when unreadable
	dream      []string          // the dream's working directory, every form (harness.DreamWorkPaths)
}

// newWorkspaces judges for root; a conversation with a workspace in exclude
// is the dream's own and never root's, even when root holds the config
// directory (a checkout at $HOME; visible dream plan D5).
func newWorkspaces(root string, exclude []string) *workspaces {
	w := &workspaces{root: filepath.Clean(root), mains: map[string]string{}, dream: exclude}
	w.main = w.mainOf(w.root)
	return w
}

// judge reports whether the conversation with these workspace URIs is kept,
// and why one that has a workspace under the root is not ("" for a
// conversation of another project, which is not named). Every URI must be a
// file URI under the root, and the nearest directory of it still on disk must
// resolve to the target's main root: a workspace since deleted is judged by
// the directory above it, so a nested checkout removed after its session
// reads as the target's own (a known limit). A conversation with a workspace
// in the dream's working directory is never kept, and is named only when a
// workspace of it is under the root.
func (w *workspaces) judge(uris string) (keep bool, why string) {
	var list []string
	if json.Unmarshal([]byte(uris), &list) != nil {
		return false, ""
	}
	under, outside, dream := 0, false, false
	for _, u := range list {
		p, err := url.Parse(u)
		if err != nil || p.Scheme != "file" {
			// A remote or unreadable workspace is not the root's: fail closed,
			// as every other doubt here does (the phase's security review,
			// finding 3).
			outside = true
			continue
		}
		ws := filepath.Clean(p.Path)
		if harness.InDreamWork(ws, w.dream) {
			dream = true
		}
		if ws != w.root && !strings.HasPrefix(ws, w.root+string(filepath.Separator)) {
			outside = true
			continue
		}
		under++
		switch m := w.mainOf(w.nearest(ws)); {
		case m == "" || w.main == "":
			why = whyUnread
		case m != w.main && why == "":
			why = whyNested
		}
	}
	switch {
	case under == 0:
		return false, ""
	case dream:
		return false, whyDream
	case why != "":
		return false, why
	case outside:
		return false, whyOutside
	}
	return true, ""
}

// nearest is ws or the closest directory above it that exists, never above
// the root; the root itself when nothing below it does.
func (w *workspaces) nearest(ws string) string {
	for d := ws; d != w.root; d = filepath.Dir(d) {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
	}
	return w.root
}

// mainOf is dir's main worktree root, read once per directory.
func (w *workspaces) mainOf(dir string) string {
	if m, ok := w.mains[dir]; ok {
		return m
	}
	m, _, err := trail.Roots(dir)
	if err != nil {
		m = ""
	}
	w.mains[dir] = m
	return m
}
