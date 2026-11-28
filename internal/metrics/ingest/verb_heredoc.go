package ingest

import "strings"

// heredoc is one here-document a line opens: its delimiter word, and whether
// it was opened with <<-, the only form whose terminator may be led by tabs
// (SD56 of the 2026-10-27 audit).
type heredoc struct {
	word string
	tabs bool
}

// StripHeredocs removes every here-document body from cmd, so that the
// lines of a file written with `cat <<EOF` never become commands.
func StripHeredocs(cmd string) string {
	lines := strings.Split(cmd, "\n")
	var out []string
	var pending []heredoc
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		out = append(out, line)
		ds, unparsed := heredocDelims(line)
		if unparsed {
			break
		}
		pending = append(pending, ds...)
		for len(pending) > 0 {
			i = skipHeredocBody(lines, i, pending[0])
			pending = pending[1:]
		}
	}
	return strings.Join(out, "\n")
}

func skipHeredocBody(lines []string, i int, d heredoc) int {
	for i+1 < len(lines) {
		i++
		l := lines[i]
		if d.tabs {
			l = strings.TrimLeft(l, "\t")
		}
		if l == d.word {
			return i
		}
	}
	return i
}

// heredocDelims returns the delimiters of the here-documents line opens.
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
		line = strings.TrimLeft(strings.TrimPrefix(line, "-"), " \t")
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

func quotedDelim(ds []heredoc, line string, tabs bool) ([]heredoc, string, bool) {
	q := line[:1]
	k := strings.Index(line[1:], q)
	if k <= 0 {
		return ds, line, true
	}
	ds = append(ds, heredoc{line[1 : 1+k], tabs})
	line = line[k+2:]
	if line != "" && !strings.ContainsRune(" \t;&|<>()`", rune(line[0])) {
		return ds, line, true
	}
	return ds, line, false
}

func unquotedDelim(ds []heredoc, line string, tabs bool) ([]heredoc, string, bool) {
	line = strings.TrimPrefix(line, "\\")
	end := strings.IndexFunc(line, func(r rune) bool {
		return strings.ContainsRune(" \t;&|<>()`'\"", r)
	})
	if end > 0 && strings.ContainsRune(`'"`, rune(line[end])) {
		return ds, line, true
	}
	if end < 0 {
		end = len(line)
	}
	if end == 0 {
		return ds, line, true
	}
	return append(ds, heredoc{line[:end], tabs}), line[end:], false
}
