package brain

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// MemoryDraftOptions provides optional part, evidence, and session metadata
// for `fyr auto` memory drafts.
type MemoryDraftOptions struct {
	Type           string
	EvidenceCount  int
	SourceSessions []string
}

// MemoryDraftWithOptions applies one validated memory draft line from `fyr auto`
// (action: add, retire or merge) without touching trail.jsonl. Global drafts
// refuse retire and merge.
func MemoryDraftWithOptions(o Options, action, id, body string, opts MemoryDraftOptions) (string, error) {
	switch action {
	case "add", "retire", "merge":
	default:
		return "", refuse("memory draft action must be add, retire or merge, not %q", action)
	}
	if o.Global && (action == "retire" || action == "merge") {
		return "", refuse("global memory cannot be %sd via draft", action)
	}
	slug := strings.TrimPrefix(strings.TrimSpace(id), "MEMORY-")
	if !memorySlug.MatchString(slug) {
		return "", refuse("memory draft id must match %s, not %q", memorySlug, id)
	}
	if opts.Type != "" {
		if _, err := NormalizeMemoryType(opts.Type); err != nil {
			return "", err
		}
	}
	recID := "MEMORY-" + slug
	text, err := readBody(strings.NewReader(body))
	if err != nil {
		return "", err
	}
	s, err := memorySession(o)
	if err != nil {
		return "", err
	}
	defer s.close()
	now := o.now()
	if err := s.st.tx(func(c *sql.Conn) error {
		return applyMemoryDraftTx(c, s.scope, s.top, action, slug, recID, text, opts, now)
	}); err != nil {
		return "", err
	}
	return recID, nil
}

func applyMemoryDraftTx(c *sql.Conn, scope, top, action, slug, recID, text string, opts MemoryDraftOptions, now time.Time) error {
	ctx := context.Background()
	stamp := now.UTC().Format(time.RFC3339)
	var curBody, curProv, curKind, curSessions string
	var curEv int
	qerr := c.QueryRowContext(ctx, `SELECT body, provenance, memory_kind, evidence_count, source_sessions FROM records WHERE scope = ? AND id = ? AND kind = 'memory'`, scope, recID).
		Scan(&curBody, &curProv, &curKind, &curEv, &curSessions)
	existsHere := qerr == nil
	if qerr != nil && !errors.Is(qerr, sql.ErrNoRows) {
		return qerr
	}
	switch action {
	case "add":
		return applyMemoryDraftAdd(c, scope, top, slug, recID, text, curProv, curEv, curSessions, existsHere, opts, now, stamp)
	case "retire":
		if !existsHere {
			return refuse("no memory %s in this project", recID)
		}
		_, err := c.ExecContext(ctx, `UPDATE records SET status = 'retired', updated = ? WHERE scope = ? AND id = ? AND kind = 'memory'`, stamp, scope, recID)
		return err
	case "merge":
		if !existsHere {
			return refuse("no memory %s in this project", recID)
		}
		return applyMemoryDraftMerge(c, scope, top, recID, text, curBody, curProv, curKind, curEv, curSessions, opts, now, stamp)
	}
	return nil
}

func applyMemoryDraftMerge(c *sql.Conn, scope, top, recID, text, curBody, curProv, curKind string, curEv int, curSessions string, opts MemoryDraftOptions, now time.Time, stamp string) error {
	ctx := context.Background()
	normType := resolveDraftType(opts.Type, text, curKind, curBody)
	full := mergeMemoryBody(text, curBody, curProv, normType)
	sessions := mergeSourceSessions(curSessions, opts.SourceSessions)
	evCount := resolveDraftEvidence(true, curEv, opts, sessions)
	var title string
	if err := c.QueryRowContext(ctx, `SELECT title FROM records WHERE scope = ? AND id = ?`, scope, recID).Scan(&title); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	rec := Record{
		Scope: scope, ID: recID, Kind: "memory", Status: "closed", Title: title, Body: full,
		Provenance: "derived", MemoryKind: normType, VerifiedAt: stamp,
		EvidenceCount: evCount, SourceSessions: sessions, Updated: stamp,
	}
	feedbacks, err := queryFeedbacksConn(ctx, c, scope)
	if err != nil {
		return err
	}
	val := ComputeMemoryValidity(top, rec, CountMemoryContradictions(rec, feedbacks), now)
	_, err = c.ExecContext(ctx, `UPDATE records SET status = 'closed', body = ?, provenance = 'derived', memory_kind = ?, validity = ?, verified_at = ?, evidence_count = ?, source_sessions = ?, updated = ? WHERE scope = ? AND id = ? AND kind = 'memory'`,
		full, normType, val, stamp, evCount, sessions, stamp, scope, recID)
	return err
}

