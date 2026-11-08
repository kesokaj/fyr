package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// ACPEvent is one normalized event emitted during an ACP prompt turn.
type ACPEvent struct {
	Kind      string `json:"kind"` // "init", "tool_call", "tool_update", "subagent", "message", "usage", "result"
	SessionID string `json:"session_id"`
	StepIndex int    `json:"step_index,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Status    string `json:"status,omitempty"`
	InputTok  int    `json:"input_tokens,omitempty"`
	OutputTok int    `json:"output_tokens,omitempty"`
}

// PromptResponse is the completed result of one ACP `session/prompt` turn.
type PromptResponse struct {
	Text       string
	StopReason string
	InputTok   int
	OutputTok  int
}

type acpUpdateEnvelope struct {
	SessionID string          `json:"sessionId"`
	Update    acpUpdateDetail `json:"update"`
}

type acpUpdateDetail struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Text          string          `json:"text"`
	Content       json.RawMessage `json:"content"`
	ToolCallID    string          `json:"toolCallId"`
	Title         string          `json:"title"`
	Name          string          `json:"name"`
	Kind          string          `json:"kind"`
	Status        string          `json:"status"`
	RawInput      json.RawMessage `json:"rawInput"`
	Usage         struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
	} `json:"usage"`
}

func rawInputMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		return m
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		_ = json.Unmarshal([]byte(s), &m)
		return m
	}
	return nil
}

type promptState struct {
	mu      sync.Mutex
	textBuf strings.Builder
	ndjson  bytes.Buffer
	stepIdx int
	inTok   int
	outTok  int
	onEvent func(ACPEvent)
}

func (s *promptState) emit(line map[string]any, ev *ACPEvent) {
	if b, err := json.Marshal(line); err == nil {
		s.ndjson.Write(b)
		s.ndjson.WriteByte('\n')
	}
	if ev != nil && s.onEvent != nil {
		s.onEvent(*ev)
	}
}

// Prompt sends one user turn on sessionID, collects streaming `session/update`
// notifications until `session/prompt` returns, and returns the assembled text
// and normalized NDJSON lines for `events.jsonl`.
func (c *ACPClient) Prompt(ctx context.Context, sessionID, model, cwd, text string, onEvent func(ACPEvent)) (*PromptResponse, []byte, error) {
	st := &promptState{onEvent: onEvent}
	initEv := ACPEvent{Kind: "init", SessionID: sessionID, Summary: model}
	st.emit(map[string]any{"event": "init", "session_id": sessionID, "model": model, "cwd": cwd}, &initEv)

	c.setNotifyHandler(func(method string, params json.RawMessage) {
		if method != "session/update" {
			return
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		handleUpdateNotification(sessionID, params, &st.textBuf, &st.stepIdx, &st.inTok, &st.outTok, st.emit)
	})
	defer c.setNotifyHandler(nil)

	req := map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": text}},
	}
	var rawRes promptRPCResult
	err := c.call(ctx, "session/prompt", req, &rawRes)
	return st.finish(sessionID, rawRes, err)
}

type promptRPCResult struct {
	StopReason string `json:"stopReason"`
	Text       string `json:"text"`
	Usage      struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
	} `json:"usage"`
}

func (s *promptState) finish(sessionID string, rawRes promptRPCResult, err error) (*PromptResponse, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rawRes.Usage.InputTokens > 0 {
		s.inTok = rawRes.Usage.InputTokens
	}
	if rawRes.Usage.OutputTokens > 0 {
		s.outTok = rawRes.Usage.OutputTokens
	}
	if s.textBuf.Len() == 0 && rawRes.Text != "" {
		s.textBuf.WriteString(rawRes.Text)
	}
	stop := firstNonEmpty(rawRes.StopReason, "end_turn")
	resEv := ACPEvent{Kind: "result", SessionID: sessionID, Status: stop, InputTok: s.inTok, OutputTok: s.outTok}
	s.emit(map[string]any{
		"event": "result", "session_id": sessionID, "stop_reason": stop,
		"input_tokens": s.inTok, "output_tokens": s.outTok,
	}, &resEv)
	return &PromptResponse{
		Text: s.textBuf.String(), StopReason: stop, InputTok: s.inTok, OutputTok: s.outTok,
	}, s.ndjson.Bytes(), err
}

func (c *ACPClient) setNotifyHandler(fn func(method string, params json.RawMessage)) {
	c.mu.Lock()
	c.notifyFn = fn
	c.mu.Unlock()
}

func handleUpdateNotification(
	sid string, params json.RawMessage, textBuf *strings.Builder,
	stepIdx, inTok, outTok *int, emit func(map[string]any, *ACPEvent),
) {
	var env acpUpdateEnvelope
	if err := json.Unmarshal(params, &env); err != nil {
		return
	}
	u := env.Update
	switch u.SessionUpdate {
	case "agent_message_chunk":
		chunk := extractUpdateText(u.Text, u.Content)
		textBuf.WriteString(chunk)
	case "tool_call", "tool_call_update":
		*stepIdx++
		ev := buildToolEvent(sid, *stepIdx, u)
		emit(map[string]any{
			"event": "step_update", "session_id": sid, "kind": ev.Kind,
			"step_index": ev.StepIndex, "tool_name": ev.ToolName,
			"summary": ev.Summary, "status": ev.Status,
		}, &ev)
	case "usage_update":
		if u.Usage.InputTokens > 0 {
			*inTok = u.Usage.InputTokens
		}
		if u.Usage.OutputTokens > 0 {
			*outTok = u.Usage.OutputTokens
		}
	}
}

func extractUpdateText(direct string, raw json.RawMessage) string {
	if direct != "" {
		return direct
	}
	if len(raw) == 0 {
		return ""
	}
	var single struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &single) == nil && single.Text != "" {
		return single.Text
	}
	var list []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &list) == nil {
		var b strings.Builder
		for _, item := range list {
			b.WriteString(item.Text)
		}
		return b.String()
	}
	return ""
}

func buildToolEvent(sid string, idx int, u acpUpdateDetail) ACPEvent {
	inMap := rawInputMap(u.RawInput)
	name := firstNonEmpty(u.Name, u.Title, u.Kind, "tool")
	summary := firstNonEmpty(u.Title, strField(inMap, "CommandLine", "commandLine", "command_line", "command", "Path", "path", "target_file", "targetFile", "query"))
	kind := "tool_call"
	if u.SessionUpdate == "tool_call_update" {
		kind = "tool_update"
	}
	subType := strField(inMap, "TypeName", "typeName", "subagent")
	if strings.Contains(strings.ToLower(name), "subagent") || subType != "" {
		kind = "subagent"
		name = "invoke_subagent"
		role := strField(inMap, "Role", "role")
		if subType != "" && role != "" {
			summary = subType + " (" + role + ")"
		} else if subType != "" {
			summary = subType
		}
	}
	return ACPEvent{
		Kind: kind, SessionID: sid, StepIndex: idx,
		ToolName: name, Summary: summary, Status: firstNonEmpty(u.Status, "running"),
	}
}

func pickOptionID(opts []acpPermOption, allow bool) string {
	want := "reject"
	fallback := "reject_once"
	if allow {
		want = "allow"
		fallback = "allow_once"
	}
	for _, o := range opts {
		if strings.Contains(strings.ToLower(o.OptionID), want) || strings.Contains(strings.ToLower(o.Kind), want) {
			if o.OptionID != "" {
				return o.OptionID
			}
		}
	}
	return fallback
}

func checkToolGrant(grants []string, p acpPermParams) (bool, string) {
	inMap := rawInputMap(p.ToolCall.RawInput)
	cmdStr := p.Command
	if cmdStr == "" {
		cmdStr = strField(inMap, "CommandLine", "commandLine", "command_line", "command", "cmd")
	}
	name := firstNonEmpty(p.ToolName, p.Tool, p.ToolCall.Name, p.ToolCall.Title, p.ToolCall.Kind)
	if cmdStr == "" && strings.HasPrefix(name, "command(") && strings.HasSuffix(name, ")") {
		cmdStr = strings.TrimSuffix(strings.TrimPrefix(name, "command("), ")")
	}
	if cmdStr == "" && isCommandTool(name, p.ToolCall.Kind) && p.ToolCall.Title != "" && !isCommandTool(p.ToolCall.Title, "") {
		cmdStr = strings.Trim(p.ToolCall.Title, "`")
	}
	desc := firstNonEmpty(name, "unknown_tool")
	if cmdStr != "" {
		desc = "command(" + cmdStr + ")"
	}
	if len(grants) == 0 {
		return false, desc
	}
	if cmdStr != "" || isCommandTool(name, p.ToolCall.Kind) {
		if slices.Contains(grants, "command(*)") {
			return true, desc
		}
		return matchCommandGrants(grants, firstNonEmpty(cmdStr, name)), desc
	}
	if isReadTool(name, p.ToolCall.Kind) {
		return slices.Contains(grants, "read_file(*)"), desc
	}
	if isWriteTool(name, p.ToolCall.Kind) {
		return slices.Contains(grants, "write_file(*)"), desc
	}
	return slices.Contains(grants, name) || slices.Contains(grants, "command(*)"), desc
}

