package isolate

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

func firstCommandBin(seg string) string {
	words, ok := splitShellWords(seg)
	if !ok {
		return ""
	}
	inEnv := false
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w.redirAttached {
			continue
		}
		if w.redirOp {
			if i+1 >= len(words) || words[i+1].redirOp || words[i+1].redirAttached {
				return ""
			}
			i++
			continue
		}
		if w.assignOK {
			continue
		}
		if inEnv {
			if !w.quoted && (w.val == "-i" || w.val == "-") {
				continue
			}
			if !w.quoted && w.val == "--" {
				inEnv = false
				continue
			}
			if strings.HasPrefix(w.val, "-") {
				return ""
			}
		} else if !w.quoted && w.val == "env" {
			inEnv = true
			continue
		}
		if w.val == "" {
			return ""
		}
		return filepath.Base(w.val)
	}
	return ""
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
	for i := start; i < len(seg); i++ {
		ch := seg[i]
		if isShellSpace(ch) || ch == '<' || ch == '>' {
			return string(buf), i, m, true
		}
		if next, ok := appendQuotedSegment(seg, i, &buf, &m); next > i || !ok {
			if !ok {
				return "", 0, wordMeta{}, false
			}
			i = next - 1
			continue
		}
		if ch == '\\' {
			if i+1 >= len(seg) {
				return "", 0, wordMeta{}, false
			}
			m.quoted = true
			buf = append(buf, seg[i+1])
			i++
			continue
		}
		if ch == '=' && !sawEq && !m.quoted {
			sawEq = true
			m.assignOK = isShellIdent(string(buf))
		}
		buf = append(buf, ch)
	}
	return string(buf), len(seg), m, true
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
