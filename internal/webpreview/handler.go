package webpreview

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type previewHandler struct {
	token      string
	tokenHash  [sha256.Size]byte
	validToken bool
	assets     map[string]Asset
	defaultCSP string
}

// NewHandler builds a read-only in-memory HTTP handler gated behind /<token>/...
// using constant-time token comparison and strict offline security headers.
func NewHandler(token string, assets map[string]Asset, fallbackCSP string) http.Handler {
	csp := fallbackCSP
	if strings.TrimSpace(csp) == "" {
		csp = defaultCSP
	}
	valid := isValidToken(token)
	var sum [sha256.Size]byte
	if valid {
		sum = sha256.Sum256([]byte(token))
	}
	return &previewHandler{
		token:      token,
		tokenHash:  sum,
		validToken: valid,
		assets:     normalizeAssets(assets),
		defaultCSP: csp,
	}
}

func isValidToken(tok string) bool {
	return tok != "" && tok != "." && tok != ".." &&
		!strings.Contains(tok, "/") && !strings.Contains(tok, "\\") &&
		!strings.ContainsRune(tok, 0)
}

func (h *previewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w, h.defaultCSP)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cand, assetPath, ok := splitTokenPath(r)
	if !ok || !h.matchToken(cand) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	asset, ok := h.assets[assetPath]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	serveAsset(w, r.Method, asset, h.defaultCSP)
}

func (h *previewHandler) matchToken(candidate string) bool {
	if !h.validToken {
		return false
	}
	candHash := sha256.Sum256([]byte(candidate))
	if subtle.ConstantTimeCompare(candHash[:], h.tokenHash[:]) != 1 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(h.token)) == 1
}

func setSecurityHeaders(w http.ResponseWriter, csp string) {
	hdr := w.Header()
	hdr.Set("Content-Security-Policy", csp)
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("X-Frame-Options", "DENY")
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("Cross-Origin-Resource-Policy", "same-origin")
}

func serveAsset(w http.ResponseWriter, method string, asset Asset, fallbackCSP string) {
	hdr := w.Header()
	if strings.TrimSpace(asset.CSP) != "" {
		hdr.Set("Content-Security-Policy", asset.CSP)
	} else {
		hdr.Set("Content-Security-Policy", fallbackCSP)
	}
	ct := asset.ContentType
	if strings.TrimSpace(ct) == "" {
		ct = "application/octet-stream"
	}
	hdr.Set("Content-Type", ct)
	hdr.Set("Content-Length", strconv.Itoa(len(asset.Body)))
	w.WriteHeader(http.StatusOK)
	if method == http.MethodGet {
		_, _ = w.Write(asset.Body) // response has started; nothing left to report
	}
}

func splitTokenPath(r *http.Request) (token, assetPath string, ok bool) {
	if r == nil || r.URL == nil || hasTraversal(r) || !strings.HasPrefix(r.URL.Path, "/") {
		return "", "", false
	}
	tok, sub, hasSlash := strings.Cut(r.URL.Path[1:], "/")
	if tok == "" {
		return "", "", false
	}
	if !hasSlash || sub == "" {
		return tok, "/", true
	}
	for _, seg := range strings.Split(sub, "/") {
		if seg == "" {
			return "", "", false
		}
	}
	return tok, "/" + sub, true
}

func hasTraversal(r *http.Request) bool {
	u := r.URL
	for _, raw := range []string{u.EscapedPath(), u.RawPath, requestURIPath(r.RequestURI)} {
		if raw != "" && hasRawTraversal(raw) {
			return true
		}
	}
	if strings.Contains(u.Path, "\\") || strings.ContainsRune(u.Path, 0) {
		return true
	}
	return checkSegments(strings.TrimPrefix(u.Path, "/"), false)
}

func requestURIPath(uri string) string {
	pathPart, _, _ := strings.Cut(uri, "?")
	pathPart, _, _ = strings.Cut(pathPart, "#")
	if strings.HasPrefix(pathPart, "/") {
		return pathPart
	}
	return ""
}

func hasRawTraversal(raw string) bool {
	lower := strings.ToLower(raw)
	if strings.Contains(raw, "\\") || strings.ContainsRune(raw, 0) ||
		strings.Contains(lower, "%5c") || strings.Contains(lower, "%00") {
		return true
	}
	return checkSegments(strings.TrimPrefix(raw, "/"), true)
}

func checkSegments(trimmed string, unescape bool) bool {
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == "." || seg == ".." {
			return true
		}
		if !unescape || seg == "" {
			continue
		}
		dec, err := url.PathUnescape(seg)
		if err != nil || dec == "." || dec == ".." ||
			strings.Contains(dec, "/") || strings.Contains(dec, "\\") ||
			strings.ContainsRune(dec, 0) {
			return true
		}
	}
	return false
}
