package search

import (
	"fmt"
	"strings"

	"github.com/kesokaj/fyr/internal/models"
)

func formatSwarmPickReason(home, picked, fbNote string) string {
	label := formatModelTag(home, picked)
	if label == "" {
		label = strings.TrimSpace(picked)
	}
	reason := "[AUTO_SWARM_MODEL] selected " + label
	if fbNote != "" {
		reason += "; " + fbNote
	}
	return reason
}

func inferSwarmRole(args map[string]any) string {
	tokens := tokenizeRoleHint(stringArgCI(args, "name"))
	if len(tokens) == 0 {
		return "builder"
	}
	switch {
	case hasSwarmRoleToken(tokens, []string{"sec"}, []string{"review", "verif", "judge", "secur", "dream"}):
		return "judge"
	case hasSwarmRoleToken(tokens, []string{"doc", "docs", "document", "documents", "documentation"}, []string{"triv", "explor"}):
		return "mechanical"
	default:
		return "builder"
	}
}

func pickSwarmModel(home, role string) (string, string) {
	resolved := strings.TrimSpace(home)
	if resolved == "" {
		return "", ""
	}
	cat, ok, err := models.LoadCatalog(resolved)
	if err != nil || !ok || len(cat.Entries) == 0 {
		return "", ""
	}
	tier := "pro"
	if role == "mechanical" {
		tier = "flash"
	}
	pick, err := models.PickFromCatalog(cat, role, tier)
	if err != nil || pick.Model == "" || models.IsInteractiveOnlyID(pick.Model) {
		return "", ""
	}
	return pick.Model, pick.FallbackReason
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

func subagentCrossFamilyNotice(home, subagent, sessionModel string, upgraded bool) string {
	resolved := strings.TrimSpace(home)
	if resolved == "" {
		return ""
	}
	cat, ok, err := models.LoadCatalog(resolved)
	if err != nil || !ok || len(cat.Entries) == 0 {
		return ""
	}
	pick, err := models.PickFromCatalog(cat, crossFamilyCatalogRole(subagent), "pro")
	if err != nil || strings.TrimSpace(pick.Model) == "" {
		return ""
	}
	pickFam, pickTag := catalogPickFamilyAndTag(cat.Entries, pick.Model)
	if pickFam != "anthropic" {
		return ""
	}
	sessFam, sessOrch := catalogSessionFamilyAndOrch(cat.Entries, sessionModel)
	if !upgraded && !sessOrch && sessFam == "anthropic" {
		return ""
	}
	return fmt.Sprintf(
		"[CROSS_FAMILY_NOTICE] invoke_subagent only accepts tier enums (inherit/pro/flash) and cannot pin concrete cross-family model %s for %s; use workflows/review_parallel.py, fyr gate --judge, or swarm for concrete cross-family pinning",
		pickTag, subagent,
	)
}

func catalogPickFamilyAndTag(entries []models.CatalogEntry, modelID string) (string, string) {
	s := strings.TrimSpace(modelID)
	for _, e := range entries {
		if strings.EqualFold(e.ID, s) || (e.DisplayName != "" && strings.EqualFold(e.DisplayName, s)) {
			label := e.ID
			if e.DisplayName != "" && !strings.EqualFold(e.DisplayName, e.ID) {
				label = fmt.Sprintf("%s [%s]", e.ID, collapseOneLine(printable(e.DisplayName), 120))
			}
			if think := normalizeThinking(e.Effort, e.ID); think != "" {
				label = fmt.Sprintf("%s, thinking=%s", label, think)
			}
			return strings.ToLower(e.Family), label
		}
	}
	fam, _, _ := models.ClassifyModel(s, "")
	return fam, s
}

func catalogSessionFamilyAndOrch(entries []models.CatalogEntry, modelName string) (string, bool) {
	s := strings.TrimSpace(modelName)
	if s == "" {
		return "", false
	}
	for _, e := range entries {
		if strings.EqualFold(e.ID, s) || (e.DisplayName != "" && strings.EqualFold(e.DisplayName, s)) {
			orch := e.InteractiveOnly || e.Mode == "orchestrator" || e.Effort == "orchestrator" || models.IsInteractiveOnlyID(e.ID)
			return strings.ToLower(e.Family), orch
		}
	}
	fam, _, eff := models.ClassifyModel(s, "")
	return fam, models.IsInteractiveOnlyID(s) || eff == "orchestrator"
}

func crossFamilyCatalogRole(subagent string) string {
	switch strings.ToLower(strings.TrimSpace(subagent)) {
	case "reviewer":
		return "reviewer"
	case "verifier":
		return "verifier"
	default:
		return "judge"
	}
}
