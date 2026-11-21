package models

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// DisabledWindow is a `/usage` window with value "disabled".
	DisabledWindow = -1
	poolLimit      = 64
	poolsLimit     = 16
	quoteLimit     = 120

	// AnthropicJudgeFloorFive and AnthropicJudgeFloorWeek are the minimum
	// remaining quota percentages required to dispatch an Anthropic judge/verifier
	// before falling back to the equivalent-tier Gemini model.
	AnthropicJudgeFloorFive = 30
	AnthropicJudgeFloorWeek = 20
)

// Pool is one quota pool's remaining percentages from `agy -p "/usage"`.
type Pool struct {
	Name string `json:"name"`
	Five int    `json:"five"`
	Week int    `json:"week"`
}

var (
	percentRe  = regexp.MustCompile(`^[0-9]{1,3}%$`)
	numTokenRe = regexp.MustCompile(`\d+`)
)

// UsageProbeFunc and ListModelsFunc are package hooks overridden in unit tests.
var (
	UsageProbeFunc = RunUsage
	ListModelsFunc = ListModels
)

// ParseUsage reads `/usage`: one line per pool and window, four tab-separated
// fields. Anything else, a window missing or a line twice is an error.
func ParseUsage(text string) ([]Pool, error) {
	var pools []Pool
	index := map[string]int{}
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 4 || f[0] == "" || len(f[0]) > poolLimit {
			return nil, fmt.Errorf("a line that is not a pool's: %q", shortQuote(line))
		}
		value, err := parseUsageValue(f[2])
		if err != nil {
			return nil, err
		}
		var errPool error
		pools, errPool = recordPoolWindow(pools, index, seen, f[0], f[1], value)
		if errPool != nil {
			return nil, errPool
		}
	}
	if len(pools) == 0 {
		return nil, errors.New("no pool")
	}
	for _, p := range pools {
		if !seen[p.Name+"\tWeekly Limit Remaining"] || !seen[p.Name+"\tFive Hour Limit Remaining"] {
			return nil, fmt.Errorf("%q lacks a window", shortQuote(p.Name))
		}
	}
	return pools, nil
}

func recordPoolWindow(pools []Pool, index map[string]int, seen map[string]bool, name, window string, value int) ([]Pool, error) {
	i, ok := index[name]
	if !ok {
		i = len(pools)
		index[name] = i
		if len(pools) == poolsLimit {
			return nil, errors.New("more pools than any account has")
		}
		pools = append(pools, Pool{Name: name})
	}
	if key := name + "\t" + window; seen[key] {
		return nil, fmt.Errorf("%q twice", shortQuote(name+" "+window))
	}
	seen[name+"\t"+window] = true
	switch window {
	case "Weekly Limit Remaining":
		pools[i].Week = value
	case "Five Hour Limit Remaining":
		pools[i].Five = value
	default:
		return nil, fmt.Errorf("an unknown window: %q", shortQuote(window))
	}
	return pools, nil
}

func parseUsageValue(s string) (int, error) {
	if s == "disabled" {
		return DisabledWindow, nil
	}
	if !percentRe.MatchString(s) {
		return 0, fmt.Errorf("a value that is no percentage: %q", shortQuote(s))
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "%"))
	if err != nil || n > 100 {
		return 0, fmt.Errorf("a value that is no percentage: %q", shortQuote(s))
	}
	return n, nil
}

func shortQuote(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > quoteLimit {
		return s[:quoteLimit] + "…"
	}
	return s
}

// RunUsage executes `agy -p "/usage"` in probeWorkDir() with process-group cleanup.
func RunUsage(run context.Context, agy string) (string, error) {
	return runUsageForHome(run, "", agy)
}

func runUsageForHome(run context.Context, home, agy string) (string, error) {
	return runAgyProbe(run, home, agy, ModelsTimeout, 5*time.Second, "-p", "/usage")
}

func probeUsageForHome(ctx context.Context, home, agyPath string) (string, error) {
	if UsageProbeFunc == nil {
		return "", errors.New("usage probe unavailable")
	}
	if reflect.ValueOf(UsageProbeFunc).Pointer() == reflect.ValueOf(RunUsage).Pointer() {
		return runUsageForHome(ctx, home, agyPath)
	}
	return UsageProbeFunc(ctx, agyPath)
}

