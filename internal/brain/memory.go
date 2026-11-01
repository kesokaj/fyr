package brain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/search"
)

// MemoryEntry is `fyr memory add`'s flags (D14: the verb is an alias over
// memory records).
type MemoryEntry struct {
	Type        string // user, feedback, project or reference
	Name        string // the slug: the record is MEMORY-<name>
	Description string // one line: the record's title
	Provenance  string // operator (default) or derived (--provenance)
}

// The caps of `memory show`: what a session loads at resume, however many
// memories the project holds.
const (
	memoryMaxLines = 200
	memoryMaxBytes = 25 * 1024
	memoryMaxLine  = maxTitle + 128
)

var (
	memorySlug  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	memoryTypes = map[string]bool{"user": true, "feedback": true, "project": true, "reference": true}
	memoryType  = regexp.MustCompile(`^type: ([a-z]+)\n`)
)

// memorySession opens the store in the project's scope for a memory call.
// Memory is never exported, so it neither loads nor writes trail.jsonl and
// an unmigrated trail does not block it (D16 is about the exported kinds).
func memorySession(o Options) (*session, error) {
	scope, top, err := resolveProject(o.Dir)
	if err != nil {
		return nil, err
	}
	st, err := openStore(o.Home)
	if err != nil {
		return nil, err
	}
	return &session{st: st, scope: scope, top: top}, nil
}

func validateMemoryEntry(e MemoryEntry) (string, error) {
	prov := e.Provenance
	if prov == "" {
		prov = "operator"
	}
	switch {
	case !memoryTypes[e.Type]:
		return "", refuse("--type must be user, feedback, project or reference, not %q", e.Type)
	case !memorySlug.MatchString(e.Name):
		return "", refuse("--name must match %s, not %q", memorySlug, e.Name)
	case prov != "operator" && prov != "derived":
		return "", refuse("--provenance must be operator or derived, not %q", e.Provenance)
	}
	return prov, nil
}

// MemoryAdd stores the memory e.Name, replacing one of the same name, and
// returns its ID.
func MemoryAdd(o Options, e MemoryEntry, body io.Reader) (string, error) {
	prov, err := validateMemoryEntry(e)
	if err != nil {
		return "", err
	}
	// The description is the record's title: the title rules hold (security
	// review of the import, finding 5); a refusal here, as `fyr memory`'s
	// refusals always were.
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
	full := "type: " + e.Type + "\n\n" + text
	if err := s.st.tx(func(c *sql.Conn) error {
		return upsertMemoryRow(c, s.scope, id, title, full, prov, now)
	}); err != nil {
		return "", err
	}
	return id, nil
}

func upsertMemoryRow(c *sql.Conn, scope, id, title, full, prov string, now time.Time) error {
	ctx := context.Background()
	taken, err := exists(c, scope, id)
	if err != nil {
		return err
	}
	if taken {
		// Saving a retired name brings it back (D28).
		_, err = c.ExecContext(ctx, `UPDATE records SET status = 'closed', title = ?, body = ?, provenance = ?, updated = ? WHERE scope = ? AND id = ? AND kind = 'memory'`,
			title, full, prov, now.UTC().Format(time.RFC3339), scope, id)
		return err
	}
	_, err = c.ExecContext(ctx, `INSERT INTO records(scope, id, kind, status, date, title, body, project, provenance, updated)
		VALUES (?, ?, 'memory', 'closed', ?, ?, ?, '', ?, ?)`, scope, id, now.Format("2006-01-02"), title, full, prov, now.UTC().Format(time.RFC3339))
	return err
}

// MemoryDraft applies one validated memory draft line from `fyr auto`
// (action: add, retire or merge) in the project's scope without touching
// trail.jsonl. What it adds or merges carries provenance "derived"; retiring
// or merging an ID not in this project, or adding over an operator memory, is
// refused.
func MemoryDraft(o Options, action, id, body string) (string, error) {
	switch action {
	case "add", "retire", "merge":
	default:
		return "", refuse("memory draft action must be add, retire or merge, not %q", action)
	}
	slug := strings.TrimPrefix(strings.TrimSpace(id), "MEMORY-")
	if !memorySlug.MatchString(slug) {
		return "", refuse("memory draft id must match %s, not %q", memorySlug, id)
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
		return applyMemoryDraftTx(c, s.scope, action, slug, recID, text, now)
	}); err != nil {
		return "", err
	}
	return recID, nil
}