func mergeMemoryBody(text, curBody, curProv, normType string) string {
	full := ensureNormalizedMemoryBody(text, normType)
	prev := strings.TrimSpace(curBody)
	if prev != "" && !strings.Contains(full, prev) {
		if curProv == "" {
			curProv = "unknown"
		}
		full += "\n\n---\nPrior (" + curProv + "):\n" + prev
	}
	return full
}

func applyMemoryDraftAdd(c *sql.Conn, scope, top, slug, recID, text, curProv string, curEv int, curSessions string, existsHere bool, opts MemoryDraftOptions, now time.Time, stamp string) error {
	if existsHere && curProv == "operator" {
		return refuse("memory %s has operator provenance", recID)
	}
	first, _, _ := strings.Cut(text, "\n")
	first = strings.TrimSpace(first)
	if len(first) > maxTitle {
		first = slug
	}
	title, err := cleanTitle(first)
	if err != nil {
		title = slug
	}
	normType := resolveDraftType(opts.Type, text, "", "")
	full := ensureNormalizedMemoryBody(text, normType)
	sessions := mergeSourceSessions(curSessions, opts.SourceSessions)
	evCount := resolveDraftEvidence(existsHere, curEv, opts, sessions)
	rec := Record{
		Scope: scope, ID: recID, Kind: "memory", Status: "closed", Title: title, Body: full,
		Provenance: "derived", MemoryKind: normType, VerifiedAt: stamp,
		EvidenceCount: evCount, SourceSessions: sessions, Updated: stamp,
	}
	ctx := context.Background()
	feedbacks, err := queryFeedbacksConn(ctx, c, scope)
	if err != nil {
		return err
	}
	val := ComputeMemoryValidity(top, rec, CountMemoryContradictions(rec, feedbacks), now)
	if existsHere {
		_, err = c.ExecContext(ctx, `UPDATE records SET status = 'closed', title = ?, body = ?, provenance = 'derived', memory_kind = ?, validity = ?, verified_at = ?, evidence_count = ?, source_sessions = ?, updated = ? WHERE scope = ? AND id = ? AND kind = 'memory'`,
			title, full, normType, val, stamp, evCount, sessions, stamp, scope, recID)
		return err
	}
	_, err = c.ExecContext(ctx, `INSERT INTO records(scope, id, kind, status, date, title, body, project, provenance, memory_kind, validity, verified_at, evidence_count, source_sessions, updated)
		VALUES (?, ?, 'memory', 'closed', ?, ?, ?, '', 'derived', ?, ?, ?, ?, ?, ?)`,
		scope, recID, now.Format("2006-01-02"), title, full, normType, val, stamp, evCount, sessions, stamp)
	return err
}

func resolveDraftType(optType, text, curKind, curBody string) string {
	for _, raw := range []string{optType, extractBodyType(text), curKind, extractBodyType(curBody)} {
		if raw == "" {
			continue
		}
		if norm, err := NormalizeMemoryType(raw); err == nil {
			return norm
		}
	}
	return "semantic"
}

func extractBodyType(body string) string {
	if m := memoryType.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

func ensureNormalizedMemoryBody(text, normType string) string {
	if m := memoryType.FindStringSubmatchIndex(text); m != nil {
		return "type: " + normType + text[m[3]:]
	}
	return "type: " + normType + "\n\n" + text
}

func resolveDraftEvidence(exists bool, cur int, opts MemoryDraftOptions, mergedSessions string) int {
	sessionCount := 0
	if mergedSessions != "" {
		sessionCount = len(strings.Split(mergedSessions, ","))
	}
	if !exists {
		if opts.EvidenceCount > 0 {
			return opts.EvidenceCount
		}
		return sessionCount
	}
	next := cur + 1
	if opts.EvidenceCount > next {
		next = opts.EvidenceCount
	}
	if sessionCount > next {
		next = sessionCount
	}
	return next
}
