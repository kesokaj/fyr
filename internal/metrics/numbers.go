package metrics

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kesokaj/fyr/internal/search"
)

// Snapshot opens a read-only copy of the store at path, made in tmp: a dry
// run and the dream's prompt read it without creating -wal or -shm beside
// the store or running a migration. A missing store is os.ErrNotExist.
func Snapshot(path, tmp string) (*Store, error) {
	if _, err := os.Lstat(path); err != nil {
		return nil, err
	}
	db, err := search.OpenCopy(filepath.Dir(path), filepath.Base(path), tmp)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, path: path}
	var v string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&v); err != nil {
		db.Close()
		return nil, fmt.Errorf("metrics: %s: %w", path, err)
	}
	if v != fmt.Sprint(len(migrations)) {
		db.Close()
		return nil, fmt.Errorf("metrics.db schema %s, this binary reads %d; run fyr metrics ingest first", v, len(migrations))
	}
	return s, nil
}

// Numbers is the summary as metric keys and numbers only: every key is one of
// this package's own names (no tool, skill, agent, label or path), the form
// the dream's prompt carries (R3, ADR-055a). A metric a CLI cannot produce is
// absent, never 0.
func (s *Summary) Numbers() map[string]float64 {
	n := map[string]float64{}
	put := func(k string, v float64) { n[k] = v }
	for _, v := range s.columns() {
		put("sessions."+v, float64(s.Sessions[v]))
		sub := 0
		for _, c := range s.Subagents[v] {
			sub += c
		}
		put("subagents."+v, float64(sub))
		c := s.A.DoDClosed[v]
		put("A.dod_closed."+v, float64(c.Closed))
		put("A.dod_ambiguous."+v, float64(c.Ambiguous))
		put("A.dod_of."+v, float64(c.Of))
		put("B.tool_calls."+v, float64(s.B.ToolCalls[v]))
		put("B.turns."+v, float64(s.B.Turns[v]))
		if t := s.B.Tokens[v]; t != nil {
			put("B.tokens_in."+v, float64(t.In))
			put("B.tokens_out."+v, float64(t.Out))
			put("B.cache_read."+v, float64(t.CacheRead))
		}
		if p := s.B.CostUSD[v]; p != nil {
			put("B.cost_usd."+v, *p)
		}
		put("B.wall_min."+v, s.B.WallMin[v])
		put("B.active_min."+v, s.B.ActiveMin[v])
		put("B.peak_context."+v, float64(s.B.PeakContext[v]))
		if p := s.B.SessionsOverContextTarget[v]; p != nil {
			put("B.sessions_over_context_target."+v, float64(*p))
		}
		if p := s.B.Compactions[v]; p != nil {
			put("B.compactions."+v, float64(*p))
		}
		put("C.sqlite_verbs."+v, float64(s.C.SQLiteVerbs[v]))
		put("C.grep_calls."+v, float64(s.C.GrepCalls[v]))
		if p := s.C.SearchGrepRatio[v]; p != nil {
			put("C.search_grep_ratio."+v, *p)
		}
		if p := s.D.ToolErrorRate[v]; p != nil {
			put("D.tool_error_rate."+v, *p)
		}
		if p := s.D.Denials[v]; p != nil {
			put("D.denials."+v, float64(*p))
		}
		if p := s.D.Interrupts[v]; p != nil {
			put("D.interrupts."+v, float64(*p))
		}
		put("D.retry_loops."+v, float64(s.D.RetryLoops[v]))
	}
	for _, st := range []string{"PASS", "FAIL", "BLOCKED"} {
		put("A.verifier."+st, float64(s.A.Verifier[st]))
	}
	for _, sev := range []string{"high", "medium", "low"} {
		put("A.review_findings."+sev, float64(s.A.ReviewFindings[sev]))
	}
	put("B.context_target", float64(s.B.ContextTarget))
	put("A.escaped_bugs", float64(s.A.EscapedBugs))
	put("A.corrections", float64(s.A.Corrections))
	for _, k := range forbiddenKeys {
		put("C.forbidden."+k[1], float64(s.C.Forbidden[k[0]]))
	}
	return n
}

// forbiddenKeys maps each forbidden shape to its key in Numbers.
var forbiddenKeys = [][2]string{
	{"git add -A", "git_add_all"}, {"git add .", "git_add_dot"}, {"git push --force", "push_force"},
	{"--no-verify", "no_verify"}, {"commit chained to test", "commit_chained_to_test"},
}
