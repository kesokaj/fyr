package brain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

func setExportSHA(c *sql.Conn, scope, sha string) error {
	_, err := c.ExecContext(context.Background(), `INSERT INTO meta(scope, export_sha) VALUES (?, ?)
		ON CONFLICT(scope) DO UPDATE SET export_sha = excluded.export_sha`, scope, sha)
	return err
}

func scopeAndTop(root string) (scope, top string) {
	if s, t, err := trail.Roots(root); err == nil {
		return s, t
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = filepath.Clean(root)
	}
	return abs, abs
}

func hasScopeDBRecords(home, scope string) (bool, error) {
	if home == "" || scope == "" {
		return false, nil
	}
	if _, err := os.Lstat(Path(home)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	st, err := openStore(home)
	if err != nil {
		return false, err
	}
	defer st.close()
	var one int
	err = st.db.QueryRow(`SELECT 1 FROM records WHERE scope = ? AND kind NOT IN `+unexportedSQL+` LIMIT 1`, scope).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// HasRecords reports whether root has project trail records: either a regular
// .agents/trail.jsonl under a real .agents directory or exported-kind rows for
// root's scope in brain.db under home. It never creates brain.db.
func HasRecords(home, root string) (bool, error) {
	scope, top := scopeAndTop(root)
	if err := guardExportPath(top); err != nil {
		return false, nil
	}
	if fi, err := os.Lstat(trailPath(top)); err == nil {
		return fi.Mode().IsRegular(), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return hasScopeDBRecords(home, scope)
}

// ReadScope returns the exported-kind records for root's scope from brain.db
// under home when present, falling back to ReadTrail(top) when brain.db has
// none for the scope. It runs guardExportPath(top) first and never creates
// brain.db.
func ReadScope(home, root string) ([]Record, bool, error) {
	scope, top := scopeAndTop(root)
	if err := guardExportPath(top); err != nil {
		return nil, false, err
	}
	if home != "" {
		if _, err := os.Lstat(Path(home)); err == nil {
			st, err := openStore(home)
			if err != nil {
				return nil, false, err
			}
			rows, err := st.db.Query(`SELECT `+recordCols+` FROM records WHERE scope = ? AND kind NOT IN `+unexportedSQL, scope)
			if err != nil {
				st.close()
				return nil, false, err
			}
			var recs []Record
			for rows.Next() {
				r, err := scanRecord(rows)
				if err != nil {
					rows.Close()
					st.close()
					return nil, false, err
				}
				recs = append(recs, r)
			}
			err = rows.Err()
			rows.Close()
			st.close()
			if err != nil {
				return nil, false, err
			}
			if len(recs) > 0 {
				sort.Slice(recs, func(i, j int) bool { return idLess(recs[i].ID, recs[j].ID) })
				return recs, true, nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, false, err
		}
	}
	return ReadTrail(top)
}

// LastUpdated returns the newest updated timestamp of root's records in
// brain.db under home, or the mtime of <top>/.agents/trail.jsonl when brain.db
// holds no records for the scope. It never creates brain.db.
func LastUpdated(home, root string) (time.Time, bool, error) {
	scope, top := scopeAndTop(root)
	if err := guardExportPath(top); err != nil {
		return time.Time{}, false, err
	}
	if home != "" {
		if _, err := os.Lstat(Path(home)); err == nil {
			st, err := openStore(home)
			if err != nil {
				return time.Time{}, false, err
			}
			var latest sql.NullString
			err = st.db.QueryRow(`SELECT max(updated) FROM records WHERE scope = ?`, scope).Scan(&latest)
			st.close()
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return time.Time{}, false, err
			}
			if latest.Valid && latest.String != "" {
				t, err := time.Parse(time.RFC3339, latest.String)
				if err != nil {
					return time.Time{}, false, err
				}
				return t, true, nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return time.Time{}, false, err
		}
	}
	fi, err := os.Stat(trailPath(top))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}
	return fi.ModTime(), true, nil
}

// Export writes the checkout's exported-kind records from brain.db to
// <top>/.agents/trail.jsonl on demand (ADR-091) and returns the number of
// records written. When o.Repo is set, it reads o.Repo's scope from o.Home's
// brain.db read-only and writes <top>/.agents/trail.jsonl in o.Dir.
func Export(o Options) (int, error) {
	if o.Global {
		return 0, usage("fyr record export takes no --global; only a project has .agents/trail.jsonl")
	}
	if o.Repo != "" {
		return exportFromScope(o)
	}
	s, err := openSession(o, false)
	if err != nil {
		return 0, err
	}
	defer s.close()
	var count int
	err = s.st.tx(func(c *sql.Conn) error {
		n, err := s.exportConn(c)
		count = n
		return err
	})
	return count, err
}

func exportFromScope(o Options) (int, error) {
	scope, _, err := resolveProject(o.Repo)
	if err != nil {
		return 0, err
	}
	_, top, err := resolveProject(o.Dir)
	if err != nil {
		return 0, err
	}
	if err := guardExportPath(top); err != nil {
		return 0, err
	}
	recs, _, err := ReadScope(o.Home, scope)
	if err != nil {
		return 0, err
	}
	data, err := encodeTrail(recs)
	if err != nil {
		return 0, err
	}
	if err := harness.WriteAtomic(trailPath(top), data, 0o644); err != nil {
		return 0, err
	}
	return len(recs), nil
}

func (s *session) exportConn(c *sql.Conn) (int, error) {
	rows, err := c.QueryContext(context.Background(), `SELECT id, kind, status, date, title, updated, body
		FROM records WHERE scope = ? AND kind NOT IN `+unexportedSQL, s.scope)
	if err != nil {
		return 0, err
	}
	var recs []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.Kind, &r.Status, &r.Date, &r.Title, &r.Updated, &r.Body); err != nil {
			rows.Close()
			return 0, err
		}
		recs = append(recs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	data, err := encodeTrail(recs)
	if err != nil {
		return 0, err
	}
	current, ok, err := hashTrail(s.top)
	if err != nil {
		return 0, err
	}
	if !ok || current != sum(data) {
		if err := guardExportPath(s.top); err != nil {
			return 0, err
		}
		// 0644: a portable snapshot file written on demand.
		if err := harness.WriteAtomic(trailPath(s.top), data, 0o644); err != nil {
			return 0, err
		}
	}
	if err := setExportSHA(c, s.scope, sum(data)); err != nil {
		return 0, err
	}
	return len(recs), nil
}

// Sync loads <top>/.agents/trail.jsonl into brain.db and its FTS5 index on
// demand (ADR-091), replacing the scope's exported rows inside one write
// transaction while preserving memory rows, and returns the loaded records.
func Sync(o Options) ([]Record, error) {
	if o.Global {
		return nil, usage("fyr record sync takes no --global; only a project has .agents/trail.jsonl")
	}
	scope, top, err := resolveProject(o.Dir)
	if err != nil {
		return nil, err
	}
	if err := guardExportPath(top); err != nil {
		return nil, err
	}
	f, ok, err := openTrail(top)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, refuse("no .agents/trail.jsonl in %s: run `fyr record export` on the source machine first", top)
	}
	defer f.Close()
	h := sha256.New()
	recs, err := readRecords(io.TeeReader(&capped{r: f, left: maxTrail}, h))
	if err != nil {
		return nil, err
	}
	sha := hex.EncodeToString(h.Sum(nil))
	st, err := openStore(o.Home)
	if err != nil {
		return nil, err
	}
	defer st.close()
	err = st.tx(func(c *sql.Conn) error {
		ctx := context.Background()
		if _, err := c.ExecContext(ctx, `DELETE FROM records WHERE scope = ? AND kind NOT IN `+unexportedSQL, scope); err != nil {
			return err
		}
		for _, r := range recs {
			if _, err := c.ExecContext(ctx, `INSERT INTO records(scope, id, kind, status, date, title, body, project, updated)
				VALUES (?, ?, ?, ?, ?, ?, ?, '', ?)`, scope, r.ID, r.Kind, r.Status, r.Date, r.Title, r.Body, r.Updated); err != nil {
				return err
			}
		}
		return setExportSHA(c, scope, sha)
	})
	if err != nil {
		return nil, err
	}
	return recs, nil
}
