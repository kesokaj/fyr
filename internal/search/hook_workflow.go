package search

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

type workflowArg struct {
	kwName   string
	raw      string
	valStart int
	valEnd   int
}

type workflowPatch struct {
	start int
	end   int
	repl  string
}

type workflowCallSpan struct {
	start     int
	end       int
	inFString bool
}

func handlePreToolWorkflow(home, dir string, in HookInput, out io.Writer) error {
	script, inlineKey, isInline, loadDeny := loadWorkflowScriptSource(dir, in.ToolCall.Args)
	if loadDeny != "" {
		return writeDecision(out, "deny", loadDeny)
	}
	if strings.TrimSpace(script) == "" {
		return writeDecision(out, "allow", "")
	}
	denyReason, patches := inspectWorkflowScript(home, script, in.ModelName, isInline)
	if denyReason != "" {
		return writeDecision(out, "deny", denyReason)
	}
	if len(patches) > 0 && isInline {
		rewritten := applyWorkflowPatches(script, patches)
		cloned := maps.Clone(in.ToolCall.Args)
		if cloned == nil {
			cloned = map[string]any{}
		}
		cloned[inlineKey] = rewritten
		reason := fmt.Sprintf("[AUTO_MODEL_UPGRADE] upgraded guarded run_workflow subagent(s) from %s to model=\"pro\"", formatModelTag(home, in.ModelName))
		return writeDecisionWithOverwrite(out, "allow", reason, cloned)
	}
	return writeDecision(out, "allow", "")
}

func loadWorkflowScriptSource(dir string, args map[string]any) (string, string, bool, string) {
	for _, key := range []string{"Script", "script"} {
		if s, ok := args[key].(string); ok && strings.TrimSpace(s) != "" {
			return s, key, true, ""
		}
	}
	path := extractStringArg(args, "ScriptPath", "scriptPath", "script_path")
	if path == "" {
		return "", "", false, ""
	}
	if strings.TrimSpace(dir) == "" {
		return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses ScriptPath outside workspace directory."
	}
	cleanDir, err := filepath.Abs(dir)
	if err != nil {
		return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses unreadable ScriptPath."
	}
	rel, deny := resolveContainedRelScriptPath(cleanDir, path)
	if deny != "" {
		return "", "", false, deny
	}
	return readContainedWorkflowScript(cleanDir, rel)
}

func resolveContainedRelScriptPath(cleanDir, path string) (string, string) {
	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(cleanDir, absPath)
	}
	absPath = filepath.Clean(absPath)
	rel, err := filepath.Rel(cleanDir, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", "[MODEL_OVERRIDE] run_workflow refuses ScriptPath outside workspace directory."
	}
	if hasSymlinkRelSegment(cleanDir, rel) {
		return "", "[MODEL_OVERRIDE] run_workflow refuses symlink ScriptPath; pass a regular workspace file."
	}
	return rel, ""
}

func hasSymlinkRelSegment(baseDir, rel string) bool {
	cur := baseDir
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		if fi, err := os.Lstat(cur); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

func readContainedWorkflowScript(cleanDir, rel string) (string, string, bool, string) {
	root, err := os.OpenRoot(cleanDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", false, ""
		}
		return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses unreadable ScriptPath."
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", false, ""
		}
		return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses symlink or unreadable ScriptPath."
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", "", false, "[MODEL_OVERRIDE] run_workflow failed to stat ScriptPath."
	}
	if !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
		return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses non-regular or > 1 MiB ScriptPath."
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return "", "", false, "[MODEL_OVERRIDE] run_workflow failed to read ScriptPath."
	}
	if len(data) > 1<<20 {
		return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses non-regular or > 1 MiB ScriptPath."
	}
	return string(data), "", false, ""
}

func inspectWorkflowScript(home, src, modelName string, isInline bool) (string, []workflowPatch) {
	fast := needsGuardedProUpgrade(modelName)
	spans, scanDeny := scanWorkflowAgentCalls(src)
	if scanDeny != "" {
		return scanDeny, nil
	}
	var (
		proDeny string
		patches []workflowPatch
	)
	for _, sp := range spans {
		denyOverride, denyPro, patch := inspectSingleAgentCall(home, src, [2]int{sp.start, sp.end}, modelName, fast, isInline)
		if denyOverride != "" {
			return denyOverride, nil
		}
		if patch != nil && sp.inFString {
			return fmt.Sprintf("[AUTO_SUBAGENT_PRO] fast session (%s) requires explicit model=\"pro\" (not \"inherit\") inside f-string run_workflow call.", formatModelTag(home, modelName)), nil
		}
		if denyPro != "" && proDeny == "" {
			proDeny = denyPro
		}
		if patch != nil {
			patches = append(patches, *patch)
		}
	}
	if proDeny != "" {
		return proDeny, nil
	}
	return "", patches
}