func applyMemoryDraftTx(c *sql.Conn, scope, action, slug, recID, text string, now time.Time) error {
	ctx := context.Background()
	stamp := now.UTC().Format(time.RFC3339)
	var curBody, curProv string
	qerr := c.QueryRowContext(ctx, `SELECT body, provenance FROM records WHERE scope = ? AND id = ? AND kind = 'memory'`, scope, recID).Scan(&curBody, &curProv)
	existsHere := qerr == nil
	if qerr != nil && !errors.Is(qerr, sql.ErrNoRows) {
		return qerr
	}
	switch action {
	case "add":
		return applyMemoryDraftAdd(c, scope, slug, recID, text, curProv, existsHere, now, stamp)
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
		full := mergeMemoryBody(text, curBody, curProv)
		_, err := c.ExecContext(ctx, `UPDATE records SET status = 'closed', body = ?, provenance = 'derived', updated = ? WHERE scope = ? AND id = ? AND kind = 'memory'`, full, stamp, scope, recID)
		return err
	}
	return nil
}

func mergeMemoryBody(text, curBody, curProv string) string {
	full := ensureMemoryType(text, curBody)
	prev := strings.TrimSpace(curBody)
	if prev != "" && !strings.Contains(full, prev) {
		if curProv == "" {
			curProv = "unknown"
		}
		full += "\n\n---\nPrior (" + curProv + "):\n" + prev
	}
	return full
}

func applyMemoryDraftAdd(c *sql.Conn, scope, slug, recID, text, curProv string, existsHere bool, now time.Time, stamp string) error {
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
	full := ensureMemoryType(text, "")
	if existsHere {
		_, err = c.ExecContext(context.Background(), `UPDATE records SET status = 'closed', title = ?, body = ?, provenance = 'derived', updated = ? WHERE scope = ? AND id = ? AND kind = 'memory'`,
			title, full, stamp, scope, recID)
		return err
	}
	_, err = c.ExecContext(context.Background(), `INSERT INTO records(scope, id, kind, status, date, title, body, project, provenance, updated)
		VALUES (?, ?, 'memory', 'closed', ?, ?, ?, '', 'derived', ?)`,
		scope, recID, now.Format("2006-01-02"), title, full, stamp)
	return err
}

func ensureMemoryType(text, curBody string) string {
	if memoryType.MatchString(text) {
		return text
	}
	typ := "project"
	if m := memoryType.FindStringSubmatch(curBody); m != nil {
		typ = m[1]
	}
	return "type: " + typ + "\n\n" + text
}

// MemoryShow is the project's memory index, one line per memory, capped at
// what a session loads.
func MemoryShow(o Options) (string, error) {
	s, err := memorySession(o)
	if err != nil {
		return "", err
	}
	defer s.close()
	rows, err := s.st.db.Query(`SELECT id, title, body FROM records WHERE scope = ? AND kind = 'memory' AND status = 'closed' ORDER BY id`, s.scope)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, title, body string
		if err := rows.Scan(&id, &title, &body); err != nil {
			return "", err
		}
		typ := "?"
		if m := memoryType.FindStringSubmatch(body); m != nil {
			typ = m[1]
		}
		l := search.Printable(fmt.Sprintf("- %s (%s) — %s", strings.TrimPrefix(id, "MEMORY-"), typ, title))
		// One line is bounded, so a long title (stored before the title rules,
		// or by another writer) cannot crowd every other memory out.
		for len(l) > memoryMaxLine {
			_, size := utf8.DecodeLastRuneInString(l)
			l = l[:len(l)-size]
		}
		lines = append(lines, l)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, l := range lines {
		if i == memoryMaxLines || b.Len()+len(l)+1 > memoryMaxBytes {
			fmt.Fprintf(&b, "(… %d more: fyr record list --kind memory, then fyr record get MEMORY-<name>)\n", len(lines)-i)
			break
		}
		b.WriteString(l + "\n")
	}
	return b.String(), nil
}
