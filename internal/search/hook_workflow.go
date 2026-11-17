package search

import (
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
	if !filepath.IsAbs(path) && dir != "" {
		path = filepath.Join(dir, path)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", false, ""
		}
		return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses unreadable ScriptPath."
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses symlink ScriptPath; pass a regular workspace file."
	}
	if !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
		return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses non-regular or > 1 MiB ScriptPath."
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false, "[MODEL_OVERRIDE] run_workflow failed to read ScriptPath."
	}
	return string(data), "", false, ""
}

func inspectWorkflowScript(home, src, modelName string, isInline bool) (string, []workflowPatch) {
	fast := isFastExecutionModel(modelName)
	spans := findPythonAgentCalls(src)
	var (
		proDeny string
		patches []workflowPatch
	)
	for _, sp := range spans {
		denyOverride, denyPro, patch := inspectSingleAgentCall(home, src, sp, modelName, fast, isInline)
		if denyOverride != "" {
			return denyOverride, nil
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
	if hasPythonKwargsSplat(args) {
		return fmt.Sprintf("[MODEL_OVERRIDE] run_workflow subagent %q forbids **kwargs splat that could override model.", typeName), "", nil
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
		if a.kwName == "" && strings.HasPrefix(strings.TrimSpace(a.raw), "**") {
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
	var spans [][2]int
	for i := 0; i < len(src); {
		if next, ok := skipPythonTrivia(src, i); ok {
			i = next
			continue
		}
		if isIdentStart(src[i]) {
			end := scanPythonIdentEnd(src, i)
			ident := src[i:end]
			j := skipPythonWSAndComments(src, end)
			if ident == "agent" && !precededByAttrOrIdent(src, i) && j < len(src) && src[j] == '(' {
				if closeIdx := findMatchingParen(src, j); closeIdx > j {
					spans = append(spans, [2]int{j + 1, closeIdx})
					i = j + 1
					continue
				}
			}
			i = end
			continue
		}
		i++
	}
	return spans
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
	if src[i] == '#' {
		for i < len(src) && src[i] != '\n' {
			i++
		}
		return i, true
	}
	qStart := skipStringPrefix(src, i)
	if qStart < len(src) && (src[qStart] == '\'' || src[qStart] == '"') {
		return skipPythonStringEnd(src, qStart), true
	}
	return i, false
}

func skipPythonStringEnd(src string, i int) int {
	q := src[i]
	if i+2 < len(src) && src[i+1] == q && src[i+2] == q {
		delim := src[i : i+3]
		i += 3
		for i+2 < len(src) {
			if src[i] == '\\' {
				i += 2
				continue
			}
			if src[i:i+3] == delim {
				return i + 3
			}
			i++
		}
		return len(src)
	}
	i++
	for i < len(src) {
		if src[i] == '\\' {
			i += 2
			continue
		}
		if src[i] == q {
			return i + 1
		}
		i++
	}
	return len(src)
}

func findMatchingParen(src string, openIdx int) int {
	depth := 0
	for i := openIdx; i < len(src); {
		if next, ok := skipPythonTrivia(src, i); ok {
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
