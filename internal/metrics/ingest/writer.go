package ingest

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// Writer turns parsed lines into rows inside one transaction; the first
// error stops it.
type Writer struct {
	tx            *sql.Tx
	salt          string
	label         string
	outcome       string
	run           string
	err           error
	project       map[string]string // project root -> docs root
	repoIDs       map[string]string // project root -> repo_id
	resolveRepoID func(string) string
}

// NewWriter constructs a transactional row writer for tests and ingest helpers.
func NewWriter(tx *sql.Tx, salt, label, outcome, run string, resolveRepoID func(string) string) *Writer {
	return &Writer{
		tx:            tx,
		salt:          salt,
		label:         label,
		outcome:       outcome,
		run:           run,
		resolveRepoID: resolveRepoID,
	}
}

// Err returns the first SQL error observed by the writer.
func (w *Writer) Err() error { return w.err }

func (w *Writer) exec(q string, args ...any) {
	if w.err != nil {
		return
	}
	_, w.err = w.tx.Exec(q, args...)
}

func (w *Writer) step(sid, ext string, ts int64, kind string) {
	w.exec(`INSERT INTO steps(session_id, ext_id, ts, kind) VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id, ext_id) DO NOTHING`, sid, ext, ts, kind)
}

func (w *Writer) timedStep(sid, ext string, ts int64, kind string, started, ended, wait any) {
	w.exec(`INSERT INTO steps(session_id, ext_id, ts, kind, started_ms, ended_ms, wait_ms) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, ext_id) DO UPDATE SET
			ts = excluded.ts, started_ms = excluded.started_ms, ended_ms = excluded.ended_ms, wait_ms = excluded.wait_ms`,
		sid, ext, ts, kind, started, ended, wait)
}

func (w *Writer) targetKind(root, p string) any {
	if p == "" || root == "" {
		return nil
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "outside"
	}
	return search.KindOf(filepath.ToSlash(rel), w.docsRoot(root))
}

func (w *Writer) docsRoot(root string) string {
	if w.project == nil {
		w.project = map[string]string{}
	}
	if d, ok := w.project[root]; ok {
		return d
	}
	d := trail.DocsRoot(root)
	w.project[root] = d
	return d
}

func (w *Writer) hash(cmd string) string {
	h := sha256.Sum256([]byte(w.salt + "\x00" + cmd))
	return hex.EncodeToString(h[:])[:16]
}

func (w *Writer) repoID(root string) string {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" || trimmed == search.GlobalWorkspace || w.resolveRepoID == nil {
		return ""
	}
	if w.repoIDs == nil {
		w.repoIDs = map[string]string{}
	}
	if id, ok := w.repoIDs[trimmed]; ok {
		return id
	}
	id := w.resolveRepoID(trimmed)
	w.repoIDs[trimmed] = id
	return id
}

// nameShape is what an identifier taken from a transcript must look like to
// be stored: a skill, a model, an agent type.
var nameShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,63}$`)

// Identifier returns s when it is name-shaped, else "".
func Identifier(s string) string {
	if nameShape.MatchString(s) {
		return s
	}
	return ""
}

// ToolName returns a tool's name as stored: name-shaped, or "unnamed".
func ToolName(s string) string {
	if id := Identifier(s); id != "" {
		return id
	}
	return "unnamed"
}
