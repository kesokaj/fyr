package guard

import (
	"strings"

	"github.com/kesokaj/fyr/internal/readledger"
)

// SplitShellSegments splits a shell command into top-level pipeline/list segments.
func SplitShellSegments(cmd string) []string {
	var out, heredocs []string
	var cur strings.Builder
	var q byte
	for i := 0; i < len(cmd); i++ {
		if q != 0 {
			i, q = stepQuotedAtom(cmd, i, q, &cur)
			continue
		}
		var flush bool
		i, q, heredocs, flush = stepShellSplitCharWith(cmd, i, heredocs, &cur, matchShellSeparator)
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

func stepShellSplitCharWith(cmd string, i int, heredocs []string, cur *strings.Builder, matchSep func(string, int) (int, bool)) (int, byte, []string, bool) {
	ch := cmd[i]
	if ch == '\\' && i+1 < len(cmd) {
		cur.WriteString(cmd[i : i+2])
		return i + 1, 0, heredocs, false
	}
	if next, handled := stepOpaqueOrComment(cmd, i, cur); handled {
		return next, 0, heredocs, false
	}
	if ch == '$' && i+1 < len(cmd) && cmd[i+1] == '\'' {
		cur.WriteString("$'")
		return i + 1, '$', heredocs, false
	}
	if ch == '\'' || ch == '"' {
		cur.WriteByte(ch)
		return i, ch, heredocs, false
	}
	if next, hd, ok := consumeHeredocOp(cmd, i, heredocs, cur); ok {
		return next, 0, hd, false
	}
	if next, sep := matchSep(cmd, i); sep {
		if ch == '\n' && len(heredocs) > 0 {
			next, heredocs = readledger.SkipHeredocBodies(cmd, next, heredocs), nil
		}
		return next - 1, 0, heredocs, true
	}
	cur.WriteByte(ch)
	return i, 0, heredocs, false
}

func stepOpaqueOrComment(cmd string, i int, cur *strings.Builder) (int, bool) {
	next, handled := writeOpaqueAtomOrComment(cmd, i, cur)
	if !handled {
		return i, false
	}
	if next == i {
		return readledger.SkipLineComment(cmd, i), true
	}
	return next - 1, true
}

func consumeHeredocOp(cmd string, i int, heredocs []string, cur *strings.Builder) (int, []string, bool) {
	if cmd[i] != '<' || i+1 >= len(cmd) || cmd[i+1] != '<' || (i+2 < len(cmd) && cmd[i+2] == '<') {
		return i, heredocs, false
	}
	delim, next := readledger.ParseHeredocDelim(cmd, i+2)
	if delim != "" {
		heredocs = append(heredocs, delim)
	}
	cur.WriteString(cmd[i:next])
	return next - 1, heredocs, true
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
		if q != 0 {
			i, q = readledger.AdvanceShellQuote(cmd, i, q)
			continue
		}
		var done bool
		i, depth, q, done = stepBalancedParenChar(cmd, i, depth)
		if done {
			return i
		}
	}
	return len(cmd)
}

func stepBalancedParenChar(cmd string, i, depth int) (int, int, byte, bool) {
	c := cmd[i]
	switch {
	case c == '\\' && i+1 < len(cmd):
		return i + 1, depth, 0, false
	case c == '$' && i+1 < len(cmd) && cmd[i+1] == '\'':
		return i + 1, depth, '$', false
	case c == '\'' || c == '"':
		return i, depth, c, false
	case c == '#' && (i == 0 || strings.ContainsRune(" \t\n\r;|&(", rune(cmd[i-1]))):
		return readledger.SkipLineComment(cmd, i), depth, 0, false
	case c == '(':
		return i, depth + 1, 0, false
	case c == ')':
		if depth-1 == 0 {
			return i + 1, 0, 0, true
		}
		return i, depth - 1, 0, false
	default:
		return i, depth, 0, false
	}
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

// TokenizeShellSegment splits a shell segment into shell words.
func TokenizeShellSegment(seg string) []string {
	return readledger.TokenizeShellSegment(seg)
}
