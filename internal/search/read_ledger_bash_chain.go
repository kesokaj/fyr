package search

import (
	"path/filepath"
	"strings"
)

type bashReadStmt struct {
	text       string
	hasHeredoc bool
}

func parseBashFileReadsExtended(cmd string) []BashFileRead {
	stmts, ok := splitBashReadStatements(cmd)
	if !ok {
		return nil
	}
	var out []BashFileRead
	cwd, unknown := "", false
	for _, stmt := range stmts {
		if stmt.hasHeredoc {
			if statementHasWriteRedirect(stmt.text) {
				break
			}
			if statementStartsWithDirChange(stmt.text) {
				cwd, unknown = "", true
			}
			continue
		}
		if isBenignBashPrefixStmt(stmt.text) {
			continue
		}
		stages, sawWrite, ok := splitReadOnlyPipeStages(stmt.text)
		if sawWrite {
			break
		}
		if !ok || len(stages) != 1 {
			if statementStartsWithDirChange(stmt.text) {
				cwd, unknown = "", true
				continue
			}
			if !ok || len(stages) == 0 {
				continue
			}
		} else if handled, nextDir, nextUnknown := stepTrackedBashDir(cwd, unknown, stages[0]); handled {
			cwd, unknown = nextDir, nextUnknown
			continue
		}
		if r, ok := parseBashReadPipeline(stages); ok && r.Path != "" && (filepath.IsAbs(r.Path) || !unknown) {
			if !filepath.IsAbs(r.Path) {
				r.Path = filepath.ToSlash(filepath.Clean(filepath.Join(cwd, r.Path)))
			}
			out = append(out, r)
		}
	}
	return out
}

func statementStartsWithDirChange(stmt string) bool {
	head := stmt
	scanUnquotedShellChars(stmt, func(i int, ch byte) bool {
		if ch == '|' {
			head = stmt[:i]
			return true
		}
		return false
	})
	toks := tokenizeShellSegment(head)
	for len(toks) > 0 {
		if isEnvAssignToken(toks[0]) {
			toks = toks[1:]
			continue
		}
		if skip := leadingRedirectSkip(toks); skip > 0 {
			toks = toks[skip:]
			continue
		}
		break
	}
	if len(toks) == 0 {
		return false
	}
	cmdTok := trimRedirectSuffix(toks[0])
	if (cmdTok == "builtin" || cmdTok == "command") && len(toks) > 1 {
		toks = toks[1:]
		if toks[0] == "--" && len(toks) > 1 {
			toks = toks[1:]
		}
		cmdTok = trimRedirectSuffix(toks[0])
	}
	return cmdTok == "cd" || cmdTok == "pushd" || cmdTok == "popd"
}

func leadingRedirectSkip(toks []string) int {
	t := toks[0]
	idx := strings.IndexAny(t, "<>")
	if idx < 0 {
		return 0
	}
	for i := 0; i < idx; i++ {
		if t[i] < '0' || t[i] > '9' {
			return 0
		}
	}
	if strings.TrimLeft(t[idx:], "<>&") == "" && len(toks) > 1 {
		return 2
	}
	return 1
}

func trimRedirectSuffix(tok string) string {
	if idx := strings.IndexAny(tok, "<>"); idx >= 0 {
		return tok[:idx]
	}
	return tok
}

func splitBashReadStatements(cmd string) ([]bashReadStmt, bool) {
	var out []bashReadStmt
	var heredocs []string
	var cur strings.Builder
	stmtHeredoc, q := false, byte(0)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, bashReadStmt{text: s, hasHeredoc: stmtHeredoc})
		}
		cur.Reset()
		stmtHeredoc = false
	}
	for i := 0; i < len(cmd); i++ {
		if q != 0 {
			var ok bool
			if i, q, ok = stepReadSplitQuoted(cmd, i, q, &cur); !ok {
				return nil, false
			}
			continue
		}
		var addedHD, didFlush, ok bool
		if i, q, heredocs, addedHD, didFlush, ok = stepReadSplitUnquoted(cmd, i, heredocs, &cur); !ok {
			return nil, false
		}
		stmtHeredoc = stmtHeredoc || addedHD
		if didFlush {
			flush()
		}
	}
	if q != 0 {
		return nil, false
	}
	flush()
	return out, len(out) > 0
}

