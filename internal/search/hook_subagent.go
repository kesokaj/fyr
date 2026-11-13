package search

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

type swarmModelEntry struct {
	ID              string `json:"id"`
	Family          string `json:"family"`
	Subfamily       string `json:"subfamily"`
	Tier            string `json:"tier"`
	Effort          string `json:"effort"`
	Status          string `json:"status"`
	RetiringOn      string `json:"retiring_on"`
	InteractiveOnly bool   `json:"interactive_only"`
}

func handlePreToolSubagent(in HookInput, out io.Writer) error {
	subagent := extractSubagentType(in.ToolCall.Args)
	if !isModelGuardedSubagent(subagent) {
		return writeDecision(out, "allow", "")
	}
	if model, blocked := nonInheritModelOverride(in.ToolCall.Args); blocked {
		reason := fmt.Sprintf("[MODEL_OVERRIDE] subagent %q forbids Model=%q override; omit Model or pass \"inherit\" (rules/agy-dispatch.md).", subagent, model)
		return writeDecision(out, "deny", reason)
	}
	if isFastExecutionModel(in.ModelName) {
		cloned := cloneArgsWithoutModelKeys(in.ToolCall.Args)
		cloned["Model"] = "pro"
		reason := fmt.Sprintf("[AUTO_MODEL_UPGRADE] upgraded %s from %s to Model=\"pro\"", subagent, strings.TrimSpace(in.ModelName))
		return writeDecisionWithOverwrite(out, "allow", reason, cloned)
	}
	return writeDecision(out, "allow", "")
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

func cloneArgsWithoutModelKeys(args map[string]any) map[string]any {
	cloned := maps.Clone(args)
	if cloned == nil {
		return map[string]any{}
	}
	for k := range cloned {
		if isSubagentModelKey(k) {
			delete(cloned, k)
		}
	}
	return cloned
}

func handlePreToolSwarm(home string, in HookInput, out io.Writer) error {
	action := stringArgCI(in.ToolCall.Args, "action")
	if !strings.EqualFold(action, "add") {
		return writeDecision(out, "allow", "")
	}
	role := inferSwarmRole(in.ToolCall.Args)
	rawModel := stringArgCI(in.ToolCall.Args, "model")
	m := strings.ToLower(rawModel)
	if m != "" && m != "auto" {
		if role == "judge" && isFastExecutionModel(rawModel) {
			return writeDecision(out, "deny", fmt.Sprintf("[MODEL_OVERRIDE] swarm review/security worker forbids weak model=%q override", rawModel))
		}
		return writeDecision(out, "allow", "")
	}
	picked := pickSwarmModel(home, role)
	if picked == "" {
		return writeDecision(out, "allow", "")
	}
	cloned := cloneArgsWithoutModelKeys(in.ToolCall.Args)
	cloned["model"] = picked
	return writeDecisionWithOverwrite(out, "allow", "[AUTO_SWARM_MODEL] selected "+picked, cloned)
}

func stringArgCI(args map[string]any, key string) string {
	for k, v := range args {
		if strings.EqualFold(strings.TrimSpace(k), key) {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func inferSwarmRole(args map[string]any) string {
	tokens := tokenizeRoleHint(stringArgCI(args, "name") + " " + stringArgCI(args, "brief"))
	switch {
	case hasSwarmRoleToken(tokens, []string{"sec"}, []string{"review", "verif", "judge", "secur"}):
		return "judge"
	case hasSwarmRoleToken(tokens, []string{"doc", "docs", "document", "documents", "documentation"}, []string{"triv", "explor"}):
		return "mechanical"
	default:
		return "builder"
	}
}

func pickSwarmModel(home, role string) string {
	resolved := strings.TrimSpace(home)
	if resolved == "" {
		resolved, _ = os.UserHomeDir()
	}
	if resolved == "" {
		return ""
	}
	p := filepath.Join(resolved, harness.StateDir, "models.json")
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
		return ""
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var cat struct {
		Entries []swarmModelEntry `json:"entries"`
	}
	if json.Unmarshal(data, &cat) != nil || len(cat.Entries) == 0 {
		return ""
	}
	return pickActiveSwarmCandidate(cat.Entries, role)
}

func pickActiveSwarmCandidate(entries []swarmModelEntry, wantRole string) string {
	var best string
	bestScore := -1
	for _, e := range entries {
		if e.ID == "" || strings.EqualFold(e.Status, "gone") || e.InteractiveOnly || strings.TrimSpace(e.RetiringOn) != "" {
			continue
		}
		if strings.Contains(strings.ToLower(e.ID), "orchestrator") {
			continue
		}
		score := scoreSwarmCandidate(e, wantRole)
		if score > bestScore || (score == bestScore && best != "" && compareSwarmModelIDs(e.ID, best) > 0) {
			bestScore = score
			best = e.ID
		}
	}
	if bestScore <= 0 {
		return ""
	}
	return best
}

func compareSwarmModelIDs(a, b string) int {
	va, vb := parseIntRuns(a), parseIntRuns(b)
	for i := 0; i < max(len(va), len(vb)); i++ {
		var xa, xb int
		if i < len(va) {
			xa = va[i]
		}
		if i < len(vb) {
			xb = vb[i]
		}
		if xa != xb {
			if xa > xb {
				return 1
			}
			return -1
		}
	}
	return strings.Compare(a, b)
}

func parseIntRuns(s string) []int {
	var nums []int
	cur := -1
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= '0' && c <= '9' {
			if cur < 0 {
				cur = 0
			}
			cur = cur*10 + int(c-'0')
		} else if cur >= 0 {
			nums = append(nums, cur)
			cur = -1
		}
	}
	if cur >= 0 {
		nums = append(nums, cur)
	}
	return nums
}

func scoreSwarmCandidate(e swarmModelEntry, role string) int {
	id := strings.ToLower(e.ID)
	fam := strings.ToLower(e.Family)
	sub := strings.ToLower(e.Subfamily)
	tier := strings.ToLower(e.Tier)
	switch role {
	case "judge":
		if fam == "anthropic" && (sub == "opus" || strings.Contains(id, "opus")) {
			return 100 + effortWeight(id)
		}
		if fam == "anthropic" && tier == "pro" {
			return 80 + effortWeight(id)
		}
		if fam == "gemini" && strings.Contains(id, "carbon") {
			return 60 + effortWeight(id)
		}
		return 0
	case "mechanical":
		if fam == "gemini" && (tier == "flash" || strings.Contains(id, "flash") || strings.Contains(id, "hydrogen")) {
			base := 80
			if strings.Contains(id, "hydrogen") {
				base = 90
			}
			return base + effortWeight(id)
		}
		return 0
	}
	if fam == "gemini" && strings.Contains(id, "carbon") {
		return 100 + effortWeight(id)
	}
	if fam == "gemini" && strings.Contains(id, "argon") {
		return 85 + effortWeight(id)
	}
	if fam == "gemini" && tier == "pro" {
		return 70 + effortWeight(id)
	}
	return 0
}

func effortWeight(id string) int {
	switch {
	case strings.Contains(id, "-max"):
		return 6
	case strings.Contains(id, "-xhigh"):
		return 5
	case strings.Contains(id, "-high"):
		return 4
	case strings.Contains(id, "-medium"):
		return 2
	case strings.Contains(id, "-low"):
		return 1
	default:
		return 3
	}
}

func tokenizeRoleHint(raw string) []string {
	return strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
}

func hasSwarmRoleToken(tokens, exact, prefixes []string) bool {
	for _, tok := range tokens {
		for _, e := range exact {
			if tok == e {
				return true
			}
		}
		for _, p := range prefixes {
			if strings.HasPrefix(tok, p) {
				return true
			}
		}
	}
	return false
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
			return "💡 model tip: architecture/deep-debug turn on fast model — consider Orchestrator (Argon) or Carbon"
		}
	}
	if (strings.Contains(lower, "argon") || strings.Contains(lower, "opus")) && res.Intent == IntentTrivial {
		if markSessionSentinelOnce(st, convID, genID, turnNum, "__model_nudge__:fast") {
			return "💡 model tip: trivial turn on heavy reasoning model — Orchestrator or Hydrogen/Flash is faster"
		}
	}
	return ""
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
