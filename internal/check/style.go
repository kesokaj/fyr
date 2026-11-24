package check

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/brain"
)

const (
	maxMermaidNodes = 12
	maxTableRows    = 12
	calloutLookback = 8
)

var (
	bannedFluffTerms = []string{"seamless", "foundational", "zero-ops", "e.g.", "i.e.", "etc."}

	destructivePatterns = []struct {
		label string
		re    *regexp.Regexp
	}{
		{"rm -rf", regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_])rm\s+-rf(?:\b|$)`)},
		{"-auto-approve", regexp.MustCompile(`(?i)(?:^|[\s-])--?auto-approve(?:\b|=|$)`)},
		{"git reset --hard", regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_])git\s+reset\s+--hard(?:\b|$)`)},
		{"DROP TABLE", regexp.MustCompile(`(?i)\bdrop\s+table\b`)},
		{"--force", regexp.MustCompile(`(?i)(?:^|\s)--force(?:\b|=|$)`)},
	}

	cautionAlertRe = regexp.MustCompile(`(?i)^\s*>\s*\[!(caution|warning)\]`)
	cautionLabelRe = regexp.MustCompile(`(?i)(?:^|[\s>*_-])(caution|warning)\s*:`)
	cautionBoldRe  = regexp.MustCompile(`(?i)\*\*(caution|warning):?\*\*`)
)

type track1Doc struct {
	where, text      string
	checkFrontmatter bool
}

type fencedBlock struct {
	openIdx int
	lang    string
	lines   []string
}

func isTrack1RootDoc(p string) bool {
	return p == "README.md" || p == "SDD.md" || p == "TDD.md" || p == "Workflow.md"
}

func isTrack1File(p string) bool {
	return isTrack1RootDoc(p) || strings.HasPrefix(p, "docs/") || strings.HasPrefix(p, "features/")
}

func (k *checker) collectTrack1Docs(docsFiles []string, living []brain.Record) []track1Doc {
	var docs []track1Doc
	for _, p := range docsFiles {
		if !isTrack1File(p) {
			continue
		}
		if data, err := k.readFile(filepath.FromSlash(p)); err == nil {
			docs = append(docs, track1Doc{p, string(data), isTrack1RootDoc(p)})
		}
	}
	for _, r := range living {
		if r.Kind == "guide" || r.Kind == "deploy" {
			docs = append(docs, track1Doc{r.ID, r.Body, true})
		}
	}
	return docs
}

// docsStyleRule runs deterministic Track 1 docs-ste100-80 checks (ADR-535):
// five hard findings under "docs-style" and advisory notes under "docs-style-note".
func (k *checker) docsStyleRule(docsFiles []string, living []brain.Record) (findings []Finding, notes []string) {
	docs := k.collectTrack1Docs(docsFiles, living)
	glossary, gNote := k.loadGlossary()
	if gNote != "" {
		notes = append(notes, gNote)
	}
	for _, d := range docs {
		rawLines := splitNormalizedLines(d.text)
		fenced, blocks := scanFences(rawLines)
		findings = append(findings, mermaidFindings(d.where, blocks)...)
		findings = append(findings, unfoldedTableFindings(d.where, rawLines, fenced)...)
		findings = append(findings, destructiveFenceFindings(d.where, rawLines, fenced, blocks)...)
		if d.checkFrontmatter {
			if f, ok := yamlFrontmatterFinding(d.where, rawLines); ok {
				findings = append(findings, f)
			}
		}
		findings = append(findings, bannedWordFindings(d.where, d.text)...)
		if note := steSentenceNote(d.where, rawLines, fenced); note != "" {
			notes = append(notes, note)
		}
		notes = append(notes, glossaryNotes(d.where, d.text, glossary)...)
	}
	return findings, notes
}

