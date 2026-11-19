package gate

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

func loadLeasePatterns(opts GateOptions) ([]string, error) {
	var patterns []string
	for _, raw := range opts.Lease {
		for _, p := range strings.Split(raw, ",") {
			cleaned, err := normalizeLeasePattern(p)
			if err != nil {
				return nil, err
			}
			if cleaned != "" {
				patterns = append(patterns, cleaned)
			}
		}
	}
	if opts.LeaseFile != "" {
		data, err := os.ReadFile(opts.LeaseFile)
		if err != nil {
			return nil, refusef("read lease file %s: %v", opts.LeaseFile, err)
		}
		fromFile, err := ParseLease(string(data))
		if err != nil {
			return nil, err
		}
		if len(fromFile) == 0 {
			return nil, refusef("lease file %s contained no patterns", opts.LeaseFile)
		}
		patterns = append(patterns, fromFile...)
	}
	return patterns, nil
}

// ParseLease extracts file/glob lease patterns from plain lists or markdown/brief
// `Files:` sections.
func ParseLease(text string) ([]string, error) {
	var out []string
	inFiles := false
	hasFilesHeader := hasFilesSection(text)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if handled, keepIn, items := parseLeaseLine(line, hasFilesHeader, inFiles); handled {
			inFiles = keepIn
			for _, item := range items {
				clean, err := normalizeLeasePattern(item)
				if err != nil {
					return nil, err
				}
				if clean != "" {
					out = append(out, clean)
				}
			}
		} else {
			inFiles = keepIn
		}
	}
	return out, nil
}

func stripHeadingHashes(line string) (string, bool) {
	if !strings.HasPrefix(line, "#") {
		return line, false
	}
	return strings.TrimSpace(strings.TrimLeft(line, "#")), true
}

func parseLeaseHeader(line string) (string, bool) {
	body, _ := stripHeadingHashes(line)
	low := strings.ToLower(body)
	if strings.HasPrefix(low, "files:") || strings.HasPrefix(low, "lease:") {
		_, rest, _ := strings.Cut(body, ":")
		return rest, true
	}
	return "", false
}

func hasFilesSection(text string) bool {
	for _, raw := range strings.Split(text, "\n") {
		if _, ok := parseLeaseHeader(strings.TrimSpace(raw)); ok {
			return true
		}
	}
	return false
}

func parseLeaseLine(line string, hasHeader, inFiles bool) (bool, bool, []string) {
	if line == "" {
		return false, inFiles, nil
	}
	if rest, ok := parseLeaseHeader(line); ok {
		return true, true, splitLeaseTokens(rest)
	}
	if strings.HasPrefix(line, "#") {
		return false, false, nil
	}
	if hasHeader {
		if !inFiles {
			return false, false, nil
		}
		item, ok := stripBulletPrefix(line)
		if !ok {
			return false, false, nil
		}
		return true, true, splitLeaseTokens(item)
	}
	item, _ := stripBulletPrefix(line)
	return true, false, splitLeaseTokens(item)
}

func stripBulletPrefix(line string) (string, bool) {
	for _, p := range []string{"- ", "* "} {
		if strings.HasPrefix(line, p) {
			return strings.TrimSpace(line[len(p):]), true
		}
	}
	return line, false
}

func splitLeaseTokens(s string) []string {
	var out []string
	for _, field := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	}) {
		tok := strings.Trim(field, "`\"'")
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

func normalizeLeasePattern(p string) (string, error) {
	p = strings.TrimSpace(strings.Trim(strings.TrimSpace(p), "`\"'"))
	if p == "" {
		return "", nil
	}
	slash := filepath.ToSlash(p)
	if strings.HasPrefix(slash, "/") || strings.Contains(slash, "..") {
		return "", refusef("invalid lease pattern %q", p)
	}
	slash = strings.TrimPrefix(slash, "./")
	return slash, nil
}

func matchesAnyLease(patterns []string, relPath string) bool {
	relPath = filepath.ToSlash(strings.TrimPrefix(relPath, "./"))
	for _, pat := range patterns {
		if matchLeasePattern(pat, relPath) {
			return true
		}
	}
	return false
}

func matchLeasePattern(pat, relPath string) bool {
	if strings.HasSuffix(pat, "/") {
		return relPath == strings.TrimSuffix(pat, "/") || strings.HasPrefix(relPath, pat)
	}
	if !strings.Contains(pat, "**") {
		ok, err := path.Match(pat, relPath)
		return err == nil && ok
	}
	return matchSegments(strings.Split(pat, "/"), strings.Split(relPath, "/"))
}

func matchSegments(pats, segs []string) bool {
	if len(pats) == 0 {
		return len(segs) == 0
	}
	if pats[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegments(pats[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := path.Match(pats[0], segs[0])
	return err == nil && ok && matchSegments(pats[1:], segs[1:])
}
