package search

import (
	"path/filepath"
	"strings"
)

type bashReadStmt struct {
	text       string
	hasHeredoc bool
	isSubshell bool
	hasOrTrue  bool
}

type readSplitStep struct {
	nextIdx  int
	nextQ    byte
	heredocs []string
	addedHD  bool
	orTrue   bool
	didFlush bool
	ok       bool
}

func parseBashFileReadsExtended(cmd string) []BashFileRead {
	return parseBashFileReadsInDir(cmd, "", false, true)
}

func parseBashFileReadsInDir(cmd, cwd string, unknown, allowSubshell bool) []BashFileRead {
	stmts, ok := splitBashReadStatements(cmd, allowSubshell)
	if !ok {
		return nil
	}
	var out []BashFileRead
	for _, stmt := range stmts {
		if stmt.isSubshell {
			out = append(out, parseBashFileReadsInDir(stmt.text, cwd, unknown, false)...)
			continue
		}
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
		stages, hadStderrRedir, sawWrite, ok := splitReadOnlyPipeStages(stmt.text)
		if sawWrite {
			break
		}
		if !ok || len(stages) != 1 || hadStderrRedir || stmt.hasOrTrue {
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
		if reads, ok := parseBashReadPipelineMulti(stages); ok {
			out = appendResolvedBashReads(out, reads, cwd, unknown)
		}
	}
	return out
}

func appendResolvedBashReads(out, reads []BashFileRead, cwd string, unknown bool) []BashFileRead {
	for _, r := range reads {
		if r.Path == "" || (!filepath.IsAbs(r.Path) && unknown) {
			continue
		}
		if !filepath.IsAbs(r.Path) {
			r.Path = filepath.ToSlash(filepath.Clean(filepath.Join(cwd, r.Path)))
		}
		out = append(out, r)
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

func splitBashReadStatements(cmd string, allowSubshell bool) ([]bashReadStmt, bool) {
	var out []bashReadStmt
	var heredocs []string
	var cur strings.Builder
	stmtHeredoc, stmtOrTrue, q := false, false, byte(0)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, bashReadStmt{text: s, hasHeredoc: stmtHeredoc, hasOrTrue: stmtOrTrue})
		}
		cur.Reset()
		stmtHeredoc, stmtOrTrue = false, false
	}
	for i := 0; i < len(cmd); i++ {
		if q != 0 {
			var ok bool
			if i, q, ok = stepReadSplitQuoted(cmd, i, q, &cur); !ok {
				return nil, false
			}
			continue
		}
		if cmd[i] == '(' && allowSubshell && !stmtHeredoc && strings.TrimSpace(cur.String()) == "" {
			inner, closeIdx, ok := extractSimpleSubshell(cmd, i)
			if !ok {
				return nil, false
			}
			out = append(out, bashReadStmt{text: inner, isSubshell: true})
			cur.Reset()
			i = closeIdx
			continue
		}
		st := stepReadSplitUnquoted(cmd, i, heredocs, &cur)
		if !st.ok {
			return nil, false
		}
		i, q, heredocs = st.nextIdx, st.nextQ, st.heredocs
		stmtHeredoc = stmtHeredoc || st.addedHD
		stmtOrTrue = stmtOrTrue || st.orTrue
		if st.didFlush {
			flush()
		}
	}
	if q != 0 {
		return nil, false
	}
	flush()
	return out, len(out) > 0
}

func extractSimpleSubshell(cmd string, openIdx int) (string, int, bool) {
	closeIdx, q := -1, byte(0)
	for i := openIdx + 1; i < len(cmd); i++ {
		ch := cmd[i]
		if q != 0 {
			if q == '"' && (ch == '`' || (ch == '$' && i+1 < len(cmd) && cmd[i+1] == '(')) {
				return "", 0, false
			}
			i, q = advanceShellQuote(cmd, i, q)
			continue
		}
		switch ch {
		case '\\':
			if i+1 >= len(cmd) {
				return "", 0, false
			}
			i++
		case '$':
			if i+1 < len(cmd) && cmd[i+1] == '\'' {
				q, i = '$', i+1
			} else if i+1 < len(cmd) && cmd[i+1] == '(' {
				return "", 0, false
			}
		case '\'', '"':
			q = ch
		case '`', '(', '|', '<', '>':
			return "", 0, false
		case ')':
			closeIdx = i
		}
		if closeIdx >= 0 {
			break
		}
	}
	if q != 0 || closeIdx < 0 {
		return "", 0, false
	}
	inner := strings.TrimSpace(cmd[openIdx+1 : closeIdx])
	if inner == "" || !isValidSimpleSubshellFollow(cmd, closeIdx+1) || subshellHasControlKeyword(inner) {
		return "", 0, false
	}
	return inner, closeIdx, true
}

