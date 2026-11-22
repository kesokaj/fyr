package models

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	// ContextKneePercent is the nominal pre-compaction context knee percentage.
	ContextKneePercent    = 60
	ContextKneeMinPercent = 40
	ContextKneeMaxPercent = 60

	// Empirical degradation onset caps (F-025): standard <=300k windows degrade
	// past 180k tokens; 500k-1M+ windows degrade past 300k tokens.
	StandardDegradationOnsetTokens int64 = 180000
	ExtendedDegradationOnsetTokens int64 = 300000

	// DefaultContextBudgetTokens is the conservative fallback pre-compaction
	// context budget when a model ID is empty or unrecognized.
	DefaultContextBudgetTokens int64 = 300000

	// DefaultContextKneeTokens is 60% of DefaultContextBudgetTokens.
	DefaultContextKneeTokens int64 = (DefaultContextBudgetTokens * ContextKneePercent) / 100

	extendedContextBudgetTokens int64 = 500000
	millionContextWindowTokens  int64 = 1000000
	gptOSSContextBudgetTokens   int64 = 128000
)

var (
	ctxHintSuffixRe = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?)\s*([km])\s*(?:tokens?\s*)?(?:context|ctx|window)\b`)
	ctxHintPrefixRe = regexp.MustCompile(`(?i)\b(?:context(?:\s+window)?|ctx)\s*[:=]?\s*(\d+(?:\.\d+)?)\s*([km])\b`)
)

// ParseContextWindowHint extracts an explicit context window token count
// (e.g. "1M context", "200K context window", "ctx: 128k") from text.
func ParseContextWindowHint(text string) int64 {
	for _, re := range []*regexp.Regexp{ctxHintSuffixRe, ctxHintPrefixRe} {
		m := re.FindStringSubmatch(text)
		if len(m) != 3 {
			continue
		}
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil || v <= 0 {
			continue
		}
		mult := 1000.0
		if strings.EqualFold(m[2], "m") {
			mult = 1_000_000.0
		}
		n := int64(math.Round(v * mult))
		if n >= 16_000 && n <= 10_000_000 {
			return n
		}
	}
	return 0
}

// DefaultContextWindow returns the raw nominal context window for modelID
// (1M defaults for Gemini 4+ and orchestrator models).
func DefaultContextWindow(modelID string) int64 {
	low := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(modelID)), "models/")
	if low == "" {
		return DefaultContextBudgetTokens
	}
	if strings.Contains(low, "orchestrator") {
		return millionContextWindowTokens
	}
	if strings.HasPrefix(low, "gemini-") {
		if strings.Contains(low, "flash-worker") {
			return millionContextWindowTokens
		}
		if nums := parseVersionNums(low); len(nums) > 0 && nums[0] >= 4 {
			return millionContextWindowTokens
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

// CalibratedContextKnee maps a context window or budget to its KNEE_EXCEEDED
// threshold using a [40%..60%] knee ratio capped at empirical degradation onset.
func CalibratedContextKnee(window int64) int64 {
	if window <= 0 {
		return DefaultContextKneeTokens
	}
	pct := kneePercentForWindow(window)
	knee := (window * pct) / 100
	capLimit := ExtendedDegradationOnsetTokens
	if window <= DefaultContextBudgetTokens {
		capLimit = StandardDegradationOnsetTokens
	}
	if knee > capLimit {
		return capLimit
	}
	return knee
}

func kneePercentForWindow(window int64) int64 {
	if window <= extendedContextBudgetTokens {
		return ContextKneeMaxPercent
	}
	if window >= millionContextWindowTokens {
		return ContextKneeMinPercent
	}
	span := millionContextWindowTokens - extendedContextBudgetTokens
	drop := ((window - extendedContextBudgetTokens) * (ContextKneeMaxPercent - ContextKneeMinPercent)) / span
	return ContextKneeMaxPercent - drop
}

// EffectiveContextBudget returns the effective pre-compaction token budget
// for modelID based on its family/generation or orchestrator wrapper.
func EffectiveContextBudget(modelID string) int64 {
	low := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(modelID)), "models/")
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
		id := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(e.ID)), "models/")
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
	low := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(modelID)), "models/")
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

// ContextKneeWithOverrides returns the calibrated KNEE_EXCEEDED token threshold
// for modelID using optional overrides from ContextWindowOverrides. Explicit
// operator overrides below 1M that differ from the model's default window keep
// the 60% override ratio; 1M+ physical windows and defaults are calibrated to
// [40%..60%] capped at empirical degradation onset (180k-300k).
func ContextKneeWithOverrides(modelID string, overrides map[string]int64) int64 {
	low := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(modelID)), "models/")
	if len(overrides) > 0 && low != "" {
		if v := overrides[low]; v > 0 && v < millionContextWindowTokens && v != DefaultContextWindow(low) {
			return (v * ContextKneePercent) / 100
		}
	}
	return CalibratedContextKnee(ContextBudgetWithOverrides(modelID, overrides))
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
