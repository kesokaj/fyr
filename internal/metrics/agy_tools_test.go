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
		{"read_file", "Read"},
		{"write_file", "Write"},
		{"edit_file", "Edit"},
		{"grep_search", "Grep"},
		{"find_by_name", "Glob"},
		{"list_dir", "LS"},
		{"invoke_subagent", "Agent"},
		{"send_message", "SendMessage"},
		{"manage_subagents", "agy:manage_subagents"},
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

// Each file tool's path is read under the key it is sent with: view_file's
// AbsolutePath, read_file's, write_file's and edit_file's Path (agy 1.4.7),
// and write_to_file's and replace_file_content's TargetFile (BUG-184). A
// SKILL.md read through read_file is a skill load, and every write or edit
// inside the project gets the same trail kind as the view_file control row.
func TestAgyPathKeys(t *testing.T) {
	home, root := agyFixture(t)
	plan := filepath.Join(root, ".agents", "docs", "plans", "p.md")
	skill := "/home/u/.gemini/config/skills/golang/SKILL.md"
	steps := []struct {
		tool, key, path string
	}{
		{"view_file", "AbsolutePath", plan},
		{"read_file", "Path", skill},
		{"read_file", "Path", plan},
		{"write_file", "Path", plan},
		{"edit_file", "Path", plan},
		{"write_to_file", "TargetFile", plan},
		{"replace_file_content", "TargetFile", plan},
		{"multi_replace_file_content", "TargetFile", plan},
	}
	p := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", "C1.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM steps`); err != nil {
		t.Fatal(err)
	}
	for i, st := range steps {
		meta := agyStepMeta(1791367200+uint64(i), st.tool, map[string]string{st.key: st.path})
		if _, err := db.Exec(`INSERT INTO steps VALUES (?, ?, 3, ?, NULL, NULL)`, i, agyTool, meta); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Home: home})
	row := func(i int) (skill, kind sql.NullString) {
		t.Helper()
		if err := s.db.QueryRow(`SELECT skill, target_kind FROM steps WHERE ext_id = ?`, fmt.Sprintf("C1:%d", i)).Scan(&skill, &kind); err != nil {
			t.Fatalf("%s: %v", steps[i].tool, err)
		}
		return skill, kind
	}
	_, control := row(0)
	if !control.Valid || control.String == "" || control.String == "outside" {
		t.Fatalf("the view_file control row has kind %v; the fixture's plan path is not a trail kind", control)
	}
	if sk, _ := row(1); sk.String != "golang" {
		t.Errorf("read_file of a SKILL.md stored skill %v, want golang", sk)
	}
	for i := 2; i < len(steps); i++ {
		if _, kind := row(i); kind != control {
			t.Errorf("%s with %s stored kind %v, want %v", steps[i].tool, steps[i].key, kind, control)
		}
	}
}
