package search

import (
	"encoding/json"
	"fmt"
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
	sandboxes  []string          // eval/sandbox/auto directories under home
}

// newWorkspaces judges for root; a conversation with a workspace in exclude
// is the dream's own and never root's, even when root holds the config
// directory (a checkout at $HOME; visible dream plan D5).
func newWorkspaces(root string, exclude []string) *workspaces {
	clean := filepath.Clean(root)
	w := &workspaces{root: clean, mains: map[string]string{}, dream: exclude}
	if clean != GlobalWorkspace {
		w.main = w.mainOf(w.root)
	}
	return w
}

// judge reports whether the conversation with these workspace URIs is kept,
// and why one that has a workspace under the root is not ("" for a
// conversation of another project, which is not named). Every URI must be a
// file URI under the root, and the nearest directory of it still on disk must
// resolve to the target's main root (or "" when root itself is non-git). A
// conversation with a workspace in the dream's working directory is never kept,
// and is named only when a workspace of it is under the root.
func (w *workspaces) judge(uris string) (keep bool, why string) {
	if w.root == GlobalWorkspace {
		return judgeGlobalURIs(uris), ""
	}
	var list []string
	if json.Unmarshal([]byte(uris), &list) != nil {
		return false, ""
	}
	under, outside, dream := 0, false, false
	for _, u := range list {
		ws, ok := parseFileWorkspace(u)
		if !ok {
			outside = true
			continue
		}
		if harness.InDreamWork(ws, w.dream) {
			dream = true
		} else if isExcludedWorkspace(ws, nil, w.sandboxes) {
			return false, ""
		}
		if ws != w.root && !strings.HasPrefix(ws, w.root+string(filepath.Separator)) {
			outside = true
			continue
		}
		under++
		if r := w.checkUnder(ws); r == whyUnread || (r != "" && why == "") {
			why = r
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

func (w *workspaces) checkUnder(ws string) string {
	m := w.mainOf(w.nearest(ws))
	if w.main != "" && m == "" {
		return whyUnread
	}
	if m != w.main {
		return whyNested
	}
	return ""
}

func judgeGlobalURIs(uris string) bool {
	if strings.TrimSpace(uris) == "" {
		return true
	}
	var list []string
	if json.Unmarshal([]byte(uris), &list) != nil {
		return false
	}
	return len(list) == 0
}

func parseFileWorkspace(u string) (string, bool) {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "file" || p.Path == "" {
		return "", false
	}
	return filepath.Clean(p.Path), true
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

// ListAgyWorkspaces discovers unique Antigravity workspace directories from
// conversation_summaries.db, excluding fyr dream-work and {eval,sandbox,auto}
// sandboxes, and mapping empty workspace_uris lists to GlobalWorkspace.
func ListAgyWorkspaces(home string) ([]Project, error) {
	var out []Project
	err := withAgyTemp(home, "dream-agy-ws-", func(src, tmp string) error {
		var err error
		out, err = listAgyWorkspacesFromDB(src, tmp, home)
		return err
	})
	return out, err
}

func listAgyWorkspacesFromDB(src, tmp, home string) ([]Project, error) {
	db, err := openCopy(src, "conversation_summaries.db", tmp)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT COALESCE(workspace_uris, '') FROM conversation_summaries ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("reading Antigravity's conversation list: %w", err)
	}
	defer rows.Close()
	dreamPaths, sandboxes := harness.DreamWorkPaths(home), sandboxExcludePaths(home)
	seen := map[string]bool{}
	var out []Project
	for rows.Next() {
		var uris string
		if err := rows.Scan(&uris); err != nil {
			return nil, err
		}
		out = appendDiscoveredWorkspaces(out, seen, uris, dreamPaths, sandboxes)
	}
	return out, rows.Err()
}

func appendDiscoveredWorkspaces(out []Project, seen map[string]bool, uris string, dream, sandboxes []string) []Project {
	if strings.TrimSpace(uris) == "" {
		return addUniqueProject(out, seen, GlobalWorkspace)
	}
	var list []string
	if json.Unmarshal([]byte(uris), &list) != nil {
		return out
	}
	if len(list) == 0 {
		return addUniqueProject(out, seen, GlobalWorkspace)
	}
	var paths []string
	for _, u := range list {
		ws, ok := parseFileWorkspace(u)
		if !ok {
			continue
		}
		if isExcludedWorkspace(ws, dream, sandboxes) {
			return out
		}
		paths = append(paths, ws)
	}
	for _, ws := range paths {
		out = addUniqueProject(out, seen, ws)
	}
	return out
}

func addUniqueProject(out []Project, seen map[string]bool, root string) []Project {
	if seen[root] {
		return out
	}
	seen[root] = true
	return append(out, Project{Root: root, Name: filepath.Base(root)})
}

func sandboxExcludePaths(home string) []string {
	var homes []string
	if home != "" {
		homes = append(homes, filepath.Clean(home))
		if real, err := filepath.EvalSymlinks(home); err == nil && filepath.Clean(real) != homes[0] {
			homes = append(homes, filepath.Clean(real))
		}
	}
	var out []string
	for _, h := range homes {
		for _, sub := range []string{"eval", "sandbox", "auto"} {
			out = append(out, filepath.Clean(filepath.Join(h, harness.StateDir, sub)))
		}
		for _, sub := range []string{"fyr-auto", "fyr-eval"} {
			out = append(out, filepath.Clean(filepath.Join(h, "sandbox", sub)))
		}
	}
	return out
}

func isExcludedWorkspace(ws string, dream, sandboxes []string) bool {
	if harness.InDreamWork(ws, dream) || harness.InDreamWork(ws, sandboxes) {
		return true
	}
	slash := filepath.ToSlash(filepath.Clean(ws))
	for _, seg := range []string{"/.config/fyr/eval", "/.config/fyr/sandbox", "/.config/fyr/auto"} {
		if strings.HasSuffix(slash, seg) || strings.Contains(slash, seg+"/") {
			return true
		}
	}
	return false
}
