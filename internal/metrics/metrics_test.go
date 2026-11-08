package metrics

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadEfficiencyTelemetryAndHashedPaths(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(filepath.Join(root, "internal", "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(dir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	sqliteT(t, filepath.Join(dir, "conversation_summaries.db"),
		"CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')",
		`INSERT INTO conversation_summaries VALUES ('C_READ', '2026-10-08 10:05:00+00:00', '["file://`+root+`"]', '', '')`,
	)

	secretFile := filepath.Join(root, "internal", "core", "secret_"+canary+".go")
	secondFile := filepath.Join(root, "internal", "core", "other.go")
	const t0 = 1791367200
	steps := [][]any{
		{14, 3, agyStepMeta(t0, "", nil), nil},
		// 1. Full read of secretFile -> f:<hash>
		{132, 3, agyStepMeta(t0+1, "read_file", map[string]any{"Path": secretFile}), nil},
		// 2. Redundant ranged read of secretFile (same session, no edit) -> r:<hash>, redundant=1
		{132, 3, agyStepMeta(t0+2, "read_file", map[string]any{"Path": secretFile, "StartLine": 10, "EndLine": 40}), nil},
		// 3. Edit secretFile -> f:<hash>, resets seen read state for secretFile
		{132, 3, agyStepMeta(t0+3, "edit_file", map[string]any{"Path": secretFile}), nil},
		// 4. Ranged read of secretFile after edit -> r:<hash>, NOT redundant!
		{132, 3, agyStepMeta(t0+4, "read_file", map[string]any{"Path": secretFile, "StartLine": 1, "EndLine": 25}), nil},
		// 5. Ranged read of secondFile -> r:<hash>, NOT redundant!
		{132, 3, agyStepMeta(t0+5, "view_file", map[string]any{"AbsolutePath": secondFile, "StartLine": 5, "EndLine": 15}), nil},
		// 6. Bash fyr search + grep for Search : Grep ratio
		{132, 3, agyStepMeta(t0+6, "run_command", map[string]any{"CommandLine": "fyr search TargetAlpha"}), nil},
		{132, 3, agyStepMeta(t0+7, "grep_search", map[string]any{"Query": "TargetAlpha"}), nil},
	}

	convDB := filepath.Join(dir, "conversations", "C_READ.db")
	sqliteT(t, convDB,
		"CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)",
		"CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
	db, err := sql.Open("sqlite", convDB)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range steps {
		if _, err := db.Exec(`INSERT INTO steps VALUES (?, ?, ?, ?, ?, ?)`, i, s[0], s[1], s[2], s[3], []byte("payload "+canary)); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	storeDir := t.TempDir()
	s := openT(t, filepath.Join(storeDir, "metrics.db"))
	ingestT(t, s, Options{Home: home})

	// Verify cmd_hash prefixes ("f:" for full read and edit, "r:" for ranged reads) and no raw paths.
	rows, err := s.db.Query(`SELECT tool, cmd_hash FROM steps WHERE session_id = 'C_READ' AND tool IN ('Read', 'Edit') ORDER BY ext_id`)
	if err != nil {
		t.Fatal(err)
	}
	var hashes []string
	for rows.Next() {
		var tool, h sql.NullString
		if err := rows.Scan(&tool, &h); err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, tool.String+":"+h.String)
	}
	rows.Close()
	if len(hashes) != 5 {
		t.Fatalf("expected 5 Read/Edit steps, got %v", hashes)
	}
	if !strings.HasPrefix(hashes[0], "Read:f:") {
		t.Errorf("step 1 should be full read Read:f:<hash>, got %s", hashes[0])
	}
	if !strings.HasPrefix(hashes[1], "Read:r:") {
		t.Errorf("step 2 should be ranged read Read:r:<hash>, got %s", hashes[1])
	}
	if !strings.HasPrefix(hashes[2], "Edit:f:") {
		t.Errorf("step 3 should be Edit:f:<hash>, got %s", hashes[2])
	}
	if !strings.HasPrefix(hashes[3], "Read:r:") {
		t.Errorf("step 4 should be ranged read Read:r:<hash>, got %s", hashes[3])
	}
	if !strings.HasPrefix(hashes[4], "Read:r:") {
		t.Errorf("step 5 should be ranged read Read:r:<hash>, got %s", hashes[4])
	}
	// Verify the underlying path hash of steps 1, 2, 3, 4 is identical.
	baseHash := strings.TrimPrefix(hashes[0], "Read:f:")
	if strings.TrimPrefix(hashes[1], "Read:r:") != baseHash || strings.TrimPrefix(hashes[2], "Edit:f:") != baseHash {
		t.Errorf("expected identical normalized path hash across Read/Edit of same file, got %v", hashes[:4])
	}

	// Verify canary never appears in metrics.db.
	rawDB, err := os.ReadFile(filepath.Join(storeDir, "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rawDB, []byte(canary)) {
		t.Errorf("metrics.db contains raw path canary %s", canary)
	}

	sum, err := s.Show(ShowOptions{
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Now:   time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Total Read calls = 4 (1 full + 3 ranged -> 75.0% ranged).
	// Redundant reads = 1 (step 2 was redundant; step 4 followed an Edit so it was not redundant).
	if got := sum.C.RedundantReads["agy"]; got != 1 {
		t.Errorf("RedundantReads[agy] = %d, want 1", got)
	}
	if pct := sum.C.RangedReadPct["agy"]; pct == nil || *pct != 75.0 {
		t.Errorf("RangedReadPct[agy] = %v, want 75.0", pct)
	}
	if ratio := sum.C.SearchGrepRatio["agy"]; ratio == nil || *ratio != 1.0 {
		t.Errorf("SearchGrepRatio[agy] = %v, want 1.0", ratio)
	}
	if retries := sum.D.RetryLoops["agy"]; retries != 0 {
		t.Errorf("RetryLoops[agy] = %d, want 0 (file reads must not count as Bash retry loops)", retries)
	}

	var textOut bytes.Buffer
	sum.WriteText(&textOut)
	txt := textOut.String()
	for _, want := range []string{"redundant reads", "ranged read %", "75.0%", "search : grep", "1.000"} {
		if !strings.Contains(txt, want) {
			t.Errorf("WriteText output missing %q in:\n%s", want, txt)
		}
	}
}
