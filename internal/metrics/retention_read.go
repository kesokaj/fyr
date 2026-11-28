package metrics

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

// IngestedThrough returns the latest ingested mtime recorded in metrics.db's
// ingest_state table for convID, or the zero time when absent or when
// metrics.db does not exist. It is strictly read-only and never creates or
// mutates metrics.db or its WAL/SHM sidecars.
func IngestedThrough(home, convID string) (time.Time, error) {
	if !metricsRetValidConvID(convID) {
		return time.Time{}, nil
	}
	dbPath := Path(home)
	if _, err := os.Lstat(dbPath); errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, nil
	} else if err != nil {
		return time.Time{}, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dbPath), "metrics-ret-")
	if err != nil {
		return time.Time{}, err
	}
	defer os.RemoveAll(tmp)
	db, err := search.OpenCopy(filepath.Dir(dbPath), filepath.Base(dbPath), tmp)
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	defer db.Close()
	return metricsRetQueryIngestMtime(db, metricsRetCandidateSources(home, convID))
}

func metricsRetValidConvID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}

func metricsRetCandidateSources(home, convID string) []string {
	rel := filepath.Join(".gemini", "antigravity-cli", "conversations", convID+".db")
	first := filepath.Join(home, rel)
	out := []string{first}
	if realHome, err := filepath.EvalSymlinks(home); err == nil {
		if alt := filepath.Join(realHome, rel); alt != first {
			out = append(out, alt)
		}
	}
	return out
}

func metricsRetQueryIngestMtime(db *sql.DB, sources []string) (time.Time, error) {
	var hasTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'ingest_state'`,
	).Scan(&hasTable); err != nil {
		return time.Time{}, err
	}
	if hasTable == 0 {
		return time.Time{}, nil
	}
	var best int64
	for _, src := range sources {
		var mtime sql.NullInt64
		err := db.QueryRow(`SELECT mtime FROM ingest_state WHERE source = ?`, src).Scan(&mtime)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return time.Time{}, err
		}
		if mtime.Valid && mtime.Int64 > best {
			best = mtime.Int64
		}
	}
	if best <= 0 {
		return time.Time{}, nil
	}
	return time.Unix(best, 0).UTC(), nil
}
