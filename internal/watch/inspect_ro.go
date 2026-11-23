package watch

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
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

func openSearchMetaDB(dir, scratch, tmp string) (*sql.DB, bool, error) {
	if err := trail.CheckScratchNoSymlinks(dir); err != nil {
		return nil, false, fmt.Errorf("watch: scratch path: %w", err)
	}
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
	if err := trail.CheckScratchNoSymlinks(dir); err != nil {
		return nil, false, fmt.Errorf("watch: scratch path: %w", err)
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

func checkStateDirNoSymlinks(home string) (string, error) {
	cur := filepath.Clean(home)
	fi, err := os.Lstat(cur)
	if err != nil {
		return "", err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return "", fmt.Errorf("%s: %w", cur, search.ErrNotRegular)
	}
	for _, seg := range strings.Split(filepath.ToSlash(harness.StateDir), "/") {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		sfi, serr := os.Lstat(cur)
		if serr != nil {
			return "", serr
		}
		if sfi.Mode()&os.ModeSymlink != 0 || !sfi.IsDir() {
			return "", fmt.Errorf("%s: %w", cur, search.ErrNotRegular)
		}
	}
	return cur, nil
}

type metricsEndedLookup struct {
	db         *sql.DB
	idCol      string
	hasEndedAt bool
	hasOutcome bool
}

func openMetricsEndedLookup(home string) *metricsEndedLookup {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	stateDir, err := checkStateDirNoSymlinks(home)
	if err != nil {
		return nil
	}
	db, err := openReadOnlySQLite(filepath.Join(stateDir, "metrics.db"))
	if err != nil {
		return nil
	}
	cols, err := tableColumns(db, "sessions")
	if err != nil || (!cols["ended_at"] && !cols["outcome"]) {
		_ = db.Close()
		return nil
	}
	idCol := pickMetricsIDCol(cols)
	if idCol == "" {
		_ = db.Close()
		return nil
	}
	return &metricsEndedLookup{
		db:         db,
		idCol:      idCol,
		hasEndedAt: cols["ended_at"],
		hasOutcome: cols["outcome"],
	}
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

func (m *metricsEndedLookup) lookup(convID string) (endedAt time.Time, hasEnded bool, outcome string, ok bool) {
	if m == nil || m.db == nil || convID == "" {
		return time.Time{}, false, "", false
	}
	endedExpr := "NULL"
	if m.hasEndedAt {
		endedExpr = "ended_at"
	}
	outcomeExpr := "''"
	if m.hasOutcome {
		outcomeExpr = "COALESCE(outcome, '')"
	}
	var rawEnded any
	var rawOutcome string
	q := "SELECT " + endedExpr + ", " + outcomeExpr + " FROM sessions WHERE " + m.idCol + " = ? LIMIT 1"
	if err := m.db.QueryRow(q, convID).Scan(&rawEnded, &rawOutcome); err != nil {
		return time.Time{}, false, "", false
	}
	endedAt, hasEnded = parseMetaTimestamp(rawEnded)
	return endedAt, hasEnded, strings.TrimSpace(rawOutcome), true
}

func (m *metricsEndedLookup) hasTerminalOutcome(convID string) bool {
	_, _, outcome, ok := m.lookup(convID)
	return ok && outcome != ""
}

func (m *metricsEndedLookup) isEnded(convID string, cutoff, lastAct time.Time, hasLastAct bool) bool {
	endedAt, hasEnded, outcome, ok := m.lookup(convID)
	if !ok {
		return false
	}
	if outcome != "" {
		return true
	}
	if !hasEnded || !endedAt.Before(cutoff) {
		return false
	}
	return !hasLastAct || !lastAct.After(endedAt)
}
