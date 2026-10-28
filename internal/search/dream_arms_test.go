package search

import (
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// The arms of AgyConversationText and walk that their splits move into
// helpers, pinned before the splits (PLAN-2026-10-27-funclen Phase 4, D13).

// summaries makes home's conversation list database with schema, then runs
// each statement in it.
func summaries(t *testing.T, home, schema string, stmts ...string) {
	t.Helper()
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(dir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn(filepath.Join(dir, "conversation_summaries.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range append([]string{schema}, stmts...) {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
}

// The conversation list that cannot be copied, read or scanned fails the
// call, and the temporary copies go with it.
func TestAgyConversationListErrors(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, home string)
		want  string
	}{
		{"not a file", func(t *testing.T, home string) {
			writeFile(t, filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db"), "x", "")
		}, "not a regular file"},
		{"no table", func(t *testing.T, home string) {
			summaries(t, home, `CREATE TABLE other(x)`)
		}, "reading Antigravity's conversation list"},
		{"null id", func(t *testing.T, home string) {
			summaries(t, home, "CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL)",
				`INSERT INTO conversation_summaries VALUES(NULL, '2026-09-30 10:00:00+00:00', '["file:///p/one"]')`)
		}, "NULL"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			c.setup(t, home)
			got, _, err := AgyConversationText(home, "/p/one", time.Time{})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("AgyConversationText: %v, %d conversations; want %q", err, len(got), c.want)
			}
			if _, err := os.Stat(filepath.Join(home, harness.StateDir)); !os.IsNotExist(err) {
				t.Errorf("the temporary copies outlived the failed call: %v", err)
			}
		})
	}
}

// walk refuses a message whose varint, 64-bit or 32-bit field runs past its
// end, and steps over a whole 64-bit or 32-bit field to the prose after it.
func TestWalkFixedAndVarintFields(t *testing.T) {
	text := []byte("the operator asked for a smaller split")
	fixed32 := append(binary.AppendUvarint(nil, 1<<3|5), 0, 0, 0, 0)
	fixed64 := append(binary.AppendUvarint(nil, 1<<3|1), 0, 0, 0, 0, 0, 0, 0, 0)
	for _, c := range []struct {
		name   string
		msg    []byte
		ok     bool
		leaves int
	}{
		{"short varint", []byte{1 << 3, 0x80}, false, 0},
		{"short 64-bit", append(binary.AppendUvarint(nil, 1<<3|1), 1, 2, 3, 4, 5, 6, 7), false, 0},
		{"short 32-bit", append(binary.AppendUvarint(nil, 1<<3|5), 1, 2, 3), false, 0},
		{"32-bit then prose", append(fixed32, pb(2, text)...), true, 1},
		{"64-bit then prose", append(fixed64, pb(2, text)...), true, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out []string
			if ok := walk(c.msg, 0, &out); ok != c.ok || len(out) != c.leaves {
				t.Errorf("walk: %v with %q, want %v with %d leaves", ok, out, c.ok, c.leaves)
			}
		})
	}
}
