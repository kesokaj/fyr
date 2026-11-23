package brain

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

// MemoryShowOptions configures `MemoryShowWithOptions` and `MemoryShowJSONWithOptions`.
type MemoryShowOptions struct {
	IncludeLow bool
	Limit      int
}

// MemoryShow is the memory index, one line per active memory (>= 0.40), capped
// at what a session loads.
func MemoryShow(o Options) (string, error) {
	return MemoryShowWithOptions(o, MemoryShowOptions{})
}

// MemoryShowWithOptions returns the memory index formatted one line per card
// (with [STALE: ...] markers when stale), hiding cards with validity < 0.40
// unless showOpts.IncludeLow is true.
func MemoryShowWithOptions(o Options, showOpts MemoryShowOptions) (string, error) {
	mems, err := loadActiveMemories(o, showOpts)
	if err != nil {
		return "", err
	}
	lines := make([]string, len(mems))
	for i, m := range mems {
		lines[i] = formatMemoryIndexLineWithStale(m.rec, m.val, m.staleTag)
	}
	return capMemoryLines(lines), nil
}

// MemoryShowJSONWithOptions returns active memories (or all when IncludeLow is
// true) as a JSON array including the boolean stale field.
func MemoryShowJSONWithOptions(o Options, showOpts MemoryShowOptions) (string, error) {
	mems, err := loadActiveMemories(o, showOpts)
	if err != nil {
		return "", err
	}
	items := make([]MemorySummary, 0, len(mems))
	for _, m := range mems {
		r := m.rec
		prov := r.Provenance
		if prov == "" {
			prov = "unknown"
		}
		items = append(items, MemorySummary{
			ID:          search.Printable(r.ID),
			Name:        search.Printable(strings.TrimPrefix(r.ID, "MEMORY-")),
			Type:        search.Printable(resolveMemoryKind(r)),
			Description: search.Printable(r.Title),
			Provenance:  search.Printable(prov),
			Validity:    m.val,
			Stale:       m.staleTag != "",
			Global:      r.Scope == "",
			Updated:     search.Printable(r.Updated),
		})
	}
	return marshalIndentedJSON(items)
}

func loadActiveMemories(o Options, showOpts MemoryShowOptions) ([]scoredMemory, error) {
	s, err := memorySession(o)
	if err != nil {
		return nil, err
	}
	defer s.close()
	recs, feedbacks, err := loadShowMemories(s.st.db, s.scope)
	if err != nil {
		return nil, err
	}
	now := o.now()
	var mems []scoredMemory
	for _, r := range recs {
		staleTag := MemoryStalenessTag(s.top, r)
		val := computeMemoryValidityWithStale(r, staleTag != "", CountMemoryContradictions(r, feedbacks), now)
		if !showOpts.IncludeLow && val < MinActiveValidity {
			continue
		}
		mems = append(mems, scoredMemory{rec: r, val: val, staleTag: staleTag})
	}
	sortScoredMemories(mems)
	if showOpts.Limit > 0 && len(mems) > showOpts.Limit {
		mems = mems[:showOpts.Limit]
	}
	return mems, nil
}

func capMemoryLines(lines []string) string {
	var b strings.Builder
	for i, l := range lines {
		if i == memoryMaxLines || b.Len()+len(l)+1 > memoryMaxBytes {
			fmt.Fprintf(&b, "(… %d more: fyr record list --kind memory, then fyr record get MEMORY-<name>)\n", len(lines)-i)
			break
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}

type scoredMemory struct {
	rec      Record
	val      float64
	staleTag string
}

func sortScoredMemories(mems []scoredMemory) {
	sort.Slice(mems, func(i, j int) bool {
		a, b := mems[i], mems[j]
		if a.val != b.val {
			return a.val > b.val
		}
		ta, tb := memoryRefTime(a.rec), memoryRefTime(b.rec)
		if !ta.Equal(tb) {
			return ta.After(tb)
		}
		if a.rec.Date != b.rec.Date {
			return a.rec.Date > b.rec.Date
		}
		if a.rec.ID != b.rec.ID {
			return idLess(a.rec.ID, b.rec.ID)
		}
		return a.rec.Scope > b.rec.Scope
	})
}

func loadShowMemories(db *sql.DB, scope string) ([]Record, []Record, error) {
	rows, err := db.Query(`SELECT `+recordCols+` FROM records WHERE (scope = ? OR scope = '') AND kind = 'memory' AND status = 'closed' ORDER BY id`, scope)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	recs, err := scanRecordsRows(rows)
	if err != nil {
		return nil, nil, err
	}
	feedbacks, err := queryFeedbacksDB(db, scope)
	if err != nil {
		return nil, nil, err
	}
	return recs, feedbacks, nil
}

func feedbackQuery(scope string) (string, []any) {
	return feedbackQueryVer(len(migrations), scope)
}

func feedbackQueryVer(ver int, scope string) (string, []any) {
	q := `SELECT ` + selectRecordCols(ver) + ` FROM records WHERE kind = 'feedback'`
	if scope != "" {
		return q + ` AND (project = '' OR project = ?)`, []any{scope}
	}
	return q, nil
}

func queryFeedbacksDB(db *sql.DB, scope string) ([]Record, error) {
	return queryFeedbacksDBVer(db, len(migrations), scope)
}

func queryFeedbacksDBVer(db *sql.DB, ver int, scope string) ([]Record, error) {
	q, args := feedbackQueryVer(ver, scope)
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecordsRows(rows)
}

func queryFeedbacksConn(ctx context.Context, c *sql.Conn, scope string) ([]Record, error) {
	q, args := feedbackQuery(scope)
	rows, err := c.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecordsRows(rows)
}

func scanRecordsRows(rows *sql.Rows) ([]Record, error) {
	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
