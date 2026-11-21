package search

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
)

type hookSubagentMeta struct {
	AgentName            string
	ParentConversationID string
}

type hookSubagentMetaKey struct{}

func withHookSubagentMeta(ctx context.Context, meta hookSubagentMeta) context.Context {
	if meta.AgentName == "" && meta.ParentConversationID == "" {
		return ctx
	}
	return context.WithValue(ctx, hookSubagentMetaKey{}, meta)
}

func hookSubagentMetaFromCtx(ctx context.Context) hookSubagentMeta {
	if ctx == nil {
		return hookSubagentMeta{}
	}
	if v, ok := ctx.Value(hookSubagentMetaKey{}).(hookSubagentMeta); ok {
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

func extractToolTargetPath(args map[string]any) string {
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

func extractLineRangeArgs(args map[string]any) (int, int) {
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
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		case json.Number:
			if parsed, err := n.Int64(); err == nil {
				return int(parsed)
			}
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func parseAndNormalizeHookInput(o Options, data []byte) (HookInput, error) {
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
	if len(in.WorkspacePaths) == 0 {
		in.WorkspacePaths = extractStringSliceArg(raw, "workspace_paths", "WorkspacePaths")
	}
	if strings.TrimSpace(in.Cwd) == "" {
		in.Cwd = extractStringArg(raw, "cwd", "Cwd", "workingDirectory", "working_directory")
	}
	if strings.TrimSpace(in.TranscriptPath) == "" {
		in.TranscriptPath = extractStringArg(raw, "transcript_path", "TranscriptPath")
	}
	if strings.TrimSpace(in.ArtifactDirectoryPath) == "" {
		in.ArtifactDirectoryPath = extractStringArg(raw, "artifact_directory_path", "ArtifactDirectoryPath")
	}
	if strings.TrimSpace(in.LastUserInput) == "" {
		in.LastUserInput = extractStringArg(raw, "last_user_input", "prompt", "user_prompt")
	}
	if in.InvocationNum == 0 {
		in.InvocationNum = extractIntArg(raw, "invocation_num")
	}
	if in.InitialNumSteps == 0 {
		in.InitialNumSteps = extractIntArg(raw, "initial_num_steps")
	}
	if in.StepIdx == 0 {
		in.StepIdx = extractIntArg(raw, "step_idx")
	}
	if in.ExecutionNum == 0 {
		in.ExecutionNum = extractIntArg(raw, "execution_num")
	}
	if !in.ContinuationActive {
		for _, k := range []string{"continuation_active", "is_continuation", "isContinuation"} {
			if v, ok := raw[k].(bool); ok && v {
				in.ContinuationActive = true
				break
			}
		}
	}
	in.ConversationID = resolveHookConversationID(*in, raw, optSession)
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
	for _, key := range []string{"toolCall", "tool_call", "ToolCall"} {
		if m, ok := raw[key].(map[string]any); ok {
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

func normalizeHookToolName(raw string) string {
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

func resolveToolProjectDir(dir string, _ HookInput, target string) string {
	t := strings.TrimSpace(target)
	if t == "" || !filepath.IsAbs(t) {
		return dir
	}
	canonTarget := resolveTargetWithAncestors("", t)
	targetAnc := existingAncestorDir(filepath.FromSlash(canonTarget))
	targetRoot, _, hasTargetRoot := findGitRootFast(targetAnc)
	if dirRoot, _, ok := findGitRootFast(dir); ok {
		canonDir := strings.TrimRight(CanonicalizePath("", dirRoot), "/")
		if canonDir != "" && (canonTarget == canonDir || strings.HasPrefix(canonTarget, canonDir+"/")) {
			if !hasTargetRoot || strings.TrimRight(CanonicalizePath("", targetRoot), "/") == canonDir {
				return dir
			}
		}
	}
	if hasTargetRoot && !isRefusedAutoInitDir(targetRoot) {
		return targetRoot
	}
	return dir
}
