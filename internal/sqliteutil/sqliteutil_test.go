package sqliteutil

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDSN(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tricky := filepath.Join(dir, "dir?a=1#frag%20", "db.sqlite")
	got := DSN(tricky)
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if u.Scheme != "file" {
		t.Fatalf("scheme = %q, want file", u.Scheme)
	}
	if !strings.HasSuffix(u.Path, "dir?a=1#frag%20/db.sqlite") {
		t.Fatalf("decoded path = %q, lost special characters", u.Path)
	}
	for _, want := range []string{
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=busy_timeout(10000)",
		"_pragma=foreign_keys(0)",
	} {
		if !strings.Contains(u.RawQuery, want) {
			t.Errorf("DSN query %q missing %q", u.RawQuery, want)
		}
	}
	if custom := DSNWithBusyTimeout(tricky, 250); !strings.Contains(custom, "_pragma=busy_timeout(250)") {
		t.Errorf("DSNWithBusyTimeout(250) = %q", custom)
	}
	if fallback := DSNWithBusyTimeout(tricky, -5); !strings.Contains(fallback, "_pragma=busy_timeout(10000)") {
		t.Errorf("DSNWithBusyTimeout(-5) = %q, want default 10000", fallback)
	}
}

func makeBusyError(t *testing.T) error {
	t.Helper()
	p := filepath.Join(t.TempDir(), "lock.db")
	db1, err := sql.Open("sqlite", DSNWithBusyTimeout(p, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer db1.Close()
	if _, err := db1.Exec(`CREATE TABLE t(x INT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db1.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db1.Exec(`ROLLBACK`) }()

	db2, err := sql.Open("sqlite", DSNWithBusyTimeout(p, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	_, busyErr := db2.Exec(`INSERT INTO t VALUES (1)`)
	if busyErr == nil {
		t.Fatal("expected SQLITE_BUSY error while EXCLUSIVE lock held")
	}
	return busyErr
}

func TestIsBusyAndIsUnreadable(t *testing.T) {
	t.Parallel()
	busyErr := makeBusyError(t)
	if !IsBusy(busyErr) {
		t.Fatalf("IsBusy(%v) = false, want true", busyErr)
	}
	if !IsBusy(fmt.Errorf("wrapped: %w", busyErr)) {
		t.Fatal("IsBusy(wrapped) = false, want true")
	}
	if IsUnreadable(busyErr) {
		t.Fatalf("IsUnreadable(busyErr) = true, want false")
	}

	// Must-fail cases on non-SQLite errors.
	for _, err := range []error{nil, errors.New("boom"), sql.ErrNoRows, fmt.Errorf("wrap: %w", sql.ErrNoRows)} {
		if IsBusy(err) {
			t.Errorf("IsBusy(%v) = true, want false", err)
		}
		if IsUnreadable(err) {
			t.Errorf("IsUnreadable(%v) = true, want false", err)
		}
	}

	// Positive case for IsUnreadable: non-SQLite file content triggers SQLITE_NOTADB.
	junk := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(junk, []byte("this is definitely not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", DSN(junk))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	corruptErr := db.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n)
	if !IsUnreadable(corruptErr) {
		t.Fatalf("IsUnreadable(%v) = false, want true", corruptErr)
	}
	if IsBusy(corruptErr) {
		t.Fatalf("IsBusy(corruptErr) = true, want false")
	}
}

func TestOpenDBWithRetry(t *testing.T) {
	t.Parallel()
	// Negative case: nil openFn.
	if _, err := OpenDBWithRetry(10*time.Millisecond, nil); err == nil {
		t.Fatal("nil openFn succeeded, want error")
	}

	// Negative case: non-busy error stops immediately without retry.
	sentinel := errors.New("non-busy failure")
	calls := 0
	if _, err := OpenDBWithRetry(200*time.Millisecond, func() (*sql.DB, error) {
		calls++
		return nil, sentinel
	}); !errors.Is(err, sentinel) || calls != 1 {
		t.Fatalf("non-busy got err=%v calls=%d, want sentinel and 1 call", err, calls)
	}

	busyErr := makeBusyError(t)
	dbPath := filepath.Join(t.TempDir(), "retry.db")

	// Positive case: retries busy errors and closes leaked handle returned alongside busy error.
	attempts := 0
	db, err := OpenDBWithRetry(500*time.Millisecond, func() (*sql.DB, error) {
		attempts++
		if attempts < 3 {
			leaked, _ := sql.Open("sqlite", DSN(dbPath))
			return leaked, busyErr
		}
		return sql.Open("sqlite", DSN(dbPath))
	})
	if err != nil || db == nil || attempts != 3 {
		t.Fatalf("retry success got db=%v err=%v attempts=%d, want non-nil db, nil err, 3 attempts", db, err, attempts)
	}
	_ = db.Close()

	// Negative case: timeout while busy returns busy error.
	timeoutAttempts := 0
	_, err = OpenDBWithRetry(80*time.Millisecond, func() (*sql.DB, error) {
		timeoutAttempts++
		return nil, busyErr
	})
	if !IsBusy(err) || timeoutAttempts < 2 {
		t.Fatalf("busy timeout got err=%v attempts=%d, want busy err and >=2 attempts", err, timeoutAttempts)
	}
}

func TestBoolInt(t *testing.T) {
	t.Parallel()
	if got := BoolInt(true); got != 1 {
		t.Fatalf("BoolInt(true) = %d, want 1", got)
	}
	if got := BoolInt(false); got != 0 {
		t.Fatalf("BoolInt(false) = %d, want 0", got)
	}
}

func TestTempDBReadWriteAndUserVersion(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", DSN(p))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || strings.ToLower(mode) != "wal" {
		t.Fatalf("journal_mode = %q (%v), want wal", mode, err)
	}
	if _, err := db.Exec(`CREATE TABLE kv(k TEXT PRIMARY KEY, v TEXT); INSERT INTO kv VALUES ('a', '1'); PRAGMA user_version = 7;`); err != nil {
		t.Fatal(err)
	}
	var val string
	if err := db.QueryRow(`SELECT v FROM kv WHERE k = 'a'`).Scan(&val); err != nil || val != "1" {
		t.Fatalf("SELECT v = %q (%v), want 1", val, err)
	}
	ver, err := UserVersion(context.Background(), db)
	if err != nil || ver != 7 {
		t.Fatalf("UserVersion = %d (%v), want 7", ver, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := UserVersion(context.Background(), db); err == nil {
		t.Fatal("UserVersion on closed db succeeded, want error")
	}
}
