package metrics

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

// ContextKneeTokens is the context token threshold above which a session is
// flagged with the KNEE_EXCEEDED anomaly (ADR-513, D6).
const ContextKneeTokens = 180000

// DefaultTrajectoryLimit is the default number of main sessions returned by
// Trajectories when TrajectoryOptions.Limit is not set.
const DefaultTrajectoryLimit = 10

// TrajectoryOptions configures a Trajectories query.
type TrajectoryOptions struct {
	Project   string
	SessionID string
	Limit     int
}

// SessionTrajectory summarizes one session's token trajectory, subagent
// hierarchy, and context/tool anomalies (ADR-513, D6).
type SessionTrajectory struct {
	SessionID      string              `json:"sessionId"`
	ParentID       string              `json:"parentId,omitempty"`
	AgentType      string              `json:"agentType,omitempty"`
	Project        string              `json:"project"`
	Model          string              `json:"model"`
	StartedAt      string              `json:"startedAt"`
	DurationSec    int64               `json:"durationSec"`
	PeakContextTok int64               `json:"peakContextTok"`
	CacheHitPct    float64             `json:"cacheHitPct"`
	Steps          int                 `json:"steps"`
	Subagents      []SessionTrajectory `json:"subagents,omitempty"`
	TurnTokens     []int64             `json:"turnTokens,omitempty"`
	Anomalies      []string            `json:"anomalies,omitempty"`
}

// Trajectories returns session trajectories matching o, newest first.
func (s *Store) Trajectories(o TrajectoryOptions) ([]SessionTrajectory, error) {
	roots, err := s.queryRootSessions(o)
	if err != nil {
		return nil, err
	}
	out := make([]SessionTrajectory, 0, len(roots))
	for _, root := range roots {
		if err := s.populateSteps(&root); err != nil {
			return nil, err
		}
		subs, err := s.queryChildSessions(root.SessionID)
		if err != nil {
			return nil, err
		}
		root.Subagents = subs
		out = append(out, root)
	}
	return out, nil
}

const trajectoryCols = `SELECT id, COALESCE(parent_id,''), COALESCE(agent_type,''),
	COALESCE(project_root,''), COALESCE(model,''), COALESCE(started_at,0),
	COALESCE(ended_at,0), COALESCE(duration_ms,0) FROM sessions`

func (s *Store) queryRootSessions(o TrajectoryOptions) ([]SessionTrajectory, error) {
	if o.SessionID != "" {
		return s.queryBySessionID(o.SessionID, o.Project)
	}
	limit := o.Limit
	if limit <= 0 {
		limit = DefaultTrajectoryLimit
	}
	q := trajectoryCols + ` WHERE COALESCE(parent_id,'') = ''`
	var args []any
	q, args = appendProjectClause(q, args, o.Project)
	q += ` ORDER BY COALESCE(started_at, ended_at, 0) DESC, id DESC LIMIT ?`
	args = append(args, limit)
	return s.scanTrajectoryRows(q, args...)
}

func (s *Store) queryBySessionID(sessionID, project string) ([]SessionTrajectory, error) {
	q := trajectoryCols + ` WHERE (id = ? OR substr(id, 1, length(?)) = ?)`
	args := []any{sessionID, sessionID, sessionID}
	q, args = appendProjectClause(q, args, project)
	q += ` ORDER BY COALESCE(started_at, ended_at, 0) DESC, id DESC`
	rows, err := s.scanTrajectoryRows(q, args...)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.SessionID == sessionID {
			return []SessionTrajectory{r}, nil
		}
	}
	if len(rows) > 1 {
		return nil, fmt.Errorf("ambiguous session prefix %q (%d matches)", sessionID, len(rows))
	}
	return rows, nil
}

func appendProjectClause(q string, args []any, project string) (string, []any) {
	if project == "" {
		return q, args
	}
	p := filepath.Clean(project)
	q += ` AND (project_root = ? OR substr(project_root, 1, length(?)) = ?)`
	return q, append(args, p, p+"/", p+"/")
}

func (s *Store) queryChildSessions(parentID string) ([]SessionTrajectory, error) {
	q := trajectoryCols + ` WHERE parent_id = ? ORDER BY COALESCE(started_at, ended_at, 0) ASC, id ASC`
	subs, err := s.scanTrajectoryRows(q, parentID)
	if err != nil {
		return nil, err
	}
	for i := range subs {
		if err := s.populateSteps(&subs[i]); err != nil {
			return nil, err
		}
	}
	return subs, nil
}

