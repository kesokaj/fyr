package brain

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
)

// RetrieveSIMSlice runs read-only 4-tier priority retrieval over the global
// SIM ledger, returning a bounded, redacted text slice for optimizer prompts.
// Missing or pre-v5 stores return an empty SIMSlice and nil error.
func RetrieveSIMSlice(home string, q SIMQuery) (SIMSlice, error) {
	st, _, err := openSIMReadOnly(home)
	if err != nil || st == nil {
		return SIMSlice{}, err
	}
	defer st.close()
	cfg := normalizeSIMQuery(q)
	ctx := context.Background()
	aliasMap, err := loadSIMAliasMap(ctx, st.db)
	if err != nil {
		return SIMSlice{}, err
	}
	col, err := collectSIMTierProblems(ctx, st.db, aliasMap, cfg)
	if err != nil || len(col.probs) == 0 {
		return SIMSlice{Truncated: col.truncated}, err
	}
	return renderSIMSlice(ctx, st.db, home, aliasMap, col, cfg)
}

func normalizeSIMQuery(q SIMQuery) SIMQuery {
	if q.MaxBytes <= 0 {
		q.MaxBytes = defaultSIMMaxBytes
	}
	if q.MaxProblems <= 0 {
		q.MaxProblems = defaultSIMMaxProblems
	}
	if q.MaxAttemptsPerProblem <= 0 {
		q.MaxAttemptsPerProblem = defaultSIMMaxAttempts
	}
	if q.RecentFailedDays <= 0 {
		q.RecentFailedDays = defaultSIMRecentFailedDays
	}
	if q.Now.IsZero() {
		q.Now = time.Now()
	}
	return q
}

type simIDCollector struct {
	ctx       context.Context
	q         sqlQuerier
	aliases   map[string]string
	limit     int
	seen      map[string]bool
	probs     []SIMProblem
	truncated bool
}

