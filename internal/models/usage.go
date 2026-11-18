package models

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
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
	ctx, cancel := context.WithTimeout(run, ModelsTimeout)
	defer cancel()
	dir := probeWorkDir()
	cmd := exec.CommandContext(ctx, agy, "-p", "/usage")
	cmd.Dir = dir
	cmd.Env = probeEnv(dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	stdout, stderr := &Capped{Over: ErrModelsOver}, &Capped{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if stdout.full {
		err = ErrModelsOver
	} else if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("no answer in %s", ModelsTimeout)
	}
	if err != nil {
		if l := LastLine(stderr.String()); l != "" {
			err = fmt.Errorf("%v: %s", err, l)
		}
		return "", err
	}
	return stdout.String(), nil
}

// ResolveRoleModel resolves cross-family models dynamically from listed agy models
// without hardcoding model IDs.
func ResolveRoleModel(ctx context.Context, agyPath, role, tier, explicitModel string) (string, string, error) {
	return resolveRoleModel(ctx, os.Getenv("HOME"), agyPath, role, tier, explicitModel, false)
}

// ResolveRoleModelForHome behaves like ResolveRoleModel, reading models.json from
// home (when non-empty) rather than os.Getenv("HOME") and persisting live /usage
// pools into <home>/.config/fyr/models.json when present.
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
	ids, err := ListModelsFunc(ctx, agyPath)
	if err != nil {
		return "", "", fmt.Errorf("agy models: %w", err)
	}
	if len(ids) == 0 {
		return "", "", errors.New("agy models listed no model")
	}
	entries := BuildListedCatalogEntries(home, ids)
	pick, pools, err := PickRoleFromCatalogWithPools(ctx, Catalog{Entries: entries}, agyPath, role, tier)
	if err != nil {
		return "", "", err
	}
	if persistPools && len(pools) > 0 && home != "" {
		PersistCatalogPoolsBestEffort(home, pools)
	}
	return pick.Model, pick.Reason, nil
}

// PickRoleFromCatalogWithPools selects an active model from cat for role+tier,
// probing live Anthropic quota via agyPath when choosing an Anthropic judge/reviewer/verifier,
// and also returns any quota pools parsed from a live `agy -p "/usage"` probe.
func PickRoleFromCatalogWithPools(ctx context.Context, cat Catalog, agyPath, role, tier string) (RolePick, []Pool, error) {
	normRole, normTier, err := NormalizeRoleTier(role, tier)
	if err != nil {
		return RolePick{}, nil, err
	}
	pick, err := PickFromEntries(cat.Entries, cat.Picks, normRole, normTier)
	if err != nil {
		return RolePick{}, nil, err
	}
	if (normRole != "judge" && normRole != "reviewer" && normRole != "verifier") || agyPath == "" {
		return pick, nil, nil
	}
	if !strings.HasPrefix(strings.ToLower(pick.Model), "claude-") {
		return pick, nil, nil
	}
	active := filterEntries(cat.Entries, func(e CatalogEntry) bool { return e.Status != "gone" })
	gem, _, _ := pickGeminiOrAny(active, nil, "", "builder", normTier)
	model, reason, fb, pools := probeAnthropicQuotaPools(ctx, agyPath, normRole, pick.Model, gem.ID)
	if fb == "" {
		fb = pick.FallbackReason
	}
	return RolePick{
		Role:           normRole,
		Tier:           normTier,
		Model:          model,
		Reason:         reason,
		FallbackReason: fb,
	}, pools, nil
}

func probeAnthropicQuotaPools(ctx context.Context, agyPath, role, anthropicModel, geminiFallback string) (string, string, string, []Pool) {
	uText, err := UsageProbeFunc(ctx, agyPath)
	if err != nil {
		fb := fmt.Sprintf("role=%s fallback to Gemini (%s): /usage unreadable (%s)", role, geminiFallback, shortQuote(err.Error()))
		return geminiFallback, fb, fb, nil
	}
	pools, err := ParseUsage(uText)
	if err != nil {
		fb := fmt.Sprintf("role=%s fallback to Gemini (%s): /usage parse error (%s)", role, geminiFallback, shortQuote(err.Error()))
		return geminiFallback, fb, fb, nil
	}
	pool, ok := findAnthropicPool(pools)
	if !ok {
		fb := fmt.Sprintf("role=%s fallback to Gemini (%s): no Anthropic/Claude pool in /usage", role, geminiFallback)
		return geminiFallback, fb, fb, pools
	}
	if pool.Week < AnthropicJudgeFloorWeek || pool.Five < AnthropicJudgeFloorFive {
		fb := fmt.Sprintf("role=%s fallback to Gemini (%s): %s quota low (5h=%s, week=%s; floor %d%%/%d%%)",
			role, geminiFallback, pool.Name, formatPoolPct(pool.Five), formatPoolPct(pool.Week), AnthropicJudgeFloorFive, AnthropicJudgeFloorWeek)
		return geminiFallback, fb, fb, pools
	}
	note := fmt.Sprintf("role=%s -> Anthropic (%s; %s 5h=%s, week=%s)", role, anthropicModel, pool.Name, formatPoolPct(pool.Five), formatPoolPct(pool.Week))
	return anthropicModel, note, "", pools
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

func effectivePassRate(ev ModelEvidence) (float64, bool) {
	wRuns := max(ev.WorkRuns, 0)
	eRuns := max(ev.EvalRuns, 0)
	runs := wRuns + eRuns
	if runs < 5 {
		return 0, false
	}
	wPass := min(max(ev.WorkPass, 0), wRuns)
	ePass := min(max(ev.EvalPass, 0), eRuns)
	combined := float64(wPass+ePass) / float64(runs)
	if wRuns >= 5 && wPass*2 < wRuns {
		workRate := float64(wPass) / float64(wRuns)
		return min(workRate, combined), true
	}
	return combined, true
}
