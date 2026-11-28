package sim

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/trail"
)

// RetrieveSlice runs read-only 4-tier priority retrieval over the global SIM
// ledger, returning a bounded, redacted text slice for optimizer prompts.
// Missing or pre-v5 stores return an empty Slice and nil error.
func RetrieveSlice(deps StoreDeps, home string, q Query) (Slice, error) {
	r := runner{d: deps}
	dbq, closeFn, err := r.openRO(home)
	if err != nil || dbq == nil {
		return Slice{}, err
	}
	defer closeFn()
	cfg := simNormalizeQuery(q)
	ctx := context.Background()
	aliasMap, err := simLoadAliasMap(ctx, dbq)
	if err != nil {
		return Slice{}, err
	}
	col, err := r.collectTierProblems(ctx, dbq, aliasMap, cfg)
	if err != nil || len(col.probs) == 0 {
		return Slice{Truncated: col.truncated}, err
	}
	return r.renderSlice(ctx, dbq, home, aliasMap, col, cfg)
}

func simNormalizeQuery(q Query) Query {
	if q.MaxBytes <= 0 {
		q.MaxBytes = defaultMaxBytes
	}
	if q.MaxProblems <= 0 {
		q.MaxProblems = defaultMaxProblems
	}
	if q.MaxAttemptsPerProblem <= 0 {
		q.MaxAttemptsPerProblem = defaultMaxAttempts
	}
	if q.RecentFailedDays <= 0 {
		q.RecentFailedDays = defaultRecentFailedDays
	}
	if q.Now.IsZero() {
		q.Now = time.Now()
	}
	return q
}

type simIDCollector struct {
	ctx       context.Context
	q         Querier
	aliases   map[string]string
	limit     int
	seen      map[string]bool
	probs     []Problem
	truncated bool
}

func (c *simIDCollector) addAll(rawIDs []string) error {
	for _, rawID := range rawIDs {
		canonID, ok := simResolveAliasInMap(c.aliases, rawID)
		if !ok || c.seen[canonID] {
			continue
		}
		prob, exists, err := simLookupProblem(c.ctx, c.q, canonID)
		if err != nil {
			return err
		}
		if !exists || prob.Status == "obsolete" || prob.Status == "merged" {
			continue
		}
		c.seen[canonID] = true
		if len(c.probs) >= c.limit {
			c.truncated = true
			continue
		}
		c.probs = append(c.probs, prob)
	}
	return nil
}

func (c *simIDCollector) queryIDs(sqlq string, args ...any) error {
	rows, err := c.q.QueryContext(c.ctx, sqlq, args...)
	if err != nil {
		return err
	}
	ids, err := simScanIDRows(rows)
	if err != nil {
		return err
	}
	return c.addAll(ids)
}

func (r runner) collectTierProblems(ctx context.Context, q Querier, aliases map[string]string, cfg Query) (*simIDCollector, error) {
	col := &simIDCollector{ctx: ctx, q: q, aliases: aliases, limit: cfg.MaxProblems, seen: map[string]bool{}}
	if err := simCollectTierSignals(col, cfg.Signals); err != nil {
		return col, err
	}
	if err := simCollectTierPaths(col, cfg.TargetPaths); err != nil {
		return col, err
	}
	if err := simCollectTierRecentFailed(col, cfg.Now, cfg.RecentFailedDays); err != nil {
		return col, err
	}
	if err := r.collectTierFTS(col, cfg.Terms); err != nil {
		return col, err
	}
	return col, nil
}

