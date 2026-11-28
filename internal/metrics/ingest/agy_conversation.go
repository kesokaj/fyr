package ingest

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/search"
)

type agyStep struct {
	idx, typ, status int
	meta, errDetails []byte
}

func readAgySteps(db *sql.DB) ([]agyStep, [][]byte, error) {
	rows, hasErrDetails, err := queryAgyStepRows(db)
	if err != nil {
		return nil, nil, err
	}
	var steps []agyStep
	for rows.Next() {
		var st agyStep
		if hasErrDetails {
			err = rows.Scan(&st.idx, &st.typ, &st.status, &st.meta, &st.errDetails)
		} else {
			err = rows.Scan(&st.idx, &st.typ, &st.status, &st.meta)
		}
		if err != nil {
			rows.Close()
			return nil, nil, err
		}
		steps = append(steps, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	gens, err := readAgyGens(db)
	return steps, gens, err
}

func queryAgyStepRows(db *sql.DB) (*sql.Rows, bool, error) {
	rows, err := db.Query(`SELECT idx, step_type, status, metadata, error_details FROM steps ORDER BY idx`)
	if err == nil {
		return rows, true, nil
	}
	if !strings.Contains(err.Error(), "no such column: error_details") {
		return nil, false, err
	}
	rows, err = db.Query(`SELECT idx, step_type, status, metadata FROM steps ORDER BY idx`)
	return rows, false, err
}

func readAgyGens(db *sql.DB) ([][]byte, error) {
	rows, err := db.Query(`SELECT data FROM gen_metadata ORDER BY idx`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var gens [][]byte
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		gens = append(gens, b)
	}
	return gens, rows.Err()
}

// AgySession stores the conversation's session row inside the writer transaction.
func (w *Writer) AgySession(c AgyConv) {
	repoID := w.repoID(c.Root)
	w.exec(`INSERT INTO sessions(id, vendor, project_root, repo_id, parent_id, agent_type, label, outcome, ended_at, run)
		VALUES (?, 'agy', NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, 0), NULLIF(?, ''))
		ON CONFLICT(id) DO UPDATE SET
			project_root = CASE
				WHEN excluded.project_root IS NOT NULL AND (sessions.project_root IS NULL OR sessions.project_root = '' OR sessions.project_root LIKE '%/.gemini/antigravity-cli/worktrees/%')
				THEN excluded.project_root
				ELSE COALESCE(sessions.project_root, excluded.project_root)
			END,
			repo_id = CASE
				WHEN excluded.repo_id != '' AND (sessions.repo_id = '' OR sessions.project_root IS NULL OR sessions.project_root = '' OR sessions.project_root LIKE '%/.gemini/antigravity-cli/worktrees/%')
				THEN excluded.repo_id
				ELSE sessions.repo_id
			END,
			parent_id = COALESCE(sessions.parent_id, excluded.parent_id),
			agent_type = COALESCE(sessions.agent_type, excluded.agent_type),
			label = COALESCE(sessions.label, excluded.label),
			outcome = COALESCE(sessions.outcome, excluded.outcome),
			ended_at = MAX(COALESCE(sessions.ended_at, 0), COALESCE(excluded.ended_at, 0)),
			run = COALESCE(sessions.run, excluded.run)`,
		c.ID, c.Root, repoID, c.Parent, Identifier(c.Agent), w.label, w.outcome, c.Modified, w.run)
}

func (w *Writer) agySteps(c AgyConv, steps []agyStep) []int64 {
	var replyTimes []int64
	for _, st := range steps {
		f := FieldMap(st.meta)
		ts := f.TimeMS(1)
		_, ended, startedCol, endedCol, waitCol := stepTimes(st.meta, f)
		ext := fmt.Sprintf("%s:%d", c.ID, st.idx)
		switch st.typ {
		case AgyUser:
			w.step(c.ID, ext, ts, "user")
		case AgyReply:
			w.timedStep(c.ID, ext, ts, "assistant", startedCol, endedCol, waitCol)
			replyTimes = append(replyTimes, ts)
		case AgyCheckpoint:
			w.step(c.ID, ext, ts, "compaction")
		case AgyTool:
			w.agyTool(c, ext, ts, st, f, startedCol, endedCol, waitCol)
		default:
			continue
		}
		end := max(ts, ended)
		if ts > 0 {
			w.exec(`UPDATE sessions SET started_at = MIN(COALESCE(started_at, ?), ?), ended_at = MAX(COALESCE(ended_at, ?), ?) WHERE id = ?`,
				ts, ts, end, end, c.ID)
		}
	}
	return replyTimes
}

type statusEntry struct {
	status int
	at     int64
}

func statusHistory(meta []byte) []statusEntry {
	var out []statusEntry
	for _, f := range search.ProtoFields(meta) {
		if f.Wire == 2 && len(f.Path) == 2 && f.Path[0] == 26 && f.Path[1] == 1 {
			ef := FieldMap(f.Bytes)
			out = append(out, statusEntry{status: int(ef.Varint(1)), at: ef.TimeMS(2)})
		}
	}
	return out
}

// WaitMS returns the milliseconds a step spent in status 9 (WAITING).
func WaitMS(meta []byte, ended int64) int64 {
	hist := statusHistory(meta)
	var wait int64
	for i, e := range hist {
		if e.status != AgyWaiting || e.at <= 0 {
			continue
		}
		next := ended
		if i+1 < len(hist) && hist[i+1].at > 0 {
			next = hist[i+1].at
		}
		if next > e.at {
			wait += next - e.at
		}
	}
	return wait
}

func stepTimes(meta []byte, f Fields) (started, ended int64, startedCol, endedCol, waitCol any) {
	started = f.TimeMS(6)
	if started == 0 {
		started = f.TimeMS(7)
	}
	ended = f.TimeMS(8)
	if ended == 0 {
		ended = f.TimeMS(7)
	}
	wait := WaitMS(meta, ended)
	if started > 0 {
		startedCol = started
	}
	if ended > 0 {
		endedCol = ended
	}
	if started > 0 || ended > 0 || wait > 0 {
		waitCol = wait
	}
	return started, ended, startedCol, endedCol, waitCol
}

type genScanState struct {
	seenModels map[string]bool
	firstModel string
	peakCtx    int64
	peakTS     int64
}

func (st *genScanState) trackContext(effCtx, tsMS int64) {
	if effCtx <= st.peakCtx {
		return
	}
	st.peakCtx = effCtx
	if tsMS > 0 {
		st.peakTS = tsMS
	}
}

func (st *genScanState) trackModel(m string) {
	if m == "" {
		return
	}
	if st.firstModel == "" {
		st.firstModel = m
	}
	st.seenModels[strings.ToLower(m)] = true
}

// AgyGensWithOverrides stores one generation step per gen_metadata row and updates session model/knee state.
func (w *Writer) AgyGensWithOverrides(c AgyConv, gens [][]byte, replyTimes []int64, overrides map[string]int64) {
	st := &genScanState{seenModels: map[string]bool{}}
	for i, g := range gens {
		w.recordAgyGen(c, i, g, replyTimes, st)
	}
	sessionModel := NormalizeSessionModel(st.firstModel, st.seenModels)
	if sessionModel != "" {
		w.exec(`UPDATE sessions SET model = ? WHERE id = ?`, sessionModel, c.ID)
	}
	if len(gens) > 0 {
		w.syncKneeExceededEvent(c, sessionModel, st.peakCtx, st.peakTS, overrides)
	}
}

func (w *Writer) recordAgyGen(c AgyConv, i int, g []byte, replyTimes []int64, st *genScanState) {
	f := FieldMap(g)
	var ts any
	var tsMS int64
	if i < len(replyTimes) && replyTimes[i] > 0 {
		ts, tsMS = replyTimes[i], replyTimes[i]
	}
	tokIn, tokOut, cacheRead := f.Varint(1, 4, 2), f.Varint(1, 4, 3), f.Varint(1, 4, 5)
	used := f.Varint(1, 9, 10, 1)
	var ctx any
	effCtx := int64(tokIn + cacheRead)
	if used > 0 {
		ctx = used
		effCtx = int64(used)
	}
	st.trackContext(effCtx, tsMS)
	w.exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tok_in, tok_out, cache_read, context)
		VALUES (?, ?, ?, 'generation', ?, ?, ?, ?) ON CONFLICT(session_id, ext_id) DO UPDATE SET
			ts = excluded.ts, tok_in = excluded.tok_in, tok_out = excluded.tok_out,
			cache_read = excluded.cache_read, context = excluded.context`,
		c.ID, fmt.Sprintf("%s:gen:%d", c.ID, i), ts, tokIn, tokOut, cacheRead, ctx)
	st.trackModel(genModelID(f))
}

func genModelID(f Fields) string {
	if m := Identifier(f.Text(3, 28)); m != "" {
		return m
	}
	return Identifier(f.Text(1, 19))
}

func (w *Writer) syncKneeExceededEvent(c AgyConv, sessionModel string, peakCtx, peakTS int64, overrides map[string]int64) {
	knee := models.ContextKneeWithOverrides(sessionModel, overrides)
	if knee > 0 && peakCtx >= knee {
		ts := peakTS
		if ts == 0 {
			ts = c.Modified
		}
		w.exec(`INSERT INTO events(session_id, ts, kind, ref, value) VALUES (?, ?, 'knee_exceeded', 'context_knee', ?)
			ON CONFLICT(session_id, kind, ref) DO UPDATE SET ts = excluded.ts, value = excluded.value`,
			c.ID, ts, strconv.FormatInt(peakCtx, 10))
		return
	}
	w.exec(`DELETE FROM events WHERE session_id = ? AND kind = 'knee_exceeded' AND ref = 'context_knee'`, c.ID)
}

// NormalizeSessionModel collapses bare stems paired with effort variants or formats multi-model orchestrators.
func NormalizeSessionModel(first string, seen map[string]bool) string {
	if len(seen) == 0 {
		return first
	}
	list := make([]string, 0, len(seen))
	for m := range seen {
		list = append(list, strings.ToLower(strings.TrimSpace(m)))
	}
	sort.Strings(list)
	for _, m := range list {
		if strings.Contains(m, "orchestrator") {
			return m
		}
	}
	collapsed := collapseBareModelStems(list)
	if len(collapsed) == 1 {
		if first != "" && strings.EqualFold(first, collapsed[0]) {
			return first
		}
		return collapsed[0]
	}
	return "orchestrator(" + strings.Join(collapsed, "+") + ")"
}

func collapseBareModelStems(sortedModels []string) []string {
	stemsWithEffort := map[string]bool{}
	for _, m := range sortedModels {
		if stem, hasEffort := modelStemAndEffort(m); hasEffort {
			stemsWithEffort[stem] = true
		}
	}
	if len(stemsWithEffort) == 0 {
		return sortedModels
	}
	out := make([]string, 0, len(sortedModels))
	for _, m := range sortedModels {
		if _, hasEffort := modelStemAndEffort(m); !hasEffort && stemsWithEffort[m] {
			continue
		}
		out = append(out, m)
	}
	return out
}

func modelStemAndEffort(m string) (string, bool) {
	for _, suf := range []string{"-xhigh", "-max", "-high", "-medium", "-low", "-minimal"} {
		if strings.HasSuffix(m, suf) {
			if stem := strings.TrimSuffix(m, suf); stem != "" {
				return stem, true
			}
		}
	}
	return m, false
}

func (w *Writer) agyIngestState(source string, modified int64) {
	fi, err := os.Lstat(source)
	var size int64
	if err == nil && fi.Mode().IsRegular() {
		size = fi.Size()
	}
	w.exec(`INSERT INTO ingest_state(source, size, mtime, offset) VALUES (?, ?, ?, 0)
		ON CONFLICT(source) DO UPDATE SET size = excluded.size, mtime = excluded.mtime`, source, size, modified)
}
