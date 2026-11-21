package brain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// StateExportOpts configures ExportStateRecords for portable NDJSON state sync.
type StateExportOpts struct {
	RepoID        string
	IncludeMemory bool
	Since         time.Time
}

// StateImportOpts configures ImportStateRecords for idempotent NDJSON state merge.
type StateImportOpts struct {
	RepoID        string
	LocalScope    string
	LocalRepoID   string
	IncludeMemory bool
	TrustOperator bool
	DryRun        bool
}

// StateImportStats summarizes one ImportStateRecords call.
type StateImportStats struct {
	Total             int `json:"total"`
	Inserted          int `json:"inserted"`
	Updated           int `json:"updated"`
	Skipped           int `json:"skipped"`
	Conflicts         int `json:"conflicts"`
	OperatorPreserved int `json:"operator_preserved"`
}

type sqlQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ExportStateRecords reads portable state records from brain.db under home
// without creating or mutating the store. By default only project trail records
// are exported; IncludeMemory adds memory, feedback, and proposal rows.
func ExportStateRecords(home string, opts StateExportOpts) ([]Record, error) {
	st, ver, err := openStoreReadOnly(home)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer st.close()
	if ver < 1 {
		return nil, nil
	}
	q := `SELECT ` + selectRecordCols(ver) + ` FROM records`
	if !opts.IncludeMemory {
		q += ` WHERE scope != '' AND kind NOT IN ` + unexportedSQL
	}
	rows, err := st.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all, err := scanRecordsRows(rows)
	if err != nil {
		return nil, err
	}
	return filterAndSortStateExport(all, opts), nil
}

func filterAndSortStateExport(all []Record, opts StateExportOpts) []Record {
	repoCache := map[string]string{}
	var out []Record
	for _, r := range all {
		fillMissingRepoID(&r, repoCache)
		if matchStateExport(r, opts) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RepoID != out[j].RepoID {
			return out[i].RepoID < out[j].RepoID
		}
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return idLess(out[i].ID, out[j].ID)
	})
	return out
}

func fillMissingRepoID(r *Record, cache map[string]string) {
	if r.RepoID != "" {
		return
	}
	key := r.Scope
	if key == "" {
		key = r.Project
	}
	if key == "" {
		return
	}
	id, ok := cache[key]
	if !ok {
		id = ResolveRepoID(key)
		cache[key] = id
	}
	r.RepoID = id
}

func matchStateExport(r Record, opts StateExportOpts) bool {
	if !opts.IncludeMemory && (unexported[r.Kind] || r.Scope == "") {
		return false
	}
	if opts.RepoID != "" && r.RepoID != opts.RepoID {
		return false
	}
	if !opts.Since.IsZero() {
		ts := parseRecordTimestamp(r)
		if ts.IsZero() || ts.Before(opts.Since) {
			return false
		}
	}
	return true
}

// ImportStateRecords merges recs into brain.db under home inside a single
// write transaction (or read-only simulation when opts.DryRun is true).
func ImportStateRecords(home string, recs []Record, opts StateImportOpts) (StateImportStats, error) {
	if strings.TrimSpace(home) == "" {
		return StateImportStats{}, refuse("brain: home directory is required")
	}
	eligible, err := filterStateImport(recs, opts)
	if err != nil || len(eligible) == 0 {
		return StateImportStats{Total: len(eligible)}, err
	}
	if opts.DryRun {
		return importStateDryRun(home, eligible, opts)
	}
	st, err := openStore(home)
	if err != nil {
		return StateImportStats{}, err
	}
	defer st.close()
	var stats StateImportStats
	err = st.tx(func(c *sql.Conn) error {
		var txErr error
		stats, txErr = applyStateImportTx(context.Background(), c, eligible, opts)
		return txErr
	})
	return stats, err
}

func filterStateImport(recs []Record, opts StateImportOpts) ([]Record, error) {
	var out []Record
	for _, r := range recs {
		if !knownKind(r.Kind) {
			return nil, refuse("state import: unknown kind %q", r.Kind)
		}
		if !opts.IncludeMemory && (unexported[r.Kind] || (r.Scope == "" && r.RepoID == "")) {
			continue
		}
		if opts.RepoID != "" && r.RepoID != opts.RepoID {
			continue
		}
		if !validStateRepoID(r.RepoID) {
			return nil, refuse("state import %s: invalid repo_id %q", r.ID, r.RepoID)
		}
		if !globalOnly[r.Kind] && r.RepoID == "" && !opts.TrustOperator && r.Scope != "" {
			return nil, refuse("state import %s: repo_id required for scoped record", r.ID)
		}
		if r.Kind == "memory" || r.Kind == "feedback" {
			if r.Provenance == "" {
				r.Provenance = "unknown"
			} else if r.Provenance == "operator" && !opts.TrustOperator {
				r.Provenance = "derived"
			}
		}
		if err := checkRecord(r); err != nil {
			return nil, refuse("state import %s: %v", r.ID, err)
		}
		out = append(out, r)
	}
	return out, nil
}

