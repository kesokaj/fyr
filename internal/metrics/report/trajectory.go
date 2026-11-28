package report

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/models"
)

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
	KneeTokens     int64               `json:"kneeTokens,omitempty"`
	CacheHitPct    float64             `json:"cacheHitPct"`
	Steps          int                 `json:"steps"`
	Subagents      []SessionTrajectory `json:"subagents,omitempty"`
	TurnTokens     []int64             `json:"turnTokens,omitempty"`
	Anomalies      []string            `json:"anomalies,omitempty"`
}

// TrajectoryStep carries the minimal step fields scanned for trajectory anomalies.
type TrajectoryStep struct {
	Kind, Tool, Hash string
	IsErr, Denied    bool
	In, CR, CW, Ctx  int64
}

// TrajectoryDeps supplies store-backed step loading and range helpers.
type TrajectoryDeps struct {
	Home               string
	StepsOf            func(sessionID string) ([]TrajectoryStep, error)
	ParseFileHashRange func(hash string) (ranged bool, base string, start, end int)
	RecordReadInterval func(seen map[string][][2]int, base string, start, end int) bool
}

// Trajectories returns session trajectories matching o, newest first.
func Trajectories(db *sql.DB, o TrajectoryOptions, deps TrajectoryDeps) ([]SessionTrajectory, error) {
	roots, err := queryRootSessions(db, o)
	if err != nil {
		return nil, err
	}
	overrides := models.ContextWindowOverrides(deps.Home)
	out := make([]SessionTrajectory, 0, len(roots))
	for _, root := range roots {
		if err := populateTrajectorySteps(&root, overrides, deps); err != nil {
			return nil, err
		}
		subs, err := queryChildSessions(db, root.SessionID, overrides, deps)
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

func queryRootSessions(db *sql.DB, o TrajectoryOptions) ([]SessionTrajectory, error) {
	if o.SessionID != "" {
		return queryBySessionID(db, o.SessionID, o.Project)
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
	return scanTrajectoryRows(db, q, args...)
}

func queryBySessionID(db *sql.DB, sessionID, project string) ([]SessionTrajectory, error) {
	q := trajectoryCols + ` WHERE (id = ? OR substr(id, 1, length(?)) = ?)`
	args := []any{sessionID, sessionID, sessionID}
	q, args = appendProjectClause(q, args, project)
	q += ` ORDER BY COALESCE(started_at, ended_at, 0) DESC, id DESC`
	rows, err := scanTrajectoryRows(db, q, args...)
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

func queryChildSessions(db *sql.DB, parentID string, overrides map[string]int64, deps TrajectoryDeps) ([]SessionTrajectory, error) {
	q := trajectoryCols + ` WHERE parent_id = ? ORDER BY COALESCE(started_at, ended_at, 0) ASC, id ASC`
	subs, err := scanTrajectoryRows(db, q, parentID)
	if err != nil {
		return nil, err
	}
	for i := range subs {
		if err := populateTrajectorySteps(&subs[i], overrides, deps); err != nil {
			return nil, err
		}
	}
	return subs, nil
}

func scanTrajectoryRows(db *sql.DB, q string, args ...any) ([]SessionTrajectory, error) {
	rows, err := db.Query(q, args...)
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
	t.Project = RedactHomePath(t.Project)
	return t, nil
}

// RedactHomePath replaces the current user's home directory prefix with "~".
func RedactHomePath(p string) string {
	s := strings.TrimSpace(p)
	if s == "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return p
	}
	clean := filepath.Clean(s)
	for _, cand := range homePrefixCandidates(home) {
		if clean == cand {
			return "~"
		}
		if strings.HasPrefix(clean, cand+string(filepath.Separator)) {
			return "~" + clean[len(cand):]
		}
	}
	return p
}

func homePrefixCandidates(home string) []string {
	clean := filepath.Clean(home)
	out := []string{clean}
	if realHome, err := filepath.EvalSymlinks(clean); err == nil {
		realClean := filepath.Clean(realHome)
		if realClean != "" && realClean != "/" && realClean != clean {
			out = append(out, realClean)
		}
	}
	return out
}

type stepScanner struct {
	seenReads     map[string][][2]int
	lastBashHash  string
	toolSteps     int
	tokIn         int64
	cacheRead     int64
	cacheWrite    int64
	peakTok       int64
	turnTokens    []int64
	redundantRead bool
	retryLoop     bool
	deps          TrajectoryDeps
}

func populateTrajectorySteps(t *SessionTrajectory, overrides map[string]int64, deps TrajectoryDeps) error {
	steps, err := deps.StepsOf(t.SessionID)
	if err != nil {
		return err
	}
	sc := stepScanner{seenReads: map[string][][2]int{}, deps: deps}
	for _, st := range steps {
		sc.observe(st)
	}
	sc.applyTo(t, overrides)
	return nil
}

func (sc *stepScanner) observe(st TrajectoryStep) {
	sc.tokIn += st.In
	sc.cacheRead += st.CR
	sc.cacheWrite += st.CW
	ctx := st.Ctx
	if ctx == 0 && (st.Kind == "assistant" || st.Kind == "generation") {
		ctx = st.In + st.CR + st.CW
	}
	if ctx > 0 {
		tt := append(sc.turnTokens, ctx)
		sc.turnTokens = tt
		if ctx > sc.peakTok {
			sc.peakTok = ctx
		}
	}
	switch st.Kind {
	case "compaction":
		clear(sc.seenReads)
	case "tool":
		sc.observeTool(st)
	}
}

func (sc *stepScanner) observeTool(st TrajectoryStep) {
	if st.Tool == "" {
		return
	}
	sc.toolSteps++
	if st.Tool == "Bash" {
		if st.Hash != "" && st.Hash == sc.lastBashHash {
			sc.retryLoop = true
		}
		sc.lastBashHash = st.Hash
	} else {
		sc.lastBashHash = ""
	}
	if st.IsErr || st.Denied || sc.deps.ParseFileHashRange == nil {
		return
	}
	_, base, start, end := sc.deps.ParseFileHashRange(st.Hash)
	if base == "" {
		return
	}
	switch st.Tool {
	case "Read":
		if sc.deps.RecordReadInterval != nil && sc.deps.RecordReadInterval(sc.seenReads, base, start, end) {
			sc.redundantRead = true
		}
	case "Write", "Edit":
		delete(sc.seenReads, base)
	}
}

func (sc *stepScanner) applyTo(t *SessionTrajectory, overrides map[string]int64) {
	t.Steps = sc.toolSteps
	if t.Steps == 0 {
		t.Steps = len(sc.turnTokens)
	}
	tt := sc.turnTokens
	t.TurnTokens = tt
	t.PeakContextTok = sc.peakTok
	if denom := sc.tokIn + sc.cacheRead + sc.cacheWrite; denom > 0 {
		t.CacheHitPct = float64(sc.cacheRead) * 100 / float64(denom)
	}
	knee := models.ContextKneeWithOverrides(t.Model, overrides)
	t.KneeTokens = knee
	if knee > 0 && t.PeakContextTok >= knee {
		t.Anomalies = append(t.Anomalies, "KNEE_EXCEEDED")
	}
	if sc.redundantRead {
		t.Anomalies = append(t.Anomalies, "REDUNDANT_READ")
	}
	if sc.retryLoop {
		t.Anomalies = append(t.Anomalies, "RETRY_LOOP")
	}
}
