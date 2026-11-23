package brain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

const (
	turnRecordAliasSQL      = `((r.kind = 'plan' AND r.status IN ('open', '')) OR (r.kind = 'memory' AND r.status IN ('closed', '')) OR (r.kind = 'bug' AND r.status IN ('open', '')) OR (r.kind = 'adr' AND r.status IN ('accepted', 'open', '')))`
	maxTurnFTSWords         = 8
	maxTurnPlanBodies       = 4
	maxTurnADRCandidates    = 8
	maxTurnMemoryCandidates = 8
)

var trivialTurnFTSWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true,
	"this": true, "that": true, "what": true, "when": true, "where": true,
	"which": true, "have": true, "has": true, "had": true, "yes": true,
	"okay": true, "all": true, "approved": true, "proceed": true, "continue": true,
	"done": true, "lgtm": true, "sgtm": true, "fix": true, "add": true,
	"update": true, "check": true, "test": true, "tests": true, "run": true,
	"file": true, "files": true, "code": true, "please": true, "thanks": true,
}

func filterTurnFTSWords(words []string) []string {
	if len(words) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(words))
	var out []string
	for _, raw := range words {
		w := strings.ToLower(strings.Trim(strings.TrimSpace(raw), ".,;:!?`'\"()[]{}"))
		if len(w) < 3 || trivialTurnFTSWords[w] || isAllASCIIDigits(w) || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) >= maxTurnFTSWords {
			break
		}
	}
	return out
}

func isAllASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

type turnMetaRow struct {
	rid int64
	rec Record
}

// ListTurnRecordsCtx returns only active turn-relevant records (open plans,
// active memories, open bugs, accepted/open ADRs) for o.Dir plus closed global
// memories (or the global scope when o.Global is true), without creating or
// mutating brain.db (falling back to a temporary read-only copy only if a WAL
// blocks direct mode=ro). It hydrates all active turn-relevant records so
// callers such as BrainRefs see full bodies across all active ADRs, open bugs,
// plans, and memories.
func ListTurnRecordsCtx(ctx context.Context, o Options) ([]Record, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	st, ver, err := openTurnStoreCtx(ctx, o.Home)
	if err != nil || st == nil || ver < 1 {
		if st != nil {
			_ = st.close()
		}
		return nil, err
	}
	defer st.close()
	scope, ok := resolveTurnScope(o)
	if !ok {
		return nil, nil
	}
	metaRows, err := queryTurnRecordMetaCtx(ctx, st, o, ver, scope)
	if err != nil {
		return nil, err
	}
	wantRIDs := make(map[int64]bool, len(metaRows))
	for _, row := range metaRows {
		wantRIDs[row.rid] = true
	}
	return hydrateTurnRecordsCtx(ctx, st, ver, metaRows, wantRIDs)
}

// SearchTurnRecordsCtx runs context-bounded FTS5 record search scoped only to
// active turn-relevant records, avoiding scans over rejected proposals and
// historical closed records.
func SearchTurnRecordsCtx(ctx context.Context, o Options, words []string, limit int) ([]Hit, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	meaningful := filterTurnFTSWords(words)
	if limit <= 0 || len(meaningful) == 0 {
		return nil, nil
	}
	st, ver, err := openTurnStoreCtx(ctx, o.Home)
	if err != nil || st == nil || ver < 1 {
		if st != nil {
			_ = st.close()
		}
		return nil, err
	}
	defer st.close()
	scope, ok := resolveTurnScope(o)
	if !ok {
		return nil, nil
	}
	scopes, proj, ok, err := turnSearchScopesForRoot(st, o, scope)
	if err != nil || !ok {
		return nil, err
	}
	hits, _, err := runTurnFTSPassesCtx(ctx, st, o, meaningful, limit, scopes, proj)
	return hits, err
}

// TurnLoad holds the records and FTS hits from a combined turn load along with
// whether every returned record had its full Body hydrated.
type TurnLoad struct {
	Records  []Record
	Hits     []Hit
	Complete bool
}

