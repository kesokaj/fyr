package search

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type faultTable struct {
	headers []string
	rows    [][]string
}

type parsedSpecSections struct {
	interfaces []string
	invariants []string
	faults     []faultTable
}

func parseSpecSections(body string) parsedSpecSections {
	sections := splitLevel2Sections(body)
	return parsedSpecSections{
		interfaces: extractFencedBlocks(sections["interfaces"]),
		invariants: extractDataBullets(sections["data"]),
		faults:     extractFailureTables(sections["failure modes"]),
	}
}

func splitLevel2Sections(body string) map[string][]string {
	norm := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(norm, "\n")
	out := map[string][]string{}
	cur := ""
	var f fence
	for _, line := range lines {
		if f.step(line) || f.inside() {
			if cur != "" {
				out[cur] = append(out[cur], line)
			}
			continue
		}
		if level, title, ok := parseATXHeading(line); ok {
			if level == 2 {
				cur = strings.ToLower(title)
				continue
			}
			if level == 1 {
				cur = ""
				continue
			}
		}
		if cur != "" {
			out[cur] = append(out[cur], line)
		}
	}
	return out
}

func parseATXHeading(line string) (int, string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "#") {
		return 0, "", false
	}
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n > 6 || n == len(t) || (t[n] != ' ' && t[n] != '\t') {
		return 0, "", false
	}
	title := strings.TrimSpace(t[n:])
	title = strings.TrimSpace(strings.TrimRight(title, "#"))
	if title == "" {
		return 0, "", false
	}
	return n, title, true
}

func extractFencedBlocks(lines []string) []string {
	var blocks []string
	var cur []string
	var f fence
	for _, line := range lines {
		wasInside := f.inside()
		isFence := f.step(line)
		switch {
		case isFence && !wasInside:
			cur = []string{sanitizeCodeLine(strings.TrimSpace(line))}
		case isFence && wasInside:
			cur = append(cur, sanitizeCodeLine(strings.TrimSpace(line)))
			if len(cur) > 2 {
				blocks = append(blocks, strings.Join(cur, "\n"))
			}
			cur = nil
		case wasInside:
			cur = append(cur, sanitizeCodeLine(line))
		}
	}
	return blocks
}

func extractDataBullets(lines []string) []string {
	var out []string
	var f fence
	for _, line := range lines {
		if f.step(line) || f.inside() {
			continue
		}
		expanded := strings.ReplaceAll(strings.TrimRight(line, "\r"), "\t", "  ")
		trimmed := strings.TrimLeft(expanded, " ")
		if len(trimmed) < 2 || (trimmed[0] != '-' && trimmed[0] != '*' && trimmed[0] != '+') || trimmed[1] != ' ' {
			continue
		}
		content := strings.TrimSpace(printable(trimmed[2:]))
		if content == "" {
			continue
		}
		indent := ""
		if len(expanded)-len(trimmed) >= 2 {
			indent = "  "
		}
		out = append(out, indent+"- "+content)
	}
	return out
}

func extractFailureTables(lines []string) []faultTable {
	var out []faultTable
	var f fence
	fenced := make([]bool, len(lines))
	for i, line := range lines {
		fenced[i] = f.step(line) || f.inside()
	}
	for i := 0; i+1 < len(lines); i++ {
		if fenced[i] || fenced[i+1] {
			continue
		}
		headers := splitPipeCells(lines[i])
		if len(headers) < 2 || !isPipeTableSeparator(lines[i+1], len(headers)) {
			continue
		}
		tbl := faultTable{headers: cleanCells(headers)}
		j := i + 2
		for j < len(lines) && !fenced[j] {
			row := splitPipeCells(lines[j])
			if len(row) < 2 {
				break
			}
			tbl.rows = append(tbl.rows, normalizeRowCells(cleanCells(row), len(tbl.headers)))
			j++
		}
		if len(tbl.rows) > 0 {
			out = append(out, tbl)
		}
		i = j - 1
	}
	return out
}

