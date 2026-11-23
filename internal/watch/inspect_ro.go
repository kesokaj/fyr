package watch

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

const (
	roBusyTimeoutMS         = 50
	sqliteBusyCode          = 5
	sqliteLockedCode        = 6
	sqliteCantOpenCode      = 14
	sqliteReadonlyRecovery  = 264
	sqliteReadonlyCantInit  = 1288
	sqliteReadonlyDirectory = 1544
)

var openMetaCopy = search.OpenCopy

type sqliteCoder interface{ Code() int }

func watchReadOnlyDSN(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	u := url.URL{
		Scheme:   "file",
		Path:     filepath.ToSlash(path),
		RawQuery: fmt.Sprintf("mode=ro&_pragma=busy_timeout(%d)&_pragma=query_only(ON)", roBusyTimeoutMS),
	}
	return u.String()
}

func checkRegularDBFiles(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", path, search.ErrNotRegular)
	}
	for _, suf := range []string{"-wal", "-shm", "-journal"} {
		sfi, sErr := os.Lstat(path + suf)
		if errors.Is(sErr, os.ErrNotExist) {
			continue
		}
		if sErr != nil {
			return sErr
		}
		if sfi.Mode()&os.ModeSymlink != 0 || !sfi.Mode().IsRegular() {
			return fmt.Errorf("%s: %w", path+suf, search.ErrNotRegular)
		}
	}
	return nil
}

func openReadOnlySQLite(path string) (*sql.DB, error) {
	if err := checkRegularDBFiles(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", watchReadOnlyDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA query_only = ON"); err != nil {
		_ = db.Close()
		return nil, err
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&n); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func isSQLiteROFallbackErr(err error) bool {
	if err == nil {
		return false
	}
	if search.IsBusy(err) {
		return true
	}
	var se sqliteCoder
	if errors.As(err, &se) {
		ext := se.Code()
		c := ext & 0xff
		if c == sqliteBusyCode || c == sqliteLockedCode || c == sqliteCantOpenCode {
			return true
		}
		return ext == sqliteReadonlyRecovery || ext == sqliteReadonlyCantInit || ext == sqliteReadonlyDirectory
	}
	low := strings.ToLower(err.Error())
	for _, needle := range []string{
		"sqlite_busy", "sqlite_locked", "sqlite_cantopen",
		"database is locked", "database is busy", "unable to open database",
	} {
		if strings.Contains(low, needle) {
			return true
		}
	}
	return false
}

func openSearchMetaDB(scratch, tmp string) (*sql.DB, bool, error) {
	path := filepath.Join(scratch, "search.db")
	db, err := openReadOnlySQLite(path)
	if err == nil {
		return db, true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	if !isSQLiteROFallbackErr(err) || tmp == "" {
		return nil, false, fmt.Errorf("watch: open search.db: %w", err)
	}
	db, err = openMetaCopy(scratch, "search.db", tmp)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	if err != nil {
		return nil, false, fmt.Errorf("watch: copy search.db: %w", err)
	}
	return db, false, nil
}

type metricsEndedLookup struct {
	db    *sql.DB
	idCol string
}

func openMetricsEndedLookup(home string) *metricsEndedLookup {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	db, err := openReadOnlySQLite(filepath.Join(home, harness.StateDir, "metrics.db"))
	if err != nil {
		return nil
	}
	cols, err := tableColumns(db, "sessions")
	if err != nil || !cols["ended_at"] {
		_ = db.Close()
		return nil
	}
	idCol := pickMetricsIDCol(cols)
	if idCol == "" {
		_ = db.Close()
		return nil
	}
	return &metricsEndedLookup{db: db, idCol: idCol}
}

func pickMetricsIDCol(cols map[string]bool) string {
	for _, col := range []string{"id", "conversation_id", "session_id"} {
		if cols[col] {
			return col
		}
	}
	return ""
}

func (m *metricsEndedLookup) close() {
	if m != nil && m.db != nil {
		_ = m.db.Close()
	}
}

func (m *metricsEndedLookup) isEnded(convID string) bool {
	if m == nil || m.db == nil || convID == "" {
		return false
	}
	var ended any
	err := m.db.QueryRow("SELECT ended_at FROM sessions WHERE "+m.idCol+" = ? LIMIT 1", convID).Scan(&ended)
	if err != nil {
		return false
	}
	return isEndedTimestampSet(ended)
}

func isEndedTimestampSet(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case int64:
		return x > 0
	case float64:
		return x > 0
	case time.Time:
		return !x.IsZero() && x.Unix() > 0
	case string:
		return isNonEmptyEndedText(x)
	case []byte:
		return isNonEmptyEndedText(string(x))
	default:
		return false
	}
}

func isNonEmptyEndedText(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" || s == "0" {
		return false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n > 0
	}
	return true
}