func matchCommandGrants(grants []string, cmdStr string) bool {
	segs := splitShellSegments(cmdStr)
	if len(segs) == 0 {
		return false
	}
	for _, seg := range segs {
		bin := firstCommandBin(seg)
		if isShellBuiltinNoop(bin) {
			continue
		}
		if bin == "" || !slices.Contains(grants, "command("+bin+")") {
			return false
		}
	}
	return true
}

func isShellBuiltinNoop(bin string) bool {
	return bin == "true" || bin == "false" || bin == ":" || bin == "cd"
}

func splitShellSegments(s string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		if (quote == 0 || quote == '"') && s[i] == '\\' && i+1 < len(s) {
			cur.WriteByte(s[i])
			i++
			cur.WriteByte(s[i])
			continue
		}
		if quote == 0 && (s[i] == '\'' || s[i] == '"') {
			quote = s[i]
			cur.WriteByte(s[i])
			continue
		}
		if quote != 0 && s[i] == quote {
			quote = 0
			cur.WriteByte(s[i])
			continue
		}
		if skip, split := shellOperatorLen(s, i, quote); split {
			if t := strings.TrimSpace(cur.String()); t != "" {
				out = append(out, t)
			}
			cur.Reset()
			i += skip - 1
			continue
		}
		cur.WriteByte(s[i])
	}
	if t := strings.TrimSpace(cur.String()); t != "" {
		out = append(out, t)
	}
	return out
}

