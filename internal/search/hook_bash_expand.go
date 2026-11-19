package search

import "strings"

func hasShellExpansionOutsideSingleQuotes(cmd string) bool {
	hasExp, _ := scanShellExpansions(cmd)
	return hasExp
}

func extractSubshellCmds(cmd string) []string {
	_, subs := scanShellExpansions(cmd)
	return subs
}

func scanShellExpansions(cmd string) (bool, []string) {
	hasExp := false
	var subs []string
	for i, q := 0, byte(0); i < len(cmd); i++ {
		ch := cmd[i]
		if q == '\'' || q == '$' {
			i, q = advanceShellQuote(cmd, i, q)
			continue
		}
		if q == '"' {
			if next, ok := captureDoubleQuotedSub(cmd, i, &subs); ok {
				hasExp, i = true, next-1
				continue
			}
			i, q = advanceShellQuote(cmd, i, q)
			continue
		}
		if ch == '\\' && i+1 < len(cmd) {
			i++
			continue
		}
		if ch == '$' && i+1 < len(cmd) && cmd[i+1] == '\'' {
			q = '$'
			i++
			continue
		}
		if ch == '\'' || ch == '"' {
			q = ch
			continue
		}
		if ch == '#' && (i == 0 || strings.ContainsRune(" \t\n\r;|&(", rune(cmd[i-1]))) {
			i = skipLineComment(cmd, i)
			continue
		}
		if next, exp, ok := captureUnquotedSub(cmd, i, &subs); ok {
			hasExp = hasExp || exp
			i = next - 1
		}
	}
	return hasExp, subs
}

func captureDoubleQuotedSub(cmd string, i int, subs *[]string) (int, bool) {
	if cmd[i] == '$' && i+1 < len(cmd) && cmd[i+1] == '(' {
		end := scanBalancedParenAtom(cmd, i+2)
		appendInnerBody(cmd, i+2, end, ')', subs)
		return end, true
	}
	if cmd[i] == '`' {
		end := scanBacktickAtom(cmd, i+1)
		appendInnerBody(cmd, i+1, end, '`', subs)
		return end, true
	}
	return i, false
}

func captureUnquotedSub(cmd string, i int, subs *[]string) (int, bool, bool) {
	ch := cmd[i]
	if (ch == '$' || ch == '<' || ch == '>') && i+1 < len(cmd) && cmd[i+1] == '(' {
		end := scanBalancedParenAtom(cmd, i+2)
		appendInnerBody(cmd, i+2, end, ')', subs)
		return end, true, true
	}
	if ch == '`' {
		end := scanBacktickAtom(cmd, i+1)
		appendInnerBody(cmd, i+1, end, '`', subs)
		return end, true, true
	}
	if ch == '(' && (i == 0 || cmd[i-1] != '=') {
		end := scanBalancedParenAtom(cmd, i+1)
		appendInnerBody(cmd, i+1, end, ')', subs)
		return end, false, true
	}
	return i, false, false
}

func appendInnerBody(cmd string, start, end int, closeCh byte, subs *[]string) {
	bodyEnd := end
	if bodyEnd > start && cmd[bodyEnd-1] == closeCh {
		bodyEnd--
	}
	if bodyEnd > start {
		*subs = append(*subs, cmd[start:bodyEnd])
	}
}