func inspectSingleAgentCall(home, src string, sp [2]int, modelName string, fast, isInline bool) (string, string, *workflowPatch) {
	args := splitPythonCallArgs(src, sp[0], sp[1])
	if hasPythonKwargsSplat(args) {
		return "[MODEL_OVERRIDE] run_workflow subagent forbids *args/**kwargs splat that could override type_name or model.", "", nil
	}
	typeArg := findWorkflowArg(args, "type_name", 3)
	mArg := findWorkflowArg(args, "model", 5)
	if typeArg == nil || isPythonNone(typeArg.raw) {
		return "", "", nil
	}
	typeName, typeIsLit := unquotePythonLiteral(typeArg.raw)
	typeName = strings.TrimSpace(typeName)
	if !typeIsLit {
		return inspectNonLiteralTypeAgent(home, typeArg, mArg, args, modelName, fast, isInline)
	}
	if !isModelGuardedSubagent(typeName) {
		return "", "", nil
	}
	return inspectGuardedModelArg(home, typeName, mArg, modelName, fast, isInline)
}

func inspectGuardedModelArg(home, typeName string, mArg *workflowArg, modelName string, fast, isInline bool) (string, string, *workflowPatch) {
	if mArg == nil || isPythonNone(mArg.raw) {
		if fast {
			return "", fmt.Sprintf("[AUTO_SUBAGENT_PRO] fast session (%s) forbids omitted model on guarded run_workflow subagent %q; pass model=\"pro\".", formatModelTag(home, modelName), typeName), nil
		}
		return "", "", nil
	}
	mVal, isLit := unquotePythonLiteral(mArg.raw)
	if !isLit {
		safeExpr := collapseOneLine(printable(mArg.raw), 64)
		return fmt.Sprintf("[MODEL_OVERRIDE] run_workflow subagent %q forbids non-literal model=%s; omit model or pass \"inherit\" (or \"pro\" on fast turns).", typeName, safeExpr), "", nil
	}
	if isBlockedWorkflowModel(mVal) {
		safeVal := collapseOneLine(printable(mVal), 64)
		return fmt.Sprintf("[MODEL_OVERRIDE] run_workflow subagent %q forbids model=%q; omit model or pass \"inherit\" (or \"pro\" on fast turns).", typeName, safeVal), "", nil
	}
	if strings.EqualFold(strings.TrimSpace(mVal), "inherit") && fast {
		if !isInline {
			return "", fmt.Sprintf("[AUTO_SUBAGENT_PRO] fast session (%s) requires explicit model=\"pro\" (not \"inherit\") inside ScriptPath for guarded subagent %q.", formatModelTag(home, modelName), typeName), nil
		}
		return "", "", &workflowPatch{start: mArg.valStart, end: mArg.valEnd, repl: `"pro"`}
	}
	return "", "", nil
}

func inspectNonLiteralTypeAgent(home string, typeArg, mArg *workflowArg, args []workflowArg, modelName string, fast, isInline bool) (string, string, *workflowPatch) {
	if hasPythonKwargsSplat(args) {
		safeType := collapseOneLine(printable(typeArg.raw), 64)
		return fmt.Sprintf("[MODEL_OVERRIDE] run_workflow subagent type_name=%s forbids **kwargs splat.", safeType), "", nil
	}
	if mArg == nil || isPythonNone(mArg.raw) {
		if fast {
			safeType := collapseOneLine(printable(typeArg.raw), 64)
			if !isInline {
				return "", fmt.Sprintf("[AUTO_SUBAGENT_PRO] fast session (%s) requires explicit model=\"pro\" inside ScriptPath for dynamic subagent %s.", formatModelTag(home, modelName), safeType), nil
			}
			return fmt.Sprintf("[MODEL_OVERRIDE] fast session (%s) requires explicit model=\"pro\" on inline run_workflow dynamic subagent %s.", formatModelTag(home, modelName), safeType), "", nil
		}
		return "", "", nil
	}
	mVal, isLit := unquotePythonLiteral(mArg.raw)
	if !isLit {
		safeType := collapseOneLine(printable(typeArg.raw), 64)
		safeExpr := collapseOneLine(printable(mArg.raw), 64)
		return fmt.Sprintf("[MODEL_OVERRIDE] run_workflow subagent type_name=%s forbids non-literal model=%s; pass literal \"pro\" or \"inherit\".", safeType, safeExpr), "", nil
	}
	if isBlockedWorkflowModel(mVal) {
		safeType := collapseOneLine(printable(typeArg.raw), 64)
		safeVal := collapseOneLine(printable(mVal), 64)
		return fmt.Sprintf("[MODEL_OVERRIDE] run_workflow subagent type_name=%s forbids model=%q; pass \"pro\" or \"inherit\".", safeType, safeVal), "", nil
	}
	if strings.EqualFold(strings.TrimSpace(mVal), "inherit") && fast {
		if !isInline {
			return "", fmt.Sprintf("[AUTO_SUBAGENT_PRO] fast session (%s) requires explicit model=\"pro\" (not \"inherit\") inside ScriptPath for dynamic subagent %s.", formatModelTag(home, modelName), collapseOneLine(printable(typeArg.raw), 64)), nil
		}
		return "", "", &workflowPatch{start: mArg.valStart, end: mArg.valEnd, repl: `"pro"`}
	}
	return "", "", nil
}