func stepReadSplitQuoted(cmd string, i int, q byte, cur *strings.Builder) (int, byte, bool) {
	ch := cmd[i]
	if q == '"' && (ch == '`' || (ch == '$' && i+1 < len(cmd) && cmd[i+1] == '(')) {
		return i, q, false
	}
	cur.WriteByte(ch)
	if (q == '"' || q == '$') && ch == '\\' && i+1 < len(cmd) {
		cur.WriteByte(cmd[i+1])
		return i + 1, q, true
	}
	if (q == '$' && ch == '\'') || ch == q {
		return i, 0, true
	}
	return i, q, true
}

func stepReadSplitUnquoted(cmd string, i int, heredocs []string, cur *strings.Builder) (int, byte, []string, bool, bool, bool) {
	switch ch := cmd[i]; {
	case ch == '\\':
		return stepReadSplitBackslash(cmd, i, heredocs, cur)
	case ch == '#' && (i == 0 || strings.ContainsRune(" \t\n\r;&|", rune(cmd[i-1]))):
		return skipLineComment(cmd, i), 0, heredocs, false, false, true
	case ch == '`' || ((ch == '$' || ch == '<' || ch == '>') && i+1 < len(cmd) && cmd[i+1] == '(') || ch == '(' || ch == ')':
		return i, 0, heredocs, false, false, false
	case ch == '$' && i+1 < len(cmd) && cmd[i+1] == '\'':
		cur.WriteString("$'")
		return i + 1, '$', heredocs, false, false, true
	case ch == '\'' || ch == '"':
		cur.WriteByte(ch)
		return i, ch, heredocs, false, false, true
	case ch == '<' && i+1 < len(cmd) && cmd[i+1] == '<' && (i+2 >= len(cmd) || cmd[i+2] != '<'):
		delim, next := parseHeredocDelim(cmd, i+2)
		if delim != "" {
			heredocs = append(heredocs, delim)
		}
		cur.WriteString(cmd[i:next])
		return next - 1, 0, heredocs, true, false, true
	default:
		return stepReadSplitControl(cmd, i, heredocs, cur)
	}
}

func stepReadSplitBackslash(cmd string, i int, heredocs []string, cur *strings.Builder) (int, byte, []string, bool, bool, bool) {
	if i+1 >= len(cmd) {
		return i, 0, heredocs, false, false, false
	}
	if cmd[i+1] == '\n' {
		cur.WriteByte(' ')
		return i + 1, 0, heredocs, false, false, true
	}
	if cmd[i+1] == '\r' && i+2 < len(cmd) && cmd[i+2] == '\n' {
		cur.WriteByte(' ')
		return i + 2, 0, heredocs, false, false, true
	}
	cur.WriteString(cmd[i : i+2])
	return i + 1, 0, heredocs, false, false, true
}

func stepReadSplitControl(cmd string, i int, heredocs []string, cur *strings.Builder) (int, byte, []string, bool, bool, bool) {
	switch ch := cmd[i]; ch {
	case '\r', '\n':
		next := i + 1
		if ch == '\r' && next < len(cmd) && cmd[next] == '\n' {
			next++
		}
		if len(heredocs) > 0 {
			next, heredocs = skipHeredocBodies(cmd, next, heredocs), nil
		}
		return next - 1, 0, heredocs, false, true, true
	case ';':
		return i, 0, heredocs, false, true, true
	case '&':
		if i > 0 && (cmd[i-1] == '>' || cmd[i-1] == '<') {
			cur.WriteByte(ch)
			return i, 0, heredocs, false, false, true
		}
		if i+1 < len(cmd) && cmd[i+1] == '&' && (i+2 >= len(cmd) || cmd[i+2] != '&') {
			return i + 1, 0, heredocs, false, true, true
		}
		return i, 0, heredocs, false, false, false
	case '|':
		if i+1 < len(cmd) && (cmd[i+1] == '|' || cmd[i+1] == '&') {
			return i, 0, heredocs, false, false, false
		}
		cur.WriteByte(ch)
		return i, 0, heredocs, false, false, true
	default:
		cur.WriteByte(ch)
		return i, 0, heredocs, false, false, true
	}
}