// LoadTurnDataCtx opens brain.db once read-only with ctx bounds, resolves
// trail.Roots once, projects metadata first, runs FTS5 (when meaningful words
// and limit are non-empty), fetches full Body for active plans, open bugs,
// FTS/title-matched rows, and active ADR/memory candidates, and reports in
// TurnLoad.Complete whether all active records were fully hydrated.
func LoadTurnDataCtx(ctx context.Context, o Options, words []string, limit int) (TurnLoad, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return TurnLoad{}, err
	}
	st, ver, err := openTurnStoreCtx(ctx, o.Home)
	if err != nil || st == nil || ver < 1 {
		if st != nil {
			_ = st.close()
		}
		return TurnLoad{}, err
	}
	defer st.close()
	scope, ok := resolveTurnScope(o)
	if !ok {
		return TurnLoad{}, nil
	}
	metaRows, err := queryTurnRecordMetaCtx(ctx, st, o, ver, scope)
	if err != nil {
		return TurnLoad{}, err
	}
	meaningful := filterTurnFTSWords(words)
	var hits []Hit
	var hitRIDs map[int64]bool
	if len(meaningful) > 0 && limit > 0 {
		scopes, proj, okScope, serr := turnSearchScopesForRoot(st, o, scope)
		if serr != nil {
			return TurnLoad{}, serr
		}
		if okScope {
			hits, hitRIDs, err = runTurnFTSPassesCtx(ctx, st, o, meaningful, limit, scopes, proj)
			if err != nil {
				return TurnLoad{}, err
			}
		}
	}
	wantRIDs := selectTurnBodyRIDs(metaRows, hitRIDs, words)
	recs, err := hydrateTurnRecordsCtx(ctx, st, ver, metaRows, wantRIDs)
	return TurnLoad{Records: recs, Hits: hits, Complete: len(wantRIDs) == len(metaRows)}, err
}

func resolveTurnScope(o Options) (string, bool) {
	if o.Global {
		return "", true
	}
	sc, _, err := trail.Roots(o.Dir)
	if err != nil {
		return "", false
	}
	return sc, true
}

func queryTurnRecordMetaCtx(ctx context.Context, st *store, o Options, ver int, scope string) ([]turnMetaRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q, args := listTurnMetaQueryForScope(o, ver, scope)
	rows, err := st.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("brain: %w", err)
	}
	defer rows.Close()
	var out []turnMetaRow
	for rows.Next() {
		var rid int64
		var r Record
		err := rows.Scan(&rid, &r.Scope, &r.ID, &r.Kind, &r.Status, &r.Date, &r.Title,
			&r.Provenance, &r.MemoryKind, &r.VerifiedAt, &r.EvidenceCount, &r.Updated)
		if err != nil {
			return nil, err
		}
		if (r.Kind == "memory" || r.Kind == "feedback") && r.Provenance == "" {
			r.Provenance = "unknown"
		}
		out = append(out, turnMetaRow{rid: rid, rec: r})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].rec.Date != out[j].rec.Date {
			return out[i].rec.Date > out[j].rec.Date
		}
		return out[i].rid > out[j].rid
	})
	return out, nil
}

func normalizeTurnTitleWords(words []string) []string {
	if len(words) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(words))
	var out []string
	for _, raw := range words {
		w := strings.ToLower(strings.Trim(strings.TrimSpace(raw), ".,;:!?`'\"()[]{}"))
		if len(w) < 4 || trivialTurnFTSWords[w] || isAllASCIIDigits(w) || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

func selectTurnBodyRIDs(rows []turnMetaRow, hitRIDs map[int64]bool, rawWords []string) map[int64]bool {
	words := normalizeTurnTitleWords(rawWords)
	want := make(map[int64]bool, len(hitRIDs)+maxTurnPlanBodies+maxTurnADRCandidates+maxTurnMemoryCandidates)
	plans, adrs, mems := 0, 0, 0
	for _, row := range rows {
		if hitRIDs[row.rid] || titleMatchesTurnWords(row.rec, words) {
			want[row.rid] = true
			continue
		}
		switch row.rec.Kind {
		case "bug":
			want[row.rid] = true
		case "plan":
			if plans < maxTurnPlanBodies {
				want[row.rid] = true
				plans++
			}
		case "adr":
			if adrs < maxTurnADRCandidates {
				want[row.rid] = true
				adrs++
			}
		case "memory":
			if mems < maxTurnMemoryCandidates {
				want[row.rid] = true
				mems++
			}
		}
	}
	return want
}

func titleMatchesTurnWords(r Record, words []string) bool {
	if len(words) == 0 {
		return false
	}
	hay := strings.ToLower(r.ID + " " + r.Title)
	for _, w := range words {
		if len(w) >= 4 && strings.Contains(hay, w) {
			return true
		}
	}
	return false
}

func hydrateTurnRecordsCtx(ctx context.Context, st *store, ver int, metaRows []turnMetaRow, wantRIDs map[int64]bool) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	full := make(map[int64]Record, len(wantRIDs))
	if len(wantRIDs) > 0 {
		if err := fetchFullTurnRecordsCtx(ctx, st, ver, wantRIDs, full); err != nil {
			return nil, err
		}
	}
	out := make([]Record, len(metaRows))
	for i, m := range metaRows {
		if rec, ok := full[m.rid]; ok {
			out[i] = rec
		} else {
			out[i] = m.rec
		}
	}
	sort.Slice(out, func(i, j int) bool { return recordDescLess(out[i], out[j]) })
	return out, nil
}

