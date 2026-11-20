package metrics

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

func populateAutonomySignals(sum *Summary, home string, o ShowOptions, all []sess) {
	sum.Health.KeptAutoBranches = countKeptAutoBranches(home, o, all)
	if home != "" {
		q, c := countProposalsFromBrainDB(home, o.Project)
		sum.Health.ProposalsQueued = q
		sum.Health.ProposalsClosed = c
	}
}

func countKeptAutoBranches(home string, o ShowOptions, all []sess) int {
	roots := candidateGitRoots(home, o, all)
	total := 0
	for _, root := range roots {
		out, err := trail.Command(root, "for-each-ref", "--format=%(refname:short)", "refs/heads/work/auto-*").Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(line) != "" {
				total++
			}
		}
	}
	return total
}

func candidateGitRoots(home string, o ShowOptions, all []sess) []string {
	raw := distinctProjectRoots(o, home, all)
	if o.Project == "" && home != "" {
		if projs, err := search.ListProjects(home); err == nil {
			for _, p := range projs {
				clean := filepath.Clean(strings.TrimSpace(p.Root))
				if clean != "" && (o.IncludeSandboxes || !isSandboxProjectRoot(clean, home)) {
					raw = append(raw, clean)
				}
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range raw {
		fi, err := os.Stat(r)
		if err != nil || !fi.IsDir() {
			continue
		}
		mainRoot, _, err := trail.Roots(r)
		if err != nil || mainRoot == "" || !trail.Readable(mainRoot) {
			continue
		}
		cleanMain := filepath.Clean(mainRoot)
		if !seen[cleanMain] {
			seen[cleanMain] = true
			out = append(out, cleanMain)
		}
	}
	sort.Strings(out)
	return out
}

func countProposalsFromBrainDB(home, project string) (queued, closed int) {
	db, cleanup, ok := openCopySQLiteFile(home, home, harness.StateDir, "brain.db")
	if !ok {
		return 0, 0
	}
	defer cleanup()
	cols := tableCols(db, "records")
	if !cols["kind"] || !cols["status"] || !cols["id"] {
		return 0, 0
	}
	q, args := buildProposalCountQuery(cols, project)
	rows, err := queryStatuses(db, q, args)
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

func queryStatuses(db *sql.DB, q string, args []any) ([]string, error) {
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
	t := table{
		Head: []string{"Metric", "Value"},
		Rows: [][]string{
			{"Dream runs (pass / fail)", fmt.Sprintf("%d (%d / %d)", dr.Runs, dr.Passed, dr.Failed)},
			{"Dream mean duration / outputs", fmt.Sprintf("%s (proposals:%d, ratings:%d)", fmtDurationMS(dr.Runs, dr.MeanDurationMS), dr.ProposalsWritten, dr.RatingsWritten)},
			{"Auto runs (pass / fail)", fmt.Sprintf("%d (%d / %d)", ar.Runs, ar.Passed, ar.Failed)},
			{"Auto workers run / applied commits", fmt.Sprintf("%d / %d", ar.WorkersRan, ar.Applied)},
			{"Session ratings (outcome / token efficiency)", fmt.Sprintf("%d (%s / %s)", s.A.RatingsCount, fmtFloatPtr(s.A.MeanOutcomeScore, "%.2f"), fmtFloatPtr(s.A.MeanTokenEfficiency, "%.2f"))},
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

func fmtDurationMS(runs int, ms float64) string {
	if runs <= 0 {
		return "n/a"
	}
	if ms >= 1000 {
		return fmt.Sprintf("%.2fs", ms/1000.0)
	}
	return fmt.Sprintf("%.0fms", ms)
}
