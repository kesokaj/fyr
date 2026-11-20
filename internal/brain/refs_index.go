package brain

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/trail"
)

// RecordLinks summarizes bug, ADR, and active memory records naming a path.
type RecordLinks struct {
	Bugs     int      `json:"bugs"`
	ADRs     int      `json:"adrs"`
	Memories int      `json:"memories"`
	IDs      []string `json:"ids,omitempty"`
}

type candidateRecord struct {
	id   string
	kind string
	text string
}

// IndexRecordLinks opens brain.db strictly read-only and matches normalized
// repo-relative paths against bug, adr, and active memory records in the global
// and project scopes. Missing home or absent brain.db degrades cleanly.
func IndexRecordLinks(home, root string, paths []string) (map[string]RecordLinks, error) {
	out := map[string]RecordLinks{}
	if strings.TrimSpace(home) == "" {
		return out, nil
	}
	normPaths := normalizeIndexPaths(paths)
	st, ver, err := openStoreReadOnly(home)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	defer st.close()
	if len(normPaths) == 0 {
		return out, nil
	}
	project, top := resolveIndexScope(root)
	recs, err := loadLinkCandidates(st.db, ver, project, top)
	if err != nil {
		return nil, err
	}
	matchPathsToRecords(normPaths, recs, out)
	return out, nil
}

func normalizeIndexPaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var out []string
	for _, raw := range paths {
		p := strings.TrimSpace(raw)
		p = strings.TrimPrefix(filepath.ToSlash(p), "./")
		if p == "" {
			continue
		}
		clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(p)), "./")
		if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			continue
		}
		if !seen[clean] {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	sort.Strings(out)
	return out
}

func resolveIndexScope(root string) (project, top string) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return "", ""
	}
	if sc, tp, err := trail.Roots(trimmed); err == nil {
		return sc, tp
	}
	clean := filepath.Clean(trimmed)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	return clean, clean
}

func loadLinkCandidates(db *sql.DB, ver int, project, top string) ([]candidateRecord, error) {
	if ver >= 3 {
		return loadLinkCandidatesV3(db, project, top)
	}
	return loadLinkCandidatesLegacy(db, project)
}

func loadLinkCandidatesLegacy(db *sql.DB, project string) ([]candidateRecord, error) {
	const q = `SELECT id, kind, status, title, body FROM records
		WHERE (scope = '' OR scope = ?) AND kind IN ('bug', 'adr', 'memory')
		ORDER BY id`
	rows, err := db.Query(q, project)
	if err != nil {
		return nil, fmt.Errorf("brain: %w", err)
	}
	defer rows.Close()
	var out []candidateRecord
	for rows.Next() {
		var id, kind, status, title, body string
		if err := rows.Scan(&id, &kind, &status, &title, &body); err != nil {
			return nil, err
		}
		if kind == "memory" && status != "" && status != "closed" {
			continue
		}
		out = append(out, candidateRecord{id: id, kind: kind, text: title + "\n" + body})
	}
	return out, rows.Err()
}

func loadLinkCandidatesV3(db *sql.DB, project, top string) ([]candidateRecord, error) {
	feedbacks, err := queryFeedbacksDB(db, project)
	if err != nil {
		return nil, fmt.Errorf("brain: %w", err)
	}
	q := `SELECT ` + recordCols + ` FROM records
		WHERE (scope = '' OR scope = ?) AND kind IN ('bug', 'adr', 'memory')
		ORDER BY id`
	rows, err := db.Query(q, project)
	if err != nil {
		return nil, fmt.Errorf("brain: %w", err)
	}
	defer rows.Close()
	recs, err := scanRecordsRows(rows)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var out []candidateRecord
	for _, r := range recs {
		if r.Kind == "memory" && !isActiveMemoryForLinks(r, top, feedbacks, now) {
			continue
		}
		out = append(out, candidateRecord{id: r.ID, kind: r.Kind, text: r.Title + "\n" + r.Body})
	}
	return out, nil
}

func isActiveMemoryForLinks(r Record, top string, feedbacks []Record, now time.Time) bool {
	if r.Status != "" && r.Status != "closed" {
		return false
	}
	if r.Validity < MinActiveValidity {
		return false
	}
	val := ComputeMemoryValidity(top, r, CountMemoryContradictions(r, feedbacks), now)
	return val >= MinActiveValidity
}

func matchPathsToRecords(paths []string, recs []candidateRecord, out map[string]RecordLinks) {
	for _, p := range paths {
		var links RecordLinks
		seen := map[string]bool{}
		for _, r := range recs {
			if seen[r.id] || !strings.Contains(r.text, p) {
				continue
			}
			if _, ok := naming(r.text, p); !ok {
				continue
			}
			seen[r.id] = true
			links.IDs = append(links.IDs, r.id)
			switch r.kind {
			case "bug":
				links.Bugs++
			case "adr":
				links.ADRs++
			case "memory":
				links.Memories++
			}
		}
		if len(links.IDs) > 0 {
			sort.Strings(links.IDs)
			out[p] = links
		}
	}
}
