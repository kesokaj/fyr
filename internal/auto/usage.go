package auto

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	floorFive = 50 // % of a pool's 5-hour window remaining (D1, row 7)
	floorWeek = 30 // % of its week remaining
	// disabled is a window `/usage` prints no number for. agy does so when
	// the week is out, and nothing says it means only that, so it reads as
	// under every floor.
	disabled = -1
	// poolLimit is far past any pool's name; budgetLine goes in runs.json.
	poolLimit = 64
	// poolsLimit keeps budgetLine short for the same reason.
	poolsLimit = 16
)

// Pool is one quota pool's remaining percentages.
type Pool struct {
	Name       string
	Five, Week int
}

var percent = regexp.MustCompile(`^[0-9]{1,3}%$`)

// parseUsage reads `/usage`: one line per pool and window, four tab-separated
// fields. Anything else, a window missing or a line twice is an error, and the
// caller fails closed on it.
func parseUsage(text string) ([]Pool, error) {
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
			return nil, fmt.Errorf("a line that is not a pool's: %q", short(line))
		}
		value, err := usageValue(f[2])
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
			return nil, fmt.Errorf("%q twice", short(f[0]+" "+f[1]))
		} else {
			seen[key] = true
		}
		switch f[1] {
		case "Weekly Limit Remaining":
			pools[i].Week = value
		case "Five Hour Limit Remaining":
			pools[i].Five = value
		default:
			return nil, fmt.Errorf("an unknown window: %q", short(f[1]))
		}
	}
	if len(pools) == 0 {
		return nil, errors.New("no pool")
	}
	for _, p := range pools {
		if !seen[p.Name+"\tWeekly Limit Remaining"] || !seen[p.Name+"\tFive Hour Limit Remaining"] {
			return nil, fmt.Errorf("%q lacks a window", short(p.Name))
		}
	}
	return pools, nil
}

func usageValue(s string) (int, error) {
	if s == "disabled" {
		return disabled, nil
	}
	if !percent.MatchString(s) {
		return 0, fmt.Errorf("a value that is no percentage: %q", short(s))
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "%"))
	if err != nil || n > 100 {
		return 0, fmt.Errorf("a value that is no percentage: %q", short(s))
	}
	return n, nil
}

func (p Pool) under() bool { return p.Five < floorFive || p.Week < floorWeek }

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

// gateBudget is G9 over one `/usage` reading: the verdict, and whether it
// stops a model task.
func gateBudget(runs int, text string, readErr error) (line string, stop bool) {
	if runs >= modelRunsPerDay {
		return fmt.Sprintf("G9 budget: %d of %d model runs today", runs, modelRunsPerDay), true
	}
	if readErr != nil {
		return "G9 budget: /usage unreadable: " + short(readErr.Error()), true
	}
	pools, err := parseUsage(text)
	if err != nil {
		return "G9 budget: /usage unreadable: " + short(err.Error()), true
	}
	for _, p := range pools {
		if p.under() {
			return "G9 " + budgetLine(pools), true
		}
	}
	return budgetLine(pools), false
}