func isValidSimpleSubshellFollow(cmd string, pos int) bool {
	for pos < len(cmd) && (cmd[pos] == ' ' || cmd[pos] == '\t') {
		pos++
	}
	if pos >= len(cmd) {
		return true
	}
	switch cmd[pos] {
	case ';', '\n', '\r', '#':
		return true
	case '&':
		return pos+1 < len(cmd) && cmd[pos+1] == '&' && (pos+2 >= len(cmd) || cmd[pos+2] != '&')
	default:
		return false
	}
}

func subshellHasControlKeyword(inner string) bool {
	stmts, ok := splitBashReadStatements(inner, false)
	if !ok {
		return true
	}
	for _, st := range stmts {
		if st.hasHeredoc || st.hasOrTrue {
			return true
		}
		toks := tokenizeShellSegment(st.text)
		if len(toks) == 0 {
			continue
		}
		switch toks[0] {
		case "for", "while", "until", "if", "elif", "else", "then", "fi", "do", "done", "case", "esac", "select", "function", "{", "}":
			return true
		}
	}
	return false
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

func stepReadSplitUnquoted(cmd string, i int, heredocs []string, cur *strings.Builder) readSplitStep {
	switch ch := cmd[i]; {
	case ch == '\\':
		return stepReadSplitBackslash(cmd, i, heredocs, cur)
	case ch == '#' && (i == 0 || strings.ContainsRune(" \t\n\r;&|", rune(cmd[i-1]))):
		return readSplitStep{nextIdx: skipLineComment(cmd, i), heredocs: heredocs, ok: true}
	case ch == '`' || ((ch == '$' || ch == '<' || ch == '>') && i+1 < len(cmd) && cmd[i+1] == '(') || ch == '(' || ch == ')':
		return readSplitStep{nextIdx: i, heredocs: heredocs}
	case ch == '$' && i+1 < len(cmd) && cmd[i+1] == '\'':
		cur.WriteString("$'")
		return readSplitStep{nextIdx: i + 1, nextQ: '$', heredocs: heredocs, ok: true}
	case ch == '\'' || ch == '"':
		cur.WriteByte(ch)
		return readSplitStep{nextIdx: i, nextQ: ch, heredocs: heredocs, ok: true}
	case ch == '<' && i+1 < len(cmd) && cmd[i+1] == '<' && (i+2 >= len(cmd) || cmd[i+2] != '<'):
		delim, next := parseHeredocDelim(cmd, i+2)
		if delim != "" {
			heredocs = append(heredocs, delim)
		}
		cur.WriteString(cmd[i:next])
		return readSplitStep{nextIdx: next - 1, heredocs: heredocs, addedHD: true, ok: true}
	default:
		return stepReadSplitControl(cmd, i, heredocs, cur)
	}
}

func stepReadSplitBackslash(cmd string, i int, heredocs []string, cur *strings.Builder) readSplitStep {
	if i+1 >= len(cmd) {
		return readSplitStep{nextIdx: i, heredocs: heredocs}
	}
	if cmd[i+1] == '\n' {
		cur.WriteByte(' ')
		return readSplitStep{nextIdx: i + 1, heredocs: heredocs, ok: true}
	}
	if cmd[i+1] == '\r' && i+2 < len(cmd) && cmd[i+2] == '\n' {
		cur.WriteByte(' ')
		return readSplitStep{nextIdx: i + 2, heredocs: heredocs, ok: true}
	}
	cur.WriteString(cmd[i : i+2])
	return readSplitStep{nextIdx: i + 1, heredocs: heredocs, ok: true}
}

func stepReadSplitControl(cmd string, i int, heredocs []string, cur *strings.Builder) readSplitStep {
	switch ch := cmd[i]; ch {
	case '\r', '\n':
		next := i + 1
		if ch == '\r' && next < len(cmd) && cmd[next] == '\n' {
			next++
		}
		if len(heredocs) > 0 {
			next, heredocs = skipHeredocBodies(cmd, next, heredocs), nil
		}
		return readSplitStep{nextIdx: next - 1, heredocs: heredocs, didFlush: true, ok: true}
	case ';':
		return readSplitStep{nextIdx: i, heredocs: heredocs, didFlush: true, ok: true}
	case '&':
		if i > 0 && (cmd[i-1] == '>' || cmd[i-1] == '<') {
			cur.WriteByte(ch)
			return readSplitStep{nextIdx: i, heredocs: heredocs, ok: true}
		}
		if i+1 < len(cmd) && cmd[i+1] == '&' && (i+2 >= len(cmd) || cmd[i+2] != '&') {
			return readSplitStep{nextIdx: i + 1, heredocs: heredocs, didFlush: true, ok: true}
		}
		return readSplitStep{nextIdx: i, heredocs: heredocs}
	case '|':
		if i+1 < len(cmd) && cmd[i+1] == '|' {
			if next, ok := matchTrailingOrTrue(cmd, i+2); ok {
				return readSplitStep{nextIdx: next, heredocs: heredocs, orTrue: true, didFlush: true, ok: true}
			}
			return readSplitStep{nextIdx: i, heredocs: heredocs}
		}
		if i+1 < len(cmd) && cmd[i+1] == '&' {
			return readSplitStep{nextIdx: i, heredocs: heredocs}
		}
		cur.WriteByte(ch)
		return readSplitStep{nextIdx: i, heredocs: heredocs, ok: true}
	default:
		cur.WriteByte(ch)
		return readSplitStep{nextIdx: i, heredocs: heredocs, ok: true}
	}
}

func matchTrailingOrTrue(cmd string, pos int) (int, bool) {
	j := pos
	for j < len(cmd) && (cmd[j] == ' ' || cmd[j] == '\t') {
		j++
	}
	var end int
	switch {
	case strings.HasPrefix(cmd[j:], "true"):
		end = j + 4
	case j < len(cmd) && cmd[j] == ':':
		end = j + 1
	default:
		return 0, false
	}
	if end < len(cmd) && strings.IndexByte(" \t;&\n\r#", cmd[end]) < 0 {
		return 0, false
	}
	k := end
	for k < len(cmd) && (cmd[k] == ' ' || cmd[k] == '\t') {
		k++
	}
	if k == len(cmd) {
		return k - 1, true
	}
	switch cmd[k] {
	case ';', '\n', '\r':
		return k - 1, true
	case '#':
		return skipLineComment(cmd, k), true
	case '&':
		if k+1 < len(cmd) && cmd[k+1] == '&' && (k+2 >= len(cmd) || cmd[k+2] != '&') {
			return k + 1, true
		}
	}
	return 0, false
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

func splitReadOnlyPipeStages(stmt string) ([][]string, bool, bool, bool) {
	hadStderrRedir := false
	if hasUnquotedChar(stmt, "<>") {
		cleaned, ok := stripBenignStderrRedirects(stmt)
		if !ok {
			return nil, false, statementHasWriteRedirect(stmt), false
		}
		stmt, hadStderrRedir = cleaned, true
	}
	if hasUnsafeReadExpansionOrGlob(stmt) {
		return nil, hadStderrRedir, false, false
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
		return nil, hadStderrRedir, false, false
	}
	return append(stages, tail), hadStderrRedir, false, true
}

func stripBenignStderrRedirects(stmt string) (string, bool) {
	var b strings.Builder
	last := 0
	for i, q := 0, byte(0); i < len(stmt); i++ {
		if q != 0 {
			i, q = advanceShellQuote(stmt, i, q)
			continue
		}
		switch ch := stmt[i]; {
		case ch == '\\' && i+1 < len(stmt):
			i++
		case ch == '$' && i+1 < len(stmt) && stmt[i+1] == '\'':
			q, i = '$', i+1
		case ch == '\'' || ch == '"':
			q = ch
		case ch == '<':
			return "", false
		case ch == '>':
			nextEnd, ok := matchBenignStderrRedirect(stmt, i)
			if !ok {
				return "", false
			}
			b.WriteString(stmt[last : i-1])
			b.WriteByte(' ')
			last, i = nextEnd, nextEnd-1
		}
	}
	b.WriteString(stmt[last:])
	return b.String(), true
}

func matchBenignStderrRedirect(stmt string, gtIdx int) (int, bool) {
	if gtIdx < 1 || stmt[gtIdx-1] != '2' {
		return 0, false
	}
	if gtIdx >= 2 && stmt[gtIdx-2] != ' ' && stmt[gtIdx-2] != '\t' && stmt[gtIdx-2] != '|' {
		return 0, false
	}
	j, appendMode := gtIdx+1, false
	if j < len(stmt) && stmt[j] == '>' {
		j, appendMode = j+1, true
	}
	for j < len(stmt) && (stmt[j] == ' ' || stmt[j] == '\t') {
		j++
	}
	targets := []string{"/dev/null", "'/dev/null'", `"/dev/null"`}
	if !appendMode {
		targets = append(targets, "&1", "&-")
	}
	for _, tgt := range targets {
		if strings.HasPrefix(stmt[j:], tgt) {
			end := j + len(tgt)
			if end == len(stmt) || stmt[end] == ' ' || stmt[end] == '\t' || stmt[end] == '|' {
				return end, true
			}
		}
	}
	return 0, false
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
