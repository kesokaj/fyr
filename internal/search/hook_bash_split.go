package search

import "strings"

func splitShellSegments(cmd string) []string {
	var out, heredocs []string
	var cur strings.Builder
	var q byte
	for i := 0; i < len(cmd); i++ {
		if q != 0 {
			i, q = stepQuotedAtom(cmd, i, q, &cur)
			continue
		}
		var flush bool
		i, q, heredocs, flush = stepUnquotedSplitChar(cmd, i, heredocs, &cur)
		if flush {
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func stepUnquotedSplitChar(cmd string, i int, heredocs []string, cur *strings.Builder) (int, byte, []string, bool) {
	ch := cmd[i]
	if ch == '\\' && i+1 < len(cmd) {
		cur.WriteString(cmd[i : i+2])
		return i + 1, 0, heredocs, false
	}
	if next, handled := writeOpaqueAtomOrComment(cmd, i, cur); handled {
		if next == i {
			return skipLineComment(cmd, i), 0, heredocs, false
		}
		return next - 1, 0, heredocs, false
	}
	if ch == '$' && i+1 < len(cmd) && cmd[i+1] == '\'' {
		cur.WriteString("$'")
		return i + 1, '$', heredocs, false
	}
	if ch == '\'' || ch == '"' {
		cur.WriteByte(ch)
		return i, ch, heredocs, false
	}
	if ch == '<' && i+1 < len(cmd) && cmd[i+1] == '<' && (i+2 >= len(cmd) || cmd[i+2] != '<') {
		delim, next := parseHeredocDelim(cmd, i+2)
		if delim != "" {
			heredocs = append(heredocs, delim)
		}
		cur.WriteString(cmd[i:next])
		return next - 1, 0, heredocs, false
	}
	if next, sep := matchShellSeparator(cmd, i); sep {
		if ch == '\n' && len(heredocs) > 0 {
			next, heredocs = skipHeredocBodies(cmd, next, heredocs), nil
		}
		return next - 1, 0, heredocs, true
	}
	cur.WriteByte(ch)
	return i, 0, heredocs, false
}

func stepQuotedAtom(cmd string, i int, q byte, cur *strings.Builder) (int, byte) {
	if q == '"' && cmd[i] == '$' && i+1 < len(cmd) && cmd[i+1] == '(' {
		end := scanBalancedParenAtom(cmd, i+2)
		cur.WriteString(cmd[i:end])
		return end - 1, q
	}
	if q == '"' && cmd[i] == '`' {
		end := scanBacktickAtom(cmd, i+1)
		cur.WriteString(cmd[i:end])
		return end - 1, q
	}
	cur.WriteByte(cmd[i])
	if (q == '"' || q == '$') && cmd[i] == '\\' && i+1 < len(cmd) {
		cur.WriteByte(cmd[i+1])
		return i + 1, q
	}
	if (q == '$' && cmd[i] == '\'') || cmd[i] == q {
		return i, 0
	}
	return i, q
}

func writeOpaqueAtomOrComment(cmd string, i int, cur *strings.Builder) (int, bool) {
	ch := cmd[i]
	if ch == '#' && (i == 0 || strings.ContainsRune(" \t\n\r;|&(", rune(cmd[i-1]))) {
		return i, true
	}
	if (ch == '$' || ch == '<' || ch == '>') && i+1 < len(cmd) && cmd[i+1] == '(' {
		end := scanBalancedParenAtom(cmd, i+2)
		cur.WriteString(cmd[i:end])
		return end, true
	}
	if ch == '`' {
		end := scanBacktickAtom(cmd, i+1)
		cur.WriteString(cmd[i:end])
		return end, true
	}
	return i, false
}

func skipLineComment(cmd string, i int) int {
	for i+1 < len(cmd) && cmd[i+1] != '\n' {
		i++
	}
	return i
}

func matchShellSeparator(cmd string, i int) (int, bool) {
	c := cmd[i]
	if c == '\n' || c == ';' {
		return i + 1, true
	}
	if c == '|' && i+1 < len(cmd) && cmd[i+1] == '&' {
		return i + 2, true
	}
	if c == '&' && i+1 < len(cmd) && cmd[i+1] == '>' {
		return i, false
	}
	if c != '|' && (c != '&' || (i > 0 && (cmd[i-1] == '>' || cmd[i-1] == '<'))) {
		return i, false
	}
	if i+1 < len(cmd) && cmd[i+1] == c {
		return i + 2, true
	}
	return i + 1, true
}

func scanBalancedParenAtom(cmd string, pos int) int {
	depth, q := 1, byte(0)
	for i := pos; i < len(cmd); i++ {
		c := cmd[i]
		if q != 0 {
			i, q = advanceShellQuote(cmd, i, q)
			continue
		}
		if c == '\\' && i+1 < len(cmd) {
			i++
			continue
		}
		if c == '$' && i+1 < len(cmd) && cmd[i+1] == '\'' {
			q = '$'
			i++
			continue
		}
		if c == '\'' || c == '"' {
			q = c
		} else if c == '#' && (i == 0 || strings.ContainsRune(" \t\n\r;|&(", rune(cmd[i-1]))) {
			i = skipLineComment(cmd, i)
		} else if c == '(' {
			depth++
		} else if c == ')' {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(cmd)
}

func scanBacktickAtom(cmd string, pos int) int {
	for i := pos; i < len(cmd); i++ {
		if cmd[i] == '\\' && i+1 < len(cmd) {
			i++
			continue
		}
		if cmd[i] == '`' {
			return i + 1
		}
	}
	return len(cmd)
}

func advanceShellQuote(cmd string, i int, q byte) (int, byte) {
	if (q == '"' || q == '$') && cmd[i] == '\\' && i+1 < len(cmd) {
		return i + 1, q
	}
	if (q == '$' && cmd[i] == '\'') || cmd[i] == q {
		return i, 0
	}
	return i, q
}

func parseHeredocDelim(cmd string, i int) (string, int) {
	if i < len(cmd) && cmd[i] == '-' {
		i++
	}
	for i < len(cmd) && (cmd[i] == ' ' || cmd[i] == '\t') {
		i++
	}
	var d strings.Builder
	for q := byte(0); i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case q != 0 && c == q:
			q = 0
		case q != 0:
			d.WriteByte(c)
		case c == '\'' || c == '"':
			q = c
		case c == '\\' && i+1 < len(cmd):
			i++
			d.WriteByte(cmd[i])
		case strings.ContainsRune(" \t\r\n;|&<>()", rune(c)):
			return d.String(), i
		default:
			d.WriteByte(c)
		}
	}
	return d.String(), i
}

func skipHeredocBodies(cmd string, pos int, delims []string) int {
	for _, delim := range delims {
		for pos < len(cmd) {
			line := cmd[pos:]
			if nl := strings.IndexByte(line, '\n'); nl < 0 {
				pos = len(cmd)
			} else {
				line, pos = line[:nl], pos+nl+1
			}
			if strings.TrimLeft(strings.TrimSuffix(line, "\r"), "\t") == delim {
				break
			}
		}
	}
	return pos
}

func tokenizeShellSegment(seg string) []string {
	var toks []string
	var cur strings.Builder
	inTok := false
	for i, q := 0, byte(0); i < len(seg); i++ {
		switch ch := seg[i]; {
		case (q == '"' || q == '$') && ch == '\\' && i+1 < len(seg):
			i++
			cur.WriteByte(seg[i])
		case q == '$' && ch == '\'':
			q = 0
		case q != 0 && ch == q:
			q = 0
		case q != 0:
			cur.WriteByte(ch)
		case ch == '$' && i+1 < len(seg) && seg[i+1] == '\'':
			q, inTok = '$', true
			i++
		case ch == '\'' || ch == '"':
			q, inTok = ch, true
		case ch == '\\' && i+1 < len(seg):
			i++
			cur.WriteByte(seg[i])
			inTok = true
		case ch == ' ' || ch == '\t' || ch == '\r':
			if inTok {
				toks, inTok = append(toks, cur.String()), false
				cur.Reset()
			}
		default:
			inTok = true
			cur.WriteByte(ch)
		}
	}
	if inTok {
		toks = append(toks, cur.String())
	}
	return toks
}
