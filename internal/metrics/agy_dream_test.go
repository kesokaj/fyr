package metrics

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// A conversation whose workspace is the dream's working directory is no
// project's session: an ingest stores the project's conversation only, and
// writes no ingest_state row for the dream's, whether its workspace was
// stored as the home is written or through the home's symlink, and whether
// the store is --home's or a --source sandbox's (visible dream plan D5, D15).
func TestDreamWorkNotIngested(t *testing.T) {
	real := t.TempDir()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, home); err != nil {
		t.Fatal(err)
	}
	real, _ = filepath.EvalSymlinks(real)
	root := filepath.Join(t.TempDir(), "proj")
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(dir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	convs := map[string]string{
		"project":       root,
		"dream-written": harness.DreamWork(home),
		"dream-real":    filepath.Join(harness.DreamWork(real), "sub"),
	}
	stmts := []string{"CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')"}
	for id, ws := range convs {
		stmts = append(stmts, `INSERT INTO conversation_summaries VALUES ('`+id+`', '2026-10-28 10:00:00+00:00', '["file://`+ws+`"]', '', '')`)
		p := filepath.Join(dir, "conversations", id+".db")
		sqliteT(t, p,
			"CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)",
			"CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
		db, err := sql.Open("sqlite", p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO steps VALUES (0, 14, 3, ?, NULL, NULL)`, agyStepMeta(1793181600, "", nil)); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
	sqliteT(t, filepath.Join(dir, "conversation_summaries.db"), stmts...)

	for _, o := range []Options{{Home: home}, {Home: t.TempDir(), Source: home, Label: "eval=dream-work"}} {
		dreamWorkIngest(t, o, filepath.Join(dir, "conversations", "project.db"))
	}
}

// dreamWorkIngest ingests o into a new store and checks it holds the
// project's conversation only.
func dreamWorkIngest(t *testing.T, o Options, want string) {
	t.Helper()
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	if r := ingestT(t, s, o); r.Sessions != 1 || len(r.Failed) != 0 {
		t.Fatalf("%+v: result %+v, want 1 session, none failed", o, r)
	}
	var ids, sources []string
	for q, out := range map[string]*[]string{`SELECT id FROM sessions`: &ids, `SELECT source FROM ingest_state`: &sources} {
		rows, err := s.db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			*out = append(*out, v)
		}
		rows.Close()
	}
	if !slices.Equal(ids, []string{"project"}) {
		t.Errorf("%+v: sessions %v, want the project's only", o, ids)
	}
	if !slices.Equal(sources, []string{want}) {
		t.Errorf("%+v: ingest_state %v, want %s only", o, sources, want)
	}
}