func isBenignBashPrefixStmt(stmt string) bool {
	if hasUnquotedChar(stmt, "<>|") {
		return false
	}
	toks := tokenizeShellSegment(stmt)
	if len(toks) == 0 || toks[0] == "export" || toks[0] == "unset" {
		return true
	}
	if toks[0] == "set" {
		return len(toks) >= 2 && (strings.HasPrefix(toks[1], "-") || strings.HasPrefix(toks[1], "+"))
	}
	for _, tok := range toks {
		if !isEnvAssignToken(tok) {
			return false
		}
	}
	return true
}

func scanUnquotedShellChars(s string, onUnquoted func(i int, ch byte) bool) bool {
	for i, q := 0, byte(0); i < len(s); i++ {
		if q != 0 {
			i, q = advanceShellQuote(s, i, q)
			continue
		}
		switch ch := s[i]; {
		case ch == '\\' && i+1 < len(s):
			i++
		case ch == '$' && i+1 < len(s) && s[i+1] == '\'':
			q, i = '$', i+1
		case ch == '\'' || ch == '"':
			q = ch
		case onUnquoted(i, ch):
			return true
		}
	}
	return false
}

func hasUnquotedChar(s, chars string) bool {
	return scanUnquotedShellChars(s, func(_ int, ch byte) bool { return strings.IndexByte(chars, ch) >= 0 })
}

func statementHasWriteRedirect(stmt string) bool {
	return scanUnquotedShellChars(stmt, func(i int, ch byte) bool {
		if ch != '>' {
			return false
		}
		t := strings.TrimSpace(strings.TrimPrefix(stmt[i+1:], ">"))
		return !strings.HasPrefix(t, "/dev/null") && !strings.HasPrefix(t, "&1") && !strings.HasPrefix(t, "&2")
	})
}

func splitReadOnlyPipeStages(stmt string) ([][]string, bool, bool) {
	if hasUnquotedChar(stmt, "<>") {
		return nil, statementHasWriteRedirect(stmt), false
	}
	if hasUnsafeReadExpansionOrGlob(stmt) {
		return nil, false, false
	}
	var stages [][]string
	start := 0
	badPipe := scanUnquotedShellChars(stmt, func(i int, ch byte) bool {
		if ch != '|' {
			return false
		}
		toks := tokenizeShellSegment(strings.TrimSpace(stmt[start:i]))
		if len(toks) == 0 {
			return true
		}
		stages, start = append(stages, toks), i+1
		return false
	})
	tail := tokenizeShellSegment(strings.TrimSpace(stmt[start:]))
	if badPipe || len(tail) == 0 {
		return nil, false, false
	}
	return append(stages, tail), false, true
}

func hasUnsafeReadExpansionOrGlob(stmt string) bool {
	for i, q := 0, byte(0); i < len(stmt); i++ {
		switch ch := stmt[i]; {
		case q == '\'' || q == '$':
			i, q = advanceShellQuote(stmt, i, q)
		case q == '"':
			if ch == '\\' && i+1 < len(stmt) {
				i++
			} else if ch == '$' || ch == '`' {
				return true
			} else if ch == '"' {
				q = 0
			}
		case ch == '\\' && i+1 < len(stmt):
			i++
		case ch == '$' && i+1 < len(stmt) && stmt[i+1] == '\'':
			q, i = '$', i+1
		case ch == '\'' || ch == '"':
			q = ch
		case strings.IndexByte("$`*?[]", ch) >= 0:
			return true
		}
	}
	return false
}
