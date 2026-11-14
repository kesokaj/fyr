package metrics

import "strings"

// heredoc is one here-document a line opens: its delimiter word, and whether
// it was opened with <<-, the only form whose terminator may be led by tabs
// (SD56 of the 2026-10-27 audit).
type heredoc struct {
	word string
	tabs bool
}

// stripHeredocs removes every here-document body from cmd, so that the
// lines of a file written with `cat <<EOF` never become commands (they are
// content, and the store keeps none).
func stripHeredocs(cmd string) string {
	lines := strings.Split(cmd, "\n")
	var out []string
	var pending []heredoc // delimiters opened on the current line, in order
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		out = append(out, line)
		ds, unparsed := heredocDelims(line)
		if unparsed {
			// A here-doc with no delimiter word (a bare << at the end of a
			// line) cannot be matched: everything after its line may be its
			// body, so none of it is read (security review of v2.6.0).
			break
		}
		pending = append(pending, ds...)
		for len(pending) > 0 {
			d := pending[0]
			pending = pending[1:]
			for i+1 < len(lines) {
				i++
				l := lines[i]
				if d.tabs {
					l = strings.TrimLeft(l, "\t")
				}
				if l == d.word {
					break
				}
			}
		}
	}
	return strings.Join(out, "\n")
}

// heredocDelims returns the delimiters of the here-documents line opens:
// `<<WORD`, `<<-WORD`, `<<'WORD'`, `<<"WORD"`, `<<\WORD`; `<<<` is a
// here-string and opens none.
func heredocDelims(line string) (ds []heredoc, unparsed bool) {
	for {
		j := strings.Index(line, "<<")
		if j < 0 {
			return ds, false
		}
		line = line[j+2:]
		if strings.HasPrefix(line, "<") {
			line = line[1:]
			continue
		}
		tabs := strings.HasPrefix(line, "-")
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimLeft(line, " \t")
		if strings.HasPrefix(line, "'") || strings.HasPrefix(line, "\"") {
			ds, line, unparsed = quotedDelim(ds, line, tabs)
		} else {
			ds, line, unparsed = unquotedDelim(ds, line, tabs)
		}
		if unparsed {
			return ds, true
		}
	}
}

// quotedDelim reads the quoted delimiter line starts with into ds and
// returns the rest of line; unparsed when the delimiter is not modelled.
func quotedDelim(ds []heredoc, line string, tabs bool) ([]heredoc, string, bool) {
	// A quoted delimiter is the literal text up to the closing quote:
	// <<'END!' ends only at a line END! (security review of v2.6.0).
	q := line[:1]
	k := strings.Index(line[1:], q)
	if k <= 0 {
		return ds, line, true // no closing quote, or an empty delimiter
	}
	ds = append(ds, heredoc{line[1 : 1+k], tabs})
	line = line[k+2:]
	if line != "" && !strings.ContainsRune(" \t;&|<>()`", rune(line[0])) {
		// "E"ND: more delimiter text after the closing quote, a mix
		// that is not modelled; nothing after this line is read.
		return ds, line, true
	}
	return ds, line, false
}

// unquotedDelim reads the unquoted (or backslash-quoted) delimiter line
// starts with into ds and returns the rest of line; unparsed when there is
// no delimiter word or it is not modelled.
func unquotedDelim(ds []heredoc, line string, tabs bool) ([]heredoc, string, bool) {
	line = strings.TrimPrefix(line, "\\") // <<\EOF: a quoted delimiter, the same here-doc
	// An unquoted delimiter is the whole word up to a shell
	// metacharacter, taken literally ($END included), as bash does.
	end := strings.IndexFunc(line, func(r rune) bool {
		return strings.ContainsRune(" \t;&|<>()`'\"", r)
	})
	if end > 0 && strings.ContainsRune(`'"`, rune(line[end])) {
		return ds, line, true // E'ND': a delimiter mixing quoted and unquoted parts is not modelled
	}
	if end < 0 {
		end = len(line)
	}
	if end == 0 {
		return ds, line, true // no delimiter word at all
	}
	return append(ds, heredoc{line[:end], tabs}), line[end:], false
}
