package check

import (
	"fmt"
	"strings"
)

var (
	mermaidSkipKeywords = map[string]bool{
		"flowchart": true, "graph": true, "end": true, "direction": true,
		"classdef": true, "class": true, "style": true, "linkstyle": true,
		"click": true, "acctitle": true, "accdescr": true, "title": true,
	}
	seqSkipKeywords = map[string]bool{
		"sequencediagram": true, "alt": true, "else": true, "opt": true,
		"loop": true, "par": true, "and": true, "critical": true,
		"option": true, "break": true, "rect": true, "end": true,
		"note": true, "activate": true, "deactivate": true, "autonumber": true,
		"box": true, "create": true, "destroy": true, "title": true,
	}
	seqArrows    = []string{"<<-->>", "<<->>", "-->>", "->>", "-->", "->", "--x", "-x", "--)", "-)"}
	edgePrefixes = []string{"--", "-.", "==", "~~", "->", "<-", "<=", "<~", "o--", "x--", "o-.", "x-.", "o==", "x=="}
	edgeClosers  = map[string][]string{"--": {"-->", "---", "--o", "--x"}, "-.": {".->", ".--", ".-"}, "==": {"==>", "==="}}
)

func mermaidFindings(where string, blocks []fencedBlock) []Finding {
	var out []Finding
	for _, blk := range blocks {
		if blk.lang != "mermaid" {
			continue
		}
		if n := countMermaidNodes(blk.lines); n > maxMermaidNodes {
			out = append(out, Finding{where, blk.openIdx + 1, "docs-style", fmt.Sprintf("Mermaid diagram has %d distinct nodes (max %d)", n, maxMermaidNodes)})
		}
	}
	return out
}

func countMermaidNodes(lines []string) int {
	kind := ""
	for _, raw := range lines {
		if l := strings.TrimSpace(stripMermaidComment(raw)); l != "" {
			kind = strings.ToLower(strings.Fields(l)[0])
			break
		}
	}
	switch {
	case strings.HasPrefix(kind, "sequencediagram"):
		return countSequenceNodes(lines)
	case strings.HasPrefix(kind, "statediagram"):
		return countStateNodes(lines)
	case strings.HasPrefix(kind, "flowchart") || kind == "graph":
		return countFlowchartNodes(lines)
	default:
		return 0
	}
}

func stripMermaidComment(s string) string {
	inQuote := false
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			inQuote = !inQuote
		} else if !inQuote && s[i] == '%' && i+1 < len(s) && s[i+1] == '%' {
			return s[:i]
		}
	}
	return s
}

func countSequenceNodes(lines []string) int {
	nodes := map[string]bool{}
	for _, raw := range lines {
		l := strings.TrimSpace(stripMermaidComment(raw))
		if l == "" {
			continue
		}
		low := strings.ToLower(l)
		if strings.HasPrefix(low, "participant ") || strings.HasPrefix(low, "actor ") {
			_, rest, _ := strings.Cut(l, " ")
			rest = strings.TrimSpace(rest)
			if idx := strings.Index(strings.ToLower(rest), " as "); idx >= 0 {
				rest = strings.TrimSpace(rest[:idx])
			}
			if id := strings.Trim(strings.TrimSuffix(rest, ":"), `"`); id != "" {
				nodes[id] = true
			}
			continue
		}
		if seqSkipKeywords[strings.Fields(low)[0]] {
			continue
		}
		if left, right, ok := splitSequenceArrow(l); ok {
			if left != "" && !seqSkipKeywords[strings.ToLower(left)] {
				nodes[left] = true
			}
			if right != "" && !seqSkipKeywords[strings.ToLower(right)] {
				nodes[right] = true
			}
		}
	}
	return len(nodes)
}

func splitSequenceArrow(line string) (string, string, bool) {
	bestIdx, bestArrow := -1, ""
	for _, arr := range seqArrows {
		if idx := strings.Index(line, arr); idx > 0 && (bestIdx < 0 || idx < bestIdx) {
			bestIdx, bestArrow = idx, arr
		}
	}
	if bestIdx < 0 {
		return "", "", false
	}
	rest := line[bestIdx+len(bestArrow):]
	if before, _, ok := strings.Cut(rest, ":"); ok {
		rest = before
	}
	return strings.TrimSpace(line[:bestIdx]), strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(rest), "+-")), true
}

func countStateNodes(lines []string) int {
	nodes := map[string]bool{}
	add := func(raw string) {
		if id := strings.TrimSpace(raw); id != "" && id != "[*]" {
			nodes[id] = true
		}
	}
	for _, raw := range lines {
		l := strings.TrimSpace(stripMermaidComment(raw))
		low := strings.ToLower(l)
		if l == "" || l == "{" || l == "}" || strings.HasPrefix(low, "statediagram") || strings.HasPrefix(low, "direction ") || strings.HasPrefix(low, "note ") || low == "end note" || strings.HasPrefix(low, "classdef ") || strings.HasPrefix(low, "class ") || strings.HasPrefix(low, "style ") {
			continue
		}
		if before, after, ok := strings.Cut(l, "-->"); ok {
			add(before)
			if r, _, hasColon := strings.Cut(after, ":"); hasColon {
				after = r
			}
			add(after)
		} else if strings.HasPrefix(low, "state ") {
			rest := strings.TrimSuffix(strings.TrimSpace(l[len("state"):]), "{")
			if _, after, ok := strings.Cut(rest, " as "); ok {
				if fs := strings.Fields(after); len(fs) > 0 {
					add(fs[0])
				}
			} else if fs := strings.Fields(rest); len(fs) > 0 {
				add(strings.Trim(fs[0], `"`))
			}
		}
	}
	return len(nodes)
}

