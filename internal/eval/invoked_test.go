package eval

import (
	"path/filepath"
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
