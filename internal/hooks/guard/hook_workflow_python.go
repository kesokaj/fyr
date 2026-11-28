package guard

import "strings"

func scanWorkflowAgentCalls(src string) ([]workflowCallSpan, string) {
	var spans []workflowCallSpan
	deny := scanPythonSourceRange(src, 0, len(src), false, &spans)
	return spans, deny
}

func scanPythonSourceRange(src string, start, end int, inFString bool, out *[]workflowCallSpan) string {
	for i := start; i < end; {
		next, deny := stepPythonSourceAt(src, i, end, inFString, out)
		if deny != "" {
			return deny
		}
		i = next
	}
	return ""
}

func stepPythonSourceAt(src string, i, end int, inFString bool, out *[]workflowCallSpan) (int, string) {
	if src[i] == '#' {
		return skipPythonLineComment(src, i, end), ""
	}
	if strEnd, isF, deny := tryScanPythonString(src, i, end); strEnd > i {
		return finishPythonStringStep(src, i, strEnd, isF, deny, out)
	}
	if isIdentStart(src[i]) {
		return stepPythonIdentAt(src, i, end, inFString, out)
	}
	return i + 1, ""
}

func skipPythonLineComment(src string, i, end int) int {
	for i < end && src[i] != '\n' {
		i++
	}
	return i
}

func finishPythonStringStep(src string, start, strEnd int, isF bool, deny string, out *[]workflowCallSpan) (int, string) {
	if deny != "" {
		return strEnd, deny
	}
	if isF {
		if d := scanFStringInterpolations(src, start, strEnd, out); d != "" {
			return strEnd, d
		}
	}
	return strEnd, ""
}

func stepPythonIdentAt(src string, i, end int, inFString bool, out *[]workflowCallSpan) (int, string) {
	idEnd := min(scanPythonIdentEnd(src, i), end)
	if src[i:idEnd] == "agent" {
		return handleAgentIdent(src, i, idEnd, end, inFString, out)
	}
	return idEnd, ""
}

func tryScanPythonString(src string, i, end int) (int, bool, string) {
	qStart := min(skipStringPrefix(src, i), end)
	if qStart >= end || (src[qStart] != '\'' && src[qStart] != '"') {
		return i, false, ""
	}
	isF := strings.ContainsAny(src[i:qStart], "fF")
	strEnd, closed := scanPythonStringSpan(src, qStart, end, isF)
	if !closed && isF {
		return strEnd, true, "[MODEL_OVERRIDE] run_workflow refuses unterminated f-string."
	}
	return strEnd, isF, ""
}

func scanFStringInterpolations(src string, strStart, strEnd int, out *[]workflowCallSpan) string {
	qStart := skipStringPrefix(src, strStart)
	delimLen := 1
	if qStart+2 < strEnd && src[qStart+1] == src[qStart] && src[qStart+2] == src[qStart] {
		delimLen = 3
	}
	bodyEnd := strEnd - delimLen
	for i := qStart + delimLen; i < bodyEnd; {
		next, deny := stepFStringChar(src, i, bodyEnd, out)
		if deny != "" {
			return deny
		}
		i = next
	}
	return ""
}

func stepFStringChar(src string, i, bodyEnd int, out *[]workflowCallSpan) (int, string) {
	if src[i] == '\\' && i+1 < bodyEnd {
		if src[i+1] == '{' || src[i+1] == '}' {
			return i + 1, ""
		}
		return i + 2, ""
	}
	if src[i] == '{' {
		return stepFStringOpenBrace(src, i, bodyEnd, out)
	}
	if src[i] == '}' && i+1 < bodyEnd && src[i+1] == '}' {
		return i + 2, ""
	}
	return i + 1, ""
}

func stepFStringOpenBrace(src string, i, bodyEnd int, out *[]workflowCallSpan) (int, string) {
	if i+1 < bodyEnd && src[i+1] == '{' {
		return i + 2, ""
	}
	closeBrace := findFStringExprEnd(src, i+1, bodyEnd)
	if closeBrace < 0 {
		return bodyEnd, "[MODEL_OVERRIDE] run_workflow refuses malformed f-string interpolation."
	}
	if deny := scanPythonSourceRange(src, i+1, closeBrace, true, out); deny != "" {
		return closeBrace + 1, deny
	}
	return closeBrace + 1, ""
}

