package metrics

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

const agyWorktreesMarker = ".gemini/antigravity-cli/worktrees/"

// parseAgyWorktreePath extracts the parent conversation ID and worktree name
// from a path matching */.gemini/antigravity-cli/worktrees/<conv>/<name>.
func parseAgyWorktreePath(p string) (convID, name string, ok bool) {
	s := filepath.ToSlash(filepath.Clean(strings.TrimSpace(p)))
	var rest string
	if idx := strings.Index(s, "/"+agyWorktreesMarker); idx >= 0 {
		rest = s[idx+len("/"+agyWorktreesMarker):]
	} else if strings.HasPrefix(s, agyWorktreesMarker) {
		rest = strings.TrimPrefix(s, agyWorktreesMarker)
	} else {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func isAgyWorktreePath(p string) bool {
	_, _, ok := parseAgyWorktreePath(p)
	return ok
}

// isNonCollapsibleRoot reports whether r is a system, home, temp, or ephemeral
// probe/sandbox/smoke directory that must never prefix-collapse child paths.
func isNonCollapsibleRoot(r string) bool {
	clean := filepath.Clean(strings.TrimSpace(r))
	if clean == "" || clean == "." || clean == "/" || filepath.Dir(clean) == clean {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && sameOrCleanEqual(clean, home) {
		return true
	}
	if isHomeRoot(clean) || isTempRoot(clean) {
		return true
	}
	return hasEphemeralSegment(clean)
}

func sameOrCleanEqual(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	return ca == cb
}

func isHomeRoot(clean string) bool {
	s := strings.TrimPrefix(filepath.ToSlash(clean), "/")
	parts := strings.Split(s, "/")
	if len(parts) == 1 && (parts[0] == "home" || parts[0] == "Users") {
		return true
	}
	return len(parts) == 2 && (parts[0] == "home" || parts[0] == "Users") && parts[1] != ""
}

func isTempRoot(clean string) bool {
	tmp := filepath.Clean(os.TempDir())
	for _, cand := range []string{tmp, "/tmp", "/private/tmp", "/var/tmp"} {
		if clean == filepath.Clean(cand) {
			return true
		}
	}
	if realTmp, err := filepath.EvalSymlinks(tmp); err == nil && clean == filepath.Clean(realTmp) {
		return true
	}
	return false
}

func hasEphemeralSegment(clean string) bool {
	return hasPathSegment(clean, "smoke-", "fyr-agy-probes", "probes", "sandbox") ||
		hasPathSegment(clean, "fyr-agy-probes-")
}

func isProbeBreakdownRoot(r string) bool {
	clean := filepath.Clean(strings.TrimSpace(r))
	return clean == "/" || hasPathSegment(clean, "fyr-agy-probes-", "fyr-agy-probes", "probes")
}

func hasPathSegment(clean, prefix string, exact ...string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(clean), "/") {
		if prefix != "" && strings.HasPrefix(seg, prefix) {
			return true
		}
		for _, want := range exact {
			if seg == want {
				return true
			}
		}
	}
	return false
}

func isUsageProbeSummary(root, title, preview string) bool {
	clean := filepath.Clean(strings.TrimSpace(root))
	if clean != "/" && !isProbeBreakdownRoot(clean) {
		return false
	}
	return strings.Contains(title, "/usage") || strings.Contains(preview, "/usage")
}

func resolveGitWorktreeOnDisk(dir string) (string, bool) {
	clean := filepath.Clean(strings.TrimSpace(dir))
	if clean == "" || clean == "." {
		return "", false
	}
	fi, err := os.Stat(clean)
	if err != nil || !fi.IsDir() {
		return "", false
	}
	main, top, err := trail.Roots(clean)
	if err != nil || main == "" {
		return "", false
	}
	if !sameDirPath(main, top) || isAgyWorktreePath(clean) {
		return filepath.Clean(main), true
	}
	return "", false
}

func sameDirPath(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = filepath.Clean(a)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = filepath.Clean(b)
	}
	return ra == rb
}

func resolveWorktreeRoots(convs []agyConv, db *sql.DB) {
	byID, parents, knownReal := loadExistingSessionRoots(db)
	for i := range convs {
		c := &convs[i]
		if c.parent != "" {
			parents[c.id] = c.parent
		}
		if main, ok := resolveGitWorktreeOnDisk(c.root); ok {
			c.root = main
		}
		if c.root != "" && !isAgyWorktreePath(c.root) {
			byID[c.id] = filepath.Clean(c.root)
			knownReal = appendRealProjectCandidate(knownReal, c.root)
		}
	}
	for i := range convs {
		c := &convs[i]
		if c.root != "" && !isAgyWorktreePath(c.root) {
			continue
		}
		if resolved := resolveFallbackRoot(c.id, c.root, c.parent, byID, parents, knownReal); resolved != "" {
			c.root = resolved
			byID[c.id] = resolved
		}
	}
}

func loadExistingSessionRoots(db *sql.DB) (byID, parents map[string]string, knownReal []string) {
	byID, parents = map[string]string{}, map[string]string{}
	if db == nil {
		return byID, parents, nil
	}
	rows, err := db.Query(`SELECT id, COALESCE(project_root, ''), COALESCE(parent_id, '') FROM sessions`)
	if err != nil {
		return byID, parents, nil
	}
	defer rows.Close()
	for rows.Next() {
		var id, root, parent string
		if rows.Scan(&id, &root, &parent) != nil {
			continue
		}
		if parent != "" {
			parents[id] = parent
		}
		if root != "" && !isAgyWorktreePath(root) {
			clean := filepath.Clean(root)
			byID[id] = clean
			knownReal = appendRealProjectCandidate(knownReal, clean)
		}
	}
	return byID, parents, knownReal
}

func appendRealProjectCandidate(list []string, root string) []string {
	clean := filepath.Clean(strings.TrimSpace(root))
	if clean == "" || isNonCollapsibleRoot(clean) || isAgyWorktreePath(clean) {
		return list
	}
	for _, existing := range list {
		if existing == clean {
			return list
		}
	}
	return append(list, clean)
}

func resolveFallbackRoot(id, rawRoot, parent string, byID, parents map[string]string, knownReal []string) string {
	if r := lookupParentRoot(parent, byID, parents); r != "" {
		return r
	}
	convID, wtName, ok := parseAgyWorktreePath(rawRoot)
	if !ok {
		return ""
	}
	if convID != "" && convID != id {
		if r := byID[convID]; r != "" {
			return r
		}
		if r := lookupParentRoot(convID, byID, parents); r != "" {
			return r
		}
	}
	return matchWorktreeToKnownRoot(wtName, knownReal)
}

func lookupParentRoot(parent string, byID, parents map[string]string) string {
	seen := map[string]bool{}
	for cur := parent; cur != "" && !seen[cur]; cur = parents[cur] {
		seen[cur] = true
		if r := byID[cur]; r != "" {
			return r
		}
	}
	return ""
}

func matchWorktreeToKnownRoot(wtName string, knownReal []string) string {
	for _, root := range knownReal {
		if fi, err := os.Stat(filepath.Join(root, ".git", "worktrees", wtName)); err == nil && fi.IsDir() {
			return root
		}
	}
	var match string
	for _, root := range knownReal {
		if filepath.Base(root) == wtName {
			if match != "" && match != root {
				return ""
			}
			match = root
		}
	}
	return match
}

func resolveSessionRoots(all []sess) (byID map[string]string, wtMap map[string]string, knownReal []string) {
	byID, parents, wtMap := map[string]string{}, map[string]string{}, map[string]string{}
	for _, x := range all {
		if x.parent != "" {
			parents[x.id] = x.parent
		}
		if x.root == "" {
			continue
		}
		clean := filepath.Clean(x.root)
		if main, ok := resolveGitWorktreeOnDisk(clean); ok {
			wtMap[clean] = main
			byID[x.id] = main
			knownReal = appendRealProjectCandidate(knownReal, main)
		} else if !isAgyWorktreePath(clean) {
			byID[x.id] = clean
			knownReal = appendRealProjectCandidate(knownReal, clean)
		}
	}
	for _, x := range all {
		clean := filepath.Clean(strings.TrimSpace(x.root))
		if byID[x.id] != "" {
			continue
		}
		if r := resolveFallbackRoot(x.id, clean, x.parent, byID, parents, knownReal); r != "" {
			byID[x.id] = r
			if clean != "" {
				wtMap[clean] = r
			}
		}
	}
	return byID, wtMap, knownReal
}
