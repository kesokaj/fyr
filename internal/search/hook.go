package search

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

const (
	hookMaxDirtyFastPath = 15
	hookKneeTokens       = 180000
	hookMinIdentLen      = 6
	hookConfidenceRatio  = 3.0
)

// HookInput is the JSON payload Antigravity sends to a lifecycle hook on stdin.
type HookInput struct {
	ConversationID        string       `json:"conversationId"`
	WorkspacePaths        []string     `json:"workspacePaths"`
	Cwd                   string       `json:"cwd,omitempty"`
	TranscriptPath        string       `json:"transcriptPath"`
	ArtifactDirectoryPath string       `json:"artifactDirectoryPath"`
	ModelName             string       `json:"modelName"`
	LastUserInput         string       `json:"lastUserInput"`
	InvocationNum         int          `json:"invocationNum"`
	InitialNumSteps       int          `json:"initialNumSteps"`
	StepIdx               int          `json:"stepIdx"`
	ToolCall              HookToolCall `json:"toolCall"`
	ExecutionNum          int          `json:"executionNum"`
	TerminationReason     string       `json:"terminationReason"`
	Error                 string       `json:"error"`
	FullyIdle             bool         `json:"fullyIdle"`
	ContinuationActive    bool         `json:"continuationActive"`
}

// HookToolCall is the tool name and argument map passed to a PreToolUse hook.
type HookToolCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// PreInvocationOutput is the JSON response written by a PreInvocation hook.
type PreInvocationOutput struct {
	InjectSteps         []HookInjectStep `json:"injectSteps,omitempty"`
	TerminationBehavior string           `json:"terminationBehavior,omitempty"`
}

// HookInjectStep wraps one injected systemMessage step in PreInvocationOutput.
type HookInjectStep struct {
	SystemMessage *HookSystemMessage `json:"systemMessage,omitempty"`
}

// HookSystemMessage holds the model-visible systemMessage and zero-token UI metadata.
type HookSystemMessage struct {
	SystemMessage string         `json:"systemMessage"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// HookDecisionOutput is the JSON response written by PreToolUse and Stop hooks.
type HookDecisionOutput struct {
	Decision  string         `json:"decision"`
	Reason    string         `json:"reason,omitempty"`
	Overwrite map[string]any `json:"overwrite,omitempty"`
}

func normalizeHookEvent(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "session-start", "sessionstart":
		return "session-start"
	case "pre-invocation", "preinvocation":
		return "pre-invocation"
	case "pre-tool-use", "pretooluse":
		return "pre-tool-use"
	case "post-tool-use", "posttooluse", "post_tool_use":
		return "post-tool-use"
	case "stop":
		return "stop"
	default:
		return ""
	}
}

// RunHook executes the Antigravity lifecycle hook named in o.Hook, reading JSON
// from in and writing the hook response JSON to out. It always fails open (returns nil).
func RunHook(o Options, in io.Reader, out io.Writer) error {
	event := normalizeHookEvent(o.Hook)
	if event == "" {
		return usageError{"search: --hook must be one of: session-start pre-invocation pre-tool-use post-tool-use stop; see fyr help search"}
	}
	if hooksDisabledGlobal(o.Home) {
		return writeHookAllowFallback(event, out)
	}
	return runHookWithDeadline(o, event, in, out)
}

func decodeHookInput(o Options, in io.Reader) (HookInput, error) {
	data, err := io.ReadAll(io.LimitReader(in, 4<<20))
	if err != nil {
		return HookInput{}, err
	}
	parsed, err := parseAndNormalizeHookInput(o, data)
	if err != nil {
		return HookInput{}, err
	}
	if strings.TrimSpace(parsed.ToolCall.ID) == "" {
		parsed.ToolCall.ID = extractToolCallFallbackID(data)
	} else {
		parsed.ToolCall.ID = strings.TrimSpace(parsed.ToolCall.ID)
	}
	return parsed, nil
}

func extractToolCallFallbackID(data []byte) string {
	var raw map[string]any
	if len(bytes.TrimSpace(data)) == 0 || json.Unmarshal(data, &raw) != nil {
		return ""
	}
	for _, key := range []string{"toolCall", "tool_call", "ToolCall"} {
		if m, ok := raw[key].(map[string]any); ok {
			if id := extractStringArg(m, "id", "ID", "toolCallId", "tool_call_id", "callId", "call_id"); id != "" {
				return id
			}
		}
	}
	return extractStringArg(raw, "toolCallId", "tool_call_id", "callId", "call_id")
}

func resolveHookDir(o Options, in HookInput) string {
	firstWP := ""
	for _, wp := range in.WorkspacePaths {
		p := strings.TrimSpace(wp)
		if p == "" {
			continue
		}
		if firstWP == "" {
			firstWP = p
		}
		if _, _, ok := findGitRootFast(p); ok {
			return p
		}
	}
	if firstWP != "" {
		return firstWP
	}
	if cwd := strings.TrimSpace(in.Cwd); cwd != "" {
		return cwd
	}
	if o.Dir != "" {
		return o.Dir
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func storeDegradedBanner(err error) string {
	return formatStoreDegradedBanner(err)
}

func writeStoreDegraded(out io.Writer, err error) {
	writePreInvocation(out, "", storeDegradedBanner(err))
}

// writePreInvocation serializes the PreInvocation hook response. Antigravity CLI
// v1.5.3–v1.5.9 utils.scrubMessage panics on any populated map<string,string>
// field (HookSystemMessage.metadata), so userMarkdown is combined into
// SystemMessage and Metadata is kept nil (BUG-523).
func writePreInvocation(out io.Writer, sysMsg, userMarkdown string) {
	var resp PreInvocationOutput
	if sysMsg != "" || userMarkdown != "" {
		combined := sysMsg
		if userMarkdown != "" {
			if combined == "" || combined == userMarkdown {
				combined = userMarkdown
			} else if !strings.Contains(combined, userMarkdown) {
				combined = userMarkdown + "\n" + combined
			}
		}
		resp.InjectSteps = []HookInjectStep{{SystemMessage: &HookSystemMessage{SystemMessage: combined}}}
	}
	if data, err := json.Marshal(resp); err == nil {
		_, _ = out.Write(append(data, '\n'))
	}
}

func writeDecision(out io.Writer, decision, reason string) error {
	writeDecisionWithOverwrite(out, decision, reason, nil)
	return nil
}

func writeDecisionWithOverwrite(out io.Writer, decision, reason string, overwrite map[string]any) {
	if data, err := json.Marshal(HookDecisionOutput{Decision: decision, Reason: reason, Overwrite: overwrite}); err == nil {
		_, _ = out.Write(append(data, '\n'))
	}
}

func oneLineErr(err error) string {
	if err == nil {
		return "unknown error"
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
