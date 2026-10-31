package metrics

import "strings"

// quotedMark leads a word that quoting made look like a flag: the shell
// passes `"--x"` as `--x`, but a flag is written bare, so such a word is
// content (`--title "--hunter2pass"`). With the mark it starts with no dash,
// so flagName drops it and it cannot be a wrapper's or git's option.
const quotedMark = "\x00"

// markQuoted notes a quoted or escaped rune. One after `=` sits in a flag's
// value (`--title="x"`), which flagName already cuts off. One right after a
// short flag is that flag's glued value (`-m"fix"`): only the flag is kept.
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

// runeAt reports whether rs holds r at index i.
func runeAt(rs []rune, i int, r rune) bool { return i < len(rs) && rs[i] == r }

// scanQuoted adds the quoted run or the escaped rune at rs[i] to the current
// word and returns the index of its last rune.
func (t *tokenizer) scanQuoted(rs []rune, i int) int {
	switch q := rs[i]; q {
	case '\'', '"':
		// a quoted run is part of the current word; its content is never split
		t.inWord = true
		t.markQuoted()
		for i++; i < len(rs) && rs[i] != q; i++ {
			if q == '"' && rs[i] == '\\' && i+1 < len(rs) {
				i++
			}
			t.cur.WriteRune(rs[i])
		}
	default: // a backslash with a rune after it; a newline continues the line
		i++
		if rs[i] != '\n' {
			t.markQuoted()
			t.cur.WriteRune(rs[i])
			t.inWord = true
		}
	}
	return i
}

// scanDollar takes the expansion at rs[i]: ${…} and $((…)) join the current
// word unparsed, and $( opens a subshell's segment. It returns the index of
// the last rune it took.
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

// scanOperator takes the rune at rs[i] outside quotes and expansions: a
// comment, a redirection's &, a separator, a parenthesis or brace, a blank,
// or a rune of the current word. It returns the index of the last rune it
// took.
func (t *tokenizer) scanOperator(rs []rune, i int) int {
	r := rs[i]
	switch {
	case r == '#' && !t.inWord:
		for i+1 < len(rs) && rs[i+1] != '\n' {
			i++
		}
	case r == '&' && (i > 0 && (rs[i-1] == '>' || rs[i-1] == '<') || runeAt(rs, i+1, '>')):
		t.cur.WriteRune(r) // a redirection (2>&1, &>f), never a separator
		t.inWord = true
	case r == ';' && runeAt(rs, i+1, ';'):
		i++
		t.flushSeg()
		if t.caseDepth > 0 {
			t.wantPattern = true
		}
	case r == '|' && t.wantPattern:
		t.flushWord() // an alternative in a case pattern
	case r == ';' || r == '\n' || r == '|' || r == '&' || r == '`':
		if (r == '|' || r == '&') && runeAt(rs, i+1, r) {
			i++
		}
		t.flushSeg()
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
		t.cur.WriteRune(r)
		t.inWord = true
	}
	return i
}

// closeParen ends a subshell's segment, or a case pattern.
func (t *tokenizer) closeParen() {
	switch {
	case t.wantPattern || t.parenDepth == 0:
		// the end of a case pattern: its words are content
		t.flushWord()
		t.words = nil
		t.wantPattern = false
	default:
		t.parenDepth--
		t.flushSeg()
	}
}
