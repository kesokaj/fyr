package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
)

type watchdogRingRow struct {
	tool, argHash string
	isError       int
}

func checkWatchdogPollLoop(ctx context.Context, st *Store, convID, normTool, ringTool string) string {
	if (normTool != "manage_task" && normTool != "manage_subagents") || ringTool != normTool {
		return ""
	}
	var cnt, allPoll int
	q := `SELECT COUNT(*), COALESCE(MIN(CASE WHEN tool IN ('manage_task','manage_subagents') THEN 1 ELSE 0 END), 0) FROM (SELECT tool FROM watchdog_ring WHERE conversation_id = ? ORDER BY seq DESC LIMIT 2)`
	if st.SQLDB.QueryRowContext(ctx, q, convID).Scan(&cnt, &allPoll) == nil && cnt == 2 && allPoll == 1 {
		return "[POLL_LOOP] >= 3 consecutive status/list polls; end turn to await wakeup or run fyr watch --check."
	}
	return ""
}

func checkWatchdogErrorLoop(ctx context.Context, st *Store, convID, ringTool, argHash string) string {
	rows, err := st.SQLDB.QueryContext(ctx, `SELECT tool, arg_hash, is_error FROM watchdog_ring WHERE conversation_id = ? ORDER BY seq DESC LIMIT 4`, convID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var ring []watchdogRingRow
	for rows.Next() {
		var r watchdogRingRow
		if rows.Scan(&r.tool, &r.argHash, &r.isError) == nil {
			ring = append(ring, r)
		}
	}
	if isThreeIdenticalErrors(ring, ringTool, argHash) {
		return "[ERROR_LOOP] >= 3 consecutive identical failing tool calls"
	}
	if isABABErrorLoop(ring, ringTool, argHash) {
		return "[ERROR_LOOP] repeating A-B-A-B error loop"
	}
	return ""
}

func isThreeIdenticalErrors(ring []watchdogRingRow, ringTool, argHash string) bool {
	if len(ring) < 3 {
		return false
	}
	for i := range 3 {
		if ring[i].isError != 1 || ring[i].tool != ringTool || ring[i].argHash != argHash {
			return false
		}
	}
	return true
}

func isABABErrorLoop(ring []watchdogRingRow, ringTool, argHash string) bool {
	if len(ring) < 4 {
		return false
	}
	for i := range 4 {
		if ring[i].isError != 1 {
			return false
		}
	}
	sig := func(r watchdogRingRow) string { return r.tool + ":" + r.argHash }
	a, b := sig(ring[0]), sig(ring[1])
	return a == sig(ring[2]) && b == sig(ring[3]) && a != b && (ringTool+":"+argHash) == b
}

func finalizeWatchdogPreCtx(ctx context.Context, d Deps, st *Store, dir string, in HookInput, wd watchdogPreState, raw []byte, out io.Writer) error {
	var dec HookDecisionOutput
	if json.Unmarshal(bytes.TrimSpace(raw), &dec) != nil || dec.Decision != "allow" {
		_, _ = out.Write(raw)
		return nil
	}
	if wd.active && ctx.Err() == nil {
		recordWatchdogAllowWithStore(ctx, d, st, dir, in, wd)
		if wd.budgetWarn != "" && strings.TrimSpace(dec.Reason) == "" {
			writeDecisionWithOverwrite(out, "allow", wd.budgetWarn, dec.Overwrite)
			return nil
		}
	}
	_, _ = out.Write(raw)
	return nil
}

func recordWatchdogAllowWithStore(ctx context.Context, d Deps, st *Store, dir string, in HookInput, wd watchdogPreState) {
	if ctx.Err() != nil {
		return
	}
	if st == nil {
		if d.OpenLedgerStore == nil {
			return
		}
		opened, _, err := d.OpenLedgerStore(dir)
		if err != nil || opened == nil {
			return
		}
		defer opened.Close()
		st = opened
	}
	convID := strings.TrimSpace(in.ConversationID)
	callID := strings.TrimSpace(in.ToolCall.ID)
	ts := d.now().Unix()
	inc := 1
	if wd.exempt {
		inc = 0
	}
	_ = st.WithImmediateTx(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := st.SQLDB.ExecContext(ctx,
			`INSERT INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens, agent_role, agent_name, tool_calls) VALUES (?, 0, 0, 0, 0, ?, ?, ?) ON CONFLICT(conversation_id) DO UPDATE SET agent_role = CASE WHEN session_meta.agent_role = '' THEN excluded.agent_role ELSE session_meta.agent_role END, agent_name = CASE WHEN session_meta.agent_role = '' THEN excluded.agent_name ELSE session_meta.agent_name END, tool_calls = session_meta.tool_calls + ?`,
			convID, wd.role, wd.agentName, inc, inc,
		); err != nil {
			return err
		}
		if _, err := st.SQLDB.ExecContext(ctx, `INSERT INTO watchdog_ring(conversation_id, tool_call_id, tool, arg_hash, is_error, ts) VALUES (?, ?, ?, ?, 0, ?)`, convID, callID, wd.ringTool, wd.argHash, ts); err != nil {
			return err
		}
		_, err := st.SQLDB.ExecContext(ctx, `DELETE FROM watchdog_ring WHERE conversation_id = ? AND seq NOT IN (SELECT seq FROM watchdog_ring WHERE conversation_id = ? ORDER BY seq DESC LIMIT 64)`, convID, convID)
		return err
	})
}

func handlePostToolUse(ctx context.Context, d Deps, o Options, dir string, in HookInput, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(in.Error) != "" && !watchdogShouldSkip(o.Home, dir, in) {
		markWatchdogPostErrorCtx(ctx, d, dir, in)
	}
	_, _ = out.Write([]byte("{}\n"))
	return nil
}

func markWatchdogPostErrorCtx(ctx context.Context, d Deps, dir string, in HookInput) {
	if ctx.Err() != nil || d.OpenLedgerStore == nil {
		return
	}
	st, _, err := d.OpenLedgerStore(dir)
	if err != nil || st == nil {
		return
	}
	defer st.Close()
	convID := strings.TrimSpace(in.ConversationID)
	if callID := strings.TrimSpace(in.ToolCall.ID); callID != "" {
		_, _ = st.SQLDB.ExecContext(ctx, `UPDATE watchdog_ring SET is_error = 1 WHERE conversation_id = ? AND tool_call_id = ?`, convID, callID)
		return
	}
	ringTool := watchdogRingTool(in.ToolCall)
	if execRowsAffected(ctx, st, `UPDATE watchdog_ring SET is_error = 1 WHERE seq = (SELECT seq FROM watchdog_ring WHERE conversation_id = ? AND tool = ? ORDER BY seq DESC LIMIT 1)`, convID, ringTool) > 0 {
		return
	}
	if norm := normalizeWatchdogToolName(in.ToolCall.Name); norm != "" {
		_, _ = st.SQLDB.ExecContext(ctx, `UPDATE watchdog_ring SET is_error = 1 WHERE seq = (SELECT seq FROM watchdog_ring WHERE conversation_id = ? AND (tool = ? OR tool LIKE ?) ORDER BY seq DESC LIMIT 1)`, convID, norm, norm+":%")
	}
}

func execRowsAffected(ctx context.Context, st *Store, query string, args ...any) int64 {
	res, err := st.SQLDB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}
