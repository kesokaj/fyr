package search

import "strings"

func skipPythonTrivia(src string, i int) (int, bool) {
	return skipPythonTriviaBounded(src, i, len(src))
}

func skipPythonTriviaBounded(src string, i, limit int) (int, bool) {
	if i < limit && src[i] == '#' {
		for i < limit && src[i] != '\n' {
			i++
		}
		return i, true
	}
	qStart := min(skipStringPrefix(src, i), limit)
	if qStart < limit && (src[qStart] == '\'' || src[qStart] == '"') {
		isF := strings.ContainsAny(src[i:qStart], "fF")
		end, _ := scanPythonStringSpan(src, qStart, limit, isF)
		return end, true
	}
	return i, false
}

func scanPythonStringSpan(src string, qStart, limit int, isF bool) (int, bool) {
	q := src[qStart]
	if qStart+2 < limit && src[qStart+1] == q && src[qStart+2] == q {
		return scanTripleQuotedPythonString(src, qStart, limit, isF)
	}
	return scanSingleQuotedPythonString(src, qStart, limit, isF)
}

func scanTripleQuotedPythonString(src string, qStart, limit int, isF bool) (int, bool) {
	delim := src[qStart : qStart+3]
	for i := qStart + 3; i+2 < limit; {
		if src[i] == '\\' {
			i += 2
			continue
		}
		if isF && src[i] == '{' {
			if i+1 < limit && src[i+1] == '{' {
				i += 2
				continue
			}
			if closeBrace := findFStringExprEnd(src, i+1, limit); closeBrace >= 0 {
				i = closeBrace + 1
				continue
			}
			return limit, false
		}
		if src[i:i+3] == delim {
			return i + 3, true
		}
		i++
	}
	return limit, false
}

func scanSingleQuotedPythonString(src string, qStart, limit int, isF bool) (int, bool) {
	q := src[qStart]
	for i := qStart + 1; i < limit; {
		if src[i] == '\\' {
			i += 2
			continue
		}
		if isF && src[i] == '{' {
			if i+1 < limit && src[i+1] == '{' {
				i += 2
				continue
			}
			if closeBrace := findFStringExprEnd(src, i+1, limit); closeBrace >= 0 {
				i = closeBrace + 1
				continue
			}
			return limit, false
		}
		if src[i] == q {
			return i + 1, true
		}
		i++
	}
	return limit, false
}

func findMatchingParen(src string, openIdx int) int {
	return findMatchingParenBounded(src, openIdx, len(src))
}

func findMatchingParenBounded(src string, openIdx, limit int) int {
	depth := 0
	for i := openIdx; i < limit; {
		if next, ok := skipPythonTriviaBounded(src, i, limit); ok {
			i = next
			continue
		}
		if src[i] == '(' {
			depth++
		} else if src[i] == ')' {
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

func skipStringPrefix(src string, i int) int {
	j := i
	for j < len(src) && j-i < 2 {
		switch src[j] {
		case 'r', 'R', 'f', 'F', 'u', 'U', 'b', 'B':
			j++
		default:
			return j
		}
	}
	return j
}

func unquotePythonLiteral(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 && (s[0] == 'r' || s[0] == 'R' || s[0] == 'u' || s[0] == 'U') && (s[1] == '\'' || s[1] == '"') {
		s = s[1:]
	}
	if len(s) < 2 {
		return "", false
	}
	q := s[0]
	if (q != '"' && q != '\'') || s[len(s)-1] != q {
		return "", false
	}
	for i := 1; i < len(s)-1; i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == q {
			return "", false
		}
	}
	return s[1 : len(s)-1], true
}

func isPythonNone(raw string) bool { return strings.TrimSpace(raw) == "None" }
func isIdentStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}
func isIdentChar(b byte) bool { return isIdentStart(b) || (b >= '0' && b <= '9') }
func scanPythonIdentEnd(src string, i int) int {
	for i < len(src) && isIdentChar(src[i]) {
		i++
	}
	return i
}
func skipPythonWSAndComments(src string, i int) int {
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\r', '\n':
			i++
		case '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case '\\':
			if i+1 < len(src) && src[i+1] == '\n' {
				i += 2
				continue
			}
			if i+2 < len(src) && src[i+1] == '\r' && src[i+2] == '\n' {
				i += 3
				continue
			}
			return i
		default:
			return i
		}
	}
	return i
}

func trimPythonArgBounds(src string, start, end int) (int, int) {
	s := min(skipPythonWSAndComments(src, start), end)
	last := s
	for i := s; i < end; {
		if next, ok := skipPythonTrivia(src, i); ok {
			if src[i] != '#' {
				last = min(next, end)
			}
			i = next
			continue
		}
		if src[i] == ' ' || src[i] == '\t' || src[i] == '\r' || src[i] == '\n' {
			i++
			continue
		}
		i++
		last = i
	}
	return s, last
}