func simTrimNonEmpty(items []string) []string {
	var out []string
	for _, s := range items {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func simCollectTierSignals(col *simIDCollector, signals []string) error {
	clean := simTrimNonEmpty(signals)
	if len(clean) == 0 {
		return nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(clean)), ",")
	args := make([]any, len(clean))
	for i, s := range clean {
		args[i] = s
	}
	return col.queryIDs(`SELECT id FROM sim_problems WHERE signal_id IN (`+ph+`) ORDER BY updated_at DESC, pid DESC`, args...)
}

func simCollectTierPaths(col *simIDCollector, prefixes []string) error {
	clean := simTrimNonEmpty(prefixes)
	if len(clean) == 0 {
		return nil
	}
	rows, err := col.q.QueryContext(col.ctx, `SELECT id, target_paths FROM sim_problems WHERE target_paths != '' ORDER BY updated_at DESC, pid DESC`)
	if err != nil {
		return err
	}
	var matched []string
	for rows.Next() {
		var id, tp string
		if err := rows.Scan(&id, &tp); err != nil {
			rows.Close()
			return err
		}
		if simMatchesAnyPrefix(tp, clean) {
			matched = append(matched, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return col.addAll(matched)
}

func simMatchesAnyPrefix(targetPaths string, prefixes []string) bool {
	for _, pref := range prefixes {
		if simTargetPathsMatchPrefix(targetPaths, pref) {
			return true
		}
	}
	return false
}

func simCollectTierRecentFailed(col *simIDCollector, now time.Time, days int) error {
	cutoff := now.UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
	return col.queryIDs(`SELECT problem_id FROM sim_attempts
		WHERE (eval_verdict = 'not_kept' OR field_status IN ('still_regressing', 'reverted'))
		  AND created_at >= ?
		ORDER BY created_at DESC, id DESC`, cutoff)
}

func (r runner) collectTierFTS(col *simIDCollector, terms []string) error {
	ftsQ := r.ftsQuery(simCollectFTSWords(terms, 16), " OR ")
	if ftsQ == "" {
		return nil
	}
	return col.queryIDs(`SELECT p.id FROM sim_fts
		JOIN sim_problems p ON p.pid = sim_fts.rowid
		WHERE sim_fts MATCH ?
		ORDER BY bm25(sim_fts, 2.0, 1.0), p.updated_at DESC, p.pid DESC`, ftsQ)
}

func simCollectFTSWords(terms []string, capWords int) []string {
	var words []string
	for _, raw := range terms {
		for _, w := range strings.Fields(strings.ReplaceAll(raw, "\x00", " ")) {
			words = append(words, w)
			if len(words) >= capWords {
				return words
			}
		}
	}
	return words
}

func (r runner) renderSlice(ctx context.Context, q Querier, home string, aliases map[string]string, col *simIDCollector, cfg Query) (Slice, error) {
	var out strings.Builder
	sl := Slice{Truncated: col.truncated}
	cleanHome := filepath.Clean(home)
	for _, prob := range col.probs {
		allIDs := append([]string{prob.ID}, simAliasesForCanonical(aliases, prob.ID)...)
		atts, err := simQueryAttemptsForIDs(ctx, q, allIDs)
		if err != nil {
			return Slice{}, err
		}
		if !r.appendProblemLines(&out, cleanHome, prob, atts, cfg.MaxAttemptsPerProblem, cfg.MaxBytes, &sl) {
			sl.Truncated = true
			break
		}
	}
	sl.Text = strings.TrimRight(out.String(), "\n")
	return sl, nil
}

func (r runner) appendProblemLines(out *strings.Builder, cleanHome string, p Problem, atts []Attempt, maxAtt, maxBytes int, sl *Slice) bool {
	header := r.redactLine(simFormatProblemHeader(p), cleanHome) + "\n"
	if out.Len()+len(header) > maxBytes {
		if out.Len() == 0 {
			out.WriteString(trail.TruncateUTF8(header, maxBytes))
			sl.ProblemCount = 1
		}
		return false
	}
	out.WriteString(header)
	sl.ProblemCount++
	show, older := atts, []Attempt(nil)
	if len(atts) > maxAtt {
		show, older = atts[:maxAtt], atts[maxAtt:]
	}
	for _, a := range show {
		line := r.redactLine(simFormatAttemptLine(a), cleanHome) + "\n"
		if out.Len()+len(line) > maxBytes {
			sl.Truncated = true
			return false
		}
		out.WriteString(line)
		sl.AttemptCount++
	}
	if len(older) > 0 {
		rollup := r.redactLine(simFormatRollup(older), cleanHome) + "\n"
		if out.Len()+len(rollup) > maxBytes {
			sl.Truncated = true
			return false
		}
		out.WriteString(rollup)
	}
	return true
}

func simFormatProblemHeader(p Problem) string {
	parts := []string{p.ID, p.Status}
	if p.MechanismClass != "" {
		parts = append(parts, p.MechanismClass)
	}
	if p.Provenance == "operator" {
		parts = append(parts, "operator")
	}
	if p.SignalID != "" {
		parts = append(parts, p.SignalID)
	}
	if p.TargetPaths != "" {
		parts = append(parts, p.TargetPaths)
	}
	return fmt.Sprintf("[%s] %s", strings.Join(parts, " | "), p.RootCause)
}

func simFormatAttemptLine(a Attempt) string {
	day := a.CreatedAt
	if len(day) >= 10 {
		day = day[:10]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  - %s %s verdict=%s field=%s", day, a.SourceKind, a.EvalVerdict, a.FieldStatus)
	if a.PolicyModel != "" {
		fmt.Fprintf(&b, " policy=%s", a.PolicyModel)
	}
	if a.EvalTotal > 0 {
		fmt.Fprintf(&b, " eval=%d/%d", a.EvalPassed, a.EvalTotal)
	}
	if len(a.FieldModels) > 0 {
		simAppendFieldModels(&b, a.FieldModels)
	}
	fmt.Fprintf(&b, ": %s", a.What)
	return b.String()
}

func simAppendFieldModels(b *strings.Builder, models []ModelStatus) {
	b.WriteString(" models=[")
	for i, m := range models {
		if i > 0 {
			b.WriteByte(',')
		}
		if m.Status != "" {
			fmt.Fprintf(b, "%s:%s(%d)", m.Model, m.Status, m.Sessions)
		} else {
			b.WriteString(m.Model)
		}
	}
	b.WriteByte(']')
}

func simFormatRollup(older []Attempt) string {
	var kept, notKept, recovered, reverted int
	for _, a := range older {
		switch a.EvalVerdict {
		case "kept":
			kept++
		case "not_kept":
			notKept++
		}
		switch a.FieldStatus {
		case "recovered":
			recovered++
		case "reverted":
			reverted++
		}
	}
	return fmt.Sprintf("  +%d earlier attempts: %d kept, %d not_kept, %d recovered, %d reverted",
		len(older), kept, notKept, recovered, reverted)
}

// RedactLine strips control characters, replaces home paths with "~", and
// runs the redactor on one SIM slice line.
func RedactLine(deps StoreDeps, line, cleanHome string) string {
	return runner{d: deps}.redactLine(line, cleanHome)
}

func (r runner) redactLine(line, cleanHome string) string {
	line = strings.ReplaceAll(strings.ReplaceAll(line, "\r", " "), "\n", " ")
	line = r.printable(line)
	if len(cleanHome) > 1 {
		line = strings.ReplaceAll(line, cleanHome, "~")
	}
	if realHome, err := os.UserHomeDir(); err == nil {
		if rh := filepath.Clean(realHome); len(rh) > 1 && rh != cleanHome {
			line = strings.ReplaceAll(line, rh, "~")
		}
	}
	return redact.Redact(line)
}
