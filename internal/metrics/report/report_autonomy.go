package report

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// CountKeptAutoBranches counts local refs/heads/work/auto-* branches across
// candidate git repositories derived from baseRoots and registered projects.
func CountKeptAutoBranches(home, project string, includeSandboxes bool, baseRoots []string, isSandbox func(string, string) bool) int {
	roots := CandidateGitRoots(home, project, includeSandboxes, baseRoots, isSandbox)
	total := 0
	for _, root := range roots {
		out, err := trail.Command(root, "for-each-ref", "--format=%(refname:short)", "refs/heads/work/auto-*").Output()
		if err != nil {
			continue
		}
		total += countNonEmptyLines(string(out))
	}
	return total
}

func countNonEmptyLines(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// CandidateGitRoots returns sorted, deduplicated readable git main roots from
// baseRoots plus registered projects when project is empty.
func CandidateGitRoots(home, project string, includeSandboxes bool, baseRoots []string, isSandbox func(string, string) bool) []string {
	raw := append([]string(nil), baseRoots...)
	if project == "" && home != "" {
		raw = appendRegisteredRoots(raw, home, includeSandboxes, isSandbox)
	}
	return resolveReadableGitRoots(raw)
}

func appendRegisteredRoots(raw []string, home string, includeSandboxes bool, isSandbox func(string, string) bool) []string {
	projs, err := search.ListProjects(home)
	if err != nil {
		return raw
	}
	for _, p := range projs {
		trimmed := strings.TrimSpace(p.Root)
		if trimmed == "" {
			continue
		}
		clean := filepath.Clean(trimmed)
		if !includeSandboxes && isSandbox != nil && isSandbox(clean, home) {
			continue
		}
		raw = append(raw, clean)
	}
	return raw
}

func resolveReadableGitRoots(raw []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range raw {
		cleanMain, ok := readableGitMainRoot(r)
		if !ok || seen[cleanMain] {
			continue
		}
		seen[cleanMain] = true
		out = append(out, cleanMain)
	}
	sort.Strings(out)
	return out
}

func readableGitMainRoot(r string) (string, bool) {
	fi, err := os.Stat(r)
	if err != nil || !fi.IsDir() {
		return "", false
	}
	mainRoot, _, err := trail.Roots(r)
	if err != nil || mainRoot == "" || !trail.Readable(mainRoot) {
		return "", false
	}
	return filepath.Clean(mainRoot), true
}

// CountProposalsFromBrainDB counts queued/open vs closed proposal records in brain.db.
func CountProposalsFromBrainDB(home, project string) (queued, closed int) {
	db, cleanup, ok := OpenCopySQLiteFile(home, home, harness.StateDir, "brain.db")
	if !ok {
		return 0, 0
	}
	defer cleanup()
	cols := TableCols(db, "records")
	if !cols["kind"] || !cols["status"] || !cols["id"] {
		return 0, 0
	}
	q, args := buildProposalCountQuery(cols, project)
	rows, err := queryProposalStatuses(db, q, args)
	if err != nil {
		return 0, 0
	}
	for _, st := range rows {
		low := strings.ToLower(strings.TrimSpace(st))
		if low == "queued" || low == "open" {
			queued++
		} else {
			closed++
		}
	}
	return queued, closed
}

func buildProposalCountQuery(cols map[string]bool, project string) (string, []any) {
	q := `SELECT status FROM records WHERE (kind = 'proposal' OR id LIKE 'PROPOSAL-%')`
	proj := strings.TrimSpace(project)
	if proj == "" {
		return q, nil
	}
	clean := filepath.Clean(proj)
	if cols["scope"] && cols["project"] {
		return q + ` AND (scope = ? OR project = ?)`, []any{clean, clean}
	}
	if cols["scope"] {
		return q + ` AND scope = ?`, []any{clean}
	}
	return q, nil
}

func queryProposalStatuses(db *sql.DB, q string, args []any) ([]string, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func backgroundAutonomySection(s *Summary) reportSection {
	dr := s.Health.DreamRuns
	ar := s.Health.AutoRuns
	t := Table{
		Head: []string{"Metric", "Value"},
		Rows: [][]string{
			{"Dream runs (pass / fail)", fmt.Sprintf("%d (%d / %d)", dr.Runs, dr.Passed, dr.Failed)},
			{"Dream mean duration / outputs", fmt.Sprintf("%s (proposals:%d, ratings:%d)", FmtDurationMS(dr.Runs, dr.MeanDurationMS), dr.ProposalsWritten, dr.RatingsWritten)},
			{"Auto runs (pass / fail)", fmt.Sprintf("%d (%d / %d)", ar.Runs, ar.Passed, ar.Failed)},
			{"Auto workers run / applied commits", fmt.Sprintf("%d / %d", ar.WorkersRan, ar.Applied)},
			{"Session ratings (outcome / token efficiency)", fmt.Sprintf("%d (%s / %s)", s.A.RatingsCount, FmtFloatPtr(s.A.MeanOutcomeScore, "%.2f"), FmtFloatPtr(s.A.MeanTokenEfficiency, "%.2f"))},
			{"Kept work/auto-* branches", fmt.Sprint(s.Health.KeptAutoBranches)},
			{"PROPOSAL-* records (queued / closed)", fmt.Sprintf("%d / %d", s.Health.ProposalsQueued, s.Health.ProposalsClosed)},
		},
	}
	return reportSection{
		Title: "Background Autonomy",
		Note:  "Unattended dream and auto sweeps, session ratings, kept auto branches, and proposal queue.",
		Table: t,
	}
}

// FmtDurationMS formats a mean millisecond duration, or "n/a" when runs <= 0.
func FmtDurationMS(runs int, ms float64) string {
	if runs <= 0 {
		return "n/a"
	}
	if ms >= 1000 {
		return fmt.Sprintf("%.2fs", ms/1000.0)
	}
	return fmt.Sprintf("%.0fms", ms)
}
