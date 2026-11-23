package brain

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MemoryEntry is `fyr memory add`'s flags (D14: the verb is an alias over
// memory records).
type MemoryEntry struct {
	Type           string   // semantic, procedural or episodic (or legacy aliases)
	Name           string   // the slug: the record is MEMORY-<name>
	Description    string   // one line: the record's title
	Provenance     string   // operator (default) or derived (--provenance)
	Global         bool     // --global: shared cross-project memory
	EvidenceCount  int      // count of sessions corroborating this memory
	SourceSessions []string // short session IDs corroborating this memory
}

// The caps of `memory show`: what a session loads at resume, however many
// memories the project holds.
const (
	memoryMaxLines        = 200
	memoryMaxBytes        = 25 * 1024
	memoryMaxLine         = maxTitle + 128
	maxSourceSessionCount = 20
)

var (
	memorySlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	memoryType = regexp.MustCompile(`^type: ([a-z]+)\n`)
)

// NormalizeMemoryType maps canonical memory parts ("semantic", "procedural",
// "episodic") and legacy aliases ("user"|"project"|"reference" -> "semantic",
// "feedback" -> "procedural") to a canonical memory part, refusing anything else.
func NormalizeMemoryType(raw string) (string, error) {
	switch raw {
	case "semantic", "procedural", "episodic":
		return raw, nil
	case "user", "project", "reference":
		return "semantic", nil
	case "feedback":
		return "procedural", nil
	default:
		return "", refuse("--type must be semantic, procedural or episodic, not %q", raw)
	}
}

func mergeSourceSessions(existing string, incoming []string) string {
	seen := map[string]bool{}
	var all []string
	add := func(raw string) {
		s := strings.TrimSpace(raw)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		all = append(all, s)
	}
	for _, part := range strings.Split(existing, ",") {
		add(part)
	}
	for _, part := range incoming {
		add(part)
	}
	sort.Strings(all)
	if len(all) > maxSourceSessionCount {
		all = all[:maxSourceSessionCount]
	}
	return strings.Join(all, ",")
}

// memorySession opens the store in the project's scope for a memory call, or
// the shared global scope when o.Global is true (skipping resolveProject, D10).
func memorySession(o Options) (*session, error) {
	var scope, top, repoID string
	if !o.Global {
		var err error
		scope, top, err = resolveProject(o.Dir)
		if err != nil {
			return nil, err
		}
		repoID = ResolveRepoID(scope)
	}
	st, err := openStore(o.Home)
	if err != nil {
		return nil, err
	}
	return &session{st: st, scope: scope, top: top, repoID: repoID}, nil
}

func validateMemoryEntry(e MemoryEntry) (string, string, error) {
	normType, err := NormalizeMemoryType(e.Type)
	if err != nil {
		return "", "", err
	}
	prov := e.Provenance
	if prov == "" {
		prov = "operator"
	}
	switch {
	case !memorySlug.MatchString(e.Name):
		return "", "", refuse("--name must match %s, not %q", memorySlug, e.Name)
	case prov != "operator" && prov != "derived":
		return "", "", refuse("--provenance must be operator or derived, not %q", e.Provenance)
	}
	return prov, normType, nil
}

// MemoryAdd stores the memory e.Name, replacing one of the same name (while
// refusing derived overwrites of existing operator cards), and returns its ID.
func MemoryAdd(o Options, e MemoryEntry, body io.Reader) (string, error) {
	if e.Global {
		o.Global = true
	}
	prov, normType, err := validateMemoryEntry(e)
	if err != nil {
		return "", err
	}
	title, err := cleanTitle(e.Description)
	if err != nil {
		return "", refuse("%s", strings.Replace(err.Error(), "--title", "--description", 1))
	}
	text, err := readBody(body)
	if err != nil {
		return "", err
	}
	s, err := memorySession(o)
	if err != nil {
		return "", err
	}
	defer s.close()
	id := "MEMORY-" + e.Name
	now := o.now()
	full := appendRepoFilePins(s.top, title, "type: "+normType+"\n\n"+text)
	if err := s.st.tx(func(c *sql.Conn) error {
		return upsertMemoryRow(c, s, id, title, full, prov, normType, e, now)
	}); err != nil {
		return "", err
	}
	return id, nil
}

func upsertMemoryRow(c *sql.Conn, s *session, id, title, full, prov, normType string, e MemoryEntry, now time.Time) error {
	ctx := context.Background()
	var curProv, curSessions string
	var curEv int
	qerr := c.QueryRowContext(ctx, `SELECT provenance, source_sessions, evidence_count FROM records WHERE scope = ? AND id = ? AND kind = 'memory'`, s.scope, id).Scan(&curProv, &curSessions, &curEv)
	taken := qerr == nil
	if qerr != nil && !errors.Is(qerr, sql.ErrNoRows) {
		return qerr
	}
	if taken && curProv == "operator" && prov != "operator" {
		return refuse("memory %s has operator provenance and cannot be overwritten with %s provenance", id, prov)
	}
	sessions := mergeSourceSessions(curSessions, e.SourceSessions)
	evCount := max(curEv, e.EvidenceCount)
	if evCount == 0 && sessions != "" {
		evCount = len(strings.Split(sessions, ","))
	}
	stamp := now.UTC().Format(time.RFC3339)
	rec := Record{
		Scope: s.scope, RepoID: s.repoID, ID: id, Kind: "memory", Status: "closed",
		Title: title, Body: full, Provenance: prov, MemoryKind: normType,
		VerifiedAt: stamp, EvidenceCount: evCount, SourceSessions: sessions, Updated: stamp,
	}
	feedbacks, err := queryFeedbacksConn(ctx, c, s.scope)
	if err != nil {
		return err
	}
	val := ComputeMemoryValidity(s.top, rec, CountMemoryContradictions(rec, feedbacks), now)
	if taken {
		_, err := c.ExecContext(ctx, `UPDATE records SET repo_id = ?, status = 'closed', title = ?, body = ?, provenance = ?, memory_kind = ?, validity = ?, verified_at = ?, evidence_count = ?, source_sessions = ?, updated = ? WHERE scope = ? AND id = ? AND kind = 'memory'`,
			s.repoID, title, full, prov, normType, val, stamp, evCount, sessions, stamp, s.scope, id)
		return err
	}
	_, err = c.ExecContext(ctx, `INSERT INTO records(scope, repo_id, id, kind, status, date, title, body, project, provenance, memory_kind, validity, verified_at, evidence_count, source_sessions, updated)
		VALUES (?, ?, ?, 'memory', 'closed', ?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?)`,
		s.scope, s.repoID, id, now.Format("2006-01-02"), title, full, prov, normType, val, stamp, evCount, sessions, stamp)
	return err
}
