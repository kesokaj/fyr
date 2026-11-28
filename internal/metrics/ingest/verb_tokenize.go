package ingest

import "strings"

// tokenizer splits a command line into segments of words.
type tokenizer struct {
	segs        [][]string
	words       []string
	cur         strings.Builder
	inWord      bool
	quoted      bool
	glued       bool
	parenDepth  int
	caseDepth   int
	wantPattern bool
}

func (t *tokenizer) flushWord() {
	if !t.inWord {
		return
	}
	w := t.cur.String()
	t.cur.Reset()
	t.inWord = false
	switch {
	case t.glued:
		w = w[:2]
	case t.quoted && strings.HasPrefix(w, "-"):
		w = quotedMark + w
	}
	t.quoted, t.glued = false, false
	if len(t.words) == 0 {
		t.trackCaseKeyword(w)
	}
	t.words = append(t.words, w)
	if w == "in" && len(t.words) >= 3 && t.words[0] == "case" {
		t.words = nil
		t.wantPattern = true
	}
}

func (t *tokenizer) trackCaseKeyword(w string) {
	switch w {
	case "case":
		t.caseDepth++
	case "esac":
		if t.caseDepth > 0 {
			t.caseDepth--
		}
		t.wantPattern = false
	}
}

func (t *tokenizer) flushSeg() {
	t.flushWord()
	if len(t.words) > 0 && !t.wantPattern {
		t.segs = append(t.segs, t.words)
	}
	t.words = nil
}

func segments(cmd string) [][]string {
	t := &tokenizer{}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		switch r := rs[i]; {
		case r == '\'' || r == '"' || r == '\\' && i+1 < len(rs):
			i = t.scanQuoted(rs, i)
		case r == '$' && (runeAt(rs, i+1, '{') || runeAt(rs, i+1, '(')):
			i = t.scanDollar(rs, i)
		default:
			i = t.scanOperator(rs, i)
		}
	}
	t.flushSeg()
	return t.segs
}

func (t *tokenizer) swallow(rs []rune, i int, open, close rune) int {
	t.inWord = true
	t.cur.WriteRune('$')
	depth := 0
	for ; i < len(rs); i++ {
		t.cur.WriteRune(rs[i])
		switch rs[i] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return i
}

const quotedMark = "\x00"

func (t *tokenizer) markQuoted() {
	cur := t.cur.String()
	switch {
	case t.glued || strings.Contains(cur, "="):
	case len(cur) == 2 && cur[0] == '-' && cur[1] != '-':
		t.glued = true
	default:
		t.quoted = true
	}
}

func runeAt(rs []rune, i int, r rune) bool { return i < len(rs) && rs[i] == r }

func (t *tokenizer) scanQuoted(rs []rune, i int) int {
	switch q := rs[i]; q {
	case '\'', '"':
		t.inWord = true
		t.markQuoted()
		for i++; i < len(rs) && rs[i] != q; i++ {
			if q == '"' && rs[i] == '\\' && i+1 < len(rs) {
				i++
			}
			t.cur.WriteRune(rs[i])
		}
	default:
		i++
		if rs[i] != '\n' {
			t.markQuoted()
			t.cur.WriteRune(rs[i])
			t.inWord = true
		}
	}
	return i
}

func (t *tokenizer) scanDollar(rs []rune, i int) int {
	switch {
	case runeAt(rs, i+1, '{'):
		return t.swallow(rs, i+1, '{', '}')
	case runeAt(rs, i+1, '(') && runeAt(rs, i+2, '('):
		return t.swallow(rs, i+1, '(', ')')
	default:
		t.flushSeg()
		t.parenDepth++
		return i + 1
	}
}

func (t *tokenizer) scanOperator(rs []rune, i int) int {
	r := rs[i]
	switch {
	case r == '#' && !t.inWord:
		return skipComment(rs, i)
	case isRedirectAmp(rs, i):
		t.writeWordRune(r)
	case isSeparatorRune(r):
		return t.scanSeparator(rs, i)
	case r == '(':
		t.flushSeg()
		t.parenDepth++
	case r == ')':
		t.closeParen()
	case (r == '{' || r == '}') && !t.inWord:
		t.flushSeg()
	case r == ' ' || r == '\t':
		t.flushWord()
	default:
		t.writeWordRune(r)
	}
	return i
}

func (t *tokenizer) writeWordRune(r rune) {
	t.cur.WriteRune(r)
	t.inWord = true
}

func skipComment(rs []rune, i int) int {
	for i+1 < len(rs) && rs[i+1] != '\n' {
		i++
	}
	return i
}

func isRedirectAmp(rs []rune, i int) bool {
	if rs[i] != '&' {
		return false
	}
	prevRedir := i > 0 && (rs[i-1] == '>' || rs[i-1] == '<')
	return prevRedir || runeAt(rs, i+1, '>')
}

func isSeparatorRune(r rune) bool {
	return r == ';' || r == '\n' || r == '|' || r == '&' || r == '`'
}

func (t *tokenizer) scanSeparator(rs []rune, i int) int {
	r := rs[i]
	if r == ';' && runeAt(rs, i+1, ';') {
		t.flushSeg()
		if t.caseDepth > 0 {
			t.wantPattern = true
		}
		return i + 1
	}
	if r == '|' && t.wantPattern {
		t.flushWord()
		return i
	}
	if (r == '|' || r == '&') && runeAt(rs, i+1, r) {
		i++
	}
	t.flushSeg()
	return i
}

func (t *tokenizer) closeParen() {
	switch {
	case t.wantPattern || t.parenDepth == 0:
		t.flushWord()
		t.words = nil
		t.wantPattern = false
	default:
		t.parenDepth--
		t.flushSeg()
	}
}
