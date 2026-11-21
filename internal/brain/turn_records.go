package brain

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

const (
	turnRecordFilterSQL = `((kind = 'plan' AND status IN ('open', '')) OR (kind = 'memory' AND status IN ('closed', '')) OR (kind = 'bug' AND status IN ('open', '')) OR (kind = 'adr' AND status IN ('accepted', 'open', '')))`
	turnRecordAliasSQL  = `((r.kind = 'plan' AND r.status IN ('open', '')) OR (r.kind = 'memory' AND r.status IN ('closed', '')) OR (r.kind = 'bug' AND r.status IN ('open', '')) OR (r.kind = 'adr' AND r.status IN ('accepted', 'open', '')))`
)

// ListTurnRecords returns only active turn-relevant records (open plans, active
// memories, open bugs, accepted/open ADRs) for o.Dir plus closed global memories
// (or the global scope when o.Global is true), without creating or mutating
// brain.db (falling back to a temporary read-only copy only if a WAL blocks
// direct mode=ro).
func ListTurnRecords(o Options) ([]Record, error) {
	st, ver, err := openTurnStore(o.Home)
	if err != nil || st == nil || ver < 1 {
		if st != nil {
			_ = st.close()
		}
		return nil, err
	}
	defer st.close()
	q, args, ok := listTurnQuery(o, ver)
	if !ok {
		return nil, nil
	}
	rows, err := st.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("brain: %w", err)
	}
	defer rows.Close()
	out, err := scanRecordsRows(rows)
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return recordDescLess(out[i], out[j]) })
	return out, nil
}

// SearchTurnRecords runs FTS5 record search scoped only to active turn-relevant
// records, avoiding scans over rejected proposals and historical closed records.
func SearchTurnRecords(o Options, words []string, limit int) ([]Hit, error) {
	if limit <= 0 || len(words) == 0 {
		return nil, nil
	}
	st, ver, err := openTurnStore(o.Home)
	if err != nil || st == nil || ver < 1 {
		if st != nil {
			_ = st.close()
		}
		return nil, err
	}
	defer st.close()
	scopes, proj, ok, err := turnSearchScopes(st, o)
	if err != nil || !ok {
		return nil, err
	}
	return runTurnFTSPasses(st, o, words, limit, scopes, proj)
}

func openTurnStore(home string) (*store, int, error) {
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
	return openExistingTurnStore(home, path)
}

func openExistingTurnStore(home, path string) (*store, int, error) {
	walInfo, werr := os.Lstat(path + "-wal")
	walNonEmpty := werr == nil && walInfo.Size() > 0
	_, serr := os.Lstat(path + "-shm")
	shmAbsent := errors.Is(serr, os.ErrNotExist)
	if walNonEmpty && shmAbsent {
		return openStoreReadOnly(home)
	}
	s, ver, err := openTurnSQLite(turnReadOnlyDSN(path, false), path)
	if err == nil || IsRefusal(err) {
		return s, ver, err
	}
	if walNonEmpty {
		return openStoreReadOnly(home)
	}
	if shmAbsent {
		if s2, ver2, err2 := openTurnSQLite(turnReadOnlyDSN(path, true), path); err2 == nil || IsRefusal(err2) {
			return s2, ver2, err2
		}
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

func turnReadOnlyDSN(path string, immutable bool) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	q := "mode=ro&_pragma=busy_timeout(1000)"
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

func listTurnQuery(o Options, ver int) (string, []any, bool) {
	cols := selectRecordCols(ver)
	if o.Global {
		q := `SELECT ` + cols + ` FROM records WHERE scope = '' AND ` + turnRecordFilterSQL + ` ORDER BY date DESC, updated DESC, id DESC`
		return q, nil, true
	}
	sc, _, err := trail.Roots(o.Dir)
	if err != nil {
		return "", nil, false
	}
	q := `SELECT ` + cols + ` FROM records WHERE (scope = ? AND ` + turnRecordFilterSQL + `) OR (scope = '' AND kind = 'memory' AND status IN ('closed', '')) ORDER BY date DESC, updated DESC, id DESC`
	return q, []any{sc}, true
}

func turnSearchScopes(st *store, o Options) ([]string, string, bool, error) {
	if o.Global {
		scopes, err := searchScopes(st, o, "")
		return scopes, "", err == nil, err
	}
	sc, _, err := trail.Roots(o.Dir)
	if err != nil {
		return nil, "", false, nil
	}
	return []string{"", sc}, sc, true, nil
}

func runTurnFTSPasses(st *store, o Options, words []string, limit int, scopes []string, proj string) ([]Hit, error) {
	var hits []Hit
	seen := map[int64]bool{}
	for _, op := range []string{" ", " OR "} {
		if len(hits) >= limit || (op == " OR " && len(words) < 2) {
			break
		}
		q := search.FTSQuery(words, op)
		if q == "" {
			break
		}
		var err error
		hits, err = appendTurnFTSPass(st, o, q, scopes, proj, limit, seen, hits)
		if err != nil {
			return nil, err
		}
	}
	return hits, nil
}

func appendTurnFTSPass(st *store, o Options, q string, scopes []string, proj string, limit int, seen map[int64]bool, hits []Hit) ([]Hit, error) {
	sqlq, args := turnSearchQuery(o, q, scopes, limit*2)
	rows, err := st.db.Query(sqlq, args...)
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
		FROM records_fts JOIN records r ON r.rid = records_fts.rowid
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
