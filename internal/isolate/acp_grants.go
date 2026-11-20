package isolate

import (
	"encoding/json"
	"slices"
	"strings"
)

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

type shellFrame struct {
	quote     byte
	ansiQuote bool
	close     byte
	cur       []byte
}

func splitShellSegments(s string) []string {
	stack := []shellFrame{{}}
	var out, subSegs []string
	for i := 0; i < len(s); i++ {
		top := &stack[len(stack)-1]
		if top.quote == '\'' {
			next, ok := stepShellSingleQuote(s, i, top)
			if !ok {
				return nil
			}
			i = next
			continue
		}
		if s[i] == '\\' {
			if i+1 >= len(s) || (top.close == '`' && s[i+1] == '`') {
				return nil
			}
			top.cur = append(top.cur, s[i])
			top.cur = append(top.cur, s[i+1])
			i++
			continue
		}
		next, ok := stepShellFrame(s, i, &stack, &out, &subSegs)
		if !ok {
			return nil
		}
		i = next
	}
	if len(stack) != 1 || stack[0].quote != 0 {
		return nil
	}
	flushShellSegment(&stack[0].cur, &out)
	return append(out, subSegs...)
}

func stepShellFrame(s string, i int, stack *[]shellFrame, out, subSegs *[]string) (int, bool) {
	top := &(*stack)[len(*stack)-1]
	if top.quote == 0 {
		return stepShellUnquoted(s, i, stack, out, subSegs)
	}
	ch := s[i]
	if ch == '$' && i+1 < len(s) && s[i+1] == '(' {
		top.cur = append(top.cur, '_')
		*stack = append(*stack, shellFrame{close: ')'})
		return i + 1, true
	}
	if ch == '`' {
		top.cur = append(top.cur, '_')
		*stack = append(*stack, shellFrame{close: '`'})
		return i, true
	}
	if ch == '"' {
		top.quote = 0
	}
	top.cur = append(top.cur, ch)
	return i, true
}

func stepShellSingleQuote(s string, i int, top *shellFrame) (int, bool) {
	if top.ansiQuote && s[i] == '\\' {
		if i+1 >= len(s) || s[i+1] == 'c' {
			return 0, false
		}
		top.cur = append(top.cur, s[i], s[i+1])
		return i + 1, true
	}
	if s[i] == '\'' {
		top.quote = 0
		top.ansiQuote = false
	}
	top.cur = append(top.cur, s[i])
	return i, true
}

func stepShellUnquoted(s string, i int, stack *[]shellFrame, out, subSegs *[]string) (int, bool) {
	top := &(*stack)[len(*stack)-1]
	ch := s[i]
	if ch == '#' && isShellCommentStart(s, i) {
		return skipShellComment(s, i, top.close == '`'), true
	}
	if ch == '$' && i+1 < len(s) && s[i+1] == '\'' {
		top.quote = '\''
		top.ansiQuote = true
		top.cur = append(top.cur, '$', '\'')
		return i + 1, true
	}
	if ch == '\'' || ch == '"' {
		top.quote = ch
		top.cur = append(top.cur, ch)
		return i, true
	}
	if (ch == '$' || ch == '<' || ch == '>') && i+1 < len(s) && s[i+1] == '(' {
		top.cur = append(top.cur, '_')
		*stack = append(*stack, shellFrame{close: ')'})
		return i + 1, true
	}
	if ch == '`' {
		if top.close == '`' {
			flushShellSegment(&top.cur, subSegs)
			*stack = (*stack)[:len(*stack)-1]
			return i, true
		}
		top.cur = append(top.cur, '_')
		*stack = append(*stack, shellFrame{close: '`'})
		return i, true
	}
	return stepShellParensOrOp(s, i, stack, out, subSegs)
}

func stepShellParensOrOp(s string, i int, stack *[]shellFrame, out, subSegs *[]string) (int, bool) {
	top := &(*stack)[len(*stack)-1]
	ch := s[i]
	switch ch {
	case '(':
		if strings.TrimSpace(string(top.cur)) != "" {
			return 0, false
		}
		*stack = append(*stack, shellFrame{close: ')'})
		return i, true
	case ')':
		if top.close != ')' {
			return 0, false
		}
		flushShellSegment(&top.cur, subSegs)
		*stack = (*stack)[:len(*stack)-1]
		return i, true
	}
	if skip := unquotedShellOpLen(s, i); skip > 0 {
		dst := out
		if len(*stack) > 1 {
			dst = subSegs
		}
		flushShellSegment(&top.cur, dst)
		return i + skip - 1, true
	}
	top.cur = append(top.cur, ch)
	return i, true
}

func isShellCommentStart(s string, i int) bool {
	for i > 0 && s[i-1] == '\n' && trailingBackslashes(s[:i-1])%2 == 1 {
		i -= 2
	}
	if i == 0 {
		return true
	}
	switch s[i-1] {
	case ' ', '\t', '\n', '(', ')', ';', '&', '|', '<', '>':
		return true
	}
	return false
}

func trailingBackslashes(s string) int {
	n := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		n++
	}
	return n
}

func skipShellComment(s string, i int, stopAtBacktick bool) int {
	for i < len(s) && s[i] != '\n' && (!stopAtBacktick || s[i] != '`') {
		i++
	}
	return i - 1
}

func flushShellSegment(cur *[]byte, out *[]string) {
	if t := strings.TrimSpace(string(*cur)); t != "" {
		*out = append(*out, t)
	}
	*cur = (*cur)[:0]
}

func unquotedShellOpLen(s string, i int) int {
	ch := s[i]
	if (ch == '&' || ch == '|') && i+1 < len(s) && s[i+1] == ch {
		return 2
	}
	if ch == '&' && (i == 0 || s[i-1] != '>') && (i+1 >= len(s) || s[i+1] != '>') {
		return 1
	}
	if ch == ';' || ch == '|' || ch == '\n' {
		return 1
	}
	return 0
}

func isCommandTool(name, kind string) bool {
	l := strings.ToLower(name)
	k := strings.ToLower(kind)
	return k == "execute" || k == "command" || k == "bash" || k == "terminal" ||
		strings.Contains(l, "run_command") || strings.Contains(l, "bash") ||
		strings.Contains(l, "terminal") || strings.HasPrefix(l, "command(")
}

func isReadTool(name, kind string) bool {
	l := strings.ToLower(name)
	k := strings.ToLower(kind)
	return k == "read" || k == "search" ||
		strings.Contains(l, "read_file") || strings.Contains(l, "view_file") ||
		strings.Contains(l, "list_dir") || strings.Contains(l, "grep") ||
		strings.Contains(l, "find_by_name") || strings.Contains(l, "glob")
}

func isWriteTool(name, kind string) bool {
	l := strings.ToLower(name)
	k := strings.ToLower(kind)
	return k == "edit" || k == "write" ||
		strings.Contains(l, "write_file") || strings.Contains(l, "edit_file") ||
		strings.Contains(l, "replace_file") || strings.Contains(l, "write_to_file")
}

func strField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				var u string
				if len(s) >= 2 && s[0] == '"' && json.Unmarshal([]byte(s), &u) == nil {
					return u
				}
				return s
			}
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