func hasPythonKwargsSplat(args []workflowArg) bool {
	for _, a := range args {
		if a.kwName == "" && strings.HasPrefix(strings.TrimSpace(a.raw), "*") {
			return true
		}
	}
	return false
}

func isBlockedWorkflowModel(val string) bool {
	v := strings.ToLower(strings.TrimSpace(val))
	if v == "" || v == "inherit" || v == "pro" {
		return false
	}
	return true
}

func findWorkflowArg(args []workflowArg, kw string, pos int) *workflowArg {
	for i := range args {
		if args[i].kwName == kw {
			return &args[i]
		}
	}
	for i := range args {
		if args[i].kwName == "" && i == pos {
			return &args[i]
		}
	}
	return nil
}

func applyWorkflowPatches(src string, patches []workflowPatch) string {
	out := src
	for i := len(patches) - 1; i >= 0; i-- {
		p := patches[i]
		out = out[:p.start] + p.repl + out[p.end:]
	}
	return out
}

func findPythonAgentCalls(src string) [][2]int {
	spans, _ := scanWorkflowAgentCalls(src)
	out := make([][2]int, 0, len(spans))
	for _, sp := range spans {
		out = append(out, [2]int{sp.start, sp.end})
	}
	return out
}

func scanWorkflowAgentCalls(src string) ([]workflowCallSpan, string) {
	var spans []workflowCallSpan
	deny := scanPythonSourceRange(src, 0, len(src), false, &spans)
	return spans, deny
}

func scanPythonSourceRange(src string, start, end int, inFString bool, out *[]workflowCallSpan) string {
	for i := start; i < end; {
		if src[i] == '#' {
			for i < end && src[i] != '\n' {
				i++
			}
			continue
		}
		if strEnd, isF, deny := tryScanPythonString(src, i, end); strEnd > i {
			if deny != "" {
				return deny
			}
			if isF {
				if d := scanFStringInterpolations(src, i, strEnd, out); d != "" {
					return d
				}
			}
			i = strEnd
			continue
		}
		if isIdentStart(src[i]) {
			idEnd := min(scanPythonIdentEnd(src, i), end)
			if src[i:idEnd] == "agent" {
				next, deny := handleAgentIdent(src, i, idEnd, end, inFString, out)
				if deny != "" {
					return deny
				}
				i = next
				continue
			}
			i = idEnd
			continue
		}
		i++
	}
	return ""
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
		if src[i] == '\\' && i+1 < bodyEnd {
			i += 2
			continue
		}
		if src[i] == '{' {
			if i+1 < bodyEnd && src[i+1] == '{' {
				i += 2
				continue
			}
			closeBrace := findFStringExprEnd(src, i+1, bodyEnd)
			if closeBrace < 0 {
				return "[MODEL_OVERRIDE] run_workflow refuses malformed f-string interpolation."
			}
			if deny := scanPythonSourceRange(src, i+1, closeBrace, true, out); deny != "" {
				return deny
			}
			i = closeBrace + 1
			continue
		}
		if src[i] == '}' && i+1 < bodyEnd && src[i+1] == '}' {
			i += 2
			continue
		}
		i++
	}
	return ""
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
	j := min(skipPythonWSAndComments(src, idEnd), limit)
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
		right = min(skipPythonWSAndComments(src, right+1), limit)
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
		switch src[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case '=':
			if depth == 0 {
				if i+1 < end && src[i+1] == '=' {
					return -1
				}
				if start < i && isIdentStart(src[start]) {
					kwEnd := scanPythonIdentEnd(src, start)
					if skipPythonWSAndComments(src, kwEnd) >= i {
						return i
					}
				}
				return -1
			}
		}
		i++
	}
	return -1
}

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

func precededByAttrOrIdent(src string, i int) bool {
	if i == 0 {
		return false
	}
	return src[i-1] == '.' || isIdentChar(src[i-1])
}
