package dream

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

func TestProjectWatermarkMissingDoesNotCreate(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	got, err := ProjectWatermark(home, "/work/project")
	if err != nil {
		t.Fatalf("ProjectWatermark missing: err = %v, want nil", err)
	}
	if !got.IsZero() {
		t.Fatalf("ProjectWatermark missing: got %v, want zero", got)
	}
	if _, err := os.Stat(statePath(home)); !os.IsNotExist(err) {
		t.Fatalf("ProjectWatermark created dream-state.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir)); !os.IsNotExist(err) {
		t.Fatalf("ProjectWatermark created state dir: %v", err)
	}
}

func TestProjectWatermarkCorruptFailsClosed(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	p := statePath(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{corrupt json`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ProjectWatermark(home, "/work/project")
	if err == nil {
		t.Fatalf("ProjectWatermark corrupt: err = nil, want error (got %v)", got)
	}
	if !got.IsZero() {
		t.Fatalf("ProjectWatermark corrupt: got %v, want zero", got)
	}
}

func TestProjectWatermarkLookupAndNormalization(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	p := statePath(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{
  "projects": {
    "/work/repo-a": "2026-11-20T15:30:00Z",
    "__global__": "2026-11-21T09:00:00Z"
  }
}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	wantRepo := time.Date(2026, 11, 20, 15, 30, 0, 0, time.UTC)
	got, err := ProjectWatermark(home, "/work/repo-a/.")
	if err != nil || !got.Equal(wantRepo) {
		t.Fatalf("repo watermark = (%v, %v), want (%v, nil)", got, err, wantRepo)
	}

	wantGlobal := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	gotGlobal, err := ProjectWatermark(home, search.GlobalWorkspace)
	if err != nil || !gotGlobal.Equal(wantGlobal) {
		t.Fatalf("global watermark = (%v, %v), want (%v, nil)", gotGlobal, err, wantGlobal)
	}

	gotAbsent, err := ProjectWatermark(home, "/work/unknown")
	if err != nil || !gotAbsent.IsZero() {
		t.Fatalf("unknown watermark = (%v, %v), want (zero, nil)", gotAbsent, err)
	}
}