func fetchFullTurnRecordsCtx(ctx context.Context, st *store, ver int, wantRIDs map[int64]bool, dst map[int64]Record) error {
	rids := make([]int64, 0, len(wantRIDs))
	for rid := range wantRIDs {
		rids = append(rids, rid)
	}
	sort.Slice(rids, func(i, j int) bool { return rids[i] < rids[j] })
	cols := "rid, " + selectRecordCols(ver)
	const batch = 200
	for start := 0; start < len(rids); start += batch {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := rids[start:min(start+batch, len(rids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		q := `SELECT ` + cols + ` FROM records WHERE rid IN (` + strings.Repeat("?,", len(chunk)-1) + `?)`
		rows, err := st.db.QueryContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("brain: %w", err)
		}
		for rows.Next() {
			var rid int64
			var r Record
			err := rows.Scan(&rid, &r.Scope, &r.RepoID, &r.ID, &r.Kind, &r.Status, &r.Date, &r.Title, &r.Body, &r.Project, &r.Provenance,
				&r.MemoryKind, &r.Validity, &r.VerifiedAt, &r.EvidenceCount, &r.SourceSessions, &r.Updated)
			if err != nil {
				rows.Close()
				return err
			}
			if (r.Kind == "memory" || r.Kind == "feedback") && r.Provenance == "" {
				r.Provenance = "unknown"
			}
			dst[rid] = r
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func openTurnStoreCtx(ctx context.Context, home string) (*store, int, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, 0, ctx.Err()
	}
	if strings.TrimSpace(home) == "" {
		return nil, 0, nil
	}
	path := Path(home)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	} else if err != nil {
		return nil, 0, err
	}
	if err := checkRegularReadOnly(path); err != nil {
		return nil, 0, refuse("brain: %v", err)
	}
	return openExistingTurnStoreCtx(ctx, home, path)
}

func openExistingTurnStoreCtx(ctx context.Context, home, path string) (*store, int, error) {
	busyMS := 1000
	if ctx != nil {
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) <= 200*time.Millisecond {
			busyMS = 25
		}
	}
	walInfo, werr := os.Lstat(path + "-wal")
	walNonEmpty := werr == nil && walInfo.Size() > 0
	_, serr := os.Lstat(path + "-shm")
	shmAbsent := errors.Is(serr, os.ErrNotExist)
	if walNonEmpty && shmAbsent {
		return openStoreReadOnly(home)
	}
	s, ver, err := openTurnSQLite(turnReadOnlyDSN(path, false, busyMS), path)
	if err == nil || IsRefusal(err) {
		return s, ver, err
	}
	if !walNonEmpty && shmAbsent {
		if s2, ver2, err2 := openTurnSQLite(turnReadOnlyDSN(path, true, busyMS), path); err2 == nil || IsRefusal(err2) {
			return s2, ver2, err2
		}
	}
	if walNonEmpty {
		return openStoreReadOnly(home)
	}
	return nil, 0, err
}

func openTurnSQLite(dsn, path string) (*store, int, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, 0, err
	}
	db.SetMaxOpenConns(1)
	s := &store{db: db, path: path}
	ver, err := s.version(db)
	if err != nil {
		_ = db.Close()
		return nil, 0, err
	}
	if ver > len(migrations) {
		_ = db.Close()
		return nil, 0, refuse("brain.db schema %d is newer than this binary (%d); update fyr", ver, len(migrations))
	}
	return s, ver, nil
}

func turnReadOnlyDSN(path string, immutable bool, busyMS int) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	q := fmt.Sprintf("mode=ro&_pragma=busy_timeout(%d)", busyMS)
	if immutable {
		q += "&immutable=1"
	}
	u := url.URL{
		Scheme:   "file",
		Path:     filepath.ToSlash(path),
		RawQuery: q,
	}
	return u.String()
}

const turnMetaCols = `rid, scope, id, kind, status, date, title, provenance, memory_kind, verified_at, evidence_count, updated`

