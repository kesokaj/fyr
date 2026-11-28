package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/hooks/guard"
	"github.com/kesokaj/fyr/internal/readledger"
)

type hookSubagentMeta struct {
	AgentName            string
	ParentConversationID string
}

type hookSubagentMetaCtx struct{}

func withHookSubagentMeta(ctx context.Context, meta hookSubagentMeta) context.Context {
	if meta.AgentName == "" && meta.ParentConversationID == "" {
		return ctx
	}
	return context.WithValue(ctx, hookSubagentMetaCtx{}, meta)
}

func hookSubagentMetaFromCtx(ctx context.Context) hookSubagentMeta {
	if ctx == nil {
		return hookSubagentMeta{}
	}
	if v, ok := ctx.Value(hookSubagentMetaCtx{}).(hookSubagentMeta); ok {
		return v
	}
	return hookSubagentMeta{}
}

func extractHookSubagentMeta(data []byte) hookSubagentMeta {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return hookSubagentMeta{}
	}
	var raw map[string]any
	if json.Unmarshal(trimmed, &raw) != nil {
		return hookSubagentMeta{}
	}
	return hookSubagentMeta{
		AgentName: extractStringArg(raw,
			"agentName", "agent_name", "agentType", "agent_type",
			"subagentType", "subagent_type", "subagent", "role",
		),
		ParentConversationID: extractStringArg(raw,
			"parentConversationId", "parent_conversation_id",
			"parentSessionId", "parent_session_id",
		),
	}
}

// ExtractToolTargetPath extracts the target file path from a tool argument map.
func ExtractToolTargetPath(args map[string]any) string {
	return extractStringArg(args, "Path", "path", "AbsolutePath", "absolutePath", "absolute_path", "FilePath", "filePath", "file_path", "TargetFile", "targetFile", "target_file")
}

func extractStringArg(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// ExtractLineRangeArgs extracts (start, end) line numbers from read_file arguments.
func ExtractLineRangeArgs(args map[string]any) (int, int) {
	start := extractIntArg(args, "StartLine", "startLine", "start_line", "StartLineNumber", "startLineNumber", "start_line_number", "Offset", "offset")
	end := extractIntArg(args, "EndLine", "endLine", "end_line", "EndLineNumber", "endLineNumber", "end_line_number")
	limit := extractIntArg(args, "Limit", "limit", "LineCount", "lineCount", "line_count")
	if end == 0 && limit > 0 {
		if start > 0 {
			end = start + limit - 1
		} else {
			start, end = 1, limit
		}
	}
	return start, end
}

func extractIntArg(args map[string]any, keys ...string) int {
	for _, k := range keys {
		v, ok := args[k]
		if !ok {
			continue
		}
		if n, parsed := parseAnyInt(v); parsed {
			return n
		}
	}
	return 0
}

func parseAnyInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		if parsed, err := n.Int64(); err == nil {
			return int(parsed), true
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

// ParseAndNormalizeHookInput parses a raw hook JSON payload and fills field aliases.
func ParseAndNormalizeHookInput(o Options, data []byte) (HookInput, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return HookInput{ConversationID: strings.TrimSpace(o.Session)}, nil
	}
	var input HookInput
	if err := json.Unmarshal(trimmed, &input); err != nil {
		return HookInput{}, err
	}
	var raw map[string]any
	if err := json.Unmarshal(trimmed, &raw); err == nil {
		applyHookEnvelopeAliases(&input, raw, o.Session)
		applyHookToolAliases(&input.ToolCall, raw)
	}
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	return input, nil
}

func applyHookEnvelopeAliases(in *HookInput, raw map[string]any, optSession string) {
	applyHookStringAliases(in, raw)
	applyHookNumericAliases(in, raw)
	if !in.ContinuationActive {
		in.ContinuationActive = hasTrueBoolArg(raw, "continuation_active", "is_continuation", "isContinuation")
	}
	in.ConversationID = resolveHookConversationID(*in, raw, optSession)
}

func applyHookStringAliases(in *HookInput, raw map[string]any) {
	if len(in.WorkspacePaths) == 0 {
		in.WorkspacePaths = extractStringSliceArg(raw, "workspace_paths", "WorkspacePaths")
	}
	fillEmptyString(&in.Cwd, raw, "cwd", "Cwd", "workingDirectory", "working_directory")
	fillEmptyString(&in.TranscriptPath, raw, "transcript_path", "TranscriptPath")
	fillEmptyString(&in.ArtifactDirectoryPath, raw, "artifact_directory_path", "ArtifactDirectoryPath")
	fillEmptyString(&in.LastUserInput, raw, "last_user_input", "prompt", "user_prompt")
}

func fillEmptyString(dst *string, raw map[string]any, keys ...string) {
	if strings.TrimSpace(*dst) == "" {
		*dst = extractStringArg(raw, keys...)
	}
}

func applyHookNumericAliases(in *HookInput, raw map[string]any) {
	fillZeroInt(&in.InvocationNum, raw, "invocation_num")
	fillZeroInt(&in.InitialNumSteps, raw, "initial_num_steps")
	fillZeroInt(&in.StepIdx, raw, "step_idx")
	fillZeroInt(&in.ExecutionNum, raw, "execution_num")
}

func fillZeroInt(dst *int, raw map[string]any, keys ...string) {
	if *dst == 0 {
		*dst = extractIntArg(raw, keys...)
	}
}

func hasTrueBoolArg(raw map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := raw[k].(bool); ok && v {
			return true
		}
	}
	return false
}

