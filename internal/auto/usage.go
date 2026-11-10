package auto

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/work"
)

const (
	floorFive = 50 // % of a pool's 5-hour window remaining (D1, row 7)
	floorWeek = 30 // % of its week remaining
	// disabled is a window `/usage` prints no number for. agy does so when
	// the week is out, and nothing says it means only that, so it reads as
	// under every floor.
	disabled = work.DisabledWindow
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

// parseUsage reads `/usage`: one line per pool and window, four tab-separated
// fields. Anything else, a window missing or a line twice is an error, and the
// caller fails closed on it.
func parseUsage(text string) ([]Pool, error) {
	wp, err := work.ParseUsage(text)
	if err != nil {
		return nil, err
	}
	out := make([]Pool, len(wp))
	for i, p := range wp {
		out[i] = Pool{Name: p.Name, Five: p.Five, Week: p.Week}
	}
	return out, nil
}

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
		if underPool(p) {
			return "G9 " + budgetLine(pools), true
		}
	}
	return budgetLine(pools), false
}
