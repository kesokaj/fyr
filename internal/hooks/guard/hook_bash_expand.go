package guard

import (
	"strings"

	"github.com/kesokaj/fyr/internal/readledger"
)

// HasShellExpansionOutsideSingleQuotes reports whether cmd contains command/process substitution outside single quotes.
func HasShellExpansionOutsideSingleQuotes(cmd string) bool {
	hasExp, _ := scanShellExpansions(cmd)
	return hasExp
}

// ExtractSubshellCmds returns all subshell and command-substitution bodies found in cmd.
func ExtractSubshellCmds(cmd string) []string {
	_, subs := scanShellExpansions(cmd)
	return subs
}

func scanShellExpansions(cmd string) (bool, []string) {
	hasExp := false
	var subs []string
	for i, q := 0, byte(0); i < len(cmd); i++ {
		if q == '\'' || q == '$' {
			i, q = readledger.AdvanceShellQuote(cmd, i, q)
			continue
		}
		if q == '"' {
			if next, ok := captureDoubleQuotedSub(cmd, i, &subs); ok {
				hasExp, i = true, next-1
				continue
			}
			i, q = readledger.AdvanceShellQuote(cmd, i, q)
			continue
		}
		var exp bool
		i, q, exp = stepUnquotedExpansion(cmd, i, &subs)
		hasExp = hasExp || exp
	}
	return hasExp, subs
}

func stepUnquotedExpansion(cmd string, i int, subs *[]string) (int, byte, bool) {
	ch := cmd[i]
	if ch == '\\' && i+1 < len(cmd) {
		return i + 1, 0, false
	}
	if ch == '$' && i+1 < len(cmd) && cmd[i+1] == '\'' {
		return i + 1, '$', false
	}
	if ch == '\'' || ch == '"' {
		return i, ch, false
	}
	if ch == '#' && (i == 0 || strings.ContainsRune(" \t\n\r;|&(", rune(cmd[i-1]))) {
		return readledger.SkipLineComment(cmd, i), 0, false
	}
	if next, exp, ok := captureUnquotedSub(cmd, i, subs); ok {
		return next - 1, 0, exp
	}
	return i, 0, false
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