func yamlFrontmatterFinding(where string, rawLines []string) (Finding, bool) {
	if len(rawLines) < 2 || strings.TrimRight(rawLines[0], " \t") != "---" {
		return Finding{}, false
	}
	for i := 1; i < len(rawLines); i++ {
		if strings.TrimRight(rawLines[i], " \t") == "---" {
			return Finding{where, 1, "docs-style", "YAML frontmatter is not allowed on Track 1 root docs or guide/deploy records"}, true
		}
	}
	return Finding{}, false
}

func unfoldedTableFindings(where string, rawLines []string, fenced []bool) []Finding {
	var out []Finding
	detailsDepth, inComment := 0, false
	for i := 0; i < len(rawLines); i++ {
		if fenced[i] {
			continue
		}
		masked, _ := lineCodeSpans(rawLines[i])
		var clean string
		clean, inComment = stripHTMLComments(masked, inComment)
		detailsDepth = updateDetailsDepth(clean, detailsDepth)
		if i+1 >= len(rawLines) || fenced[i+1] || tableColCount(rawLines[i]) == 0 || !isTableSeparator(rawLines[i+1]) {
			continue
		}
		startLine, inside, rows, j := i+1, detailsDepth > 0, 0, i+2
		for j < len(rawLines) && !fenced[j] && isTableDataLine(rawLines[j]) {
			rows++
			j++
		}
		if !inside && rows > maxTableRows {
			out = append(out, Finding{where, startLine, "docs-style", fmt.Sprintf("unfolded table has %d data rows (max %d; wrap in <details>)", rows, maxTableRows)})
		}
		i = j - 1
	}
	return out
}

func stripHTMLComments(line string, inComment bool) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(line); {
		if inComment {
			if end := strings.Index(line[i:], "-->"); end >= 0 {
				i += end + 3
				inComment = false
				continue
			}
			return b.String(), true
		}
		if start := strings.Index(line[i:], "<!--"); start >= 0 {
			b.WriteString(line[i : i+start])
			i += start + 4
			inComment = true
			continue
		}
		b.WriteString(line[i:])
		break
	}
	return b.String(), inComment
}

func updateDetailsDepth(clean string, depth int) int {
	low := strings.ToLower(clean)
	for i := 0; i < len(low); i++ {
		if low[i] != '<' {
			continue
		}
		if strings.HasPrefix(low[i:], "</details>") {
			if depth > 0 {
				depth--
			}
		} else if strings.HasPrefix(low[i:], "<details") {
			end := i + len("<details")
			if end == len(low) || low[end] == '>' || low[end] == ' ' || low[end] == '\t' {
				depth++
			}
		}
	}
	return depth
}

func isTableDataLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" || atxHeadingRe.MatchString(line) {
		return false
	}
	masked, _ := lineCodeSpans(line)
	low := strings.ToLower(masked)
	if strings.Contains(low, "<details") || strings.Contains(low, "</details>") {
		return false
	}
	return tableColCount(line) > 0
}

func tableCells(line string) []string {
	t := strings.TrimSpace(line)
	if !strings.Contains(t, "|") {
		return nil
	}
	parts := splitUnescapedPipes(t)
	if len(parts) < 2 {
		return nil
	}
	if strings.HasPrefix(t, "|") {
		parts = parts[1:]
	}
	if strings.HasSuffix(t, "|") && !strings.HasSuffix(t, `\|`) && len(parts) > 0 {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func tableColCount(line string) int { return len(tableCells(line)) }

func isTableSeparator(line string) bool {
	cells := tableCells(line)
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		c := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(cell), ":"), ":")
		if len(c) == 0 || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

func splitUnescapedPipes(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		} else if s[i] == '|' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func destructiveFenceFindings(where string, rawLines []string, fenced []bool, blocks []fencedBlock) []Finding {
	var out []Finding
	for _, blk := range blocks {
		switch blk.lang {
		case "", "bash", "sh", "zsh", "shell", "sql", "plain":
		default:
			continue
		}
		for offset, l := range blk.lines {
			if tok := findDestructiveToken(l); tok != "" {
				if !hasRecentCautionCallout(rawLines, fenced, blk.openIdx) {
					out = append(out, Finding{where, blk.openIdx + 2 + offset, "docs-style", fmt.Sprintf("destructive token %q in code block without Caution/Warning callout in prior %d non-empty lines", tok, calloutLookback)})
				}
				break
			}
		}
	}
	return out
}

func findDestructiveToken(line string) string {
	for _, p := range destructivePatterns {
		if p.re.MatchString(line) {
			return p.label
		}
	}
	return ""
}

func hasRecentCautionCallout(lines []string, fenced []bool, openIdx int) bool {
	for i, seen := openIdx-1, 0; i >= 0 && seen < calloutLookback; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		seen++
		if !fenced[i] && (cautionAlertRe.MatchString(lines[i]) || cautionLabelRe.MatchString(lines[i]) || cautionBoldRe.MatchString(lines[i])) {
			return true
		}
	}
	return false
}

