package metrics

import (
	"database/sql"
	"fmt"
	"os"
)

// agyStep is one row of a conversation's steps table.
type agyStep struct {
	idx, typ, status int
	meta             []byte
}

// readAgySteps reads a conversation copy's steps and its generations'
// metadata, in order; a copy with no gen_metadata table has no generations.
func readAgySteps(db *sql.DB) ([]agyStep, [][]byte, error) {
	var steps []agyStep
	rows, err := db.Query(`SELECT idx, step_type, status, metadata FROM steps ORDER BY idx`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var st agyStep
		if err := rows.Scan(&st.idx, &st.typ, &st.status, &st.meta); err != nil {
			rows.Close()
			return nil, nil, err
		}
		steps = append(steps, st)
	}
	rows.Close()
	var gens [][]byte
	if rows, err := db.Query(`SELECT data FROM gen_metadata ORDER BY idx`); err == nil {
		for rows.Next() {
			var b []byte
			if rows.Scan(&b) == nil {
				gens = append(gens, b)
			}
		}
		rows.Close()
	}
	return steps, gens, nil
}

// agySession stores the conversation's session row; a value stored before
// is kept (the run among them), and its end only moves later.
func (w *writer) agySession(c agyConv) {
	w.exec(`INSERT INTO sessions(id, vendor, project_root, parent_id, agent_type, label, outcome, ended_at, run)
		VALUES (?, 'agy', NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, 0), NULLIF(?, ''))
		ON CONFLICT(id) DO UPDATE SET
			project_root = COALESCE(sessions.project_root, excluded.project_root),
			parent_id = COALESCE(sessions.parent_id, excluded.parent_id),
			agent_type = COALESCE(sessions.agent_type, excluded.agent_type),
			label = COALESCE(sessions.label, excluded.label),
			outcome = COALESCE(sessions.outcome, excluded.outcome),
			ended_at = MAX(COALESCE(sessions.ended_at, 0), COALESCE(excluded.ended_at, 0)),
			run = COALESCE(sessions.run, excluded.run)`,
		c.id, c.root, c.parent, identifier(c.agent), w.label, w.outcome, c.modified, w.run)
}

// agySteps stores the user, reply and tool steps, widening the session to
// each one's time, and returns the replies' times, which date the
// generations.
func (w *writer) agySteps(c agyConv, steps []agyStep) []int64 {
	var replyTimes []int64
	for _, st := range steps {
		f := fieldMap(st.meta)
		ts := int64(f.varint(1, 1)) * 1000
		ext := fmt.Sprintf("%s:%d", c.id, st.idx)
		switch st.typ {
		case agyUser:
			w.step(c.id, ext, ts, "user")
		case agyReply:
			w.step(c.id, ext, ts, "assistant")
			replyTimes = append(replyTimes, ts)
		case agyTool:
			w.agyTool(c, ext, ts, st.status == agyFailed, f)
		default:
			continue
		}
		if ts > 0 {
			w.exec(`UPDATE sessions SET started_at = MIN(COALESCE(started_at, ?), ?), ended_at = MAX(COALESCE(ended_at, ?), ?) WHERE id = ?`,
				ts, ts, ts, ts, c.id)
		}
	}
	return replyTimes
}

// agyGens stores one generation step per gen_metadata row: its tokens, its
// context, and the model it names.
func (w *writer) agyGens(c agyConv, gens [][]byte, replyTimes []int64) {
	for i, g := range gens {
		f := fieldMap(g)
		var ts any
		if i < len(replyTimes) && replyTimes[i] > 0 {
			ts = replyTimes[i]
		}
		var ctx any
		if used := f.varint(1, 9, 10, 1); used > 0 {
			ctx = used
		}
		w.exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tok_in, tok_out, cache_read, context)
			VALUES (?, ?, ?, 'generation', ?, ?, ?, ?) ON CONFLICT(session_id, ext_id) DO UPDATE SET
				ts = excluded.ts, tok_in = excluded.tok_in, tok_out = excluded.tok_out,
				cache_read = excluded.cache_read, context = excluded.context`,
			c.id, fmt.Sprintf("%s:gen:%d", c.id, i), ts, f.varint(1, 4, 2), f.varint(1, 4, 3), f.varint(1, 4, 5), ctx)
		if m := identifier(f.text(1, 19)); m != "" {
			w.exec(`UPDATE sessions SET model = COALESCE(model, ?) WHERE id = ?`, m, c.id)
		}
	}
}

// agyIngestState records the conversation's source size and modified time,
// which the next ingest compares to skip an unchanged conversation.
func (w *writer) agyIngestState(source string, modified int64) {
	fi, _ := os.Stat(source)
	var size int64
	if fi != nil {
		size = fi.Size()
	}
	w.exec(`INSERT INTO ingest_state(source, size, mtime, offset) VALUES (?, ?, ?, 0)
		ON CONFLICT(source) DO UPDATE SET size = excluded.size, mtime = excluded.mtime`, source, size, modified)
}