func turnMetaColsForVer(ver int) string {
	switch {
	case ver < 2:
		return `rid, scope, id, kind, status, date, title, '' AS provenance, '' AS memory_kind, '' AS verified_at, 0 AS evidence_count, updated`
	case ver < 3:
		return `rid, scope, id, kind, status, date, title, provenance, '' AS memory_kind, '' AS verified_at, 0 AS evidence_count, updated`
	default:
		return turnMetaCols
	}
}

func listTurnMetaQueryForScope(o Options, ver int, sc string) (string, []any) {
	cols := turnMetaColsForVer(ver)
	if o.Global || sc == "" {
		return turnScopeBranchesSQL(cols, "scope = ''"), nil
	}
	q := turnScopeBranchesSQL(cols, "scope = ?") +
		` UNION ALL SELECT ` + cols + ` FROM records WHERE scope = '' AND kind = 'memory' AND status IN ('closed', '')`
	return q, []any{sc, sc, sc, sc}
}

func turnScopeBranchesSQL(cols, scopePred string) string {
	return `SELECT ` + cols + ` FROM records WHERE ` + scopePred + ` AND kind = 'plan' AND status IN ('open', '')` +
		` UNION ALL SELECT ` + cols + ` FROM records WHERE ` + scopePred + ` AND kind = 'memory' AND status IN ('closed', '')` +
		` UNION ALL SELECT ` + cols + ` FROM records WHERE ` + scopePred + ` AND kind = 'bug' AND status IN ('open', '')` +
		` UNION ALL SELECT ` + cols + ` FROM records WHERE ` + scopePred + ` AND kind = 'adr' AND status IN ('accepted', 'open', '')`
}

func turnSearchScopesForRoot(st *store, o Options, sc string) ([]string, string, bool, error) {
	if o.Global {
		scopes, err := searchScopes(st, o, "")
		return scopes, "", err == nil, err
	}
	return []string{"", sc}, sc, true, nil
}

func runTurnFTSPassesCtx(ctx context.Context, st *store, o Options, words []string, limit int, scopes []string, proj string) ([]Hit, map[int64]bool, error) {
	var hits []Hit
	seen := map[int64]bool{}
	for _, op := range []string{" ", " OR "} {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if len(hits) >= limit || (op == " OR " && len(words) < 2) {
			break
		}
		q := search.FTSQuery(words, op)
		if q == "" {
			break
		}
		var err error
		hits, err = appendTurnFTSPassCtx(ctx, st, o, q, scopes, proj, limit, seen, hits)
		if err != nil {
			return nil, nil, err
		}
	}
	return hits, seen, nil
}

func appendTurnFTSPassCtx(ctx context.Context, st *store, o Options, q string, scopes []string, proj string, limit int, seen map[int64]bool, hits []Hit) ([]Hit, error) {
	sqlq, args := turnSearchQuery(o, q, scopes, limit*2)
	rows, err := st.db.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, fmt.Errorf("brain: %w", err)
	}
	defer rows.Close()
	for rows.Next() && len(hits) < limit {
		var h Hit
		var rid int64
		var snip string
		if err := rows.Scan(&rid, &h.Scope, &h.ID, &h.Kind, &h.Status, &h.Date, &h.Title, &h.Project, &snip); err != nil {
			return nil, err
		}
		if seen[rid] {
			continue
		}
		seen[rid] = true
		h.Snippet = search.CleanSnippet(snip)
		h.Label = label(h.Scope, proj, o.Global) + h.ID
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

func turnSearchQuery(o Options, q string, scopes []string, limit int) (string, []any) {
	sqlq := `SELECT r.rid, r.scope, r.id, r.kind, r.status, r.date, r.title, r.project,
		snippet(records_fts, 1, char(1), char(2), '…', 24)
		FROM records_fts CROSS JOIN records r ON r.rid = records_fts.rowid
		WHERE records_fts MATCH ?`
	args := []any{q}
	var f string
	f, args = scopeFilter(scopes, args)
	sqlq += f
	if o.Global {
		sqlq += ` AND ` + turnRecordAliasSQL
	} else {
		sqlq += ` AND ((r.scope != '' AND ` + turnRecordAliasSQL + `) OR (r.scope = '' AND r.kind = 'memory' AND r.status IN ('closed', '')))`
	}
	sqlq += ` ORDER BY bm25(records_fts, 2.0, 1.0) * (1 + 0.1 * min(1, max(0, 1 - (julianday(?) - julianday(r.date)) / 365.0))), r.id LIMIT ?`
	return sqlq, append(args, o.now().Format("2006-01-02"), limit)
}
