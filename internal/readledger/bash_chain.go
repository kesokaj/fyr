package readledger

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

type readSplitState struct {
	out         []bashReadStmt
	heredocs    []string
	cur         strings.Builder
	stmtHeredoc bool
	stmtOrTrue  bool
	q           byte
}

func (st *readSplitState) flush() {
	if s := strings.TrimSpace(st.cur.String()); s != "" {
		st.out = append(st.out, bashReadStmt{text: s, hasHeredoc: st.stmtHeredoc, hasOrTrue: st.stmtOrTrue})
	}
	st.cur.Reset()
	st.stmtHeredoc, st.stmtOrTrue = false, false
}

func (st *readSplitState) step(cmd string, i int, allowSubshell bool) (int, bool) {
	if st.q != 0 {
		var ok bool
		i, st.q, ok = stepReadSplitQuoted(cmd, i, st.q, &st.cur)
		return i, ok
	}
	if cmd[i] == '(' && allowSubshell && !st.stmtHeredoc && strings.TrimSpace(st.cur.String()) == "" {
		inner, closeIdx, ok := extractSimpleSubshell(cmd, i)
		if !ok {
			return i, false
		}
		st.out = append(st.out, bashReadStmt{text: inner, isSubshell: true})
		st.cur.Reset()
		return closeIdx, true
	}
	res := stepReadSplitUnquoted(cmd, i, st.heredocs, &st.cur)
	if !res.ok {
		return i, false
	}
	st.q, st.heredocs = res.nextQ, res.heredocs
	st.stmtHeredoc = st.stmtHeredoc || res.addedHD
	st.stmtOrTrue = st.stmtOrTrue || res.orTrue
	if res.didFlush {
		st.flush()
	}
	return res.nextIdx, true
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
		var reads []BashFileRead
		var stop bool
		reads, cwd, unknown, stop = parseSingleBashReadStmt(stmt, cwd, unknown)
		out = append(out, reads...)
		if stop {
			break
		}
	}
	return out
}

func parseSingleBashReadStmt(stmt bashReadStmt, cwd string, unknown bool) ([]BashFileRead, string, bool, bool) {
	if stmt.hasHeredoc {
		nextDir, nextUnknown, stop := handleHeredocReadStmt(stmt.text, cwd, unknown)
		return nil, nextDir, nextUnknown, stop
	}
	if isBenignBashPrefixStmt(stmt.text) {
		return nil, cwd, unknown, false
	}
	stages, hadStderrRedir, sawWrite, ok := splitReadOnlyPipeStages(stmt.text)
	if sawWrite {
		return nil, cwd, unknown, true
	}
	if !ok || len(stages) != 1 || hadStderrRedir || stmt.hasOrTrue {
		if statementStartsWithDirChange(stmt.text) {
			return nil, "", true, false
		}
		if !ok || len(stages) == 0 {
			return nil, cwd, unknown, false
		}
	} else if handled, nextDir, nextUnknown := stepTrackedBashDir(cwd, unknown, stages[0]); handled {
		return nil, nextDir, nextUnknown, false
	}
	if reads, ok := parseBashReadPipelineMulti(stages); ok {
		return appendResolvedBashReads(nil, reads, cwd, unknown), cwd, unknown, false
	}
	return nil, cwd, unknown, false
}

func handleHeredocReadStmt(text, cwd string, unknown bool) (string, bool, bool) {
	if statementHasWriteRedirect(text) {
		return cwd, unknown, true
	}
	if statementStartsWithDirChange(text) {
		return "", true, false
	}
	return cwd, unknown, false
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
	head := firstUnquotedPipeStage(stmt)
	toks := stripLeadingEnvAndRedirects(TokenizeShellSegment(head))
	if len(toks) == 0 {
		return false
	}
	cmdTok := unwrapRedirectedCommandTok(toks)
	return cmdTok == "cd" || cmdTok == "pushd" || cmdTok == "popd"
}

func firstUnquotedPipeStage(stmt string) string {
	head := stmt
	ScanUnquotedShellChars(stmt, func(i int, ch byte) bool {
		if ch == '|' {
			head = stmt[:i]
			return true
		}
		return false
	})
	return head
}

func stripLeadingEnvAndRedirects(toks []string) []string {
	for len(toks) > 0 {
		if IsEnvAssignToken(toks[0]) {
			toks = toks[1:]
			continue
		}
		if skip := leadingRedirectSkip(toks); skip > 0 {
			toks = toks[skip:]
			continue
		}
		break
	}
	return toks
}