func (c *simIDCollector) addAll(rawIDs []string) error {
	for _, rawID := range rawIDs {
		canonID, ok := resolveAliasInMap(c.aliases, rawID)
		if !ok || c.seen[canonID] {
			continue
		}
		prob, exists, err := lookupSIMProblem(c.ctx, c.q, canonID)
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

func collectSIMTierProblems(ctx context.Context, q sqlQuerier, aliases map[string]string, cfg SIMQuery) (*simIDCollector, error) {
	col := &simIDCollector{ctx: ctx, q: q, aliases: aliases, limit: cfg.MaxProblems, seen: map[string]bool{}}
	if err := collectTierSignals(col, cfg.Signals); err != nil {
		return col, err
	}
	if err := collectTierPaths(col, cfg.TargetPaths); err != nil {
		return col, err
	}
	if err := collectTierRecentFailed(col, cfg.Now, cfg.RecentFailedDays); err != nil {
		return col, err
	}
	if err := collectTierFTS(col, cfg.Terms); err != nil {
		return col, err
	}
	return col, nil
}

func nonEmptyTrimmed(items []string) []string {
	var out []string
	for _, s := range items {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (c *simIDCollector) queryIDs(sqlq string, args ...any) error {
	rows, err := c.q.QueryContext(c.ctx, sqlq, args...)
	if err != nil {
		return err
	}
	ids, err := scanIDRows(rows)
	if err != nil {
		return err
	}
	return c.addAll(ids)
}

func collectTierSignals(col *simIDCollector, signals []string) error {
	clean := nonEmptyTrimmed(signals)
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

func collectTierPaths(col *simIDCollector, prefixes []string) error {
	clean := nonEmptyTrimmed(prefixes)
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
		for _, pref := range clean {
			if targetPathsMatchPrefix(tp, pref) {
				matched = append(matched, id)
				break
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return col.addAll(matched)
}

func collectTierRecentFailed(col *simIDCollector, now time.Time, days int) error {
	cutoff := now.UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
	return col.queryIDs(`SELECT problem_id FROM sim_attempts
		WHERE (eval_verdict = 'not_kept' OR field_status IN ('still_regressing', 'reverted'))
		  AND created_at >= ?
		ORDER BY created_at DESC, id DESC`, cutoff)
}

func collectTierFTS(col *simIDCollector, terms []string) error {
	var words []string
	for _, raw := range terms {
		for _, w := range strings.Fields(strings.ReplaceAll(raw, "\x00", " ")) {
			words = append(words, w)
			if len(words) >= 16 {
				break
			}
		}
		if len(words) >= 16 {
			break
		}
	}
	ftsQ := search.FTSQuery(words, " OR ")
	if ftsQ == "" {
		return nil
	}
	return col.queryIDs(`SELECT p.id FROM sim_fts
		JOIN sim_problems p ON p.pid = sim_fts.rowid
		WHERE sim_fts MATCH ?
		ORDER BY bm25(sim_fts, 2.0, 1.0), p.updated_at DESC, p.pid DESC`, ftsQ)
}

func renderSIMSlice(ctx context.Context, q sqlQuerier, home string, aliases map[string]string, col *simIDCollector, cfg SIMQuery) (SIMSlice, error) {
	var out strings.Builder
	sl := SIMSlice{Truncated: col.truncated}
	cleanHome := filepath.Clean(home)
	for _, prob := range col.probs {
		allIDs := append([]string{prob.ID}, aliasesForCanonical(aliases, prob.ID)...)
		atts, err := querySIMAttemptsForIDs(ctx, q, allIDs)
		if err != nil {
			return SIMSlice{}, err
		}
		if !appendSIMProblemLines(&out, cleanHome, prob, atts, cfg.MaxAttemptsPerProblem, cfg.MaxBytes, &sl) {
			sl.Truncated = true
			break
		}
	}
	sl.Text = strings.TrimRight(out.String(), "\n")
	return sl, nil
}

func appendSIMProblemLines(out *strings.Builder, cleanHome string, p SIMProblem, atts []SIMAttempt, maxAtt, maxBytes int, sl *SIMSlice) bool {
	header := redactSIMLine(formatSIMProblemHeader(p), cleanHome) + "\n"
	if out.Len()+len(header) > maxBytes {
		if out.Len() == 0 {
			out.WriteString(truncateUTF8(header, maxBytes))
			sl.ProblemCount = 1
		}
		return false
	}
	out.WriteString(header)
	sl.ProblemCount++
	show, older := atts, []SIMAttempt(nil)
	if len(atts) > maxAtt {
		show, older = atts[:maxAtt], atts[maxAtt:]
	}
	for _, a := range show {
		line := redactSIMLine(formatSIMAttemptLine(a), cleanHome) + "\n"
		if out.Len()+len(line) > maxBytes {
			sl.Truncated = true
			return false
		}
		out.WriteString(line)
		sl.AttemptCount++
	}
	if len(older) > 0 {
		rollup := redactSIMLine(formatSIMRollup(older), cleanHome) + "\n"
		if out.Len()+len(rollup) > maxBytes {
			sl.Truncated = true
			return false
		}
		out.WriteString(rollup)
	}
	return true
}

func formatSIMProblemHeader(p SIMProblem) string {
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

func formatSIMAttemptLine(a SIMAttempt) string {
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
		b.WriteString(" models=[")
		for i, m := range a.FieldModels {
			if i > 0 {
				b.WriteByte(',')
			}
			if m.Status != "" {
				fmt.Fprintf(&b, "%s:%s(%d)", m.Model, m.Status, m.Sessions)
			} else {
				b.WriteString(m.Model)
			}
		}
		b.WriteByte(']')
	}
	fmt.Fprintf(&b, ": %s", a.What)
	return b.String()
}

func formatSIMRollup(older []SIMAttempt) string {
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

func redactSIMLine(line, cleanHome string) string {
	line = strings.ReplaceAll(strings.ReplaceAll(line, "\r", " "), "\n", " ")
	if len(cleanHome) > 1 {
		line = strings.ReplaceAll(line, cleanHome, "~")
	}
	return redact.Redact(line)
}

func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	n := maxBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