func validStateRepoID(id string) bool {
	if id == "" {
		return true
	}
	if len(id) < 6 || len(id) > 512 {
		return false
	}
	if !strings.HasPrefix(id, "git:") && !strings.HasPrefix(id, "path:") {
		return false
	}
	return !strings.ContainsAny(id, "\x00\r\n\\") && !strings.Contains(id, "..")
}

func applyStateImportTx(ctx context.Context, c *sql.Conn, eligible []Record, opts StateImportOpts) (StateImportStats, error) {
	stats := StateImportStats{Total: len(eligible)}
	scopeByRepo, err := loadRepoScopeMap(ctx, c, len(migrations))
	if err != nil {
		return StateImportStats{}, err
	}
	for _, r := range eligible {
		targetScope := resolveImportScope(r, scopeByRepo, opts)
		cur, rid, exists, err := lookupStateRow(ctx, c, targetScope, r.RepoID, r.ID, len(migrations))
		if err != nil {
			return StateImportStats{}, err
		}
		if exists {
			targetScope = cur.Scope
		}
		r.Scope = targetScope
		if r.Scope != "" && r.RepoID != "" && scopeByRepo[r.RepoID] == "" {
			scopeByRepo[r.RepoID] = r.Scope
		}
		apply, isInsert := decideStateMerge(cur, exists, r, &stats)
		if !apply {
			continue
		}
		if err := writeStateRow(ctx, c, rid, cur, isInsert, r); err != nil {
			return StateImportStats{}, err
		}
	}
	return stats, nil
}

func importStateDryRun(home string, eligible []Record, opts StateImportOpts) (StateImportStats, error) {
	stats := StateImportStats{Total: len(eligible)}
	st, ver, err := openStoreReadOnly(home)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return StateImportStats{}, err
	}
	var q sqlQuerier
	scopeByRepo := map[string]string{}
	if st != nil {
		defer st.close()
		q = st.db
		if scopeByRepo, err = loadRepoScopeMap(context.Background(), q, ver); err != nil {
			return StateImportStats{}, err
		}
	}
	overlay := map[string]Record{}
	for _, r := range eligible {
		if err := stepStateDryRun(q, ver, r, scopeByRepo, overlay, &stats, opts); err != nil {
			return StateImportStats{}, err
		}
	}
	return stats, nil
}

func stepStateDryRun(q sqlQuerier, ver int, r Record, scopeByRepo map[string]string, overlay map[string]Record, stats *StateImportStats, opts StateImportOpts) error {
	targetScope := resolveImportScope(r, scopeByRepo, opts)
	key := stateOverlayKey(targetScope, r.RepoID, r.ID)
	cur, exists := overlay[key]
	if !exists && q != nil && ver >= 1 {
		var err error
		cur, _, exists, err = lookupStateRow(context.Background(), q, targetScope, r.RepoID, r.ID, ver)
		if err != nil {
			return err
		}
		if exists {
			targetScope = cur.Scope
			key = stateOverlayKey(targetScope, r.RepoID, r.ID)
		}
	}
	r.Scope = targetScope
	if r.Scope != "" && r.RepoID != "" && scopeByRepo[r.RepoID] == "" {
		scopeByRepo[r.RepoID] = r.Scope
	}
	if apply, _ := decideStateMerge(cur, exists, r, stats); apply {
		overlay[key] = r
	} else if exists {
		overlay[key] = cur
	}
	return nil
}

func stateOverlayKey(scope, repoID, id string) string {
	if scope != "" && repoID != "" {
		return "repo:" + repoID + "\x00" + id
	}
	return "scope:" + scope + "\x00" + id
}

func resolveImportScope(r Record, scopeByRepo map[string]string, opts StateImportOpts) string {
	if globalOnly[r.Kind] {
		return ""
	}
	if r.RepoID != "" {
		if opts.LocalRepoID != "" && r.RepoID == opts.LocalRepoID && opts.LocalScope != "" {
			return opts.LocalScope
		}
		if local := scopeByRepo[r.RepoID]; local != "" {
			return local
		}
		return r.RepoID
	}
	if opts.TrustOperator && r.Scope != "" {
		return r.Scope
	}
	return ""
}

func loadRepoScopeMap(ctx context.Context, q sqlQuerier, ver int) (map[string]string, error) {
	out := map[string]string{}
	if ver < 4 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT repo_id, scope FROM records WHERE repo_id != '' AND scope != '' ORDER BY rid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var repoID, sc string
		if err := rows.Scan(&repoID, &sc); err != nil {
			return nil, err
		}
		if out[repoID] == "" {
			out[repoID] = sc
		}
	}
	return out, rows.Err()
}