func listModelsForHomeOrHook(ctx context.Context, home, agyPath string) ([]string, error) {
	if ListModelsFunc == nil {
		return nil, errors.New("list models unavailable")
	}
	if reflect.ValueOf(ListModelsFunc).Pointer() == reflect.ValueOf(ListModels).Pointer() {
		return listModelsForHome(ctx, home, agyPath)
	}
	return ListModelsFunc(ctx, agyPath)
}

// ResolveRoleModelForHome resolves cross-family models dynamically from listed
// agy models without hardcoding model IDs, reading models.json from home (when
// non-empty) and persisting live /usage pools into <home>/.config/fyr/models.json.
func ResolveRoleModelForHome(ctx context.Context, home, agyPath, role, tier, explicitModel string) (string, string, error) {
	return resolveRoleModel(ctx, home, agyPath, role, tier, explicitModel, true)
}

func resolveRoleModel(ctx context.Context, home, agyPath, role, tier, explicitModel string, persistPools bool) (string, string, error) {
	if exp := strings.TrimSpace(explicitModel); exp != "" {
		return exp, "explicit model " + exp, nil
	}
	if strings.TrimSpace(agyPath) == "" {
		return "", "", errors.New("resolve role model: agy path required")
	}
	ids, err := listModelsForHomeOrHook(ctx, home, agyPath)
	if err != nil {
		return "", "", fmt.Errorf("agy models: %w", err)
	}
	if len(ids) == 0 {
		return "", "", errors.New("agy models listed no model")
	}
	entries, pools := buildListedCatalogEntriesAndPools(home, ids)
	pick, livePools, err := pickRoleFromCatalogWithPoolsForHome(ctx, home, Catalog{Entries: entries, Pools: pools}, agyPath, role, tier)
	if err != nil {
		return "", "", err
	}
	if persistPools && len(livePools) > 0 && home != "" {
		PersistCatalogPoolsBestEffort(home, livePools)
	}
	return pick.Model, pick.Reason, nil
}

// PickRoleFromCatalogWithPools selects an active model from cat for role+tier,
// reusing fresh cached pool quotas when present (<= DefaultPoolQuotaTTL) and
// otherwise probing live Anthropic quota via agyPath for judge/reviewer/verifier.
// Live pools parsed from `agy -p "/usage"` are returned for persistence.
func PickRoleFromCatalogWithPools(ctx context.Context, cat Catalog, agyPath, role, tier string) (RolePick, []Pool, error) {
	return pickRoleFromCatalogWithPoolsForHome(ctx, "", cat, agyPath, role, tier)
}

func pickRoleFromCatalogWithPoolsForHome(ctx context.Context, home string, cat Catalog, agyPath, role, tier string) (RolePick, []Pool, error) {
	return PickRoleFromCatalogForHome(ctx, cat, PickRoleOptions{
		Home:     home,
		AgyPath:  agyPath,
		QuotaTTL: DefaultPoolQuotaTTL,
	}, role, tier)
}

func probeAnthropicQuotaPools(ctx context.Context, home, agyPath, role, anthropicModel, geminiFallback string) (string, string, string, []Pool) {
	uText, err := probeUsageForHome(ctx, home, agyPath)
	if err != nil {
		fb := fmt.Sprintf("role=%s fallback to Gemini (%s): /usage unreadable (%s)", role, geminiFallback, shortQuote(err.Error()))
		return geminiFallback, fb, fb, nil
	}
	pools, err := ParseUsage(uText)
	if err != nil {
		fb := fmt.Sprintf("role=%s fallback to Gemini (%s): /usage parse error (%s)", role, geminiFallback, shortQuote(err.Error()))
		return geminiFallback, fb, fb, nil
	}
	model, reason, fb := evaluateAnthropicPools(pools, role, anthropicModel, geminiFallback, false)
	return model, reason, fb, pools
}

func isAnthropicEntryID(entries []CatalogEntry, modelID string) bool {
	for _, e := range entries {
		if e.ID == modelID {
			return isAnthropicFamily(e.Family) || strings.HasPrefix(strings.ToLower(e.ID), "claude")
		}
	}
	return strings.HasPrefix(strings.ToLower(modelID), "claude")
}

func findAnthropicPool(pools []Pool) (Pool, bool) {
	for _, p := range pools {
		low := strings.ToLower(p.Name)
		if strings.Contains(low, "claude") || strings.Contains(low, "anthropic") {
			return p, true
		}
	}
	return Pool{}, false
}

func formatPoolPct(n int) string {
	if n == DisabledWindow {
		return "disabled"
	}
	return strconv.Itoa(n) + "%"
}