func bannedWordFindings(where, text string) []Finding {
	var out []Finding
	unfenced(text, func(n int, l string) bool {
		low := strings.ToLower(maskProseLine(l))
		for _, term := range bannedFluffTerms {
			if hasWordTermLower(low, term) {
				out = append(out, Finding{where, n, "docs-style", fmt.Sprintf("banned fluff or Latin abbreviation %q in Track 1 prose", term)})
			}
		}
		return true
	})
	return out
}

func maskProseLine(l string) string {
	masked, _ := lineCodeSpans(l)
	buf := []byte(masked)
	open := -1
	for i := 0; i < len(buf); i++ {
		if buf[i] == '\\' && open >= 0 && i+1 < len(buf) {
			i++
		} else if buf[i] == '"' {
			if open < 0 {
				open = i
			} else {
				for k := open; k <= i; k++ {
					buf[k] = ' '
				}
				open = -1
			}
		}
	}
	return string(buf)
}

func hasWordTermLower(low, needle string) bool {
	if needle == "" {
		return false
	}
	hasDot := strings.Contains(needle, ".")
	for pos := 0; pos <= len(low)-len(needle); {
		idx := strings.Index(low[pos:], needle)
		if idx < 0 {
			return false
		}
		start, end := pos+idx, pos+idx+len(needle)
		leftOK := start == 0
		if !leftOK {
			r, _ := utf8.DecodeLastRuneInString(low[:start])
			leftOK = !isTermChar(r) && (!hasDot || r != '.')
		}
		rightOK := end >= len(low)
		if !rightOK {
			r, _ := utf8.DecodeRuneInString(low[end:])
			rightOK = !isTermChar(r)
		}
		if leftOK && rightOK {
			return true
		}
		pos = start + 1
	}
	return false
}

func isTermChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

func splitNormalizedLines(text string) []string {
	raw := strings.Split(text, "\n")
	for i, l := range raw {
		raw[i] = strings.TrimSuffix(l, "\r")
	}
	return raw
}

func scanFences(lines []string) ([]bool, []fencedBlock) {
	mask := make([]bool, len(lines))
	var blocks []fencedBlock
	var cur *fencedBlock
	var ch byte
	flen := 0
	for i, l := range lines {
		if c, k, rest, ok := fenceMarker(l); ok {
			if flen == 0 && (c != '`' || !strings.Contains(rest, "`")) {
				ch, flen, mask[i] = c, k, true
				lang := ""
				if fs := strings.Fields(rest); len(fs) > 0 {
					lang = strings.ToLower(fs[0])
				}
				cur = &fencedBlock{openIdx: i, lang: lang}
				continue
			}
			if flen > 0 && c == ch && k >= flen && strings.TrimSpace(rest) == "" {
				blocks = append(blocks, *cur)
				cur, flen, mask[i] = nil, 0, true
				continue
			}
		}
		if cur != nil {
			mask[i] = true
			cur.lines = append(cur.lines, l)
		}
	}
	if cur != nil {
		blocks = append(blocks, *cur)
	}
	return mask, blocks
}