func lookupStateRow(ctx context.Context, q sqlQuerier, targetScope, repoID, id string, ver int) (Record, int64, bool, error) {
	cols := `rid, ` + selectRecordCols(ver)
	var row *sql.Row
	if ver >= 4 && targetScope != "" && repoID != "" {
		row = q.QueryRowContext(ctx, `SELECT `+cols+` FROM records
			WHERE (scope != '' AND repo_id = ? AND id = ?) OR (scope = ? AND id = ?)
			ORDER BY CASE WHEN scope = ? THEN 0 ELSE 1 END, rid LIMIT 1`,
			repoID, id, targetScope, id, targetScope)
	} else {
		row = q.QueryRowContext(ctx, `SELECT `+cols+` FROM records WHERE scope = ? AND id = ? LIMIT 1`, targetScope, id)
	}
	var rid int64
	rec, err := scanRecordWithRID(row, &rid)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, 0, false, nil
	}
	if err != nil {
		return Record{}, 0, false, err
	}
	return rec, rid, true, nil
}

func scanRecordWithRID(row *sql.Row, rid *int64) (Record, error) {
	var r Record
	err := row.Scan(rid, &r.Scope, &r.RepoID, &r.ID, &r.Kind, &r.Status, &r.Date, &r.Title, &r.Body, &r.Project,
		&r.Provenance, &r.MemoryKind, &r.Validity, &r.VerifiedAt, &r.EvidenceCount, &r.SourceSessions, &r.Updated)
	if err == nil && (r.Kind == "memory" || r.Kind == "feedback") && r.Provenance == "" {
		r.Provenance = "unknown"
	}
	return r, err
}

func decideStateMerge(cur Record, exists bool, inc Record, stats *StateImportStats) (apply bool, isInsert bool) {
	if !exists {
		stats.Inserted++
		return true, true
	}
	if stateContentHash(cur) == stateContentHash(inc) {
		stats.Skipped++
		return false, false
	}
	tCur, tInc := parseTimeString(cur.Updated), parseTimeString(inc.Updated)
	incNewer := !tInc.IsZero() && (tCur.IsZero() || tInc.After(tCur))
	if cur.Provenance == "operator" {
		stats.OperatorPreserved++
		stats.Conflicts++
		stats.Skipped++
		return false, false
	}
	if incNewer {
		stats.Updated++
		return true, false
	}
	if !tInc.IsZero() && !tCur.IsZero() && tInc.Equal(tCur) {
		stats.Conflicts++
	}
	stats.Skipped++
	return false, false
}

func stateContentHash(r Record) [sha256.Size]byte {
	prov := r.Provenance
	if (r.Kind == "memory" || r.Kind == "feedback") && prov == "" {
		prov = "unknown"
	}
	s := fmt.Sprintf("%d:%s\n%d:%s\n%d:%s\n%d:%s\n%d:%s\n%d:%s\n",
		len(r.Kind), r.Kind, len(r.Status), r.Status, len(r.Title), r.Title,
		len(r.Body), r.Body, len(prov), prov, len(r.Updated), r.Updated)
	return sha256.Sum256([]byte(s))
}

func writeStateRow(ctx context.Context, c *sql.Conn, rid int64, cur Record, isInsert bool, r Record) error {
	memKind, val, verAt, evCount, sessions := importedMemoryFields(cur, !isInsert, r)
	if isInsert {
		_, err := c.ExecContext(ctx, `INSERT INTO records(scope, repo_id, id, kind, status, date, title, body, project, provenance, memory_kind, validity, verified_at, evidence_count, source_sessions, updated)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Scope, r.RepoID, r.ID, r.Kind, r.Status, r.Date, r.Title, r.Body, r.Project, r.Provenance,
			memKind, val, verAt, evCount, sessions, r.Updated)
		return err
	}
	repoID := r.RepoID
	if repoID == "" {
		repoID = cur.RepoID
	}
	_, err := c.ExecContext(ctx, `UPDATE records SET repo_id = ?, kind = ?, status = ?, date = ?, title = ?, body = ?, project = ?, provenance = ?, memory_kind = ?, validity = ?, verified_at = ?, evidence_count = ?, source_sessions = ?, updated = ? WHERE rid = ?`,
		repoID, r.Kind, r.Status, r.Date, r.Title, r.Body, r.Project, r.Provenance,
		memKind, val, verAt, evCount, sessions, r.Updated, rid)
	return err
}

func importedMemoryFields(cur Record, hasCur bool, r Record) (memKind string, val float64, verAt string, evCount int, sessions string) {
	if r.Kind != "memory" {
		v := r.Validity
		if v == 0 {
			v = 1.0
		}
		return "", v, "", 0, ""
	}
	memKind = r.MemoryKind
	if memKind == "" {
		memKind = resolveMemoryKind(r)
	}
	sessions = mergeSourceSessions(cur.SourceSessions, strings.Split(r.SourceSessions, ","))
	evCount = r.EvidenceCount
	if hasCur && cur.EvidenceCount > evCount {
		evCount = cur.EvidenceCount
	}
	val = r.Validity
	if val == 0 {
		val = memoryBaseValidity(r.Provenance, evCount)
	}
	verAt = r.VerifiedAt
	if verAt == "" {
		verAt = r.Updated
	}
	return memKind, val, verAt, evCount, sessions
}
