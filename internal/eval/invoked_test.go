package eval

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestInvoked is check f: the agent counts only when some conversation
// database holds "TypeName":"<agent>"; another agent, a name that merely
// starts the same, and the WAL (D24) do not count.
func TestInvoked(t *testing.T) {
	sb := t.TempDir()
	conv := filepath.Join(sb, ".gemini", "antigravity-cli", "conversations")
	if invokedOK(sb, "reviewer") {
		t.Error("no conversation store counts as invoked")
	}
	put(t, filepath.Join(conv, "main.db"), "SQLite format 3\x00{\"TypeName\":\"reviewer-x\"}\x00", 0o600)
	put(t, filepath.Join(conv, "main.db-wal"), "\"TypeName\":\"reviewer\"", 0o600)
	put(t, filepath.Join(conv, "notes.txt"), "\"TypeName\":\"reviewer\"", 0o600)
	if invokedOK(sb, "reviewer") {
		t.Error("a longer name, the WAL or a non-database file counts as invoked")
	}
	// The subagent's own file is often the newest; any database counts.
	put(t, filepath.Join(conv, "sub.db"), "SQLite format 3\x00\x01\x02{\"TypeName\":\"reviewer\",\"x\":1}", 0o600)
	if !invokedOK(sb, "reviewer") {
		t.Error("the agent in a second database is not found")
	}
	if invokedOK(sb, "bugs") {
		t.Error("another agent counts as invoked")
	}
}

func invokedOK(sb, agent string) bool {
	ok, _ := invokedNotes(sb, agent)
	return ok
}

// A conversation database over the scan cap is not read, and says so,
// instead of passing or silently failing check f (IN51, plan D16).
func TestInvokedSkipsAnOversizedDatabase(t *testing.T) {
	sb := t.TempDir()
	conv := filepath.Join(sb, ".gemini", "antigravity-cli", "conversations")
	put(t, filepath.Join(conv, "big.db"), "SQLite format 3\x00{\"TypeName\":\"reviewer\"}\x00", 0o600)
	orig := maxScan
	maxScan = 8
	t.Cleanup(func() { maxScan = orig })
	ok, notes := invokedNotes(sb, "reviewer")
	if ok {
		t.Error("a database over the cap was read")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "big.db") || !strings.Contains(notes[0], "not read") {
		t.Errorf("notes %q, want one naming big.db", notes)
	}
}
