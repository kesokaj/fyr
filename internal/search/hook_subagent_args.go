package search

import (
	"fmt"
	"sort"
	"strings"
)

func formatModelTag(home, raw string) string {
	mName, mThink := ResolveModelAndThinking(home, raw)
	if mName == "" {
		return strings.TrimSpace(raw)
	}
	if mThink != "" {
		return fmt.Sprintf("%s, thinking=%s", mName, mThink)
	}
	return mName
}

func isFastExecutionModel(modelName string) bool {
	s := strings.ToLower(strings.TrimSpace(modelName))
	if s == "" || strings.Contains(s, "orchestrator") || strings.Contains(s, "carbon") || strings.Contains(s, "argon") || strings.Contains(s, "opus") {
		return false
	}
	if strings.Contains(s, "flash") || strings.Contains(s, "gpt-oss") {
		return true
	}
	if strings.Contains(s, "hydrogen") {
		return !strings.Contains(s, "-high") && !strings.Contains(s, "-xhigh") && !strings.Contains(s, "-max")
	}
	return strings.Contains(s, "sonnet") && strings.Contains(s, "-low")
}

func needsGuardedProUpgrade(modelName string) bool {
	s := strings.ToLower(strings.TrimSpace(modelName))
	if s == "" || strings.Contains(s, "opus") {
		return false
	}
	if !strings.Contains(s, "orchestrator") && (strings.Contains(s, "carbon") || strings.Contains(s, "argon")) {
		return false
	}
	if strings.Contains(s, "sonnet") && !strings.Contains(s, "-low") {
		return false
	}
	if strings.Contains(s, "hydrogen") && (strings.Contains(s, "-high") || strings.Contains(s, "-xhigh") || strings.Contains(s, "-max")) {
		return false
	}
	return true
}

func suggestSessionModelNudge(sc *scope, convID string, genID, turnNum int, modelName string, res VetResult) string {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	if lower == "" || strings.Contains(lower, "orchestrator") {
		return ""
	}
	var st *store
	if sc != nil {
		st = sc.st
	}
	if isFastExecutionModel(modelName) && (res.Intent == IntentFeature || (res.Intent == IntentBug && res.Score < 70)) {
		if markSessionSentinelOnce(st, convID, genID, turnNum, "__model_nudge__:pro") {
			return "model tip: architecture/deep-debug turn on fast model — consider Orchestrator (Argon) or Carbon"
		}
	}
	if (strings.Contains(lower, "argon") || strings.Contains(lower, "opus")) && res.Intent == IntentTrivial {
		if markSessionSentinelOnce(st, convID, genID, turnNum, "__model_nudge__:fast") {
			return "model tip: trivial turn on heavy reasoning model — Orchestrator or Hydrogen/Flash is faster"
		}
	}
	return ""
}

func isModelGuardedSubagent(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "reviewer", "verifier", "security", "dreamer":
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
	var overrides []string
	seen := map[string]bool{}
	for _, k := range keys {
		v := args[k]
		if v == nil {
			continue
		}
		var val string
		if s, ok := v.(string); ok {
			val = strings.TrimSpace(s)
		} else {
			val = strings.TrimSpace(fmt.Sprint(v))
		}
		if val == "" || strings.EqualFold(val, "inherit") || seen[val] {
			continue
		}
		seen[val] = true
		overrides = append(overrides, val)
	}
	if len(overrides) == 0 {
		return "", false
	}
	return strings.Join(overrides, ", "), true
}

func isSubagentModelKey(k string) bool {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "model", "modelname", "model_name", "modeloverride", "model_override":
		return true
	default:
		return false
	}
}
