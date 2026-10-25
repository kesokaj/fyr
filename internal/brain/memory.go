package brain

import (
	"context"
	"database/sql"
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

// MemoryAdd stores the memory e.Name, replacing one of the same name, and
// returns its ID.
func MemoryAdd(o Options, e MemoryEntry, body io.Reader) (string, error) {
	switch {
	case !memoryTypes[e.Type]:
		return "", refuse("--type must be user, feedback, project or reference, not %q", e.Type)
	case !memorySlug.MatchString(e.Name):
		return "", refuse("--name must match %s, not %q", memorySlug, e.Name)
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
	err = s.st.tx(func(c *sql.Conn) error {
		ctx := context.Background()
		taken, err := exists(c, s.scope, id)
		if err != nil {
			return err
		}
		if taken {
			_, err = c.ExecContext(ctx, `UPDATE records SET title = ?, body = ?, updated = ? WHERE scope = ? AND id = ? AND kind = 'memory'`,
				title, full, now.UTC().Format(time.RFC3339), s.scope, id)
			return err
		}
		_, err = c.ExecContext(ctx, `INSERT INTO records(scope, id, kind, status, date, title, body, project, updated)
			VALUES (?, ?, 'memory', 'closed', ?, ?, ?, '', ?)`, s.scope, id, now.Format("2006-01-02"), title, full, now.UTC().Format(time.RFC3339))
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// MemoryShow is the project's memory index, one line per memory, capped at
// what a session loads.
func MemoryShow(o Options) (string, error) {
	s, err := memorySession(o)
	if err != nil {
		return "", err
	}
	defer s.close()
	rows, err := s.st.db.Query(`SELECT id, title, body FROM records WHERE scope = ? AND kind = 'memory' ORDER BY id`, s.scope)
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
