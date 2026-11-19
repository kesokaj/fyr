package brain

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/trail"
)

// session is one call's store and scope.
type session struct {
	st    *store
	scope string // the main worktree's root, or "" for global
	top   string // this checkout's top level, where trail.jsonl is; "" for global
}

func (s *session) close() { s.st.close() }

// resolveProject is the checkout holding dir: the main worktree's root, the
// scope every worktree shares (memory.Root's rule), and this checkout's own
// top level, whose trail.jsonl its branch commits.
func resolveProject(dir string) (scope, top string, err error) {
	scope, top, err = trail.Roots(dir)
	if errors.Is(err, trail.ErrNotCheckout) {
		return "", "", refuse("not a git checkout: %s (the global scope is --global)", dir)
	}
	if err != nil {
		return "", "", refuse("%v", err)
	}
	return scope, top, nil
}

// unmigratedDocsDir returns the docs directory name if top holds a file-era
// trail with no .agents/trail.jsonl yet.
func unmigratedDocsDir(top string) string {
	if _, err := os.Lstat(trailPath(top)); err == nil {
		return ""
	}
	for _, d := range []string{".agents/docs", "docs"} {
		if fi, err := os.Stat(filepath.Join(top, d)); err == nil && fi.IsDir() {
			return d
		}
	}
	return ""
}

// openSession opens the store for o's scope. In a project it refuses a
// symlinked export path and an unmigrated trail first, checking scope records
// on the already-open store without copying the database (ADR-091).
func openSession(o Options) (*session, error) {
	s := &session{}
	var docsDir string
	if !o.Global {
		scope, top, err := resolveProject(o.Dir)
		if err != nil {
			return nil, err
		}
		if err := guardExportPath(top); err != nil {
			return nil, err
		}
		docsDir = unmigratedDocsDir(top)
		if docsDir != "" && o.Home != "" {
			if _, err := os.Lstat(Path(o.Home)); errors.Is(err, os.ErrNotExist) {
				return nil, refuse("this project's trail is still files under %s/: migrate it with `fyr record import` first", docsDir)
			}
		}
		s.scope, s.top = scope, top
	}
	st, err := openStore(o.Home)
	if err != nil {
		return nil, err
	}
	if docsDir != "" {
		ok, err := hasScopeRecordsDB(st.db, s.scope)
		if err != nil {
			st.close()
			return nil, err
		}
		if !ok {
			st.close()
			return nil, refuse("this project's trail is still files under %s/: migrate it with `fyr record import` first", docsDir)
		}
	}
	s.st = st
	return s, nil
}

// write runs fn in one transaction in brain.db (ADR-091).
func (s *session) write(fn func(c *sql.Conn) error) error {
	return s.st.tx(fn)
}

// openAll opens the store for a view of every scope; in a checkout it guards
// the checkout's export path first.
func openAll(o Options) (*session, error) {
	if _, top, err := trail.Roots(o.Dir); err == nil {
		if err := guardExportPath(top); err != nil {
			return nil, err
		}
	}
	st, err := openStore(o.Home)
	if err != nil {
		return nil, err
	}
	return &session{st: st}, nil
}

