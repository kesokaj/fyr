package guard

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/vet"
)

// WorkflowInput carries home, dir, and tool call args for run_workflow guarding.
type WorkflowInput struct {
	Home     string
	Dir      string
	ToolArgs map[string]any
}

// WorkflowDeps supplies model-policy and decision-writer callbacks for run_workflow guarding.
type WorkflowDeps struct {
	EffectiveSessionModel  func() string
	NeedsGuardedProUpgrade func(home, modelName string) bool
	IsModelGuardedSubagent func(name string) bool
	FormatModelTag         func(home, raw string) string
	SanitizePrintable      func(s string) string
	WriteDecision          func(out io.Writer, decision, reason string) error
	WriteDecisionOverwrite func(out io.Writer, decision, reason string, overwrite map[string]any)
}

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

func validateWorkflowPolicyDeps(deps WorkflowDeps) string {
	switch {
	case deps.NeedsGuardedProUpgrade == nil:
		return "[MODEL_OVERRIDE] run_workflow guard misconfigured: missing NeedsGuardedProUpgrade callback."
	case deps.IsModelGuardedSubagent == nil:
		return "[MODEL_OVERRIDE] run_workflow guard misconfigured: missing IsModelGuardedSubagent callback."
	default:
		return ""
	}
}

func validateWorkflowDeps(deps WorkflowDeps) string {
	if deny := validateWorkflowPolicyDeps(deps); deny != "" {
		return deny
	}
	if deps.WriteDecisionOverwrite == nil {
		return "[MODEL_OVERRIDE] run_workflow guard misconfigured: missing WriteDecisionOverwrite callback."
	}
	return ""
}

// HandlePreToolWorkflow validates and optionally rewrites guarded agent() calls in run_workflow scripts.
func HandlePreToolWorkflow(in WorkflowInput, deps WorkflowDeps, out io.Writer) error {
	if deps.WriteDecision == nil {
		return errors.New("[MODEL_OVERRIDE] run_workflow guard misconfigured: missing WriteDecision callback")
	}
	if deny := validateWorkflowDeps(deps); deny != "" {
		return deps.WriteDecision(out, "deny", deny)
	}
	script, inlineKey, isInline, loadDeny := loadWorkflowScriptSource(in.Home, in.Dir, in.ToolArgs)
	if loadDeny != "" {
		return deps.WriteDecision(out, "deny", loadDeny)
	}
	if strings.TrimSpace(script) == "" {
		return deps.WriteDecision(out, "allow", "")
	}
	sessionModel := ""
	if deps.EffectiveSessionModel != nil {
		sessionModel = deps.EffectiveSessionModel()
	}
	denyReason, patches := inspectWorkflowScript(in.Home, script, sessionModel, isInline, deps)
	if denyReason != "" {
		return deps.WriteDecision(out, "deny", denyReason)
	}
	if len(patches) > 0 && isInline {
		rewritten := applyWorkflowPatches(script, patches)
		cloned := maps.Clone(in.ToolArgs)
		if cloned == nil {
			cloned = map[string]any{}
		}
		cloned[inlineKey] = rewritten
		reason := fmt.Sprintf("[AUTO_MODEL_UPGRADE] upgraded guarded run_workflow subagent(s) from %s to model=\"pro\"", formatWorkflowModelTag(deps, in.Home, sessionModel))
		deps.WriteDecisionOverwrite(out, "allow", reason, cloned)
		return nil
	}
	return deps.WriteDecision(out, "allow", "")
}

func formatWorkflowModelTag(deps WorkflowDeps, home, model string) string {
	if deps.FormatModelTag != nil {
		return deps.FormatModelTag(home, model)
	}
	return strings.TrimSpace(model)
}

func sanitizeWorkflowText(deps WorkflowDeps, s string) string {
	if deps.SanitizePrintable != nil {
		return deps.SanitizePrintable(s)
	}
	return s
}

func loadWorkflowScriptSource(home, dir string, args map[string]any) (string, string, bool, string) {
	for _, key := range []string{"Script", "script"} {
		if s, ok := args[key].(string); ok && strings.TrimSpace(s) != "" {
			return s, key, true, ""
		}
	}
	path := ExtractStringArg(args, "ScriptPath", "scriptPath", "script_path")
	if path == "" {
		return "", "", false, ""
	}
	rootDir, rel, isInstalled, deny := resolveAllowedWorkflowRoot(home, dir, path)
	if deny != "" {
		return "", "", false, deny
	}
	return readContainedWorkflowScript(rootDir, rel, isInstalled)
}

