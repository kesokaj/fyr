package acp

import (
	"path/filepath"
	"strings"
)

type shellWord struct {
	val           string
	quoted        bool
	assignOK      bool
	redirOp       bool
	redirAttached bool
}

// FirstCommandBin returns the base name of the first executable invoked by a
// single shell segment, skipping leading redirections, assignments, and safe
// `env` prefixes, or "" when the segment is empty or fails closed.
func FirstCommandBin(seg string) string {
	words, ok := splitShellWords(seg)
	if !ok {
		return ""
	}
	inEnv := false
	for i := 0; i < len(words); {
		step, bin, stop := stepCommandWord(words, i, &inEnv)
		if stop {
			return bin
		}
		i += step
	}
	return ""
}

func stepCommandWord(words []shellWord, i int, inEnv *bool) (step int, bin string, stop bool) {
	w := words[i]
	if w.redirAttached || w.assignOK {
		return 1, "", false
	}
	if w.redirOp {
		if !validRedirTarget(words, i) {
			return 0, "", true
		}
		return 2, "", false
	}
	skip, fail := consumeEnvWord(w, inEnv)
	if fail {
		return 0, "", true
	}
	if skip {
		return 1, "", false
	}
	if w.val == "" {
		return 0, "", true
	}
	return 0, filepath.Base(w.val), true
}

func validRedirTarget(words []shellWord, i int) bool {
	return i+1 < len(words) && !words[i+1].redirOp && !words[i+1].redirAttached
}

func consumeEnvWord(w shellWord, inEnv *bool) (skip, fail bool) {
	if !*inEnv {
		if !w.quoted && w.val == "env" {
			*inEnv = true
			return true, false
		}
		return false, false
	}
	if !w.quoted && (w.val == "-i" || w.val == "-") {
		return true, false
	}
	if !w.quoted && w.val == "--" {
		*inEnv = false
		return true, false
	}
	return false, strings.HasPrefix(w.val, "-")
}

func splitShellWords(seg string) ([]shellWord, bool) {
	var words []shellWord
	for i := 0; i < len(seg); {
		for i < len(seg) && isShellSpace(seg[i]) {
			i++
		}
		if i >= len(seg) {
			break
		}
		w, next, ok := scanShellWord(seg, i)
		if !ok {
			return nil, false
		}
		words = append(words, w)
		i = next
	}
	return words, true
}

func scanShellWord(seg string, i int) (shellWord, int, bool) {
	if n := redirPrefixLen(seg[i:]); n > 0 {
		return scanRedirWord(seg, i, n)
	}
	val, next, meta, ok := scanWordToken(seg, i)
	if !ok {
		return shellWord{}, 0, false
	}
	return shellWord{val: val, quoted: meta.quoted, assignOK: meta.assignOK}, next, true
}

func scanRedirWord(seg string, i, n int) (shellWord, int, bool) {
	j := i + n
	if j >= len(seg) || isShellSpace(seg[j]) || seg[j] == '<' || seg[j] == '>' {
		return shellWord{redirOp: true}, j, true
	}
	_, next, _, ok := scanWordToken(seg, j)
	if !ok {
		return shellWord{}, 0, false
	}
	return shellWord{redirAttached: true}, next, true
}

type wordMeta struct {
	quoted   bool
	assignOK bool
}

func scanWordToken(seg string, start int) (string, int, wordMeta, bool) {
	var buf []byte
	var m wordMeta
	sawEq := false
	for i := start; i < len(seg); {
		if isWordDelimiter(seg[i]) {
			return string(buf), i, m, true
		}
		next, ok := stepWordTokenChar(seg, i, &buf, &m, &sawEq)
		if !ok {
			return "", 0, wordMeta{}, false
		}
		i = next
	}
	return string(buf), len(seg), m, true
}

func isWordDelimiter(ch byte) bool {
	return isShellSpace(ch) || ch == '<' || ch == '>'
}

func stepWordTokenChar(seg string, i int, buf *[]byte, m *wordMeta, sawEq *bool) (int, bool) {
	next, ok := appendQuotedSegment(seg, i, buf, m)
	if !ok {
		return 0, false
	}
	if next > i {
		return next, true
	}
	ch := seg[i]
	if ch == '\\' {
		if i+1 >= len(seg) {
			return 0, false
		}
		m.quoted = true
		*buf = append(*buf, seg[i+1])
		return i + 2, true
	}
	if ch == '=' && !*sawEq && !m.quoted {
		*sawEq = true
		m.assignOK = isShellIdent(string(*buf))
	}
	*buf = append(*buf, ch)
	return i + 1, true
}

func appendQuotedSegment(seg string, i int, buf *[]byte, m *wordMeta) (int, bool) {
	ch := seg[i]
	if ch == '$' && i+1 < len(seg) && seg[i+1] == '\'' {
		m.quoted = true
		return consumeAnsiSingleQuote(seg, i+2, buf)
	}
	if ch == '\'' {
		m.quoted = true
		return consumeSingleQuote(seg, i+1, buf)
	}
	if ch == '"' {
		m.quoted = true
		return consumeDoubleQuote(seg, i+1, buf)
	}
	return i, true
}

func consumeSingleQuote(seg string, i int, buf *[]byte) (int, bool) {
	for i < len(seg) {
		if seg[i] == '\'' {
			return i + 1, true
		}
		*buf = append(*buf, seg[i])
		i++
	}
	return 0, false
}

func consumeAnsiSingleQuote(seg string, i int, buf *[]byte) (int, bool) {
	for i < len(seg) {
		if seg[i] == '\\' {
			if i+1 >= len(seg) || seg[i+1] == 'c' {
				return 0, false
			}
			*buf = append(*buf, seg[i], seg[i+1])
			i += 2
			continue
		}
		if seg[i] == '\'' {
			return i + 1, true
		}
		*buf = append(*buf, seg[i])
		i++
	}
	return 0, false
}

func consumeDoubleQuote(seg string, i int, buf *[]byte) (int, bool) {
	for i < len(seg) {
		if seg[i] == '\\' {
			if i+1 >= len(seg) {
				return 0, false
			}
			next := seg[i+1]
			if next == '$' || next == '`' || next == '"' || next == '\\' {
				*buf = append(*buf, next)
			} else {
				*buf = append(*buf, '\\', next)
			}
			i += 2
			continue
		}
		if seg[i] == '"' {
			return i + 1, true
		}
		*buf = append(*buf, seg[i])
		i++
	}
	return 0, false
}

func redirPrefixLen(s string) int {
	if strings.HasPrefix(s, "&>>") {
		return 3
	}
	if strings.HasPrefix(s, "&>") {
		return 2
	}
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	rest := s[n:]
	for _, op := range []string{"<<<", "<<-", ">>", ">|", ">&", "<>", "<<", "<&", ">", "<"} {
		if strings.HasPrefix(rest, op) {
			return n + len(op)
		}
	}
	return 0
}

func isShellIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isAlpha := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
		isDigit := c >= '0' && c <= '9'
		if !isAlpha && (i == 0 || !isDigit) {
			return false
		}
	}
	return true
}

func isShellSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n'
}
