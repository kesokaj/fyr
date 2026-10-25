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

// Options is what one call runs with.
type Options struct {
	Home   string           // the home whose ~/.config/fyr holds brain.db (--home)
	Dir    string           // the working directory: its checkout is the project
	Global bool             // the global scope instead of the project's
	Now    func() time.Time // the clock; nil is time.Now
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// AddInput is `record add`'s arguments.
type AddInput struct {
	Kind, Title, Status, ID string
	Project                 string // --project: the project a global record is about
	Body                    io.Reader
}

// EditInput is `record edit`'s arguments: nil leaves a field as it is.
type EditInput struct {
	ID            string
	Status, Title *string
	Body          io.Reader // --body: the new body on stdin
}

// ListFilter is `record list`'s flags.
type ListFilter struct {
	Kind    string
	Open    bool
	Project string // with Global: the global records about this project
	All     bool   // every scope
}

// session is one call's store and scope.
type session struct {
	st    *store
	scope string // the main worktree's root, or "" for global
	top   string // this checkout's top level, where trail.jsonl is; "" for global
}

func (s *session) close() { s.st.close() }

func (s *session) project() bool { return s.top != "" }

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

// unmigrated refuses a project whose trail is still files (D16): a docs tree
// and no trail.jsonl.
func unmigrated(top string) error {
	if _, err := os.Lstat(trailPath(top)); err == nil {
		return nil
	}
	for _, d := range []string{".agents/docs", "docs"} {
		if fi, err := os.Stat(filepath.Join(top, d)); err == nil && fi.IsDir() {
			return refuse("this project's trail is still files under %s/: migrate it with `fyr record import` first", d)
		}
	}
	return nil
}

// openSession opens the store for o's scope. In a project it refuses a
// symlinked export path and an unmigrated trail first, and a read loads a
// changed trail.jsonl before it answers; a write loads inside its own
// transaction instead (write).
func openSession(o Options, writing bool) (*session, error) {
	s := &session{}
	if !o.Global {
		scope, top, err := resolveProject(o.Dir)
		if err != nil {
			return nil, err
		}
		if err := guardExportPath(top); err != nil {
			return nil, err
		}
		if err := unmigrated(top); err != nil {
			return nil, err
		}
		s.scope, s.top = scope, top
	}
	st, err := openStore(o.Home)
	if err != nil {
		return nil, err
	}
	s.st = st
	if s.project() && !writing {
		if err := s.sync(); err != nil {
			st.close()
			return nil, err
		}
	}
	return s, nil
}

// sync loads the checkout's trail.jsonl when it changed since the last
// export or load. The SHA is compared without the lock first, so a read of
// an unchanged trail takes no write lock.
func (s *session) sync() error {
	data, ok, err := readTrail(s.top)
	if err != nil || !ok {
		return err
	}
	var have sql.NullString
	err = s.st.db.QueryRow(`SELECT export_sha FROM meta WHERE scope = ?`, s.scope).Scan(&have)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if have.String == sum(data) {
		return nil
	}
	return s.st.tx(s.loadLocked)
}

// write runs fn in one transaction: in a project, load a changed
// trail.jsonl, then fn, then export, so the store and the file agree after
// every write (D21).
func (s *session) write(fn func(c *sql.Conn) error) error {
	return s.st.tx(func(c *sql.Conn) error {
		if s.project() {
			if err := s.loadLocked(c); err != nil {
				return err
			}
		}
		if err := fn(c); err != nil {
			return err
		}
		if s.project() {
			return s.exportLocked(c)
		}
		return nil
	})
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

// Add stores one record and returns its ID; in a project it exports at once.
func Add(o Options, in AddInput) (string, error) {
	kind := in.Kind
	if !knownKind(kind) {
		return "", usage("unknown kind %q: one of %s", kind, strings.Join(Kinds(), " "))
	}
	title, err := cleanTitle(in.Title)
	if err != nil {
		return "", err
	}
	if in.Project != "" && !o.Global {
		return "", usage("--project names the project a global record is about: it needs --global")
	}
	if in.ID != "" && !validID(kind, in.ID) {
		return "", usage("--id %q is not a %s id (%s)", in.ID, kind, idGrammar(kind))
	}
	// A given status is checked before stdin is read; a plan's is derived
	// from the body below.
	if in.Status != "" || kind == "handoff" {
		if _, err := resolveStatus(kind, in.Status, ""); err != nil {
			return "", err
		}
	}
	switch {
	case o.Global && kind == "memory":
		return "", refuse("memory records are project-scoped (D14): drop --global")
	case !o.Global && globalOnly[kind]:
		return "", refuse("%s records are global only (R8): add --global, and --project <root> for the project it is about", kind)
	}
	body, err := readBody(in.Body)
	if err != nil {
		return "", err
	}
	status, err := resolveStatus(kind, in.Status, body)
	if err != nil {
		return "", err
	}
	s, err := openSession(o, true)
	if err != nil {
		return "", err
	}
	defer s.close()
	project := ""
	if in.Project != "" {
		project = canonicalProject(in.Project)
	}
	now := o.now()
	var id string
	err = s.write(func(c *sql.Conn) error {
		id = in.ID
		if id == "" {
			var err error
			if id, err = s.allocate(c, kind, title, now); err != nil {
				return err
			}
		} else if taken, err := exists(c, s.scope, id); err != nil {
			return err
		} else if taken {
			return refuse("%s already exists in this scope", id)
		}
		_, err := c.ExecContext(context.Background(), `INSERT INTO records(scope, id, kind, status, date, title, body, project, updated)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.scope, id, kind, status, now.Format("2006-01-02"), title, body, project, now.UTC().Format(time.RFC3339))
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

const recordCols = `scope, id, kind, status, date, title, body, project, updated`

func scanRecord(sc interface{ Scan(...any) error }) (Record, error) {
	var r Record
	err := sc.Scan(&r.Scope, &r.ID, &r.Kind, &r.Status, &r.Date, &r.Title, &r.Body, &r.Project, &r.Updated)
	return r, err
}

// Get returns the records named, in the order named; any not in the scope
// are returned as missing, with a refusal naming them.
func Get(o Options, ids []string) ([]Record, []string, error) {
	s, err := openSession(o, false)
	if err != nil {
		return nil, nil, err
	}
	defer s.close()
	var recs []Record
	var missing []string
	for _, id := range ids {
		r, err := scanRecord(s.st.db.QueryRow(`SELECT `+recordCols+` FROM records WHERE scope = ? AND id = ?`, s.scope, id))
		if errors.Is(err, sql.ErrNoRows) {
			missing = append(missing, id)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		recs = append(recs, r)
	}
	if len(missing) > 0 {
		return recs, missing, refuse("no record %s in this scope", strings.Join(missing, ", "))
	}
	return recs, nil, nil
}

// Edit changes a record's status, title or body (D9: without a body only the
// metadata changes); a plan's status follows its new body.
func Edit(o Options, in EditInput) error {
	if in.Status == nil && in.Title == nil && in.Body == nil {
		return usage("usage: fyr record edit <id> [--status S] [--title T] [--body] needs at least one of them")
	}
	if in.Status != nil && *in.Status == "" {
		return usage("--status needs a value")
	}
	var title string
	if in.Title != nil {
		t, err := cleanTitle(*in.Title)
		if err != nil {
			return err
		}
		title = t
	}
	var body string
	if in.Body != nil {
		b, err := readBody(in.Body)
		if err != nil {
			return err
		}
		body = b
	}
	s, err := openSession(o, true)
	if err != nil {
		return err
	}
	defer s.close()
	now := o.now()
	return s.write(func(c *sql.Conn) error {
		ctx := context.Background()
		r, err := scanRecord(c.QueryRowContext(ctx, `SELECT `+recordCols+` FROM records WHERE scope = ? AND id = ?`, s.scope, in.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return refuse("no record %s in this scope", in.ID)
		}
		if err != nil {
			return err
		}
		if in.Status != nil {
			if r.Status, err = resolveStatus(r.Kind, *in.Status, r.Body); err != nil {
				return err
			}
		}
		if in.Title != nil {
			r.Title = title
		}
		if in.Body != nil {
			r.Body = body
			if r.Kind == "plan" {
				r.Status = planStatus(body)
			}
		}
		_, err = c.ExecContext(ctx, `UPDATE records SET status = ?, title = ?, body = ?, updated = ? WHERE scope = ? AND id = ?`,
			r.Status, r.Title, r.Body, now.UTC().Format(time.RFC3339), s.scope, in.ID)
		return err
	})
}

// List returns the scope's records, newest first: with All every scope's.
func List(o Options, f ListFilter) ([]Record, error) {
	switch {
	case f.Kind != "" && !knownKind(f.Kind):
		return nil, usage("unknown kind %q: one of %s", f.Kind, strings.Join(Kinds(), " "))
	case f.Project != "" && !o.Global:
		return nil, usage("--project lists the global records about a project: it needs --global")
	case f.All && o.Global:
		return nil, usage("--all already covers every scope: drop --global")
	}
	var s *session
	var err error
	if f.All {
		s, err = openAll(o)
	} else {
		s, err = openSession(o, false)
	}
	if err != nil {
		return nil, err
	}
	defer s.close()
	q := `SELECT ` + recordCols + ` FROM records WHERE 1 = 1`
	var args []any
	if !f.All {
		q += ` AND scope = ?`
		args = append(args, s.scope)
	}
	if f.Kind != "" {
		q += ` AND kind = ?`
		args = append(args, f.Kind)
	}
	if f.Open {
		q += ` AND ` + openSQL
	}
	if f.Project != "" {
		q += ` AND project = ?`
		args = append(args, canonicalProject(f.Project))
	}
	q += ` ORDER BY date DESC, updated DESC, id DESC`
	rows, err := s.st.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// openAll opens the store for a view of every scope; in a migrated checkout
// that checkout's trail.jsonl is loaded first, so its rows are current.
func openAll(o Options) (*session, error) {
	st, err := openStore(o.Home)
	if err != nil {
		return nil, err
	}
	s := &session{st: st}
	if scope, top, err := trail.Roots(o.Dir); err == nil {
		if err := guardExportPath(top); err != nil {
			st.close()
			return nil, err
		}
		if unmigrated(top) == nil {
			p := &session{st: st, scope: scope, top: top}
			if err := p.sync(); err != nil {
				st.close()
				return nil, err
			}
		}
	}
	return s, nil
}
