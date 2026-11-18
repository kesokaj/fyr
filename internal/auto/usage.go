package auto

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/models"
)

const (
	floorFive = 50 // % of a pool's 5-hour window remaining (D1, row 7)
	floorWeek = 30 // % of its week remaining
	// disabled is a window `/usage` prints no number for. agy does so when
	// the week is out, and nothing says it means only that, so it reads as
	// under every floor.
	disabled = models.DisabledWindow
)

// Pool is one quota pool's remaining percentages.
type Pool = models.Pool

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
	pools, err := models.ParseUsage(text)
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
