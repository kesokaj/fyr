package check

import "strings"

// chunk is one piece of code a document holds: an inline span's content, or
// one logical line of a fence (a line ending in `\` joined to the next).
// line is 1-based, where the span or the logical line starts.
type chunk struct {
	line  int
	text  string
	fence bool
}

// scan returns the code spans and fence lines of a Markdown text (plan
// fyr-check D15): backtick and tilde fences of any info string, closed by a
// run of the same character at least as long, or running to the end when
// left open; inline spans of N backticks closed by the next run of exactly N
// on the same line. Indented code blocks and prose are not read.
func scan(text string) []chunk {
	s := &scanner{prevBlank: true}
	for i, l := range strings.Split(text, "\n") {
		s.line(strings.TrimSuffix(l, "\r"), i+1)
	}
	s.flush()
	return s.out
}

// scanner is scan's state between lines.
type scanner struct {
	out                            []chunk
	fenceChar                      byte
	fenceLen                       int
	inFence, inIndented, prevBlank bool
	joined                         strings.Builder
	joinedAt                       int
}

// flush ends the fence's logical line being joined, if any.
func (s *scanner) flush() {
	if s.joinedAt > 0 {
		s.out = append(s.out, chunk{line: s.joinedAt, text: s.joined.String(), fence: true})
		s.joined.Reset()
		s.joinedAt = 0
	}
}

// line reads line l, numbered n.
func (s *scanner) line(l string, n int) {
	if s.inFence {
		s.fenceLine(l, n)
		return
	}
	if c, k, rest, ok := fenceMarker(l); ok && (c != '`' || !strings.Contains(rest, "`")) {
		s.inFence, s.fenceChar, s.fenceLen, s.inIndented = true, c, k, false
		return
	}
	blank := strings.TrimSpace(l) == ""
	if !blank && indentedCode(l) && (s.prevBlank || s.inIndented) {
		s.inIndented = true
		s.prevBlank = false
		return
	}
	if !blank {
		s.inIndented = false
	}
	s.prevBlank = blank
	s.out = append(s.out, spans(l, n)...)
}

// fenceLine reads a line inside a fence: its closing marker, or one more
// piece of a logical line, a line ending in `\` joined to the next.
func (s *scanner) fenceLine(l string, n int) {
	if c, k, rest, ok := fenceMarker(l); ok && c == s.fenceChar && k >= s.fenceLen && strings.TrimSpace(rest) == "" {
		s.flush()
		s.inFence = false
		s.prevBlank = false
		return
	}
	if s.joinedAt == 0 {
		s.joinedAt = n
	} else {
		s.joined.WriteByte(' ')
	}
	if rest, cont := strings.CutSuffix(l, `\`); cont {
		s.joined.WriteString(rest)
		return
	}
	s.joined.WriteString(l)
	s.flush()
}

// fenceMarker reads a fence line: up to three spaces of indent, then three
// or more backticks or tildes; rest is what follows the run.
func fenceMarker(l string) (c byte, n int, rest string, ok bool) {
	i := 0
	for i < len(l) && i < 3 && l[i] == ' ' {
		i++
	}
	if i >= len(l) || (l[i] != '`' && l[i] != '~') {
		return 0, 0, "", false
	}
	c = l[i]
	j := i
	for j < len(l) && l[j] == c {
		j++
	}
	if j-i < 3 {
		return 0, 0, "", false
	}
	return c, j - i, l[j:], true
}

// indentedCode is a line indented four columns or more that is not a list
// item: a nested bullet is prose, an indented block after a blank line code.
func indentedCode(l string) bool {
	col, i := 0, 0
	for i < len(l) && (l[i] == ' ' || l[i] == '\t') {
		if l[i] == '\t' {
			col += 4 - col%4
		} else {
			col++
		}
		i++
	}
	if col < 4 {
		return false
	}
	rest := l[i:]
	if strings.HasPrefix(rest, "- ") || strings.HasPrefix(rest, "* ") || strings.HasPrefix(rest, "+ ") {
		return false
	}
	j := 0
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		j++
	}
	return !(j > 0 && j < len(rest) && (rest[j] == '.' || rest[j] == ')'))
}

// spans is every inline code span on one line: a run of N backticks closed
// by the next run of exactly N; a run with no closer is literal text. Each
// run's closer is found once, from the end of the line back, so a line of
// unclosed runs costs one pass, not one per run (security review
// 2026-10-25, finding 4).
func spans(l string, n int) []chunk {
	type run struct{ at, k, next int }
	var runs []run
	for i := 0; i < len(l); {
		if l[i] != '`' {
			i++
			continue
		}
		k := tickRun(l, i)
		runs = append(runs, run{at: i, k: k})
		i += k
	}
	if len(runs) < 2 {
		return nil
	}
	after := map[int]int{} // run length -> index of the nearest later run of it
	for r := len(runs) - 1; r >= 0; r-- {
		runs[r].next = -1
		if c, ok := after[runs[r].k]; ok {
			runs[r].next = c
		}
		after[runs[r].k] = r
	}
	var out []chunk
	for r := 0; r < len(runs); {
		c := runs[r].next
		if c < 0 {
			r++
			continue
		}
		out = append(out, chunk{line: n, text: strings.TrimSpace(l[runs[r].at+runs[r].k : runs[c].at])})
		r = c + 1
	}
	return out
}

func tickRun(l string, i int) int {
	j := i
	for j < len(l) && l[j] == '`' {
		j++
	}
	return j - i
}