// readBody reads a body from stdin: at most 1 MiB of UTF-8 text with no NUL
// byte, CRLF folded to LF, leading blank lines and trailing whitespace
// dropped; an empty one is refused (D9).
func readBody(r io.Reader) (string, error) {
	if r == nil {
		r = strings.NewReader("")
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	if err != nil {
		return "", err
	}
	switch {
	case len(raw) > maxBody:
		return "", refuse("the body on stdin is over 1 MiB; store a shorter record")
	case bytes.IndexByte(raw, 0) >= 0:
		return "", refuse("the body on stdin holds a NUL byte")
	case !utf8.Valid(raw):
		return "", refuse("the body on stdin is not UTF-8 text")
	}
	text := strings.TrimRight(strings.ReplaceAll(string(raw), "\r\n", "\n"), " \t\r\n")
	if !bodyText(text) {
		return "", refuse("the body on stdin holds a control character other than tab and newline")
	}
	for {
		line, rest, found := strings.Cut(text, "\n")
		if !found || strings.TrimSpace(line) != "" {
			break
		}
		text = rest
	}
	if strings.TrimSpace(text) == "" {
		return "", refuse("the body comes on stdin and is empty")
	}
	return text, nil
}

// canonicalProject is --project as the store keys it: absolute, and the main
// worktree's root when it names a checkout.
func canonicalProject(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if main, _, err := trail.Roots(abs); err == nil {
		return main
	}
	return filepath.Clean(abs)
}

func exists(c *sql.Conn, scope, id string) (bool, error) {
	var n int
	err := c.QueryRowContext(context.Background(), `SELECT count(*) FROM records WHERE scope = ? AND id = ?`, scope, id).Scan(&n)
	return n > 0, err
}

// allocate gives a record its ID inside the write's transaction (D7):
// BUG-NNN and ADR-NNN as the scope's highest number plus one, compared as
// numbers; every other kind <KIND>-<YYYY-MM-DD-HHMM>-<slug>, with -2, -3 …
// when that is taken.
func (s *session) allocate(c *sql.Conn, kind, title string, now time.Time) (string, error) {
	prefix := prefixes[kind]
	if kind == "bug" || kind == "adr" {
		rows, err := c.QueryContext(context.Background(), `SELECT id FROM records WHERE scope = ? AND kind = ?`, s.scope, kind)
		if err != nil {
			return "", err
		}
		max := 0
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return "", err
			}
			re := bugID
			if kind == "adr" {
				re = adrID
			}
			if m := re.FindStringSubmatch(id); m != nil {
				// At most nine digits (bugID, adrID): Atoi cannot overflow.
				if n, err := strconv.Atoi(m[1]); err == nil && n > max {
					max = n
				}
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s-%03d", prefix, max+1), nil
	}
	base := fmt.Sprintf("%s-%s-%s", prefix, now.Format("2006-01-02-1504"), slugify(title))
	id := base
	for n := 2; ; n++ {
		taken, err := exists(c, s.scope, id)
		if err != nil || !taken {
			return id, err
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

func resolveProvenance(kind, given string) (string, error) {
	switch kind {
	case "feedback":
		if given == "" {
			return "operator", nil
		}
		if given == "operator" || given == "derived" || given == "unknown" {
			return given, nil
		}
		return "", refuse("--provenance must be operator or derived, not %q", given)
	case "memory":
		if given == "" {
			return "unknown", nil
		}
		if given == "operator" || given == "derived" || given == "unknown" {
			return given, nil
		}
		return "", refuse("--provenance must be operator or derived, not %q", given)
	default:
		if given != "" {
			return "", refuse("a %s record takes no provenance", kind)
		}
		return "", nil
	}
}

const recordCols = `scope, id, kind, status, date, title, body, project, provenance, memory_kind, validity, verified_at, evidence_count, source_sessions, updated`

func selectRecordCols(ver int) string {
	switch {
	case ver < 2:
		return `scope, id, kind, status, date, title, body, project, '' AS provenance, '' AS memory_kind, 1.0 AS validity, '' AS verified_at, 0 AS evidence_count, '' AS source_sessions, updated`
	case ver < 3:
		return `scope, id, kind, status, date, title, body, project, provenance, '' AS memory_kind, 1.0 AS validity, '' AS verified_at, 0 AS evidence_count, '' AS source_sessions, updated`
	default:
		return recordCols
	}
}

func scanRecord(sc interface{ Scan(...any) error }) (Record, error) {
	var r Record
	err := sc.Scan(&r.Scope, &r.ID, &r.Kind, &r.Status, &r.Date, &r.Title, &r.Body, &r.Project, &r.Provenance,
		&r.MemoryKind, &r.Validity, &r.VerifiedAt, &r.EvidenceCount, &r.SourceSessions, &r.Updated)
	if err == nil && (r.Kind == "memory" || r.Kind == "feedback") && r.Provenance == "" {
		r.Provenance = "unknown"
	}
	return r, err
}