func unwrapRedirectedCommandTok(toks []string) string {
	cmdTok := trimRedirectSuffix(toks[0])
	if (cmdTok == "builtin" || cmdTok == "command") && len(toks) > 1 {
		toks = toks[1:]
		if toks[0] == "--" && len(toks) > 1 {
			toks = toks[1:]
		}
		cmdTok = trimRedirectSuffix(toks[0])
	}
	return cmdTok
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
	var st readSplitState
	for i := 0; i < len(cmd); i++ {
		var ok bool
		if i, ok = st.step(cmd, i, allowSubshell); !ok {
			return nil, false
		}
	}
	if st.q != 0 {
		return nil, false
	}
	st.flush()
	return st.out, len(st.out) > 0
}

func extractSimpleSubshell(cmd string, openIdx int) (string, int, bool) {
	closeIdx, ok := scanSimpleSubshellClose(cmd, openIdx)
	if !ok {
		return "", 0, false
	}
	inner := strings.TrimSpace(cmd[openIdx+1 : closeIdx])
	if inner == "" || !isValidSimpleSubshellFollow(cmd, closeIdx+1) || subshellHasControlKeyword(inner) {
		return "", 0, false
	}
	return inner, closeIdx, true
}

func scanSimpleSubshellClose(cmd string, openIdx int) (int, bool) {
	q := byte(0)
	for i := openIdx + 1; i < len(cmd); i++ {
		if q != 0 {
			if q == '"' && (cmd[i] == '`' || (cmd[i] == '$' && i+1 < len(cmd) && cmd[i+1] == '(')) {
				return 0, false
			}
			i, q = AdvanceShellQuote(cmd, i, q)
			continue
		}
		nextIdx, nextQ, closed, ok := stepSimpleSubshellUnquoted(cmd, i)
		if !ok {
			return 0, false
		}
		if closed {
			return nextIdx, true
		}
		i, q = nextIdx, nextQ
	}
	return 0, false
}

func stepSimpleSubshellUnquoted(cmd string, i int) (int, byte, bool, bool) {
	switch ch := cmd[i]; ch {
	case '\\':
		if i+1 >= len(cmd) {
			return 0, 0, false, false
		}
		return i + 1, 0, false, true
	case '$':
		if i+1 < len(cmd) && cmd[i+1] == '\'' {
			return i + 1, '$', false, true
		}
		if i+1 < len(cmd) && cmd[i+1] == '(' {
			return 0, 0, false, false
		}
		return i, 0, false, true
	case '\'', '"':
		return i, ch, false, true
	case '`', '(', '|', '<', '>':
		return 0, 0, false, false
	case ')':
		return i, 0, true, true
	default:
		return i, 0, false, true
	}
}

func isValidSimpleSubshellFollow(cmd string, pos int) bool {
	pos = skipHorizontalSpace(cmd, pos)
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
		toks := TokenizeShellSegment(st.text)
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
		return readSplitStep{nextIdx: SkipLineComment(cmd, i), heredocs: heredocs, ok: true}
	case ch == '`' || ((ch == '$' || ch == '<' || ch == '>') && i+1 < len(cmd) && cmd[i+1] == '(') || ch == '(' || ch == ')':
		return readSplitStep{nextIdx: i, heredocs: heredocs}
	case ch == '$' && i+1 < len(cmd) && cmd[i+1] == '\'':
		cur.WriteString("$'")
		return readSplitStep{nextIdx: i + 1, nextQ: '$', heredocs: heredocs, ok: true}
	case ch == '\'' || ch == '"':
		cur.WriteByte(ch)
		return readSplitStep{nextIdx: i, nextQ: ch, heredocs: heredocs, ok: true}
	case ch == '<' && i+1 < len(cmd) && cmd[i+1] == '<' && (i+2 >= len(cmd) || cmd[i+2] != '<'):
		delim, next := ParseHeredocDelim(cmd, i+2)
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
			next, heredocs = SkipHeredocBodies(cmd, next, heredocs), nil
		}
		return readSplitStep{nextIdx: next - 1, heredocs: heredocs, didFlush: true, ok: true}
	case ';':
		return readSplitStep{nextIdx: i, heredocs: heredocs, didFlush: true, ok: true}
	case '&':
		return stepReadSplitAmp(cmd, i, heredocs, cur)
	case '|':
		return stepReadSplitPipe(cmd, i, heredocs, cur)
	default:
		cur.WriteByte(ch)
		return readSplitStep{nextIdx: i, heredocs: heredocs, ok: true}
	}
}

func stepReadSplitAmp(cmd string, i int, heredocs []string, cur *strings.Builder) readSplitStep {
	if i > 0 && (cmd[i-1] == '>' || cmd[i-1] == '<') {
		cur.WriteByte('&')
		return readSplitStep{nextIdx: i, heredocs: heredocs, ok: true}
	}
	if i+1 < len(cmd) && cmd[i+1] == '&' && (i+2 >= len(cmd) || cmd[i+2] != '&') {
		return readSplitStep{nextIdx: i + 1, heredocs: heredocs, didFlush: true, ok: true}
	}
	return readSplitStep{nextIdx: i, heredocs: heredocs}
}

