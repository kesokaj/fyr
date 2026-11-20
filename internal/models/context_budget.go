package models

import "strings"

const (
	// ContextKneePercent is the share of effective pre-compaction context budget
	// above which sessions are flagged with KNEE_EXCEEDED and receive handoff warnings.
	ContextKneePercent = 60

	// DefaultContextBudgetTokens is the conservative fallback pre-compaction
	// context budget when a model ID is empty or unrecognized.
	DefaultContextBudgetTokens int64 = 300000

	// DefaultContextKneeTokens is 60% of DefaultContextBudgetTokens.
	DefaultContextKneeTokens int64 = (DefaultContextBudgetTokens * ContextKneePercent) / 100

	extendedContextBudgetTokens int64 = 500000
	gptOSSContextBudgetTokens   int64 = 128000
)

// EffectiveContextBudget returns the effective pre-compaction token budget
// for modelID based on its family/generation or orchestrator wrapper.
func EffectiveContextBudget(modelID string) int64 {
	low := strings.ToLower(strings.TrimSpace(modelID))
	if low == "" {
		return DefaultContextBudgetTokens
	}
	if strings.HasPrefix(low, "orchestrator(") && strings.HasSuffix(low, ")") {
		return orchestratorCompositeBudget(low[len("orchestrator("):len(low)-1], nil)
	}
	if strings.Contains(low, "orchestrator") {
		return extendedContextBudgetTokens
	}
	if strings.HasPrefix(low, "gemini-") {
		if strings.Contains(low, "flash-worker") {
			return extendedContextBudgetTokens
		}
		if nums := parseVersionNums(low); len(nums) > 0 && nums[0] >= 4 {
			return extendedContextBudgetTokens
		}
		return DefaultContextBudgetTokens
	}
	if strings.HasPrefix(low, "claude-") {
		if nums := parseVersionNums(low); len(nums) > 0 && nums[0] >= 5 {
			return extendedContextBudgetTokens
		}
		return DefaultContextBudgetTokens
	}
	if strings.HasPrefix(low, "gpt-") {
		return gptOSSContextBudgetTokens
	}
	return DefaultContextBudgetTokens
}

func orchestratorCompositeBudget(inner string, overrides map[string]int64) int64 {
	best := int64(0)
	for _, part := range strings.Split(inner, "+") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		if b := ContextBudgetWithOverrides(part, overrides); b > best {
			best = b
		}
	}
	if best <= 0 {
		return DefaultContextBudgetTokens
	}
	return best
}

// ContextWindowOverrides loads explicit per-model context_window overrides
// from <home>/.config/fyr/models.json keyed by lowercase model ID.
func ContextWindowOverrides(home string) map[string]int64 {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	cat, ok, err := LoadCatalog(home)
	if err != nil || !ok || len(cat.Entries) == 0 {
		return nil
	}
	var out map[string]int64
	for _, e := range cat.Entries {
		if e.ContextWindow <= 0 {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(e.ID))
		if id == "" {
			continue
		}
		if out == nil {
			out = make(map[string]int64)
		}
		out[id] = e.ContextWindow
	}
	return out
}

// ContextBudgetWithOverrides resolves modelID's token budget using overrides
// first and falling back to EffectiveContextBudget.
func ContextBudgetWithOverrides(modelID string, overrides map[string]int64) int64 {
	low := strings.ToLower(strings.TrimSpace(modelID))
	if len(overrides) > 0 && low != "" {
		if v := overrides[low]; v > 0 {
			return v
		}
	}
	if strings.HasPrefix(low, "orchestrator(") && strings.HasSuffix(low, ")") {
		return orchestratorCompositeBudget(low[len("orchestrator("):len(low)-1], overrides)
	}
	return EffectiveContextBudget(low)
}

// ContextKneeWithOverrides returns the 60% KNEE_EXCEEDED token threshold for
// modelID using optional overrides from ContextWindowOverrides.
func ContextKneeWithOverrides(modelID string, overrides map[string]int64) int64 {
	return (ContextBudgetWithOverrides(modelID, overrides) * ContextKneePercent) / 100
}

// ContextKneeTokens returns the 60% KNEE_EXCEEDED token threshold for modelID.
func ContextKneeTokens(modelID string) int64 {
	return ContextKneeWithOverrides(modelID, nil)
}

// ContextKneeTokensForHome resolves the 60% KNEE_EXCEEDED token threshold for
// modelID including any models.json context_window overrides under home.
func ContextKneeTokensForHome(home, modelID string) int64 {
	return ContextKneeWithOverrides(modelID, ContextWindowOverrides(home))
}
