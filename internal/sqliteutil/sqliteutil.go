// Package sqliteutil centralizes SQLite driver registration, DSN formatting,
// error classification, busy-retry opening, and integer boolean helpers shared
// across internal/search, internal/metrics, and internal/brain (ADR-537).
package sqliteutil

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// OpenWait is the default busy-retry window when opening a SQLite store.
const OpenWait = 10 * time.Second

const (
	defaultBusyTimeoutMS = int(OpenWait / time.Millisecond)
	busyRetryInterval    = 50 * time.Millisecond
)

// RowQuerier matches *sql.DB, *sql.Tx, and *sql.Conn for single-row queries.
type RowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DSN returns the escaped read-write SQLite file: URI with WAL, NORMAL sync,
// foreign_keys(0), and the default OpenWait busy timeout.
func DSN(path string) string {
	return DSNWithBusyTimeout(path, defaultBusyTimeoutMS)
}

// DSNWithBusyTimeout returns the escaped read-write SQLite file: URI with WAL,
// NORMAL sync, foreign_keys(0), and the given busy_timeout in milliseconds
// (falling back to defaultBusyTimeoutMS when busyTimeoutMS < 0).
// Escaping prevents raw '?', '#', or '%' in a path from injecting pragmas.
func DSNWithBusyTimeout(path string, busyTimeoutMS int) string {
	if busyTimeoutMS < 0 {
		busyTimeoutMS = defaultBusyTimeoutMS
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	u := url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
		RawQuery: fmt.Sprintf(
			"_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(%d)&_pragma=foreign_keys(0)",
			busyTimeoutMS,
		),
	}
	return u.String()
}

func sqliteResultCode(err error) int {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code() & 0xff
	}
	return 0
}

// IsBusy reports whether err is SQLite's SQLITE_BUSY or SQLITE_LOCKED.
func IsBusy(err error) bool {
	c := sqliteResultCode(err)
	return c == sqlite3.SQLITE_BUSY || c == sqlite3.SQLITE_LOCKED
}

// IsUnreadable reports whether err is SQLite's SQLITE_CORRUPT or SQLITE_NOTADB.
func IsUnreadable(err error) bool {
	c := sqliteResultCode(err)
	return c == sqlite3.SQLITE_CORRUPT || c == sqlite3.SQLITE_NOTADB
}

// OpenDBWithRetry invokes openFn until it succeeds, returns a non-busy error,
// or exceeds timeout (defaulting to OpenWait when timeout <= 0).
func OpenDBWithRetry(timeout time.Duration, openFn func() (*sql.DB, error)) (*sql.DB, error) {
	if openFn == nil {
		return nil, errors.New("sqliteutil: nil open function")
	}
	if timeout <= 0 {
		timeout = OpenWait
	}
	deadline := time.Now().Add(timeout)
	for {
		db, err := openFn()
		if err == nil {
			return db, nil
		}
		if db != nil {
			_ = db.Close()
		}
		if !IsBusy(err) || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(busyRetryInterval)
	}
}

// BoolInt converts b to SQLite's 1/0 integer representation.
func BoolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// UserVersion reads SQLite's PRAGMA user_version from q.
func UserVersion(ctx context.Context, q RowQuerier) (int, error) {
	var v int
	if err := q.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}