func resolveAllowedWorkflowRoot(home, dir, path string) (string, string, bool, string) {
	if strings.TrimSpace(dir) != "" {
		cleanDir, err := filepath.Abs(dir)
		if err != nil {
			return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses unreadable ScriptPath."
		}
		if rel, contained, deny := matchContainedWorkspaceScript(cleanDir, path); contained || deny != "" {
			return cleanDir, rel, false, deny
		}
	}
	if !filepath.IsAbs(path) {
		return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses ScriptPath outside workspace directory."
	}
	if strings.TrimSpace(home) != "" {
		if wfDir, rel, matched, deny := matchInstalledWorkflowScript(home, path); matched || deny != "" {
			return wfDir, rel, true, deny
		}
	}
	return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses ScriptPath outside workspace or installed workflows directory."
}

func matchContainedWorkspaceScript(cleanDir, path string) (string, bool, string) {
	if hasDotDotSegment(path) {
		return "", true, "[MODEL_OVERRIDE] run_workflow refuses ScriptPath with '..' segment."
	}
	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(cleanDir, absPath)
	}
	rel, err := filepath.Rel(cleanDir, filepath.Clean(absPath))
	if err != nil || !isContainedRelPath(rel) {
		return "", false, ""
	}
	if hasSymlinkRelSegment(cleanDir, rel) {
		return "", true, "[MODEL_OVERRIDE] run_workflow refuses symlink ScriptPath; pass a regular workspace file."
	}
	return rel, true, ""
}

func matchInstalledWorkflowScript(home, path string) (string, string, bool, string) {
	if hasDotDotSegment(path) {
		return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses ScriptPath with '..' segment."
	}
	cleanHome, err := filepath.Abs(home)
	if err != nil {
		return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses unreadable ScriptPath."
	}
	wfRelFromHome := filepath.Join(".gemini", "config", "workflows")
	wfDir := filepath.Join(cleanHome, wfRelFromHome)
	rel, err := filepath.Rel(wfDir, filepath.Clean(path))
	if err != nil || !isContainedRelPath(rel) {
		return "", "", false, ""
	}
	if hasSymlinkRelSegment(cleanHome, wfRelFromHome) || !isRegularDirNoSymlink(wfDir) {
		return "", "", true, "[MODEL_OVERRIDE] run_workflow refuses symlink or invalid installed workflows directory."
	}
	if hasSymlinkRelSegment(wfDir, rel) {
		return "", "", true, "[MODEL_OVERRIDE] run_workflow refuses symlink ScriptPath; pass a regular .py workflow file."
	}
	if filepath.Ext(rel) != ".py" {
		return "", "", true, "[MODEL_OVERRIDE] run_workflow refuses non-.py ScriptPath in installed workflows directory."
	}
	fi, err := os.Lstat(filepath.Join(wfDir, rel))
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
		return "", "", true, "[MODEL_OVERRIDE] run_workflow refuses missing, non-regular, or > 1 MiB installed workflow ScriptPath."
	}
	return wfDir, rel, true, ""
}

func isContainedRelPath(rel string) bool {
	return rel != "" && rel != "." && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(rel)
}

func hasDotDotSegment(p string) bool {
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return true
		}
	}
	return false
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

