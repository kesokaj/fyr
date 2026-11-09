package metrics

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrajectoriesEmpty(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	list, err := st.Trajectories(TrajectoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("got %d trajectories, want 0", len(list))
	}

	var jsonBuf bytes.Buffer
	if err := WriteTrajectoriesJSON(&jsonBuf, list); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(jsonBuf.String()) != "[]" {
		t.Fatalf("empty JSON = %q, want []", jsonBuf.String())
	}

	var textBuf bytes.Buffer
	WriteTrajectoriesText(&textBuf, list)
	if strings.TrimSpace(textBuf.String()) != "(no sessions)" {
		t.Fatalf("empty text = %q, want (no sessions)", textBuf.String())
	}
}

func TestTrajectoriesLineageAndAnomalies(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	t0 := time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC).UnixMilli()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	// Session 1: exceeds 180k knee, has redundant read and bash retry loop, and one subagent.
	sid1 := "aaaa1111-0000-0000-0000-000000000001"
	mustExec(`INSERT INTO sessions(id, vendor, project_root, parent_id, agent_type, model, started_at, ended_at, duration_ms)
		VALUES(?, 'agy', '/work/fyr', '', '', 'claude-opus-4-6', ?, ?, 120000)`,
		sid1, t0, t0+120_000)
	mustExec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, cmd_hash, is_error, denied, tok_in, tok_out, cache_read, cache_write, context) VALUES
		(?, '1', ?, 'assistant', '', '', 0, 0, 10000, 1000, 40000, 0, 50000),
		(?, '2', ?, 'tool', 'Read', 'f:abc123', 0, 0, 0, 0, 0, 0, 0),
		(?, '3', ?, 'tool', 'Read', 'r:abc123', 0, 0, 0, 0, 0, 0, 0),
		(?, '4', ?, 'assistant', '', '', 0, 0, 10000, 1000, 110000, 0, 120000),
		(?, '5', ?, 'tool', 'Bash', 'cmd999', 1, 0, 0, 0, 0, 0, 0),
		(?, '6', ?, 'tool', 'Bash', 'cmd999', 0, 0, 0, 0, 0, 0, 0),
		(?, '7', ?, 'assistant', '', '', 0, 0, 15000, 1000, 170000, 5000, 190000)`,
		sid1, t0+10_000, sid1, t0+15_000, sid1, t0+20_000, sid1, t0+30_000, sid1, t0+35_000, sid1, t0+40_000, sid1, t0+60_000)

	// Child subagent of Session 1: clean trajectory (Edit between Reads clears seenReads,
	// and Edit between identical Bash hashes clears lastBashHash so RETRY_LOOP does not fire).
	subID := "bbbb2222-0000-0000-0000-000000000002"
	mustExec(`INSERT INTO sessions(id, vendor, project_root, parent_id, agent_type, model, started_at, ended_at, duration_ms)
		VALUES(?, 'agy', '/work/fyr', ?, 'reviewer', 'gemini-3-flash', ?, ?, 30000)`,
		subID, sid1, t0+70_000, t0+100_000)
	mustExec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, cmd_hash, is_error, denied, tok_in, tok_out, cache_read, cache_write, context) VALUES
		(?, '1', ?, 'assistant', '', '', 0, 0, 5000, 500, 15000, 0, 20000),
		(?, '2', ?, 'tool', 'Read', 'f:abc123', 0, 0, 0, 0, 0, 0, 0),
		(?, '3', ?, 'tool', 'Bash', 'cmd777', 1, 0, 0, 0, 0, 0, 0),
		(?, '4', ?, 'tool', 'Edit', 'f:abc123', 0, 0, 0, 0, 0, 0, 0),
		(?, '5', ?, 'tool', 'Bash', 'cmd777', 0, 0, 0, 0, 0, 0, 0),
		(?, '6', ?, 'tool', 'Read', 'f:abc123', 0, 0, 0, 0, 0, 0, 0)`,
		subID, t0+75_000, subID, t0+80_000, subID, t0+82_000, subID, t0+85_000, subID, t0+88_000, subID, t0+90_000)

	// Session 2: older clean session on another project.
	sid2 := "aaaa3333-0000-0000-0000-000000000003"
	mustExec(`INSERT INTO sessions(id, vendor, project_root, parent_id, agent_type, model, started_at, ended_at, duration_ms)
		VALUES(?, 'agy', '/work/other', '', '', 'claude-sonnet-4-6', ?, ?, 30000)`,
		sid2, t0-3600_000, t0-3570_000)
	mustExec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, cmd_hash, is_error, denied, tok_in, tok_out, cache_read, cache_write, context) VALUES
		(?, '1', ?, 'assistant', '', '', 0, 0, 10000, 500, 30000, 0, 40000)`,
		sid2, t0-3590_000)

	list, err := st.Trajectories(TrajectoryOptions{Project: "/work/fyr"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d trajectories, want 1", len(list))
	}
	got := list[0]
	if got.SessionID != sid1 {
		t.Errorf("SessionID = %q, want %q", got.SessionID, sid1)
	}
	if got.DurationSec != 120 {
		t.Errorf("DurationSec = %d, want 120", got.DurationSec)
	}
	if got.Steps != 4 {
		t.Errorf("Steps = %d, want 4", got.Steps)
	}
	if got.PeakContextTok != 190000 {
		t.Errorf("PeakContextTok = %d, want 190000", got.PeakContextTok)
	}
	if len(got.TurnTokens) != 3 || got.TurnTokens[0] != 50000 || got.TurnTokens[1] != 120000 || got.TurnTokens[2] != 190000 {
		t.Errorf("TurnTokens = %v, want [50000 120000 190000]", got.TurnTokens)
	}
	wantAnom := "KNEE_EXCEEDED,REDUNDANT_READ,RETRY_LOOP"
	if strings.Join(got.Anomalies, ",") != wantAnom {
		t.Errorf("Anomalies = %v, want %s", got.Anomalies, wantAnom)
	}
	if len(got.Subagents) != 1 {
		t.Fatalf("Subagents len = %d, want 1", len(got.Subagents))
	}
	sub := got.Subagents[0]
	if sub.AgentType != "reviewer" || sub.DurationSec != 30 || sub.Steps != 5 || len(sub.Anomalies) != 0 {
		t.Errorf("subagent = %+v", sub)
	}

	// Prefix lookup: unique prefix "aaaa1111" succeeds; ambiguous prefix "aaaa" fails.
	byPrefix, err := st.Trajectories(TrajectoryOptions{SessionID: "aaaa1111"})
	if err != nil || len(byPrefix) != 1 {
		t.Fatalf("byPrefix = %v, err = %v", byPrefix, err)
	}
	if _, err := st.Trajectories(TrajectoryOptions{SessionID: "aaaa"}); err == nil || !strings.Contains(err.Error(), "ambiguous session prefix") {
		t.Fatalf("ambiguous prefix err = %v, want ambiguous session prefix error", err)
	}

	// JSON and text rendering.
	var jb bytes.Buffer
	if err := WriteTrajectoriesJSON(&jb, list); err != nil {
		t.Fatal(err)
	}
	var decoded []SessionTrajectory
	if err := json.Unmarshal(jb.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal JSON: %v", err)
	}
	if len(decoded) != 1 || len(decoded[0].Subagents) != 1 {
		t.Fatalf("decoded JSON = %+v", decoded)
	}

	var tb bytes.Buffer
	WriteTrajectoriesText(&tb, list)
	txt := tb.String()
	for _, want := range []string{
		"aaaa1111",
		"KNEE_EXCEEDED,REDUNDANT_READ,RETRY_LOOP",
		"turns: t1=50.0k -> t2=120.0k -> t3=190.0k (knee 180.0k)",
		"└─ bbbb2222 (reviewer)",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("text output missing %q:\n%s", want, txt)
		}
	}
}
