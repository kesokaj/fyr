package brain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListCurrentSpecsCtxReadOnlyCancelAndSymlink(t *testing.T) {
	dir := repo(t)
	home := t.TempDir()
	bo := Options{Home: home, Dir: dir}

	if _, err := Add(bo, AddInput{
		Kind: "spec", ID: "SPEC-live", Status: "current", Title: "Live spec",
		Body: strings.NewReader("## Purpose\nLive.\n"),
	}); err != nil {
		t.Fatalf("add live spec: %v", err)
	}
	if _, err := Add(bo, AddInput{
		Kind: "spec", ID: "SPEC-old", Status: "retired", Title: "Retired spec",
		Body: strings.NewReader("## Purpose\nOld.\n"),
	}); err != nil {
		t.Fatalf("add retired spec: %v", err)
	}
	if _, err := Add(bo, AddInput{
		Kind: "adr", Status: "accepted", Title: "Accepted ADR",
		Body: strings.NewReader("Decision.\n"),
	}); err != nil {
		t.Fatalf("add adr: %v", err)
	}

	// 1. Returns only current specs for this repo scope.
	recs, err := ListCurrentSpecsCtx(context.Background(), bo)
	if err != nil {
		t.Fatalf("ListCurrentSpecsCtx: %v", err)
	}
	if len(recs) != 1 || recs[0].ID != "SPEC-live" || recs[0].Status != "current" || !strings.Contains(recs[0].Body, "Live.") {
		t.Fatalf("got %+v, want only SPEC-live with hydrated body", recs)
	}

	// 2. Works strictly read-only (0444 DB inside 0555 dir; zero writes/migrations).
	dbPath := Path(home)
	dbDir := filepath.Dir(dbPath)
	// Checkpoint WAL if present so mode=ro reads directly without needing -shm creation.
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	if err := os.Chmod(dbPath, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dbDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dbDir, 0o755)
		_ = os.Chmod(dbPath, 0o644)
	})
	roRecs, err := ListCurrentSpecsCtx(context.Background(), bo)
	if err != nil {
		t.Fatalf("ListCurrentSpecsCtx on read-only DB/dir: %v", err)
	}
	if len(roRecs) != 1 || roRecs[0].ID != "SPEC-live" {
		t.Fatalf("read-only ListCurrentSpecsCtx = %+v, want SPEC-live", roRecs)
	}

	// 3. Respects cancelled context immediately.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ListCurrentSpecsCtx(cctx, bo); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx err = %v, want context.Canceled", err)
	}

	// 4. Refuses symlinked brain.db without following it.
	linkHome := t.TempDir()
	linkDB := Path(linkHome)
	if err := os.MkdirAll(filepath.Dir(linkDB), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dbPath, linkDB); err != nil {
		t.Fatal(err)
	}
	if _, err := ListCurrentSpecsCtx(context.Background(), Options{Home: linkHome, Dir: dir}); err == nil || !IsRefusal(err) {
		t.Errorf("symlink brain.db err = %v (IsRefusal=%v), want refusal", err, IsRefusal(err))
	}

	// 5. Missing brain.db returns nil, nil cleanly.
	emptyRecs, err := ListCurrentSpecsCtx(context.Background(), Options{Home: t.TempDir(), Dir: dir})
	if err != nil || len(emptyRecs) != 0 {
		t.Errorf("missing brain.db = (%+v, %v), want (nil, nil)", emptyRecs, err)
	}
}
