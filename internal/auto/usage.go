package auto

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	floorFive = 50 // % of a pool's 5-hour window remaining (D1, row 7)
	floorWeek = 30 // % of its week remaining
	// disabled is a window `/usage` prints no number for. agy does so when
	// the week is out, and nothing says it means only that, so it reads as
	// under every floor.
	disabled = disabledWindow
)

func underPool(p Pool) bool { return p.Five < floorFive || p.Week < floorWeek }

func said(n int) string {
	if n == disabled {
		return "disabled"
	}
	return strconv.Itoa(n) + "%"
}

// budgetLine is the plan's failure-mode text, every pool on one line.
func budgetLine(pools []Pool) string {
	parts := make([]string, 0, len(pools))
	for _, p := range pools {
		parts = append(parts, fmt.Sprintf("%s %s of 5h, %s of week remaining", short(p.Name), said(p.Five), said(p.Week)))
	}
	return fmt.Sprintf("budget: %s; floors %d/%d", strings.Join(parts, ", "), floorFive, floorWeek)
}

// gateBudgetForModel is G9 over one `/usage` reading gating only the pool
// matching targetModel against [50% 5h / 30% week] (ADR-534, F-004). An empty
// targetModel gates all pools; an unreadable or missing target pool fails closed.
func gateBudgetForModel(runs int, targetModel, text string, readErr error) (line string, stop bool) {
	return gateBudgetForModelWith(defaultPorts.parseUsage, runs, targetModel, text, readErr)
}

func gateBudgetForModelWith(parseUsage func(string) ([]Pool, error), runs int, targetModel, text string, readErr error) (line string, stop bool) {
	if runs >= modelRunsPerDay {
		return fmt.Sprintf("G9 budget: %d of %d model runs today", runs, modelRunsPerDay), true
	}
	if readErr != nil {
		return "G9 budget: /usage unreadable: " + short(readErr.Error()), true
	}
	if parseUsage == nil {
		return "G9 budget: /usage unreadable: usage parser not configured", true
	}
	pools, err := parseUsage(text)
	if err != nil {
		return "G9 budget: /usage unreadable: " + short(err.Error()), true
	}
	matched := matchingPoolsForModel(pools, targetModel)
	if len(matched) == 0 {
		return fmt.Sprintf("G9 budget: target pool for %s unreadable (%s)", short(targetModel), budgetLine(pools)), true
	}
	for _, p := range matched {
		if underPool(p) {
			return "G9 " + budgetLine(pools), true
		}
	}
	return budgetLine(pools), false
}

func matchingPoolsForModel(pools []Pool, targetModel string) []Pool {
	fam := modelPoolFamily(targetModel)
	if fam == "" {
		return pools
	}
	var out []Pool
	for _, p := range pools {
		if poolMatchesFamily(p.Name, fam) {
			out = append(out, p)
		}
	}
	return out
}

func modelPoolFamily(targetModel string) string {
	low := strings.ToLower(strings.TrimSpace(targetModel))
	switch {
	case low == "":
		return ""
	case strings.Contains(low, "gemini"):
		return "gemini"
	case strings.Contains(low, "claude") || strings.Contains(low, "anthropic"):
		return "claude"
	case strings.Contains(low, "gpt") || strings.Contains(low, "openai") ||
		strings.HasPrefix(low, "o1") || strings.HasPrefix(low, "o3") || strings.HasPrefix(low, "o4"):
		return "gpt"
	default:
		return low
	}
}

func poolMatchesFamily(poolName, fam string) bool {
	low := strings.ToLower(poolName)
	switch fam {
	case "gemini":
		return strings.Contains(low, "gemini")
	case "claude":
		return strings.Contains(low, "claude") || strings.Contains(low, "anthropic")
	case "gpt":
		return strings.Contains(low, "gpt") || strings.Contains(low, "openai")
	default:
		return fam != "" && strings.Contains(low, fam)
	}
}

func targetPoolWeekQuotaWith(parseUsage func(string) ([]Pool, error), targetModel, text string, readErr error) (int, bool) {
	if readErr != nil || strings.TrimSpace(text) == "" || parseUsage == nil {
		return 0, false
	}
	pools, err := parseUsage(text)
	if err != nil {
		return 0, false
	}
	matched := matchingPoolsForModel(pools, targetModel)
	if len(matched) == 0 {
		return 0, false
	}
	minWeek := 101
	for _, p := range matched {
		if p.Week == disabled {
			return 0, false
		}
		if p.Week < minWeek {
			minWeek = p.Week
		}
	}
	return minWeek, minWeek <= 100
}

// adaptiveModelCooldown returns the per-run cooldown for target pool weekly
// remaining quota: 1h at >=70%, 2h at 50..69%, and 4h at 30..49% or unknown
// (ADR-534, D9).
func adaptiveModelCooldown(weekPct int, known bool) time.Duration {
	if !known {
		return modelRunCooldown
	}
	switch {
	case weekPct >= 70:
		return time.Hour
	case weekPct >= 50:
		return 2 * time.Hour
	default:
		return modelRunCooldown
	}
}
