package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/metrics"
)

// The arms of runMetrics that its split moves into helpers, pinned before
// the split (PLAN-2026-10-27-funclen Phase 6, D13). The store is broken by
// its own schema, never through a seam (D12).

// craftedStore writes a metrics.db under home whose schema row says current
// and whose tables are the given ones.
func craftedStore(t *testing.T, home string, tables ...string) {
	t.Helper()
	p := metrics.Path(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := append([]string{`CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT)`, `INSERT INTO meta VALUES ('schema', '3')`}, tables...)
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
}

func metricsCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(append([]string{"metrics"}, args...), &out)
	return out.String(), err
}

// A store that cannot be opened, and one whose counts cannot be read, fail
// ingest with the error.
func TestMetricsIngestStoreErrors(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(metrics.Path(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := metricsCmd(t, "ingest", "--home", home); err == nil || out != "" {
		t.Errorf("a directory as the store: %v %q", err, out)
	}

	home = t.TempDir()
	craftedStore(t, home)
	if out, err := metricsCmd(t, "ingest", "--home", home); err == nil || !strings.Contains(err.Error(), "no such table: sessions") || out != "" {
		t.Errorf("no sessions table: %v %q", err, out)
	}
}

// A run whose outcome cannot be stamped counts as failed: the summary line,
// then exit 1.
func TestMetricsIngestFailedSource(t *testing.T) {
	home := t.TempDir()
	craftedStore(t, home, `CREATE TABLE sessions(id TEXT)`, `CREATE TABLE steps(id TEXT)`, `CREATE TABLE events(id TEXT)`)
	out, err := metricsCmd(t, "ingest", "--source", t.TempDir(), "--label", "a=b", "--outcome", "pass", "--home", home)
	if exitOf(err) != 1 || !strings.Contains(out, "metrics: 0 sessions, 0 steps, 0 events added; ") || strings.Contains(out, "; 0 sources failed") {
		t.Errorf("exit %d: %q", exitOf(err), out)
	}
}

// A report over an existing file is refused with the error.
func TestMetricsReportRefusesExistingFile(t *testing.T) {
	home := t.TempDir()
	if _, err := metricsCmd(t, "ingest", "--home", home); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "r.html")
	if err := os.WriteFile(file, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := metricsCmd(t, "report", "--out", file, "--home", home)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite "+file) || strings.Contains(out, "report written") {
		t.Errorf("%v %q", err, out)
	}
}
