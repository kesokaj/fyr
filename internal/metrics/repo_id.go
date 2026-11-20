package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// NormalizeRepoID returns a canonical portable repository identifier:
// "git:<host>/<org>/<repo>" (lowercased, stripped of scheme, userinfo, port,
// trailing '/' and '.git') when originURL names a remote host and repository,
// else "path:<sha256(cleanAbsRoot)[:16]>" when absRoot is non-empty.
func NormalizeRepoID(originURL, absRoot string) string {
	if gitID, ok := normalizeGitOrigin(originURL); ok {
		return gitID
	}
	root := strings.TrimSpace(absRoot)
	if root == "" || root == search.GlobalWorkspace {
		return ""
	}
	clean := filepath.Clean(root)
	sum := sha256.Sum256([]byte(clean))
	return "path:" + hex.EncodeToString(sum[:])[:16]
}

// ResolveRepoID inspects root's git origin URL best-effort and combines it with
// filepath.Abs(root) via NormalizeRepoID without leaking embedded credentials.
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
	originURL := readGitOriginURL(absRoot)
	if originURL == "" && isAgyWorktreePath(absRoot) {
		if _, err := os.Stat(absRoot); err != nil {
			return ""
		}
	}
	return NormalizeRepoID(originURL, absRoot)
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

func normalizeGitOrigin(raw string) (string, bool) {
	u := strings.TrimSpace(raw)
	if u == "" {
		return "", false
	}
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	var host, repoPath string
	var ok bool
	if strings.Contains(u, "://") {
		host, repoPath, ok = parseSchemeOrigin(u)
	} else {
		host, repoPath, ok = parseSCPOrigin(u)
	}
	if !ok {
		return "", false
	}
	repoPath = cleanRepoPath(repoPath)
	host = strings.TrimSpace(host)
	if host == "" || repoPath == "" {
		return "", false
	}
	return "git:" + strings.ToLower(host+"/"+repoPath), true
}

func parseSchemeOrigin(u string) (string, string, bool) {
	idx := strings.Index(u, "://")
	scheme := strings.ToLower(strings.TrimSpace(u[:idx]))
	if scheme == "" || scheme == "file" {
		return "", "", false
	}
	rest := u[idx+3:]
	hostPart, pathPart, _ := strings.Cut(rest, "/")
	if at := strings.LastIndexByte(hostPart, '@'); at >= 0 {
		hostPart = hostPart[at+1:]
	}
	if h, afterColon, hasColon := strings.Cut(hostPart, ":"); hasColon {
		if isDigitsOnly(afterColon) {
			hostPart = h
		} else if h != "" {
			hostPart = h
			pathPart = strings.Trim(afterColon+"/"+pathPart, "/")
		}
	}
	return hostPart, pathPart, hostPart != "" && pathPart != ""
}

func parseSCPOrigin(u string) (string, string, bool) {
	if strings.HasPrefix(u, "/") || strings.HasPrefix(u, "./") || strings.HasPrefix(u, "../") || strings.HasPrefix(u, "~") {
		return "", "", false
	}
	left, right, hasColon := strings.Cut(u, ":")
	if !hasColon || strings.Contains(left, "/") {
		return "", "", false
	}
	if at := strings.LastIndexByte(left, '@'); at >= 0 {
		left = left[at+1:]
	}
	return left, right, left != "" && right != ""
}

func cleanRepoPath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	for {
		next := strings.Trim(p, "/")
		if len(next) >= 4 && strings.EqualFold(next[len(next)-4:], ".git") {
			next = strings.Trim(next[:len(next)-4], "/")
		}
		if next == p {
			break
		}
		p = next
	}
	if p == "" || p == "." {
		return ""
	}
	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == "/" || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return ""
	}
	return strings.Trim(cleaned, "/")
}

func isDigitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func readGitOriginURL(absRoot string) string {
	if cfgPath := findGitConfigPath(absRoot); cfgPath != "" {
		if b, err := os.ReadFile(cfgPath); err == nil {
			if u := parseOriginFromGitConfig(string(b)); u != "" {
				return u
			}
		}
	}
	fi, err := os.Stat(absRoot)
	if err != nil || !fi.IsDir() {
		return ""
	}
	out, err := trail.Command(absRoot, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func findGitConfigPath(dir string) string {
	cur := filepath.Clean(dir)
	for {
		dotGit := filepath.Join(cur, ".git")
		if fi, err := os.Lstat(dotGit); err == nil {
			if fi.IsDir() {
				return resolveGitDirConfig(dotGit)
			}
			if fi.Mode().IsRegular() {
				return resolveGitFileConfig(cur, dotGit)
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func resolveGitDirConfig(gitDir string) string {
	if commonRaw, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		cd := strings.TrimSpace(string(commonRaw))
		if cd != "" {
			if !filepath.IsAbs(cd) {
				cd = filepath.Join(gitDir, cd)
			}
			return filepath.Join(filepath.Clean(cd), "config")
		}
	}
	return filepath.Join(gitDir, "config")
}

func resolveGitFileConfig(cur, dotGit string) string {
	raw, err := os.ReadFile(dotGit)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(raw))
	gitDir, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return ""
	}
	gitDir = strings.TrimSpace(gitDir)
	if gitDir == "" {
		return ""
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(cur, gitDir)
	}
	return resolveGitDirConfig(filepath.Clean(gitDir))
}

func parseOriginFromGitConfig(data string) string {
	inOrigin := false
	for _, line := range strings.Split(data, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			sec := strings.ToLower(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
			inOrigin = sec == `remote "origin"` || sec == `remote 'origin'` || sec == "remote.origin"
			continue
		}
		if !inOrigin {
			continue
		}
		k, v, ok := strings.Cut(trimmed, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "url") {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