func readContainedWorkflowScript(cleanDir, rel string, requireRegular bool) (string, string, bool, string) {
	root, err := os.OpenRoot(cleanDir)
	if err != nil {
		if !requireRegular && errors.Is(err, os.ErrNotExist) {
			return "", "", false, ""
		}
		return "", "", false, "[MODEL_OVERRIDE] run_workflow refuses unreadable ScriptPath."
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		if !requireRegular && errors.Is(err, os.ErrNotExist) {
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

func inspectWorkflowScript(home, src, modelName string, isInline bool, deps WorkflowDeps) (string, []workflowPatch) {
	if deny := validateWorkflowPolicyDeps(deps); deny != "" {
		return deny, nil
	}
	fast := deps.NeedsGuardedProUpgrade(home, modelName)
	spans, scanDeny := scanWorkflowAgentCalls(src)
	if scanDeny != "" {
		return scanDeny, nil
	}
	var (
		proDeny string
		patches []workflowPatch
	)
	for _, sp := range spans {
		denyOverride, denyPro, patch := inspectSingleAgentCall(home, src, [2]int{sp.start, sp.end}, modelName, fast, isInline, deps)
		if denyOverride != "" {
			return denyOverride, nil
		}
		if patch != nil && sp.inFString {
			return fmt.Sprintf("[AUTO_SUBAGENT_PRO] fast session (%s) requires explicit model=\"pro\" (not \"inherit\") inside f-string run_workflow call.", formatWorkflowModelTag(deps, home, modelName)), nil
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

func inspectSingleAgentCall(home, src string, sp [2]int, modelName string, fast, isInline bool, deps WorkflowDeps) (string, string, *workflowPatch) {
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
		return inspectNonLiteralTypeAgent(home, typeArg, mArg, args, modelName, fast, isInline, deps)
	}
	if deps.IsModelGuardedSubagent == nil {
		return "[MODEL_OVERRIDE] run_workflow guard misconfigured: missing IsModelGuardedSubagent callback.", "", nil
	}
	if !deps.IsModelGuardedSubagent(typeName) {
		return "", "", nil
	}
	return inspectGuardedModelArg(home, typeName, mArg, modelName, fast, isInline, deps)
}

func inspectGuardedModelArg(home, typeName string, mArg *workflowArg, modelName string, fast, isInline bool, deps WorkflowDeps) (string, string, *workflowPatch) {
	if mArg == nil || isPythonNone(mArg.raw) {
		if fast {
			return "", fmt.Sprintf("[AUTO_SUBAGENT_PRO] fast session (%s) forbids omitted model on guarded run_workflow subagent %q; pass model=\"pro\".", formatWorkflowModelTag(deps, home, modelName), typeName), nil
		}
		return "", "", nil
	}
	mVal, isLit := unquotePythonLiteral(mArg.raw)
	if !isLit {
		safeExpr := vet.CollapseOneLine(sanitizeWorkflowText(deps, mArg.raw), 64)
		return fmt.Sprintf("[MODEL_OVERRIDE] run_workflow subagent %q forbids non-literal model=%s; omit model or pass \"inherit\" (or \"pro\" on fast turns).", typeName, safeExpr), "", nil
	}
	if isBlockedWorkflowModel(mVal) {
		safeVal := vet.CollapseOneLine(sanitizeWorkflowText(deps, mVal), 64)
		return fmt.Sprintf("[MODEL_OVERRIDE] run_workflow subagent %q forbids model=%q; omit model or pass \"inherit\" (or \"pro\" on fast turns).", typeName, safeVal), "", nil
	}
	if strings.EqualFold(strings.TrimSpace(mVal), "inherit") && fast {
		if !isInline {
			return "", fmt.Sprintf("[AUTO_SUBAGENT_PRO] fast session (%s) requires explicit model=\"pro\" (not \"inherit\") inside ScriptPath for guarded subagent %q.", formatWorkflowModelTag(deps, home, modelName), typeName), nil
		}
		return "", "", &workflowPatch{start: mArg.valStart, end: mArg.valEnd, repl: `"pro"`}
	}
	return "", "", nil
}

func inspectNonLiteralTypeAgent(home string, typeArg, mArg *workflowArg, args []workflowArg, modelName string, fast, isInline bool, deps WorkflowDeps) (string, string, *workflowPatch) {
	if hasPythonKwargsSplat(args) {
		safeType := vet.CollapseOneLine(sanitizeWorkflowText(deps, typeArg.raw), 64)
		return fmt.Sprintf("[MODEL_OVERRIDE] run_workflow subagent type_name=%s forbids **kwargs splat.", safeType), "", nil
	}
	if mArg == nil || isPythonNone(mArg.raw) {
		if fast {
			safeType := vet.CollapseOneLine(sanitizeWorkflowText(deps, typeArg.raw), 64)
			if !isInline {
				return "", fmt.Sprintf("[AUTO_SUBAGENT_PRO] fast session (%s) requires explicit model=\"pro\" inside ScriptPath for dynamic subagent %s.", formatWorkflowModelTag(deps, home, modelName), safeType), nil
			}
			return fmt.Sprintf("[MODEL_OVERRIDE] fast session (%s) requires explicit model=\"pro\" on inline run_workflow dynamic subagent %s.", formatWorkflowModelTag(deps, home, modelName), safeType), "", nil
		}
		return "", "", nil
	}
	mVal, isLit := unquotePythonLiteral(mArg.raw)
	if !isLit {
		safeType := vet.CollapseOneLine(sanitizeWorkflowText(deps, typeArg.raw), 64)
		safeExpr := vet.CollapseOneLine(sanitizeWorkflowText(deps, mArg.raw), 64)
		return fmt.Sprintf("[MODEL_OVERRIDE] run_workflow subagent type_name=%s forbids non-literal model=%s; pass literal \"pro\" or \"inherit\".", safeType, safeExpr), "", nil
	}
	if isBlockedWorkflowModel(mVal) {
		safeType := vet.CollapseOneLine(sanitizeWorkflowText(deps, typeArg.raw), 64)
		safeVal := vet.CollapseOneLine(sanitizeWorkflowText(deps, mVal), 64)
		return fmt.Sprintf("[MODEL_OVERRIDE] run_workflow subagent type_name=%s forbids model=%q; pass \"pro\" or \"inherit\".", safeType, safeVal), "", nil
	}
	if strings.EqualFold(strings.TrimSpace(mVal), "inherit") && fast {
		if !isInline {
			return "", fmt.Sprintf("[AUTO_SUBAGENT_PRO] fast session (%s) requires explicit model=\"pro\" (not \"inherit\") inside ScriptPath for dynamic subagent %s.", formatWorkflowModelTag(deps, home, modelName), vet.CollapseOneLine(sanitizeWorkflowText(deps, typeArg.raw), 64)), nil
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
