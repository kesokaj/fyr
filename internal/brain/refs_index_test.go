package brain

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestIndexRecordLinks(t *testing.T) {
	t.Parallel()
	t.Run("missing_brain_db_degrades_cleanly_without_creating_files", func(t *testing.T) {
		emptyHome := t.TempDir()
		repo := testGitRepo(t)

		gotEmpty, err := IndexRecordLinks("", repo, []string{"internal/search/store.go"})
		if err != nil || len(gotEmpty) != 0 {
			t.Fatalf("empty home: got (%v, %v), want empty map, nil", gotEmpty, err)
		}

		got, err := IndexRecordLinks(emptyHome, repo, []string{"internal/search/store.go"})
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("missing brain.db: got (%v, %v), want empty non-nil map, nil", got, err)
		}
		entries, rerr := os.ReadDir(emptyHome)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if len(entries) != 0 {
			t.Fatalf("missing brain.db created files in home: %v", entries)
		}
	})

	t.Run("counts_sorted_ids_boundaries_and_low_validity_exclusion", func(t *testing.T) {
		home := t.TempDir()
		repo := testGitRepo(t)
		opts := Options{Home: home, Dir: repo}

		for _, rel := range []string{
			"internal/search/store.go",
			"internal/search/store_test.go",
			"cmd/store.go",
			"internal/graph/build.go",
		} {
			full := filepath.Join(repo, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("package p\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		if _, err := Add(opts, AddInput{
			Kind:  "bug",
			ID:    "BUG-042",
			Title: "Fix migration in internal/search/store.go",
			Body:  strings.NewReader("Touches ./internal/search/store.go:40 and internal/graph/build.go."),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := Add(opts, AddInput{
			Kind:  "adr",
			ID:    "ADR-007",
			Title: "Schema invariants",
			Body:  strings.NewReader("Enforced by `internal/search/store.go`."),
		}); err != nil {
			t.Fatal(err)
		}
		// Partial prefix/suffix lookalikes that must NOT match `internal/search/store.go` or `cmd/store.go`.
		if _, err := Add(opts, AddInput{
			Kind:  "bug",
			ID:    "BUG-099",
			Title: "Unrelated test and wrapper paths",
			Body:  strings.NewReader("Only touches internal/search/store_test.go, xinternal/search/store.go, and sub/cmd/store.go.bak"),
		}); err != nil {
			t.Fatal(err)
		}
		// Unlinked kind (`plan`) must be ignored even when naming the path.
		if _, err := Add(opts, AddInput{
			Kind:  "plan",
			ID:    "PLAN-2026-11-20-test",
			Title: "Plan mentioning internal/search/store.go",
			Body:  strings.NewReader("Touches internal/search/store.go"),
		}); err != nil {
			t.Fatal(err)
		}

		// High-validity operator memory (>= 0.40) -> included.
		if _, err := MemoryAdd(opts, MemoryEntry{
			Type:        "semantic",
			Name:        "store-invariant",
			Description: "Keep internal/search/store.go schema monotonic",
			Provenance:  "operator",
		}, strings.NewReader("Applies to internal/search/store.go.")); err != nil {
			t.Fatal(err)
		}

		// Derived memory whose referenced file pin mismatches -> validity drops to 0.35 (< 0.40) -> excluded.
		if _, err := MemoryAdd(opts, MemoryEntry{
			Type:        "semantic",
			Name:        "stale-derived",
			Description: "Old note on internal/search/store.go",
			Provenance:  "derived",
		}, strings.NewReader("Pinned internal/search/store.go sha256:000000000000")); err != nil {
			t.Fatal(err)
		}

		// Memory whose DB validity column is directly < 0.40 -> excluded.
		if _, err := MemoryAdd(opts, MemoryEntry{
			Type:        "semantic",
			Name:        "low-col",
			Description: "Low score memory for internal/graph/build.go",
			Provenance:  "operator",
		}, strings.NewReader("Mentions internal/graph/build.go")); err != nil {
			t.Fatal(err)
		}
		st, err := openStore(home)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.db.Exec(`UPDATE records SET validity = 0.25 WHERE id = 'MEMORY-low-col'`); err != nil {
			st.close()
			t.Fatal(err)
		}
		st.close()

		got, err := IndexRecordLinks(home, repo, []string{
			"./internal/search/store.go",
			"internal/search/store_test.go",
			"cmd/store.go",
			"internal/graph/build.go",
			"internal/unmentioned.go",
		})
		if err != nil {
			t.Fatalf("IndexRecordLinks: %v", err)
		}

		wantStore := RecordLinks{
			Bugs:     1,
			ADRs:     1,
			Memories: 1,
			IDs:      []string{"ADR-007", "BUG-042", "MEMORY-store-invariant"},
		}
		if !reflect.DeepEqual(got["internal/search/store.go"], wantStore) {
			t.Errorf("store.go links = %+v, want %+v", got["internal/search/store.go"], wantStore)
		}

		wantStoreTest := RecordLinks{
			Bugs: 1,
			IDs:  []string{"BUG-099"},
		}
		if !reflect.DeepEqual(got["internal/search/store_test.go"], wantStoreTest) {
			t.Errorf("store_test.go links = %+v, want %+v", got["internal/search/store_test.go"], wantStoreTest)
		}

		if links, exists := got["cmd/store.go"]; exists {
			t.Errorf("cmd/store.go matched false-positive suffix: %+v", links)
		}

		wantBuild := RecordLinks{
			Bugs:     1,
			ADRs:     0,
			Memories: 0,
			IDs:      []string{"BUG-042"},
		}
		if !reflect.DeepEqual(got["internal/graph/build.go"], wantBuild) {
			t.Errorf("build.go links = %+v, want %+v", got["internal/graph/build.go"], wantBuild)
		}

		if _, exists := got["internal/unmentioned.go"]; exists {
			t.Errorf("unmentioned file present in map: %+v", got["internal/unmentioned.go"])
		}
	})
}