func splitPipeCells(line string) []string {
	t := strings.TrimSpace(line)
	if t == "" || !strings.Contains(t, "|") {
		return nil
	}
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(t); i++ {
		if t[i] == '\\' && i+1 < len(t) && t[i+1] == '|' {
			cur.WriteByte('|')
			i++
			continue
		}
		if t[i] == '|' {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(t[i])
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells
}

func isPipeTableSeparator(line string, cols int) bool {
	cells := splitPipeCells(line)
	if len(cells) != cols {
		return false
	}
	for _, c := range cells {
		c = strings.TrimSpace(c)
		c = strings.TrimPrefix(c, ":")
		c = strings.TrimSuffix(c, ":")
		if len(c) == 0 || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

func cleanCells(raw []string) []string {
	out := make([]string, len(raw))
	for i, c := range raw {
		out[i] = printable(strings.Join(strings.Fields(c), " "))
	}
	return out
}

func normalizeRowCells(row []string, cols int) []string {
	if len(row) == cols {
		return row
	}
	out := make([]string, cols)
	copy(out, row)
	return out
}

func formatFaultTable(t faultTable) string {
	var b strings.Builder
	if len(t.rows) >= faultsTSVThreshold {
		hdrs := make([]string, len(t.headers))
		for i, h := range t.headers {
			hdrs[i] = strings.ReplaceAll(h, ",", ";")
		}
		fmt.Fprintf(&b, "faults[%d]{%s}:\n", len(t.rows), strings.Join(hdrs, ","))
		for _, r := range t.rows {
			b.WriteString(strings.Join(r, "\t") + "\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}
	b.WriteString("| " + strings.Join(t.headers, " | ") + " |\n")
	seps := make([]string, len(t.headers))
	for i := range seps {
		seps[i] = "---"
	}
	b.WriteString("| " + strings.Join(seps, " | ") + " |\n")
	for _, r := range t.rows {
		b.WriteString("| " + strings.Join(r, " | ") + " |\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func sanitizeCodeLine(line string) string {
	return printable(strings.ReplaceAll(strings.TrimRight(line, "\r"), "\t", "  "))
}

func truncateMultilineUTF8(s string, maxB int) string {
	if maxB <= 0 {
		return ""
	}
	if len(s) <= maxB {
		return s
	}
	i := maxB
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return strings.TrimRight(s[:i], " \t")
}

// FormatContractDigest returns a deterministic, Printable-sanitized, UTF-8 safe
// digest of linked SPEC-* contracts and accepted ADR guardrails capped at
// <= 600 bytes (and <= maxBytes) for slice extras and worker capsules.
func FormatContractDigest(sources []SpecContractSource, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if maxBytes > defaultContractDigestBytes {
		maxBytes = defaultContractDigestBytes
	}
	specs, adrs := partitionContractSources(sources)
	lines := buildContractDigestLines(specs, adrs)
	if len(lines) == 0 {
		return ""
	}
	var out strings.Builder
	for _, line := range lines {
		sep := ""
		if out.Len() > 0 {
			sep = "\n"
		}
		if out.Len()+len(sep)+len(line) <= maxBytes {
			out.WriteString(sep + line)
			continue
		}
		if out.Len() == 0 {
			return truncateBytesUTF8(line, maxBytes)
		}
		if rem := maxBytes - out.Len() - len(sep); rem >= 24 {
			if clipped := truncateBytesUTF8(line, rem); clipped != "" {
				out.WriteString(sep + clipped)
			}
		}
		break
	}
	return out.String()
}

func buildContractDigestLines(specs []SpecContractSource, adrs []SpecADRRef) []string {
	var lines []string
	for _, sp := range specs {
		id := printable(strings.TrimSpace(sp.ID))
		sec := parseSpecSections(sp.Body)
		title := printable(strings.Join(strings.Fields(sp.Title), " "))
		if title != "" {
			lines = append(lines, fmt.Sprintf("%s: %s", id, title))
		} else {
			lines = append(lines, id+":")
		}
		lines = append(lines, digestInterfaceLines(sec.interfaces, 2)...)
		lines = append(lines, digestBulletLines(sec.invariants, 3)...)
		lines = append(lines, digestFaultLines(sec.faults, 2)...)
	}
	for _, a := range adrs {
		lines = append(lines, fmt.Sprintf("- %s: %s", printable(a.ID), printable(strings.Join(strings.Fields(a.Title), " "))))
	}
	return lines
}

func digestInterfaceLines(blocks []string, limit int) []string {
	var out []string
	for _, blk := range blocks {
		for _, line := range strings.Split(blk, "\n") {
			t := strings.TrimSpace(line)
			if t == "" || strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") || strings.HasPrefix(t, "//") {
				continue
			}
			out = append(out, "  iface: "+t)
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}

func digestBulletLines(bullets []string, limit int) []string {
	if len(bullets) > limit {
		bullets = bullets[:limit]
	}
	out := make([]string, 0, len(bullets))
	for _, b := range bullets {
		out = append(out, "  "+strings.TrimSpace(b))
	}
	return out
}

func digestFaultLines(tables []faultTable, limit int) []string {
	var out []string
	for _, t := range tables {
		for _, r := range t.rows {
			if len(r) == 0 {
				continue
			}
			out = append(out, "  fault: "+strings.Join(r, " | "))
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}
