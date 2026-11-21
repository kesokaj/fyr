package search

import (
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
	case hasSwarmRoleToken(tokens, []string{"sec"}, []string{"review", "verif", "judge", "secur"}):
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