func countFlowchartNodes(lines []string) int {
	nodes, subgraphs := map[string]bool{}, map[string]bool{}
	for _, raw := range lines {
		l := strings.TrimSpace(stripMermaidComment(raw))
		if l == "" {
			continue
		}
		first := strings.ToLower(strings.Fields(l)[0])
		if mermaidSkipKeywords[first] {
			continue
		}
		if first == "subgraph" {
			if _, rest, ok := strings.Cut(l, " "); ok {
				rest = strings.TrimSpace(rest)
				if rest != "" && rest[0] != '"' {
					if end := readNodeID(rest, 0); end > 0 {
						subgraphs[rest[:end]] = true
					}
				}
			}
			continue
		}
		scanFlowchartNodes(l, nodes)
	}
	for id := range subgraphs {
		delete(nodes, id)
	}
	return len(nodes)
}

func scanFlowchartNodes(l string, nodes map[string]bool) {
	var noClose map[string]bool
	for i := 0; i < len(l); {
		if l[i] == ' ' || l[i] == '\t' || l[i] == ';' || l[i] == '&' {
			i++
		} else if next := skipFlowchartEdge(l, i, &noClose); next > i {
			i = next
		} else if end := readNodeID(l, i); end > i {
			if id := l[i:end]; !mermaidSkipKeywords[strings.ToLower(id)] {
				nodes[id] = true
			}
			i = skipShapeAndClass(l, end)
		} else {
			i++
		}
	}
}

func readNodeID(s string, i int) int {
	if i >= len(s) || !isNodeIDChar(s[i]) {
		return i
	}
	j := i + 1
	for j < len(s) && (isNodeIDChar(s[j]) || (s[j] == '-' && j+1 < len(s) && isNodeIDChar(s[j+1]))) {
		j++
	}
	return j
}

func isNodeIDChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func skipShapeAndClass(s string, i int) int {
	if i < len(s) {
		var closeCh byte
		switch s[i] {
		case '[', '>':
			closeCh = ']'
		case '(':
			closeCh = ')'
		case '{':
			closeCh = '}'
		}
		if closeCh != 0 {
			i = skipBalancedShape(s, i, s[i], closeCh)
		}
	}
	if strings.HasPrefix(s[i:], ":::") {
		for i += 3; i < len(s) && (isNodeIDChar(s[i]) || s[i] == '-'); i++ {
		}
	}
	return i
}

func skipBalancedShape(s string, i int, open, closeCh byte) int {
	depth, inQuote := 1, false
	for j := i + 1; j < len(s); j++ {
		if s[j] == '"' {
			inQuote = !inQuote
		} else if !inQuote {
			if s[j] == open && open != '>' {
				depth++
			} else if s[j] == closeCh {
				if depth--; depth == 0 {
					return j + 1
				}
			}
		}
	}
	return len(s)
}

func skipFlowchartEdge(s string, i int, noClose *map[string]bool) int {
	if s[i] == '|' {
		if end := strings.IndexByte(s[i+1:], '|'); end >= 0 {
			return i + end + 2
		}
		return i + 1
	}
	matched := false
	for _, pfx := range edgePrefixes {
		if strings.HasPrefix(s[i:], pfx) {
			matched = true
			break
		}
	}
	if !matched {
		return i
	}
	j := i
	if s[j] == '<' || s[j] == 'o' || s[j] == 'x' {
		j++
	}
	for j < len(s) && (s[j] == '-' || s[j] == '.' || s[j] == '=' || s[j] == '~') {
		j++
	}
	if j < len(s) && (s[j] == '>' || ((s[j] == 'o' || s[j] == 'x') && (j+1 == len(s) || !isNodeIDChar(s[j+1])))) {
		j++
	}
	if j < len(s) && s[j] == '|' {
		if end := strings.IndexByte(s[j+1:], '|'); end >= 0 {
			return j + end + 2
		}
	}
	tok := s[i:j]
	if cls := edgeClosers[tok]; len(cls) > 0 && j < len(s) && (s[j] == ' ' || s[j] == '\t') && !(*noClose)[tok] {
		if end, ok := findEdgeTextClose(s, j, cls); ok {
			return end
		}
		if *noClose == nil {
			*noClose = make(map[string]bool, len(edgeClosers))
		}
		(*noClose)[tok] = true
	}
	return j
}

func findEdgeTextClose(s string, start int, closers []string) (int, bool) {
	inQuote := false
	for i := start; i < len(s); i++ {
		if s[i] == '"' {
			inQuote = !inQuote
		} else if !inQuote {
			for _, cl := range closers {
				if strings.HasPrefix(s[i:], cl) {
					return i + len(cl), true
				}
			}
		}
	}
	return start, false
}
