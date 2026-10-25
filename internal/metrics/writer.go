package metrics

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

// writer turns parsed lines into rows inside one transaction; the first
// error stops it.
type writer struct {
	tx             *sql.Tx
	salt           string
	label, outcome string
	err            error
	project        map[string]string // project root -> docs root
}

func (w *writer) exec(q string, args ...any) {
	if w.err != nil {
		return
	}
	_, w.err = w.tx.Exec(q, args...)
}

func (w *writer) step(sid, ext string, ts int64, kind string) {
	w.exec(`INSERT INTO steps(session_id, ext_id, ts, kind) VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id, ext_id) DO NOTHING`, sid, ext, ts, kind)
}

// targetKind is the trail kind of an absolute path inside the session's
// project, "outside" for one elsewhere; the path is discarded.
func (w *writer) targetKind(root, p string) any {
	if p == "" || root == "" {
		return nil
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "outside"
	}
	return search.KindOf(filepath.ToSlash(rel), w.docsRoot(root))
}

func (w *writer) docsRoot(root string) string {
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

func (w *writer) hash(cmd string) string {
	h := sha256.Sum256([]byte(w.salt + "\x00" + cmd))
	return hex.EncodeToString(h[:])[:16]
}

func (s *Store) salt() string {
	var v string
	s.db.QueryRow(`SELECT value FROM meta WHERE key = 'salt'`).Scan(&v)
	return v
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nameShape is what an identifier taken from a transcript must look like to
// be stored: a skill, a model, an agent type. Anything else (escape bytes,
// prose a prompt-injected session put there) is dropped, so it reaches
// neither the store nor the operator's terminal (security review of v2.6.0,
// part 1).
var nameShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,63}$`)

// identifier is s when it is name-shaped, else "".
func identifier(s string) string {
	if nameShape.MatchString(s) {
		return s
	}
	return ""
}

// toolName is a tool's name as stored: name-shaped, or "unnamed".
func toolName(s string) string {
	if id := identifier(s); id != "" {
		return id
	}
	return "unnamed"
}
