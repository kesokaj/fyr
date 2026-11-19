package search

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

func checkWatchdogPollLoop(ctx context.Context, st *store, convID, normTool, ringTool string) string {
	if (normTool != "manage_task" && normTool != "manage_subagents") || ringTool != normTool {
		return ""
	}
	var cnt, allPoll int
	q := `SELECT COUNT(*), COALESCE(MIN(CASE WHEN tool IN ('manage_task','manage_subagents') THEN 1 ELSE 0 END), 0) FROM (SELECT tool FROM watchdog_ring WHERE conversation_id = ? ORDER BY seq DESC LIMIT 2)`
	if st.db.QueryRowContext(ctx, q, convID).Scan(&cnt, &allPoll) == nil && cnt == 2 && allPoll == 1 {
		return "[POLL_LOOP] >= 3 consecutive status/list polls; end turn to await wakeup or run fyr watch --check."
	}
	return ""
}

func checkWatchdogErrorLoop(ctx context.Context, st *store, convID, ringTool, argHash string) string {
	rows, err := st.db.QueryContext(ctx, `SELECT tool, arg_hash, is_error FROM watchdog_ring WHERE conversation_id = ? ORDER BY seq DESC LIMIT 4`, convID)
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
	if len(ring) >= 3 && ring[0].isError == 1 && ring[1].isError == 1 && ring[2].isError == 1 &&
		ring[0].tool == ringTool && ring[1].tool == ringTool && ring[2].tool == ringTool &&
		ring[0].argHash == argHash && ring[1].argHash == argHash && ring[2].argHash == argHash {
		return "[TOKEN_LOOP] >= 3 consecutive identical failing tool calls"
	}
	key := func(r watchdogRingRow) string { return r.tool + ":" + r.argHash }
	incoming := ringTool + ":" + argHash
	if len(ring) >= 4 && ring[0].isError == 1 && ring[1].isError == 1 && ring[2].isError == 1 && ring[3].isError == 1 &&
		key(ring[0]) == key(ring[2]) && key(ring[1]) == key(ring[3]) && key(ring[0]) != key(ring[1]) &&
		incoming == key(ring[1]) {
		return "[TOKEN_LOOP] repeating A-B-A-B error loop"
	}
	return ""
}

func finalizeWatchdogPreCtx(ctx context.Context, st *store, dir string, in HookInput, wd watchdogPreState, raw []byte, out io.Writer) error {
	var dec HookDecisionOutput
	if json.Unmarshal(bytes.TrimSpace(raw), &dec) != nil || dec.Decision != "allow" {
		_, _ = out.Write(raw)
		return nil
	}
	if wd.active && ctx.Err() == nil {
		recordWatchdogAllowWithStore(ctx, st, dir, in, wd)
		if wd.budgetWarn != "" && strings.TrimSpace(dec.Reason) == "" {
			writeDecisionWithOverwrite(out, "allow", wd.budgetWarn, dec.Overwrite)
			return nil
		}
	}
	_, _ = out.Write(raw)
	return nil
}

func recordWatchdogAllowWithStore(ctx context.Context, st *store, dir string, in HookInput, wd watchdogPreState) {
	if ctx.Err() != nil {
		return
	}
	if st == nil {
		opened, _, err := openLedgerStore(dir)
		if err != nil || opened == nil {
			return
		}
		defer opened.close()
		st = opened
	}
	convID := strings.TrimSpace(in.ConversationID)
	callID := strings.TrimSpace(in.ToolCall.ID)
	ts := nowFunc().Unix()
	_ = st.withImmediateTx(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := st.db.ExecContext(ctx,
			`INSERT INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens, agent_role, agent_name, tool_calls) VALUES (?, 0, 0, 0, 0, ?, ?, 1) ON CONFLICT(conversation_id) DO UPDATE SET agent_role = CASE WHEN session_meta.agent_role = '' THEN excluded.agent_role ELSE session_meta.agent_role END, agent_name = CASE WHEN session_meta.agent_role = '' THEN excluded.agent_name ELSE session_meta.agent_name END, tool_calls = session_meta.tool_calls + 1`,
			convID, wd.role, wd.agentName,
		); err != nil {
			return err
		}
		if _, err := st.db.ExecContext(ctx, `INSERT INTO watchdog_ring(conversation_id, tool_call_id, tool, arg_hash, is_error, ts) VALUES (?, ?, ?, ?, 0, ?)`, convID, callID, wd.ringTool, wd.argHash, ts); err != nil {
			return err
		}
		_, err := st.db.ExecContext(ctx, `DELETE FROM watchdog_ring WHERE conversation_id = ? AND seq NOT IN (SELECT seq FROM watchdog_ring WHERE conversation_id = ? ORDER BY seq DESC LIMIT 64)`, convID, convID)
		return err
	})
}

func handlePostToolUse(ctx context.Context, o Options, dir string, in HookInput, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(in.Error) != "" && !watchdogShouldSkip(o.Home, dir, in) {
		markWatchdogPostErrorCtx(ctx, dir, in)
	}
	_, _ = out.Write([]byte("{}\n"))
	return nil
}

func markWatchdogPostErrorCtx(ctx context.Context, dir string, in HookInput) {
	if ctx.Err() != nil {
		return
	}
	st, _, err := openLedgerStore(dir)
	if err != nil || st == nil {
		return
	}
	defer st.close()
	convID := strings.TrimSpace(in.ConversationID)
	if callID := strings.TrimSpace(in.ToolCall.ID); callID != "" {
		_, _ = st.db.ExecContext(ctx, `UPDATE watchdog_ring SET is_error = 1 WHERE conversation_id = ? AND tool_call_id = ?`, convID, callID)
		return
	}
	ringTool := watchdogRingTool(in.ToolCall)
	if execRowsAffected(ctx, st, `UPDATE watchdog_ring SET is_error = 1 WHERE seq = (SELECT seq FROM watchdog_ring WHERE conversation_id = ? AND tool = ? ORDER BY seq DESC LIMIT 1)`, convID, ringTool) > 0 {
		return
	}
	if norm := normalizeWatchdogToolName(in.ToolCall.Name); norm != "" {
		_, _ = st.db.ExecContext(ctx, `UPDATE watchdog_ring SET is_error = 1 WHERE seq = (SELECT seq FROM watchdog_ring WHERE conversation_id = ? AND (tool = ? OR tool LIKE ?) ORDER BY seq DESC LIMIT 1)`, convID, norm, norm+":%")
	}
}

func execRowsAffected(ctx context.Context, st *store, query string, args ...any) int64 {
	res, err := st.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}