func stepReadSplitPipe(cmd string, i int, heredocs []string, cur *strings.Builder) readSplitStep {
	if i+1 < len(cmd) && cmd[i+1] == '|' {
		if next, ok := matchTrailingOrTrue(cmd, i+2); ok {
			return readSplitStep{nextIdx: next, heredocs: heredocs, orTrue: true, didFlush: true, ok: true}
		}
		return readSplitStep{nextIdx: i, heredocs: heredocs}
	}
	if i+1 < len(cmd) && cmd[i+1] == '&' {
		return readSplitStep{nextIdx: i, heredocs: heredocs}
	}
	cur.WriteByte('|')
	return readSplitStep{nextIdx: i, heredocs: heredocs, ok: true}
}

func matchTrailingOrTrue(cmd string, pos int) (int, bool) {
	j := skipHorizontalSpace(cmd, pos)
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
	return matchTrailingOrTrueFollow(cmd, end)
}

func matchTrailingOrTrueFollow(cmd string, end int) (int, bool) {
	k := skipHorizontalSpace(cmd, end)
	if k == len(cmd) {
		return k - 1, true
	}
	switch cmd[k] {
	case ';', '\n', '\r':
		return k - 1, true
	case '#':
		return SkipLineComment(cmd, k), true
	case '&':
		if k+1 < len(cmd) && cmd[k+1] == '&' && (k+2 >= len(cmd) || cmd[k+2] != '&') {
			return k + 1, true
		}
	}
	return 0, false
}

func skipHorizontalSpace(s string, pos int) int {
	for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
		pos++
	}
	return pos
}

func isBenignBashPrefixStmt(stmt string) bool {
	if hasUnquotedChar(stmt, "<>|") {
		return false
	}
	toks := TokenizeShellSegment(stmt)
	if len(toks) == 0 || toks[0] == "export" || toks[0] == "unset" {
		return true
	}
	if toks[0] == "set" {
		return len(toks) >= 2 && (strings.HasPrefix(toks[1], "-") || strings.HasPrefix(toks[1], "+"))
	}
	for _, tok := range toks {
		if !IsEnvAssignToken(tok) {
			return false
		}
	}
	return true
}

// ScanUnquotedShellChars invokes onUnquoted for each unquoted, unescaped byte in s until it returns true.
func ScanUnquotedShellChars(s string, onUnquoted func(i int, ch byte) bool) bool {
	for i, q := 0, byte(0); i < len(s); i++ {
		if q != 0 {
			i, q = AdvanceShellQuote(s, i, q)
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
	return ScanUnquotedShellChars(s, func(_ int, ch byte) bool { return strings.IndexByte(chars, ch) >= 0 })
}

func statementHasWriteRedirect(stmt string) bool {
	return ScanUnquotedShellChars(stmt, func(i int, ch byte) bool {
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
	badPipe := ScanUnquotedShellChars(stmt, func(i int, ch byte) bool {
		if ch != '|' {
			return false
		}
		toks := TokenizeShellSegment(strings.TrimSpace(stmt[start:i]))
		if len(toks) == 0 {
			return true
		}
		stages, start = append(stages, toks), i+1
		return false
	})
	tail := TokenizeShellSegment(strings.TrimSpace(stmt[start:]))
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
			i, q = AdvanceShellQuote(stmt, i, q)
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
	return matchStderrTarget(stmt, skipHorizontalSpace(stmt, j), appendMode)
}

func matchStderrTarget(stmt string, pos int, appendMode bool) (int, bool) {
	targets := []string{"/dev/null", "'/dev/null'", `"/dev/null"`}
	if !appendMode {
		targets = append(targets, "&1", "&-")
	}
	for _, tgt := range targets {
		if !strings.HasPrefix(stmt[pos:], tgt) {
			continue
		}
		end := pos + len(tgt)
		if end == len(stmt) || stmt[end] == ' ' || stmt[end] == '\t' || stmt[end] == '|' {
			return end, true
		}
	}
	return 0, false
}

func hasUnsafeReadExpansionOrGlob(stmt string) bool {
	for i, q := 0, byte(0); i < len(stmt); i++ {
		switch ch := stmt[i]; {
		case q == '\'' || q == '$':
			i, q = AdvanceShellQuote(stmt, i, q)
		case q == '"':
			var closed, unsafe bool
			if i, closed, unsafe = stepDoubleQuotedUnsafe(stmt, i); unsafe {
				return true
			}
			if closed {
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

func stepDoubleQuotedUnsafe(stmt string, i int) (int, bool, bool) {
	ch := stmt[i]
	if ch == '\\' && i+1 < len(stmt) {
		return i + 1, false, false
	}
	if ch == '$' || ch == '`' {
		return i, false, true
	}
	return i, ch == '"', false
}
