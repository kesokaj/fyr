package search

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func retentionWriteSummariesDB(t *testing.T, home, ddl string, inserts []string) string {
	t.Helper()
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "conversation_summaries.db")
	db, err := sql.Open("sqlite", dsn(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		t.Fatalf("create table: %v", err)
	}
	for _, q := range inserts {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			t.Fatalf("insert %q: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	return dir
}

func retentionRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestConversationWorkspaceRootMissingAndInvalid(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	root, exists, err := ConversationWorkspaceRoot(home, "conv-1")
	if err != nil || exists || root != "" {
		t.Fatalf("missing DB = (%q, %v, %v), want (\"\", false, nil)", root, exists, err)
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir)); !os.IsNotExist(err) {
		t.Fatalf("ConversationWorkspaceRoot created state dir: %v", err)
	}
	for _, id := range []string{"", ".", "..", "a/b", `a\b`} {
		r, ex, err := ConversationWorkspaceRoot(home, id)
		if err != nil || ex || r != "" {
			t.Errorf("invalid id %q = (%q, %v, %v), want (\"\", false, nil)", id, r, ex, err)
		}
	}
}

func TestConversationWorkspaceRootResolution(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	base := t.TempDir()
	mainRepo := filepath.Join(base, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	retentionRunGit(t, mainRepo, "init", "-q")
	if err := os.WriteFile(filepath.Join(mainRepo, "a.txt"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	retentionRunGit(t, mainRepo, "add", "a.txt")
	retentionRunGit(t, mainRepo, "commit", "-q", "-m", "init")
	wtDir := filepath.Join(base, "wt-1")
	retentionRunGit(t, mainRepo, "worktree", "add", "-q", "-b", "feat", wtDir)

	plainDir := filepath.Join(base, "plain")
	if err := os.MkdirAll(plainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goneDir := filepath.Join(base, "gone-project")

	ddl := `CREATE TABLE conversation_summaries (
		conversation_id TEXT,
		last_modified_time TEXT,
		workspace_uris TEXT
	)`
	ts := "2026-11-20 12:00:00+00:00"
	dbDir := retentionWriteSummariesDB(t, home, ddl, []string{
		`INSERT INTO conversation_summaries VALUES('conv-wt', '` + ts + `', '["file://` + filepath.ToSlash(wtDir) + `"]')`,
		`INSERT INTO conversation_summaries VALUES('conv-plain', '` + ts + `', '["file://` + filepath.ToSlash(plainDir) + `"]')`,
		`INSERT INTO conversation_summaries VALUES('conv-gone', '` + ts + `', '["file://` + filepath.ToSlash(goneDir) + `"]')`,
		`INSERT INTO conversation_summaries VALUES('conv-global-empty', '` + ts + `', '[]')`,
		`INSERT INTO conversation_summaries VALUES('conv-global-blank', '` + ts + `', '')`,
		`INSERT INTO conversation_summaries VALUES('conv-nonfile', '` + ts + `', '["https://example.com/ws"]')`,
	})
	before := listDir(t, dbDir)

	wantMain, err := filepath.EvalSymlinks(mainRepo)
	if err != nil {
		wantMain = mainRepo
	}

	r, ex, err := ConversationWorkspaceRoot(home, "conv-wt")
	if err != nil || !ex {
		t.Fatalf("conv-wt = (%q, %v, %v)", r, ex, err)
	}
	gotReal, _ := filepath.EvalSymlinks(r)
	if gotReal != wantMain && filepath.Clean(r) != filepath.Clean(mainRepo) {
		t.Errorf("conv-wt root = %q (real %q), want main repo %q", r, gotReal, wantMain)
	}

	r, ex, err = ConversationWorkspaceRoot(home, "conv-plain")
	if err != nil || !ex || r != filepath.Clean(plainDir) {
		t.Errorf("conv-plain = (%q, %v, %v), want (%q, true, nil)", r, ex, err, filepath.Clean(plainDir))
	}

	r, ex, err = ConversationWorkspaceRoot(home, "conv-gone")
	if err != nil || ex || r != filepath.Clean(goneDir) {
		t.Errorf("conv-gone = (%q, %v, %v), want (%q, false, nil)", r, ex, err, filepath.Clean(goneDir))
	}

	for _, id := range []string{"conv-global-empty", "conv-global-blank"} {
		r, ex, err = ConversationWorkspaceRoot(home, id)
		if err != nil || !ex || r != GlobalWorkspace {
			t.Errorf("%s = (%q, %v, %v), want (%q, true, nil)", id, r, ex, err, GlobalWorkspace)
		}
	}

	r, ex, err = ConversationWorkspaceRoot(home, "conv-missing")
	if err != nil || ex || r != "" {
		t.Errorf("conv-missing = (%q, %v, %v), want (\"\", false, nil)", r, ex, err)
	}

	r, ex, err = ConversationWorkspaceRoot(home, "conv-nonfile")
	if err != nil || ex || r != "" {
		t.Errorf("conv-nonfile = (%q, %v, %v), want (\"\", false, nil)", r, ex, err)
	}

	if after := listDir(t, dbDir); after != before {
		t.Errorf("live db dir modified: before=%q after=%q", before, after)
	}
}

func TestListOrphanChildConversations(t *testing.T) {
	t.Parallel()
	cutoff := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	t.Run("missing DB", func(t *testing.T) {
		t.Parallel()
		got, err := ListOrphanChildConversations(t.TempDir(), cutoff)
		if err != nil || len(got) != 0 {
			t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("legacy schema without parent column", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		ddl := `CREATE TABLE conversation_summaries (
			conversation_id TEXT,
			last_modified_time TEXT,
			workspace_uris TEXT
		)`
		retentionWriteSummariesDB(t, home, ddl, []string{
			`INSERT INTO conversation_summaries VALUES('c1', '2026-11-10 00:00:00+00:00', '[]')`,
		})
		got, err := ListOrphanChildConversations(home, cutoff)
		if err != nil || len(got) != 0 {
			t.Fatalf("legacy schema got (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("filters orphans by cutoff and live parent", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		ddl := `CREATE TABLE conversation_summaries (
			conversation_id TEXT,
			parent_conversation_id TEXT,
			last_modified_time TEXT,
			workspace_uris TEXT
		)`
		retentionWriteSummariesDB(t, home, ddl, []string{
			`INSERT INTO conversation_summaries VALUES('live-parent', '', '2026-11-01 00:00:00+00:00', '[]')`,
			`INSERT INTO conversation_summaries VALUES('child-live', 'live-parent', '2026-11-10 00:00:00+00:00', '[]')`,
			`INSERT INTO conversation_summaries VALUES('orphan-b', 'gone-parent', '2026-11-10 00:00:00+00:00', '[]')`,
			`INSERT INTO conversation_summaries VALUES('orphan-a', 'gone-parent', '2026-11-19 23:59:59+00:00', '[]')`,
			`INSERT INTO conversation_summaries VALUES('orphan-a', 'gone-parent', '2026-11-19 23:59:59+00:00', '[]')`,
			`INSERT INTO conversation_summaries VALUES('orphan-new', 'gone-parent', '2026-11-20 12:00:00+00:00', '[]')`,
			`INSERT INTO conversation_summaries VALUES('orphan-future', 'gone-parent', '2026-11-25 00:00:00+00:00', '[]')`,
			`INSERT INTO conversation_summaries VALUES('top-old', NULL, '2026-11-01 00:00:00+00:00', '[]')`,
			`INSERT INTO conversation_summaries VALUES('../bad', 'gone-parent', '2026-11-01 00:00:00+00:00', '[]')`,
		})
		got, err := ListOrphanChildConversations(home, cutoff)
		if err != nil {
			t.Fatalf("ListOrphanChildConversations: %v", err)
		}
		want := []string{"orphan-a", "orphan-b"}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
}

func TestListLocalRetentionRows(t *testing.T) {
	t.Parallel()

	t.Run("missing DB", func(t *testing.T) {
		t.Parallel()
		got, err := ListLocalRetentionRows(t.TempDir())
		if err != nil || len(got) != 0 {
			t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("full schema filters children and resolves fields", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		proj := filepath.Join(t.TempDir(), "proj")
		if err := os.MkdirAll(proj, 0o755); err != nil {
			t.Fatal(err)
		}
		ddl := `CREATE TABLE conversation_summaries (
			conversation_id TEXT,
			project_id TEXT,
			parent_conversation_id TEXT,
			archived INTEGER,
			last_modified_time TEXT,
			workspace_uris TEXT
		)`
		retentionWriteSummariesDB(t, home, ddl, []string{
			`INSERT INTO conversation_summaries VALUES('top-1', 'proj-id-1', '', 1, '2026-11-20 10:00:00+00:00', '["file://` + filepath.ToSlash(proj) + `"]')`,
			`INSERT INTO conversation_summaries VALUES('top-global', 'proj-id-2', NULL, 0, '2026-11-21 11:00:00+00:00', '[]')`,
			`INSERT INTO conversation_summaries VALUES('child-1', 'proj-id-1', 'top-1', 0, '2026-11-20 10:05:00+00:00', '["file://` + filepath.ToSlash(proj) + `"]')`,
			`INSERT INTO conversation_summaries VALUES('bad-ts', 'proj-id-1', '', 0, 'not-a-timestamp', '[]')`,
			`INSERT INTO conversation_summaries VALUES('../bad-id', 'proj-id-1', '', 0, '2026-11-20 10:00:00+00:00', '[]')`,
		})
		got, err := ListLocalRetentionRows(home)
		if err != nil {
			t.Fatalf("ListLocalRetentionRows: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len(got) = %d (%+v), want 2", len(got), got)
		}
		byID := map[string]LocalRetentionRow{}
		for _, r := range got {
			byID[r.ID] = r
		}
		r1, ok := byID["top-1"]
		if !ok || r1.ProjectID != "proj-id-1" || r1.WorkspaceRoot != filepath.Clean(proj) || !r1.Archived || !r1.UpdatedAt.Equal(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)) {
			t.Errorf("top-1 = %+v", r1)
		}
		r2, ok := byID["top-global"]
		if !ok || r2.ProjectID != "proj-id-2" || r2.WorkspaceRoot != GlobalWorkspace || r2.Archived {
			t.Errorf("top-global = %+v", r2)
		}
	})

	t.Run("minimal legacy schema", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		ddl := `CREATE TABLE conversation_summaries (
			conversation_id TEXT,
			last_modified_time TEXT,
			workspace_uris TEXT
		)`
		retentionWriteSummariesDB(t, home, ddl, []string{
			`INSERT INTO conversation_summaries VALUES('legacy-1', '2026-11-20 08:00:00+00:00', '')`,
		})
		got, err := ListLocalRetentionRows(home)
		if err != nil || len(got) != 1 {
			t.Fatalf("got (%+v, %v), want 1 row", got, err)
		}
		if got[0].ID != "legacy-1" || got[0].ProjectID != "" || got[0].WorkspaceRoot != GlobalWorkspace || got[0].Archived {
			t.Errorf("legacy-1 = %+v", got[0])
		}
	})
}
