package check

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	atxHeadingRe = regexp.MustCompile(`^ {0,3}(#{1,6})[ \t]+(.+?)$`)
	mdLinkRe     = regexp.MustCompile(`\[(?:[^\[\]\n]|\[[^\[\]\n]*\])*\]\(([^)\n]+)\)`)
)

type inlineSpan struct {
	start, end int
	text       string
}

// anchorFindings checks Markdown #anchor links and § section refs across
// unfenced lines of one document (IN63).
func (k *checker) anchorFindings(where, text string) []Finding {
	var out []Finding
	unfenced(text, func(n int, l string) bool {
		masked, sp := lineCodeSpans(l)
		out = append(out, k.mdAnchorLinkFindings(where, text, n, masked)...)
		out = append(out, k.sectionRefFindings(where, n, l, sp)...)
		return true
	})
	return out
}

func (k *checker) mdAnchorLinkFindings(where, docText string, line int, masked string) []Finding {
	var out []Finding
	for _, m := range mdLinkRe.FindAllStringSubmatch(masked, -1) {
		fields := strings.Fields(m[1])
		if len(fields) == 0 {
			continue
		}
		rawURL := fields[0]
		if strings.Contains(rawURL, "://") || strings.HasPrefix(strings.ToLower(rawURL), "mailto:") || strings.ContainsAny(rawURL, "<>*{}$…") {
			continue
		}
		target, anchor, hasHash := strings.Cut(rawURL, "#")
		if !hasHash || anchor == "" {
			continue
		}
		if f, ok := k.checkMDAnchor(where, docText, line, rawURL, target, anchor); ok {
			out = append(out, f)
		}
	}
	return out
}

func (k *checker) checkMDAnchor(where, docText string, line int, rawURL, target, anchor string) (Finding, bool) {
	want := strings.ToLower(anchor)
	if target == "" {
		if !githubSlugs(extractHeadings(docText))[want] {
			return Finding{where, line, "anchor", fmt.Sprintf("`%s`: heading #%s not found in %s", rawURL, anchor, where)}, true
		}
		return Finding{}, false
	}
	if !strings.HasSuffix(strings.ToLower(path.Clean(target)), ".md") {
		return Finding{}, false
	}
	resolved, hs, ok := k.resolveMDTarget(where, target)
	if !ok {
		return Finding{where, line, "anchor", fmt.Sprintf("`%s`: target %s does not exist", rawURL, target)}, true
	}
	if !githubSlugs(hs)[want] {
		return Finding{where, line, "anchor", fmt.Sprintf("`%s`: heading #%s not found in %s", rawURL, anchor, resolved)}, true
	}
	return Finding{}, false
}

func (k *checker) resolveMDTarget(where, target string) (string, []string, bool) {
	cands := []string{path.Clean(path.Join(path.Dir(where), target)), path.Clean(target)}
	for _, p := range cands {
		if hs, ok := k.fileHeadings(p); ok {
			return p, hs, true
		}
	}
	return "", nil, false
}

func (k *checker) fileHeadings(p string) ([]string, bool) {
	if p == "" || p == "." || strings.HasPrefix(p, "..") || strings.HasPrefix(p, "/") || !strings.HasSuffix(strings.ToLower(p), ".md") {
		return nil, false
	}
	if hs, ok := k.headings[p]; ok {
		return hs, hs != nil
	}
	data, err := k.readFile(filepath.FromSlash(p))
	if err != nil {
		k.headings[p] = nil
		return nil, false
	}
	hs := extractHeadings(string(data))
	if hs == nil {
		hs = []string{}
	}
	k.headings[p] = hs
	return hs, true
}

func extractHeadings(text string) []string {
	hs := []string{}
	unfenced(text, func(_ int, l string) bool {
		if m := atxHeadingRe.FindStringSubmatch(l); m != nil {
			title := stripTrailingHashes(strings.TrimSpace(m[2]))
			if title != "" {
				hs = append(hs, title)
			}
		}
		return true
	})
	return hs
}

func stripTrailingHashes(s string) string {
	s = strings.TrimRight(s, " \t")
	i := len(s)
	for i > 0 && s[i-1] == '#' {
		i--
	}
	if i < len(s) && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
		return strings.TrimRight(s[:i], " \t")
	}
	return s
}

func githubSlug(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

func githubSlugs(headings []string) map[string]bool {
	out := make(map[string]bool, len(headings))
	seen := make(map[string]int, len(headings))
	for _, h := range headings {
		base := githubSlug(h)
		if base == "" {
			continue
		}
		n := seen[base]
		seen[base] = n + 1
		cand := base
		if n > 0 {
			for out[fmt.Sprintf("%s-%d", base, n)] {
				n++
			}
			cand = fmt.Sprintf("%s-%d", base, n)
			seen[base] = n + 1
		}
		out[cand] = true
	}
	return out
}

func lineCodeSpans(l string) (string, []inlineSpan) {
	type run struct{ at, k, next int }
	var runs []run
	for i := 0; i < len(l); {
		if l[i] != '`' {
			i++
			continue
		}
		k := tickRun(l, i)
		runs = append(runs, run{at: i, k: k})
		i += k
	}
	if len(runs) < 2 {
		return l, nil
	}
	after := map[int]int{}
	for r := len(runs) - 1; r >= 0; r-- {
		runs[r].next = -1
		if c, ok := after[runs[r].k]; ok {
			runs[r].next = c
		}
		after[runs[r].k] = r
	}
	buf := []byte(l)
	var out []inlineSpan
	for r := 0; r < len(runs); {
		c := runs[r].next
		if c < 0 {
			r++
			continue
		}
		start, end := runs[r].at, runs[c].at+runs[c].k
		out = append(out, inlineSpan{start: start, end: end, text: strings.TrimSpace(l[runs[r].at+runs[r].k : runs[c].at])})
		for j := start; j < end; j++ {
			buf[j] = ' '
		}
		r = c + 1
	}
	return string(buf), out
}
