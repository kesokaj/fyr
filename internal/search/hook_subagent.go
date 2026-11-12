package search

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// handlePreToolSubagent blocks non-inherit Model overrides on judgement and
// audit subagents so a builder cannot downgrade reviewer, verifier, security,
// dreamer, or bugs onto a weaker tier at dispatch time.
func handlePreToolSubagent(in HookInput, out io.Writer) error {
	subagent := extractSubagentType(in.ToolCall.Args)
	if !isModelGuardedSubagent(subagent) {
		return writeDecision(out, "allow", "")
	}
	if model, blocked := nonInheritModelOverride(in.ToolCall.Args); blocked {
		reason := fmt.Sprintf("[MODEL_OVERRIDE] subagent %q forbids Model=%q override; omit Model or pass \"inherit\" (rules/agy-dispatch.md).", subagent, model)
		return writeDecision(out, "deny", reason)
	}
	return writeDecision(out, "allow", "")
}

func isModelGuardedSubagent(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "reviewer", "verifier", "security", "dreamer", "bugs":
		return true
	default:
		return false
	}
}

func extractSubagentType(args map[string]any) string {
	var first string
	for _, k := range []string{
		"TypeName", "typeName", "type_name",
		"SubagentType", "subagentType", "subagent_type",
		"Subagent", "subagent", "Name", "name",
	} {
		s, ok := args[k].(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if isModelGuardedSubagent(s) {
			return strings.ToLower(s)
		}
		if first == "" {
			first = s
		}
	}
	return first
}

func nonInheritModelOverride(args map[string]any) (string, bool) {
	var keys []string
	for k := range args {
		if isSubagentModelKey(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := args[k]
		if v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return fmt.Sprint(v), true
		}
		trimmed := strings.TrimSpace(s)
		if trimmed == "" || strings.EqualFold(trimmed, "inherit") {
			continue
		}
		return trimmed, true
	}
	return "", false
}

func isSubagentModelKey(k string) bool {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "model", "modelname", "model_name", "modeloverride", "model_override":
		return true
	default:
		return false
	}
}