func (s *Store) scanTrajectoryRows(q string, args ...any) ([]SessionTrajectory, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("metrics: query trajectory sessions: %w", err)
	}
	defer rows.Close()
	var list []SessionTrajectory
	for rows.Next() {
		t, err := scanOneTrajectory(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func scanOneTrajectory(rows *sql.Rows) (SessionTrajectory, error) {
	var t SessionTrajectory
	var startMS, endMS, durMS int64
	if err := rows.Scan(&t.SessionID, &t.ParentID, &t.AgentType, &t.Project,
		&t.Model, &startMS, &endMS, &durMS); err != nil {
		return SessionTrajectory{}, fmt.Errorf("metrics: scan trajectory session: %w", err)
	}
	if startMS > 0 {
		t.StartedAt = time.UnixMilli(startMS).UTC().Format(time.RFC3339)
	}
	if endMS > startMS && startMS > 0 {
		t.DurationSec = (endMS - startMS) / 1000
	} else if durMS > 0 {
		t.DurationSec = durMS / 1000
	}
	return t, nil
}

type stepScanner struct {
	seenReads     map[string]bool
	lastBashHash  string
	toolSteps     int
	tokIn         int64
	cacheRead     int64
	cacheWrite    int64
	peakTok       int64
	turnTokens    []int64
	redundantRead bool
	retryLoop     bool
}

func (s *Store) populateSteps(t *SessionTrajectory) error {
	steps, err := s.stepsOf(t.SessionID)
	if err != nil {
		return err
	}
	sc := stepScanner{seenReads: map[string]bool{}}
	for _, st := range steps {
		sc.observe(st)
	}
	sc.applyTo(t)
	return nil
}

func (sc *stepScanner) observe(st stp) {
	sc.tokIn += st.in
	sc.cacheRead += st.cr
	sc.cacheWrite += st.cw
	ctx := st.ctx
	if ctx == 0 && (st.kind == "assistant" || st.kind == "generation") {
		ctx = st.in + st.cr + st.cw
	}
	if ctx > 0 {
		sc.turnTokens = append(sc.turnTokens, ctx)
		if ctx > sc.peakTok {
			sc.peakTok = ctx
		}
	}
	switch st.kind {
	case "compaction":
		clear(sc.seenReads)
	case "tool":
		sc.observeTool(st)
	}
}

func (sc *stepScanner) observeTool(st stp) {
	if st.tool == "" {
		return
	}
	sc.toolSteps++
	if st.tool == "Bash" {
		if st.hash != "" && st.hash == sc.lastBashHash {
			sc.retryLoop = true
		}
		sc.lastBashHash = st.hash
	} else {
		sc.lastBashHash = ""
	}
	if st.isErr || st.denied {
		return
	}
	_, base := parseFileHash(st.hash)
	if base == "" {
		return
	}
	switch st.tool {
	case "Read":
		if sc.seenReads[base] {
			sc.redundantRead = true
		} else {
			sc.seenReads[base] = true
		}
	case "Write", "Edit":
		delete(sc.seenReads, base)
	}
}

func (sc *stepScanner) applyTo(t *SessionTrajectory) {
	t.Steps = sc.toolSteps
	if t.Steps == 0 {
		t.Steps = len(sc.turnTokens)
	}
	t.TurnTokens = sc.turnTokens
	t.PeakContextTok = sc.peakTok
	if denom := sc.tokIn + sc.cacheRead + sc.cacheWrite; denom > 0 {
		t.CacheHitPct = float64(sc.cacheRead) * 100 / float64(denom)
	}
	if t.PeakContextTok > ContextKneeTokens {
		t.Anomalies = append(t.Anomalies, "KNEE_EXCEEDED")
	}
	if sc.redundantRead {
		t.Anomalies = append(t.Anomalies, "REDUNDANT_READ")
	}
	if sc.retryLoop {
		t.Anomalies = append(t.Anomalies, "RETRY_LOOP")
	}
}

// WriteTrajectoriesJSON encodes trajectories as indented JSON to w.
func WriteTrajectoriesJSON(w io.Writer, list []SessionTrajectory) error {
	if list == nil {
		list = []SessionTrajectory{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

// WriteTrajectoriesText renders trajectories in human-readable form to w.
func WriteTrajectoriesText(w io.Writer, list []SessionTrajectory) {
	if len(list) == 0 {
		fmt.Fprintln(w, "(no sessions)")
		return
	}
	for i, t := range list {
		if i > 0 {
			fmt.Fprintln(w)
		}
		writeOneTrajectoryText(w, t)
	}
}

func writeOneTrajectoryText(w io.Writer, t SessionTrajectory) {
	status := "OK"
	if len(t.Anomalies) > 0 {
		status = strings.Join(t.Anomalies, ",")
	}
	proj := t.Project
	if proj == "" {
		proj = "-"
	}
	fmt.Fprintf(w, "%s  %s  project=%s  model=%s  dur=%ds  steps=%d  peak=%s  cache=%.0f%%  [%s]\n",
		shortID(t.SessionID), t.StartedAt, proj, t.Model, t.DurationSec, t.Steps,
		fmtTokens(t.PeakContextTok), t.CacheHitPct, status)
	if len(t.TurnTokens) > 0 {
		parts := make([]string, len(t.TurnTokens))
		for i, tok := range t.TurnTokens {
			parts[i] = fmt.Sprintf("t%d=%s", i+1, fmtTokens(tok))
		}
		fmt.Fprintf(w, "  turns: %s (knee %s)\n", strings.Join(parts, " -> "), fmtTokens(ContextKneeTokens))
	}
	for _, sub := range t.Subagents {
		writeSubagentText(w, sub)
	}
}

func writeSubagentText(w io.Writer, sub SessionTrajectory) {
	subStatus := "OK"
	if len(sub.Anomalies) > 0 {
		subStatus = strings.Join(sub.Anomalies, ",")
	}
	agent := sub.AgentType
	if agent == "" {
		agent = "subagent"
	}
	fmt.Fprintf(w, "  └─ %s (%s)  model=%s  dur=%ds  steps=%d  peak=%s  cache=%.0f%%  [%s]\n",
		shortID(sub.SessionID), agent, sub.Model, sub.DurationSec, sub.Steps,
		fmtTokens(sub.PeakContextTok), sub.CacheHitPct, subStatus)
}

func fmtTokens(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
