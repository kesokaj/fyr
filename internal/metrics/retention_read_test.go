package metrics

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/sqliteutil"
)

func TestIngestedThroughMissingDB(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	got, err := IngestedThrough(home, "conv-123")
	if err != nil {
		t.Fatalf("IngestedThrough missing DB: err = %v, want nil", err)
	}
	if !got.IsZero() {
		t.Fatalf("IngestedThrough missing DB: got %v, want zero", got)
	}
	if _, err := os.Stat(Path(home)); !os.IsNotExist(err) {
		t.Fatalf("IngestedThrough created metrics.db: stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir)); !os.IsNotExist(err) {
		t.Fatalf("IngestedThrough created state dir: stat err = %v", err)
	}
}

func TestIngestedThroughInvalidConvID(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	for _, id := range []string{"", ".", "..", "a/b", `a\b`, "../escape"} {
		got, err := IngestedThrough(home, id)
		if err != nil || !got.IsZero() {
			t.Errorf("IngestedThrough(%q) = (%v, %v), want (zero, nil)", id, got, err)
		}
	}
}

func TestIngestedThroughLookupAndReadOnly(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dbPath := Path(home)
	s := openT(t, dbPath)
	const wantUnix int64 = 1730000123
	srcOK := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", "conv-ok.db")
	srcZero := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", "conv-zero.db")
	for _, row := range []struct {
		src   string
		mtime int64
	}{
		{srcOK, wantUnix},
		{srcZero, 0},
	} {
		if _, err := s.db.Exec(
			`INSERT INTO ingest_state(source, size, mtime, offset) VALUES(?, ?, ?, ?)`,
			row.src, 100, row.mtime, 0,
		); err != nil {
			t.Fatalf("insert ingest_state: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	beforeEntries, err := os.ReadDir(filepath.Dir(dbPath))
	if err != nil {
		t.Fatal(err)
	}

	got, err := IngestedThrough(home, "conv-ok")
	if err != nil {
		t.Fatalf("IngestedThrough(conv-ok): %v", err)
	}
	want := time.Unix(wantUnix, 0).UTC()
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("IngestedThrough(conv-ok) = %v (%s), want %v (UTC)", got, got.Location(), want)
	}

	if gotZero, err := IngestedThrough(home, "conv-zero"); err != nil || !gotZero.IsZero() {
		t.Fatalf("IngestedThrough(conv-zero) = (%v, %v), want (zero, nil)", gotZero, err)
	}
	if gotAbsent, err := IngestedThrough(home, "conv-absent"); err != nil || !gotAbsent.IsZero() {
		t.Fatalf("IngestedThrough(conv-absent) = (%v, %v), want (zero, nil)", gotAbsent, err)
	}

	afterEntries, err := os.ReadDir(filepath.Dir(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(afterEntries) != len(beforeEntries) {
		t.Fatalf("state dir entries changed: before=%d after=%d", len(beforeEntries), len(afterEntries))
	}
}

func TestIngestedThroughSymlinkedHome(t *testing.T) {
	t.Parallel()
	realHome := t.TempDir()
	linkParent := t.TempDir()
	linkHome := filepath.Join(linkParent, "home-link")
	if err := os.Symlink(realHome, linkHome); err != nil {
		t.Fatal(err)
	}
	s := openT(t, Path(realHome))
	const tsReal int64 = 1731000000
	const tsLink int64 = 1732000000
	realSrc := filepath.Join(realHome, ".gemini", "antigravity-cli", "conversations", "conv-real.db")
	linkSrc := filepath.Join(linkHome, ".gemini", "antigravity-cli", "conversations", "conv-link.db")
	for _, row := range []struct {
		src   string
		mtime int64
	}{
		{realSrc, tsReal},
		{linkSrc, tsLink},
	} {
		if _, err := s.db.Exec(
			`INSERT INTO ingest_state(source, size, mtime, offset) VALUES(?, ?, ?, ?)`,
			row.src, 50, row.mtime, 0,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	gotReal, err := IngestedThrough(linkHome, "conv-real")
	if err != nil || !gotReal.Equal(time.Unix(tsReal, 0).UTC()) {
		t.Fatalf("IngestedThrough(linkHome, conv-real) = (%v, %v), want %v", gotReal, err, time.Unix(tsReal, 0).UTC())
	}
	gotLink, err := IngestedThrough(linkHome, "conv-link")
	if err != nil || !gotLink.Equal(time.Unix(tsLink, 0).UTC()) {
		t.Fatalf("IngestedThrough(linkHome, conv-link) = (%v, %v), want %v", gotLink, err, time.Unix(tsLink, 0).UTC())
	}
}

func TestIngestedThroughMissingTable(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dbPath := Path(home)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteutil.DSN(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE other(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := IngestedThrough(home, "conv-1")
	if err != nil || !got.IsZero() {
		t.Fatalf("IngestedThrough without ingest_state table = (%v, %v), want (zero, nil)", got, err)
	}
}
