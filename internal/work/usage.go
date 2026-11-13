package work

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
	Name       string
	Five, Week int
}

var (
	percentRe  = regexp.MustCompile(`^[0-9]{1,3}%$`)
	numTokenRe = regexp.MustCompile(`\d+`)
)

var (
	usageProbeFunc = RunUsage
	listModelsFunc = listModels
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
		i, ok := index[f[0]]
		if !ok {
			i = len(pools)
			index[f[0]] = i
			if len(pools) == poolsLimit {
				return nil, errors.New("more pools than any account has")
			}
			pools = append(pools, Pool{Name: f[0]})
		}
		if key := f[0] + "\t" + f[1]; seen[key] {
			return nil, fmt.Errorf("%q twice", shortQuote(f[0]+" "+f[1]))
		}
		seen[f[0]+"\t"+f[1]] = true
		switch f[1] {
		case "Weekly Limit Remaining":
			pools[i].Week = value
		case "Five Hour Limit Remaining":
			pools[i].Five = value
		default:
			return nil, fmt.Errorf("an unknown window: %q", shortQuote(f[1]))
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

// RunUsage executes `agy -p "/usage"` in / with process-group cleanup.
func RunUsage(run context.Context, agy string) (string, error) {
	ctx, cancel := context.WithTimeout(run, modelsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, agy, "-p", "/usage")
	cmd.Dir = "/"
	cmd.Env = rootEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	stdout, stderr := &capped{over: errModelsOver}, &capped{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if stdout.full {
		err = errModelsOver
	} else if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("no answer in %s", modelsTimeout)
	}
	if err != nil {
		if l := lastLine(stderr.String()); l != "" {
			err = fmt.Errorf("%v: %s", err, l)
		}
		return "", err
	}
	return stdout.String(), nil
}

// ResolveRoleModel resolves cross-family models dynamically from listed agy models
// without hardcoding model IDs:
//   - "builder" / "worker": active Gemini (preferring Carbon, then newest in tier)
//   - "mechanical": active Gemini Flash
//   - "judge" / "verifier" / "reviewer": active Anthropic (Opus for pro, Sonnet for flash),
//     falling back to the equivalent-tier listed Gemini model when no Anthropic model is
//     listed or when Anthropic pool quota in `agy -p "/usage"` is < 20% weekly or < 30% 5h.
func ResolveRoleModel(ctx context.Context, agyPath, role, tier, explicitModel string) (string, string, error) {
	if exp := strings.TrimSpace(explicitModel); exp != "" {
		return exp, "explicit model " + exp, nil
	}
	if strings.TrimSpace(agyPath) == "" {
		return "", "", errors.New("resolve role model: agy path required")
	}
	ids, err := listModelsFunc(ctx, agyPath)
	if err != nil {
		return "", "", fmt.Errorf("agy models: %w", err)
	}
	if len(ids) == 0 {
		return "", "", errors.New("agy models listed no model")
	}
	entries := buildListedCatalogEntries(os.Getenv("HOME"), ids)
	pick, err := PickRoleFromCatalog(ctx, Catalog{Entries: entries}, agyPath, role, tier)
	if err != nil {
		return "", "", err
	}
	return pick.Model, pick.Reason, nil
}

// PickRoleFromCatalog selects an active model from cat for role+tier, probing
// live Anthropic quota via agyPath when choosing an Anthropic judge/reviewer/verifier.
func PickRoleFromCatalog(ctx context.Context, cat Catalog, agyPath, role, tier string) (RolePick, error) {
	normRole, normTier, err := NormalizeRoleTier(role, tier)
	if err != nil {
		return RolePick{}, err
	}
	pick, err := pickFromEntries(cat.Entries, cat.Picks, normRole, normTier)
	if err != nil {
		return RolePick{}, err
	}
	if (normRole != "judge" && normRole != "reviewer" && normRole != "verifier") || agyPath == "" {
		return pick, nil
	}
	if !strings.HasPrefix(strings.ToLower(pick.Model), "claude-") {
		return pick, nil
	}
	active := filterEntries(cat.Entries, func(e CatalogEntry) bool { return e.Status != "gone" })
	gem, _, _ := pickGeminiOrAny(active, nil, "", "builder", normTier)
	model, reason, fb := probeAnthropicQuota(ctx, agyPath, normRole, pick.Model, gem.ID)
	if fb == "" {
		fb = pick.FallbackReason
	}
	return RolePick{
		Role:           normRole,
		Tier:           normTier,
		Model:          model,
		Reason:         reason,
		FallbackReason: fb,
	}, nil
}

func probeAnthropicQuota(ctx context.Context, agyPath, role, anthropicModel, geminiFallback string) (string, string, string) {
	uText, err := usageProbeFunc(ctx, agyPath)
	if err != nil {
		fb := fmt.Sprintf("role=%s fallback to Gemini (%s): /usage unreadable (%s)", role, geminiFallback, shortQuote(err.Error()))
		return geminiFallback, fb, fb
	}
	pools, err := ParseUsage(uText)
	if err != nil {
		fb := fmt.Sprintf("role=%s fallback to Gemini (%s): /usage parse error (%s)", role, geminiFallback, shortQuote(err.Error()))
		return geminiFallback, fb, fb
	}
	pool, ok := findAnthropicPool(pools)
	if !ok {
		fb := fmt.Sprintf("role=%s fallback to Gemini (%s): no Anthropic/Claude pool in /usage", role, geminiFallback)
		return geminiFallback, fb, fb
	}
	if pool.Week < AnthropicJudgeFloorWeek || pool.Five < AnthropicJudgeFloorFive {
		fb := fmt.Sprintf("role=%s fallback to Gemini (%s): %s quota low (5h=%s, week=%s; floor %d%%/%d%%)",
			role, geminiFallback, pool.Name, formatPoolPct(pool.Five), formatPoolPct(pool.Week), AnthropicJudgeFloorFive, AnthropicJudgeFloorWeek)
		return geminiFallback, fb, fb
	}
	note := fmt.Sprintf("role=%s -> Anthropic (%s; %s 5h=%s, week=%s)", role, anthropicModel, pool.Name, formatPoolPct(pool.Five), formatPoolPct(pool.Week))
	return anthropicModel, note, ""
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
