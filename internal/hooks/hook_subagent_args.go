package hooks

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/hooks/telemetry"
	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/vet"
)

// FormatModelTag formats a model ID with display name and thinking effort for hook notices.
func FormatModelTag(home, raw string) string {
	id, disp, think := telemetry.ResolveModelDetails(home, raw)
	if id == "" {
		return strings.TrimSpace(raw)
	}
	lbl := id
	if disp != "" {
		lbl = fmt.Sprintf("%s [%s]", id, disp)
	}
	if think != "" {
		return fmt.Sprintf("%s, thinking=%s", lbl, think)
	}
	return lbl
}

func lookupCatalogHookModel(home, s string) (fam, tier, eff string, isOrch, found bool) {
	if strings.TrimSpace(home) == "" {
		return "", "", "", false, false
	}
	cat, ok, err := models.LoadCatalog(home)
	if err != nil || !ok {
		return "", "", "", false, false
	}
	for _, e := range cat.Entries {
		if strings.EqualFold(e.ID, s) || (e.DisplayName != "" && strings.EqualFold(e.DisplayName, s)) {
			orch := e.InteractiveOnly || e.Mode == "orchestrator" || e.Effort == "orchestrator" || models.IsInteractiveOnlyID(e.ID)
			return strings.ToLower(e.Family), strings.ToLower(e.Tier), strings.ToLower(e.Effort), orch, true
		}
	}
	return "", "", "", false, false
}

func classifyHookModel(home, modelName string) (fam, tier, eff string, isOrch bool) {
	s := strings.TrimSpace(modelName)
	if s == "" {
		return "", "", "", false
	}
	if f, t, e, orch, ok := lookupCatalogHookModel(home, s); ok {
		return f, t, e, orch
	}
	fam, tier, eff = models.ClassifyModel(s, "")
	isOrch = models.IsInteractiveOnlyID(s) || eff == "orchestrator"
	if !isOrch && tier == "pro" && strings.Contains(strings.ToLower(s), "hydrogen") {
		tier = "flash"
	}
	return fam, tier, eff, isOrch
}

func isFastExecutionModelForHome(home, modelName string) bool {
	s := strings.TrimSpace(modelName)
	if s == "" || strings.EqualFold(s, "auto") {
		return false
	}
	fam, tier, eff, isOrch := classifyHookModel(home, s)
	if isOrch || tier == "pro" {
		return false
	}
	if tier == "light" {
		return true
	}
	if fam == "anthropic" {
		return eff == "low"
	}
	if strings.Contains(strings.ToLower(s), "flash") || fam == "other" {
		return true
	}
	return eff != "high" && eff != "xhigh" && eff != "max"
}

// NeedsGuardedProUpgradeForHome reports whether a guarded subagent requires a pro model upgrade.
func NeedsGuardedProUpgradeForHome(home, modelName string) bool {
	s := strings.TrimSpace(modelName)
	if s == "" {
		return false
	}
	if strings.EqualFold(s, "auto") {
		return true
	}
	_, _, _, isOrch := classifyHookModel(home, s)
	return isOrch || isFastExecutionModelForHome(home, s)
}

func classifyModelNudge(home, s, fam, tier, eff string, res vet.Result) (string, string) {
	if isFastExecutionModelForHome(home, s) && (res.Intent == vet.IntentFeature || (res.Intent == vet.IntentBug && res.Score < 70)) {
		return "__model_nudge__:pro", "model tip: architecture/deep-debug turn on fast model — consider a pro builder/planner model or orchestrator"
	}
	isHeavyPro := tier == "pro" && (eff == "high" || eff == "xhigh" || eff == "max" || fam == "anthropic")
	if isHeavyPro && res.Intent == vet.IntentTrivial {
		return "__model_nudge__:fast", "model tip: trivial turn on heavy pro reasoning model — consider a flash-tier model or orchestrator"
	}
	return "", ""
}

func suggestSessionModelNudgeForHome(home string, sc *Scope, convID string, genID, turnNum int, modelName string, res vet.Result) string {
	s := strings.TrimSpace(modelName)
	if s == "" || strings.EqualFold(s, "auto") {
		return ""
	}
	fam, tier, eff, isOrch := classifyHookModel(home, s)
	if isOrch {
		return ""
	}
	sent, tip := classifyModelNudge(home, s, fam, tier, eff, res)
	if sent == "" {
		return ""
	}
	var db *sql.DB
	if sc != nil && sc.St != nil {
		db = sc.St.SQLDB
	}
	if MarkSessionSentinelOnce(db, convID, genID, turnNum, sent) {
		return tip
	}
	return ""
}

// IsModelGuardedSubagent reports whether name is a guarded audit/judge subagent role.
func IsModelGuardedSubagent(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "reviewer", "verifier", "security", "dreamer", "judge":
		return true
	default:
		return false
	}
}

// IsGuardedSubagentCall extracts the subagent role from args and reports whether it is model-guarded.
func IsGuardedSubagentCall(args map[string]any) (string, bool) {
	subagent := extractSubagentType(args)
	if IsModelGuardedSubagent(subagent) {
		return subagent, true
	}
	if subagent == "" || strings.EqualFold(subagent, "self") {
		if role := guardedRoleFromHint(stringArgCI(args, "Role")); role != "" {
			return role, true
		}
	}
	return subagent, false
}

func guardedRoleFromHint(raw string) string {
	tokens := tokenizeRoleHint(raw)
	if len(tokens) == 0 {
		return ""
	}
	switch {
	case hasSwarmRoleToken(tokens, nil, []string{"judge"}):
		return "judge"
	case hasSwarmRoleToken(tokens, []string{"sec"}, []string{"secur"}):
		return "security"
	case hasSwarmRoleToken(tokens, nil, []string{"review"}):
		return "reviewer"
	case hasSwarmRoleToken(tokens, nil, []string{"verif"}):
		return "verifier"
	case hasSwarmRoleToken(tokens, nil, []string{"dream"}):
		return "dreamer"
	default:
		return ""
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
		if IsModelGuardedSubagent(s) {
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