func shellOperatorLen(s string, i int, quote byte) (int, bool) {
	if quote == '\'' {
		return 0, false
	}
	ch := s[i]
	if (ch == '$' || ch == '<' || ch == '>') && i+1 < len(s) && s[i+1] == '(' {
		return 2, true
	}
	if ch == '`' {
		return 1, true
	}
	if quote == '"' {
		return 0, false
	}
	if (ch == '&' || ch == '|') && i+1 < len(s) && s[i+1] == ch {
		return 2, true
	}
	if ch == '&' && (i == 0 || s[i-1] != '>') && (i+1 >= len(s) || s[i+1] != '>') {
		return 1, true
	}
	if ch == ';' || ch == '|' || ch == '\n' {
		return 1, true
	}
	return 0, false
}

func firstCommandBin(seg string) string {
	for _, tok := range strings.Fields(seg) {
		tok = strings.Trim(tok, `"'()`)
		if tok == "" || tok == "export" || tok == "env" {
			continue
		}
		if strings.HasPrefix(tok, ">") || strings.HasPrefix(tok, "<") || strings.HasPrefix(tok, "2>") || strings.HasPrefix(tok, "1>") {
			continue
		}
		if eq := strings.IndexByte(tok, '='); eq > 0 && !strings.Contains(tok[:eq], "/") {
			continue
		}
		return filepath.Base(tok)
	}
	return ""
}
