package brain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/harness"
)

// maxTrail caps what is read of trail.jsonl: far above any real trail (a
// thousand records of a full 1 MiB each), low enough that a planted file
// cannot exhaust memory.
const maxTrail = 1 << 30

// trailPath is a checkout's export.
func trailPath(top string) string { return filepath.Join(top, ".agents", "trail.jsonl") }

// guardExportPath refuses a symlinked or non-directory .agents and a
// trail.jsonl that is a symlink or not a regular file: a commit can carry a
// link, and the export must never write, or the load read, through one.
func guardExportPath(top string) error {
	agents := filepath.Join(top, ".agents")
	fi, err := os.Lstat(agents)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return err
	case fi.Mode()&os.ModeSymlink != 0:
		return refuse("%s is a symlink: the trail must be the project's own file", agents)
	case !fi.IsDir():
		return refuse("%s is not a directory", agents)
	}
	file := trailPath(top)
	fi, err = os.Lstat(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return err
	case fi.Mode()&os.ModeSymlink != 0:
		return refuse("%s is a symlink: the trail must be the project's own file", file)
	case !fi.Mode().IsRegular():
		return refuse("%s is not a regular file", file)
	}
	return nil
}

// readTrail reads a checkout's trail.jsonl; ok is false when there is none.
// The open does not block, so a FIFO swapped in after the check returns at
// once, and the opened file is checked again.
func readTrail(top string) (data []byte, ok bool, err error) {
	if err := guardExportPath(top); err != nil {
		return nil, false, err
	}
	p := trailPath(top)
	f, err := openNonBlocking(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, false, refuse("%s is not a regular file", p)
	}
	data, err = io.ReadAll(io.LimitReader(f, maxTrail+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > maxTrail {
		return nil, false, refuse("%s is over %d bytes; refusing to load it", p, maxTrail)
	}
	return data, true, nil
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// wireLine is one exported record, its keys in the order the file carries
// them (Interfaces § Export).
type wireLine struct {
	Date    string `json:"date"`
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Title   string `json:"title"`
	Updated string `json:"updated"`
	Body    string `json:"body"`
}

// readLine is the same line as the load reads it: a missing key is nil.
type readLine struct {
	Date    *string `json:"date"`
	ID      *string `json:"id"`
	Kind    *string `json:"kind"`
	Status  *string `json:"status"`
	Title   *string `json:"title"`
	Updated *string `json:"updated"`
	Body    *string `json:"body"`
}

var conflictMarkers = [][]byte{[]byte("<<<<<<<"), []byte("======="), []byte(">>>>>>>"), []byte("|||||||")}

// parseTrail reads every record of a trail.jsonl, or refuses the file with
// the line number of its first bad line: a git conflict marker, a line that
// is not one JSON object with exactly the seven keys, a kind that is never
// exported, an ID, status or date the kind does not take, or an ID a line
// above already holds. A blank line is skipped.
func parseTrail(data []byte) ([]Record, error) {
	var out []Record
	seen := map[string]int{}
	for i, l := range bytes.Split(data, []byte("\n")) {
		n := i + 1
		l = bytes.TrimSuffix(l, []byte("\r"))
		if len(bytes.TrimSpace(l)) == 0 {
			continue
		}
		for _, m := range conflictMarkers {
			if bytes.HasPrefix(l, m) {
				return nil, refuse("trail.jsonl line %d: a git conflict marker; resolve the conflict as text (one record per line) and run the command again", n)
			}
		}
		var w readLine
		dec := json.NewDecoder(bytes.NewReader(l))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&w); err != nil {
			return nil, refuse("trail.jsonl line %d: %v", n, err)
		}
		var extra json.RawMessage
		if err := dec.Decode(&extra); err != io.EOF {
			return nil, refuse("trail.jsonl line %d: more than one JSON value on the line", n)
		}
		for _, f := range []struct {
			name string
			v    *string
		}{{"date", w.Date}, {"id", w.ID}, {"kind", w.Kind}, {"status", w.Status}, {"title", w.Title}, {"updated", w.Updated}, {"body", w.Body}} {
			if f.v == nil {
				return nil, refuse("trail.jsonl line %d: no %q key", n, f.name)
			}
		}
		r := Record{ID: *w.ID, Kind: *w.Kind, Status: *w.Status, Date: *w.Date, Title: *w.Title, Body: *w.Body, Updated: *w.Updated}
		switch {
		case !knownKind(r.Kind) || unexported[r.Kind]:
			return nil, refuse("trail.jsonl line %d: kind %q is not one the export carries", n, r.Kind)
		case !validID(r.Kind, r.ID):
			return nil, refuse("trail.jsonl line %d: id %q is not a %s id (%s)", n, r.ID, r.Kind, idGrammar(r.Kind))
		case !allowed(r.Kind, r.Status):
			return nil, refuse("trail.jsonl line %d: status %q is not one a %s takes", n, r.Status, r.Kind)
		case !dateRe.MatchString(r.Date):
			return nil, refuse("trail.jsonl line %d: date %q is not YYYY-MM-DD", n, r.Date)
		case r.Title == "" || bytes.ContainsAny([]byte(r.Title), "\r\n\x00"):
			return nil, refuse("trail.jsonl line %d: the title must be one non-empty line", n)
		case len(r.Body) > maxBody || bytes.IndexByte([]byte(r.Body), 0) >= 0 || !utf8.ValidString(r.Body):
			return nil, refuse("trail.jsonl line %d: the body is over 1 MiB or not text", n)
		}
		if _, err := time.Parse(time.RFC3339, r.Updated); err != nil {
			return nil, refuse("trail.jsonl line %d: updated %q is not RFC 3339", n, r.Updated)
		}
		if first, dup := seen[r.ID]; dup {
			return nil, refuse("trail.jsonl line %d: duplicate ID %s (first at line %d); renumber one of the two lines by hand", n, r.ID, first)
		}
		seen[r.ID] = n
		out = append(out, r)
	}
	return out, nil
}

// encodeTrail is the export of rows: one line each, sorted by ID (numbers
// numerically), keys in order, no HTML escaping, a final newline. The same
// rows always give the same bytes.
func encodeTrail(rows []Record) ([]byte, error) {
	sort.Slice(rows, func(i, j int) bool { return idLess(rows[i].ID, rows[j].ID) })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		if err := enc.Encode(wireLine{r.Date, r.ID, r.Kind, r.Status, r.Title, r.Updated, r.Body}); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func exportSHA(c *sql.Conn, scope string) (string, error) {
	var have sql.NullString
	err := c.QueryRowContext(context.Background(), `SELECT export_sha FROM meta WHERE scope = ?`, scope).Scan(&have)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return have.String, err
}

func setExportSHA(c *sql.Conn, scope, sha string) error {
	_, err := c.ExecContext(context.Background(), `INSERT INTO meta(scope, export_sha) VALUES (?, ?)
		ON CONFLICT(scope) DO UPDATE SET export_sha = excluded.export_sha`, scope, sha)
	return err
}

// loadLocked replaces the scope's exported rows with the checkout's
// trail.jsonl when the file's SHA-256 is not the one the last export or load
// stored (a pull, checkout or merge changed it): the file wins (D21). It
// runs under the write lock. No file loads nothing and keeps the rows: a
// deleted file is restored by the next write, never read as "no records".
func (s *session) loadLocked(c *sql.Conn) error {
	data, ok, err := readTrail(s.top)
	if err != nil || !ok {
		return err
	}
	sha := sum(data)
	have, err := exportSHA(c, s.scope)
	if err != nil || have == sha {
		return err
	}
	recs, err := parseTrail(data)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if _, err := c.ExecContext(ctx, `DELETE FROM records WHERE scope = ? AND kind NOT IN `+unexportedSQL, s.scope); err != nil {
		return err
	}
	for _, r := range recs {
		if _, err := c.ExecContext(ctx, `INSERT INTO records(scope, id, kind, status, date, title, body, project, updated)
			VALUES (?, ?, ?, ?, ?, ?, ?, '', ?)`, s.scope, r.ID, r.Kind, r.Status, r.Date, r.Title, r.Body, r.Updated); err != nil {
			if isConstraint(err) {
				// A memory row already holds the ID: the file's line cannot be
				// loaded beside it.
				return refuse("trail.jsonl: %s is already a local memory record's id", r.ID)
			}
			return err
		}
	}
	return setExportSHA(c, s.scope, sha)
}

// exportLocked writes the scope's exported rows to the checkout's
// trail.jsonl (temp file plus rename; unchanged bytes are not rewritten) and
// stores their SHA-256, under the same write lock as the write it follows, so
// the file and meta.export_sha always describe one commit of the store.
func (s *session) exportLocked(c *sql.Conn) error {
	rows, err := c.QueryContext(context.Background(), `SELECT id, kind, status, date, title, updated, body
		FROM records WHERE scope = ? AND kind NOT IN `+unexportedSQL, s.scope)
	if err != nil {
		return err
	}
	var recs []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.Kind, &r.Status, &r.Date, &r.Title, &r.Updated, &r.Body); err != nil {
			rows.Close()
			return err
		}
		recs = append(recs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	data, err := encodeTrail(recs)
	if err != nil {
		return err
	}
	current, ok, err := readTrail(s.top)
	if err != nil {
		return err
	}
	if !ok || !bytes.Equal(current, data) {
		// Checked again right before the write: readTrail's guard ran on
		// an earlier read. A link that appears between this check and the
		// rename is not closed here, as in the search index: whoever can race
		// it already writes to the checkout.
		if err := guardExportPath(s.top); err != nil {
			return err
		}
		// 0644: a committed file, read by git like any other.
		if err := harness.WriteAtomic(trailPath(s.top), data, 0o644); err != nil {
			return err
		}
	}
	return setExportSHA(c, s.scope, sum(data))
}
