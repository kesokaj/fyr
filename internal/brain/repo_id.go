package brain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

// NormalizeRepoID returns a canonical repository identifier: when originURL
// names a remote host and repository path (SSH or HTTPS), it normalizes both to
// "git:<host>/<org>/<repo>" in lowercase without userinfo, port, trailing
// slashes, or ".git"; otherwise it hashes filepath.Clean(absRoot) as
// "path:<16-hex-sha256>".
func NormalizeRepoID(originURL, absRoot string) string {
	if norm := normalizeGitOrigin(originURL); norm != "" {
		return "git:" + norm
	}
	trimmed := strings.TrimSpace(absRoot)
	if trimmed == "" {
		return ""
	}
	clean := filepath.Clean(trimmed)
	sum := sha256.Sum256([]byte(clean))
	return "path:" + hex.EncodeToString(sum[:])[:16]
}

// ResolveRepoID inspects root's git checkout (origin remote URL and main
// worktree root, falling back to filepath.Abs) and returns its canonical
// repo_id. Empty root returns "".
func ResolveRepoID(root string) string {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return ""
	}
	if main, _, err := trail.Roots(trimmed); err == nil {
		return NormalizeRepoID(readGitOriginURL(main), main)
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		abs = filepath.Clean(trimmed)
	}
	return NormalizeRepoID("", filepath.Clean(abs))
}

func readGitOriginURL(mainRoot string) string {
	cfgPath := filepath.Join(mainRoot, ".git", "config")
	if data, err := os.ReadFile(cfgPath); err == nil {
		if !bytes.Contains(data, []byte("origin")) && !bytes.Contains(data, []byte("include")) {
			return ""
		}
	}
	out, err := trail.Command(mainRoot, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func normalizeGitOrigin(raw string) string {
	u := strings.TrimSpace(raw)
	if u == "" || strings.HasPrefix(u, "/") || strings.HasPrefix(u, "./") || strings.HasPrefix(u, "../") || strings.HasPrefix(u, "~") {
		return ""
	}
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	hasScheme := false
	if idx := strings.Index(u, "://"); idx >= 0 {
		scheme := strings.ToLower(strings.TrimSpace(u[:idx]))
		if scheme == "" || scheme == "file" {
			return ""
		}
		hasScheme = true
		u = u[idx+3:]
	}
	host, repoPath := splitOriginAuthority(u, hasScheme)
	host = strings.TrimSpace(strings.Trim(host, "/"))
	repoPath = cleanOriginRepoPath(repoPath)
	if host == "" || repoPath == "" {
		return ""
	}
	return strings.ToLower(host + "/" + repoPath)
}

func splitOriginAuthority(s string, hasScheme bool) (host, repoPath string) {
	if hasScheme {
		auth, rest, ok := strings.Cut(s, "/")
		if !ok {
			return "", ""
		}
		if at := strings.LastIndexByte(auth, '@'); at >= 0 {
			auth = auth[at+1:]
		}
		if h, port, hasColon := strings.Cut(auth, ":"); hasColon {
			if isDigitsOnly(port) {
				return h, rest
			}
			return h, port + "/" + rest
		}
		return auth, rest
	}
	s = stripLeadingUserInfo(s)
	colon := strings.IndexByte(s, ':')
	slash := strings.IndexByte(s, '/')
	if colon >= 0 && (slash < 0 || colon < slash) {
		return s[:colon], s[colon+1:]
	}
	if slash >= 0 {
		return s[:slash], s[slash+1:]
	}
	return "", ""
}

func stripLeadingUserInfo(s string) string {
	head := s
	if slash := strings.IndexByte(s, '/'); slash >= 0 {
		head = s[:slash]
	}
	if at := strings.LastIndexByte(head, '@'); at >= 0 {
		return s[at+1:]
	}
	return s
}

func cleanOriginRepoPath(p string) string {
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
