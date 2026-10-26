package metrics

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Every Antigravity tool name the spec's shared vocabulary table maps is
// stored under its vocabulary name, and an unmapped one as agy:<name>. The
// want table is written out here, not read from agyTools, so a changed or
// dropped mapping fails the test (TestAgyIngestShape covers only run_command
// and view_file).
func TestAgyToolVocabulary(t *testing.T) {
	want := []struct{ agy, stored string }{
		{"run_command", "Bash"},
		{"view_file", "Read"},
		{"write_to_file", "Write"},
		{"replace_file_content", "Edit"},
		{"multi_replace_file_content", "Edit"},
		{"grep_search", "Grep"},
		{"find_by_name", "Glob"},
		{"list_dir", "LS"},
		{"invoke_subagent", "Agent"},
		{"send_message", "SendMessage"},
		{"brand_new_tool", "agy:brand_new_tool"},
	}
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(root, 0o755)
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	os.MkdirAll(filepath.Join(dir, "conversations"), 0o755)
	sqliteT(t, filepath.Join(dir, "conversation_summaries.db"),
		"CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')",
		`INSERT INTO conversation_summaries VALUES ('V', '2026-10-08 10:05:00+00:00', '["file://`+root+`"]', '', '')`)
	p := filepath.Join(dir, "conversations", "V.db")
	sqliteT(t, p,
		"CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)",
		"CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range want {
		if _, err := db.Exec(`INSERT INTO steps VALUES (?, ?, 3, ?, NULL, NULL)`, i, agyTool, agyStepMeta(1791367200+uint64(i), w.agy, map[string]string{})); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Home: home})
	for i, w := range want {
		var got string
		if err := s.db.QueryRow(`SELECT tool FROM steps WHERE ext_id = ?`, fmt.Sprintf("V:%d", i)).Scan(&got); err != nil {
			t.Fatalf("%s: %v", w.agy, err)
		}
		if got != w.stored {
			t.Errorf("%s stored as %q, want %q", w.agy, got, w.stored)
		}
	}
}