func findFStringExprEnd(src string, start, bodyEnd int) int {
	braceDepth, parenDepth, bracketDepth := 1, 0, 0
	for i := start; i < bodyEnd; {
		if next, ok := skipPythonTriviaBounded(src, i, bodyEnd); ok {
			i = next
			continue
		}
		switch src[i] {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '{':
			braceDepth++
		case '}':
			braceDepth--
			if braceDepth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

func handleAgentIdent(src string, idStart, idEnd, limit int, inFString bool, out *[]workflowCallSpan) (int, string) {
	j := min(SkipPythonWSAndComments(src, idEnd), limit)
	if j < limit && src[j] == '(' {
		closeIdx := findMatchingParenBounded(src, j, limit)
		if closeIdx < 0 {
			return idEnd, "[MODEL_OVERRIDE] run_workflow refuses unclosed agent(...) call."
		}
		*out = append(*out, workflowCallSpan{start: j + 1, end: closeIdx, inFString: inFString})
		return j + 1, ""
	}
	parens, afterParens := countWrappingParens(src, idStart, j, limit)
	if parens > 0 {
		if afterParens < limit && src[afterParens] == '(' {
			closeIdx := findMatchingParenBounded(src, afterParens, limit)
			if closeIdx < 0 {
				return idEnd, "[MODEL_OVERRIDE] run_workflow refuses unclosed (agent)(...) call."
			}
			*out = append(*out, workflowCallSpan{start: afterParens + 1, end: closeIdx, inFString: inFString})
			return afterParens + 1, ""
		}
		return idEnd, "[MODEL_OVERRIDE] run_workflow forbids parenthesized or aliased reference to agent."
	}
	if j < limit && src[j] == '=' && (j+1 >= limit || src[j+1] != '=') {
		return idEnd, ""
	}
	if isAgentBindingSite(src, idStart) {
		return idEnd, ""
	}
	return idEnd, "[MODEL_OVERRIDE] run_workflow forbids aliasing or indirect reference to agent; call agent(...) directly."
}

func countWrappingParens(src string, idStart, afterIdent, limit int) (int, int) {
	left := idStart
	right := afterIdent
	count := 0
	for right < limit && src[right] == ')' {
		l := prevNonWSOrComment(src, left)
		if l < 0 || src[l] != '(' {
			break
		}
		count++
		left = l
		right = min(SkipPythonWSAndComments(src, right+1), limit)
	}
	return count, right
}

func prevNonWSOrComment(src string, pos int) int {
	i := pos - 1
	for i >= 0 && (src[i] == ' ' || src[i] == '\t' || src[i] == '\r' || src[i] == '\n') {
		i--
	}
	return i
}

func isAgentBindingSite(src string, idStart int) bool {
	end := prevNonWSOrComment(src, idStart)
	if end < 0 || !isIdentChar(src[end]) {
		return false
	}
	start := end
	for start > 0 && isIdentChar(src[start-1]) {
		start--
	}
	prev := src[start : end+1]
	return prev == "for" || prev == "as"
}

func splitPythonCallArgs(src string, start, end int) []workflowArg {
	var args []workflowArg
	depth, itemStart := 0, start
	for i := start; i < end; {
		if next, ok := skipPythonTrivia(src, i); ok {
			i = next
			continue
		}
		switch src[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				args = append(args, parseWorkflowArg(src, itemStart, i))
				itemStart = i + 1
			}
		}
		i++
	}
	if s, e := trimPythonArgBounds(src, itemStart, end); s < e {
		args = append(args, parseWorkflowArg(src, s, e))
	}
	return args
}

func parseWorkflowArg(src string, start, end int) workflowArg {
	s, e := trimPythonArgBounds(src, start, end)
	eqIdx := findTopLevelEq(src, s, e)
	if eqIdx < 0 {
		return workflowArg{raw: src[s:e], valStart: s, valEnd: e}
	}
	kwEnd := scanPythonIdentEnd(src, s)
	vs, ve := trimPythonArgBounds(src, eqIdx+1, e)
	return workflowArg{kwName: src[s:kwEnd], raw: src[vs:ve], valStart: vs, valEnd: ve}
}

func findTopLevelEq(src string, start, end int) int {
	depth := 0
	for i := start; i < end; {
		if next, ok := skipPythonTrivia(src, i); ok {
			i = next
			continue
		}
		if src[i] == '=' && depth == 0 {
			if isValidKwArgEq(src, start, i, end) {
				return i
			}
			return -1
		}
		depth = adjustPythonBracketDepth(src[i], depth)
		i++
	}
	return -1
}

func isValidKwArgEq(src string, start, eqIdx, end int) bool {
	if eqIdx+1 < end && src[eqIdx+1] == '=' {
		return false
	}
	if start >= eqIdx || !isIdentStart(src[start]) {
		return false
	}
	kwEnd := scanPythonIdentEnd(src, start)
	return SkipPythonWSAndComments(src, kwEnd) >= eqIdx
}

func adjustPythonBracketDepth(ch byte, depth int) int {
	switch ch {
	case '(', '[', '{':
		return depth + 1
	case ')', ']', '}':
		if depth > 0 {
			return depth - 1
		}
	}
	return depth
}