func resolveHookConversationID(in HookInput, raw map[string]any, optSession string) string {
	if id := strings.TrimSpace(in.ConversationID); id != "" {
		return id
	}
	if id := extractStringArg(raw, "conversation_id", "sessionId", "session_id", "ConversationID", "SessionID"); id != "" {
		return id
	}
	if id := strings.TrimSpace(optSession); id != "" {
		return id
	}
	return extractBrainConversationID(in.ArtifactDirectoryPath, in.TranscriptPath)
}

func applyHookToolAliases(tc *HookToolCall, raw map[string]any) {
	for _, k := range []string{"toolCall", "tool_call", "ToolCall"} {
		if m, ok := raw[k].(map[string]any); ok {
			if strings.TrimSpace(tc.Name) == "" {
				tc.Name = extractStringArg(m, "name", "Name", "toolName", "tool_name")
			}
			if len(tc.Args) == 0 {
				tc.Args = extractToolArgsMap(m, "args", "Args", "arguments", "parameters", "input", "toolInput", "tool_input")
			}
		}
	}
	if strings.TrimSpace(tc.Name) == "" {
		tc.Name = extractStringArg(raw, "toolName", "tool_name", "ToolName")
	}
	if len(tc.Args) == 0 {
		tc.Args = extractToolArgsMap(raw, "toolInput", "tool_input", "ToolInput", "args", "arguments", "parameters", "input")
	}
}

func extractToolArgsMap(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		if args, ok := v.(map[string]any); ok {
			return args
		}
		if s, ok := v.(string); ok && strings.HasPrefix(strings.TrimSpace(s), "{") {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(s), &parsed); err == nil {
				return parsed
			}
		}
	}
	return nil
}

func extractStringSliceArg(m map[string]any, keys ...string) []string {
	for _, k := range keys {
		arr, ok := m[k].([]any)
		if !ok {
			continue
		}
		var out []string
		for _, elem := range arr {
			if s, ok := elem.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func extractBrainConversationID(paths ...string) string {
	for _, p := range paths {
		slash := filepath.ToSlash(strings.TrimSpace(p))
		idx := strings.Index(slash, "/brain/")
		if idx < 0 {
			continue
		}
		seg, _, _ := strings.Cut(slash[idx+len("/brain/"):], "/")
		seg = strings.TrimSpace(seg)
		if seg != "" && seg != "." && seg != ".." {
			return seg
		}
	}
	return ""
}

// NormalizeHookToolName maps Antigravity/IDE tool names to canonical hook labels.
func NormalizeHookToolName(raw string) string {
	s := strings.TrimSpace(raw)
	if idx := strings.LastIndexByte(s, ':'); idx >= 0 {
		s = s[idx+1:]
	}
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "cortex_step_type_")
	switch s {
	case "read_file", "readfile", "view_file", "viewfile", "read":
		return "read_file"
	case "run_command", "runcommand", "bash":
		return "run_command"
	case "edit_file", "editfile", "replace_file_content", "multi_replace_file_content", "write_file", "writefile", "write_to_file", "edit", "write":
		return "write_or_edit"
	case "swarm":
		return "swarm"
	case "run_workflow", "runworkflow":
		return "run_workflow"
	default:
		return s
	}
}

// ResolveToolProjectDir resolves the project directory governing a tool target path.
func ResolveToolProjectDir(dir string, _ HookInput, target string) string {
	t := strings.TrimSpace(target)
	if t == "" || !filepath.IsAbs(t) {
		return dir
	}
	canonTarget := guard.ResolveTargetWithAncestors("", t)
	targetAnc := guard.ExistingAncestorDir(filepath.FromSlash(canonTarget))
	targetRoot, _, hasTargetRoot := guard.FindGitRootFast(targetAnc)
	if dirRoot, _, ok := guard.FindGitRootFast(dir); ok {
		canonDir := strings.TrimRight(readledger.CanonicalizePath("", dirRoot), "/")
		if canonDir != "" && (canonTarget == canonDir || strings.HasPrefix(canonTarget, canonDir+"/")) {
			if !hasTargetRoot || strings.TrimRight(readledger.CanonicalizePath("", targetRoot), "/") == canonDir {
				return dir
			}
		}
	}
	if hasTargetRoot && !isRefusedAutoInitDir(targetRoot) {
		return targetRoot
	}
	return dir
}
