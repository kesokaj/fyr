package search

import "strings"

type mdHeading struct {
	line  int
	level int
	title string
}

type mdFence struct {
	char byte
	len  int
}

func extractMarkdownSymbols(_ string, text string) ([]symbolRow, []symRefRow) {
	lines := splitLines(text)
	headings := collectMarkdownHeadings(lines)
	if len(headings) == 0 {
		return nil, extractMarkdownDocRefs(text)
	}
	syms := make([]symbolRow, 0, len(headings))
	for i, h := range headings {
		endLine := len(lines)
		if i+1 < len(headings) {
			endLine = headings[i+1].line - 1
		}
		if endLine < h.line {
			endLine = h.line
		}
		body := strings.Join(lines[h.line-1:endLine], "\n")
		sig := strings.Repeat("#", h.level) + " " + h.title
		syms = append(syms, symbolRow{
			name:      h.title,
			kind:      "heading",
			pkg:       "",
			startLine: h.line,
			endLine:   endLine,
			sig:       sig,
			exported:  0,
			bodyHash:  hashString(body),
		})
	}
	return syms, extractMarkdownDocRefs(text)
}

func collectMarkdownHeadings(lines []string) []mdHeading {
	var headings []mdHeading
	var f mdFence
	for i, line := range lines {
		if f.step(line) || f.len > 0 || leadingIndentCols(line) >= 4 {
			continue
		}
		trim := strings.TrimSpace(line)
		m := atxHeading.FindStringSubmatch(trim)
		if m == nil {
			continue
		}
		title := strings.TrimSpace(m[1])
		level := countHeadingLevel(trim)
		if title == "" || level == 0 {
			continue
		}
		headings = append(headings, mdHeading{line: i + 1, level: level, title: title})
	}
	return headings
}

func (f *mdFence) step(line string) bool {
	if leadingIndentCols(line) >= 4 {
		return false
	}
	trim := strings.TrimSpace(line)
	if len(trim) < 3 || (trim[0] != '`' && trim[0] != '~') {
		return false
	}
	ch, n := trim[0], 0
	for n < len(trim) && trim[n] == ch {
		n++
	}
	if n < 3 {
		return false
	}
	rest := strings.TrimSpace(trim[n:])
	if f.len == 0 {
		if ch == '`' && strings.ContainsRune(rest, '`') {
			return false
		}
		f.char, f.len = ch, n
		return true
	}
	if ch == f.char && n >= f.len && rest == "" {
		f.char, f.len = 0, 0
		return true
	}
	return false
}

func leadingIndentCols(line string) int {
	cols := 0
	for _, r := range line {
		switch r {
		case ' ':
			cols++
		case '\t':
			cols += 4
		default:
			return cols
		}
	}
	return cols
}

func countHeadingLevel(trimmed string) int {
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level < 1 || level > 6 {
		return 0
	}
	return level
}
