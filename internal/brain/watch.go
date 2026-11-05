package brain

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// SnapshotScopes copies a regular brain.db (and its -wal and -shm companions)
// into a temporary directory under scratchDir, opens the copy read-only via
// search.OpenCopy, and returns a SHA-256 digest of the records and meta rows
// belonging to scopes (plus global records whose project is "" or in scopes),
// without opening, migrating or writing inside filepath.Dir(dbPath) (BUG-504,
// ADR-506). It returns ok == false when dbPath is not a readable, initialized
// brain store, so check c falls back to a full-file fingerprint.
func SnapshotScopes(dbPath, scratchDir string, scopes []string) (string, bool) {
	st, err := os.Lstat(dbPath)
	if err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	expanded := expandWatchedScopes(scopes)
	if len(expanded) == 0 {
		return "", false
	}
	tmp, err := os.MkdirTemp(scratchDir, ".brain-watch-")
	if err != nil {
		return "", false
	}
	defer os.RemoveAll(tmp)
	db, err := search.OpenCopy(filepath.Dir(dbPath), filepath.Base(dbPath), tmp)
	if err != nil {
		return "", false
	}
	defer db.Close()
	var ver int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil || ver < 1 {
		return "", false
	}
	h := sha256.New()
	if err := hashWatchedRecords(h, db, ver, expanded); err != nil {
		return "", false
	}
	if err := hashWatchedMeta(h, db, expanded); err != nil {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

func expandWatchedScopes(scopes []string) []string {
	var out []string
	add := func(p string) {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		add(filepath.Clean(s))
		if r, err := filepath.EvalSymlinks(s); err == nil {
			add(filepath.Clean(r))
		}
		if main, top, err := trail.Roots(s); err == nil {
			add(main)
			add(top)
		}
	}
	slices.Sort(out)
	return out
}

func hashWatchedRecords(h io.Writer, db *sql.DB, ver int, scopes []string) error {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(scopes)), ",")
	provCol := "''"
	if ver >= 2 {
		provCol = "provenance"
	}
	q := `SELECT scope, id, kind, status, date, title, body, project, ` + provCol + `, updated FROM records ` +
		`WHERE scope IN (` + ph + `) OR (scope = '' AND (project = '' OR project IN (` + ph + `))) ORDER BY scope, id`
	args := make([]any, 0, 2*len(scopes))
	for _, s := range scopes {
		args = append(args, s)
	}
	for _, s := range scopes {
		args = append(args, s)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.Scope, &r.ID, &r.Kind, &r.Status, &r.Date, &r.Title, &r.Body, &r.Project, &r.Provenance, &r.Updated); err != nil {
			return err
		}
		fmt.Fprintf(h, "rec %d:%s %d:%s %d:%s %d:%s %d:%s %d:%s %d:%s %d:%s %d:%s %d:%s\n",
			len(r.Scope), r.Scope, len(r.ID), r.ID, len(r.Kind), r.Kind, len(r.Status), r.Status,
			len(r.Date), r.Date, len(r.Title), r.Title, len(r.Body), r.Body,
			len(r.Project), r.Project, len(r.Provenance), r.Provenance, len(r.Updated), r.Updated)
	}
	return rows.Err()
}

func hashWatchedMeta(h io.Writer, db *sql.DB, scopes []string) error {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(scopes)), ",")
	args := make([]any, len(scopes))
	for i, s := range scopes {
		args[i] = s
	}
	rows, err := db.Query(`SELECT scope, export_sha, last_dream FROM meta WHERE scope IN (`+ph+`) ORDER BY scope`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var scope string
		var exp, dream sql.NullString
		if err := rows.Scan(&scope, &exp, &dream); err != nil {
			return err
		}
		fmt.Fprintf(h, "meta %d:%s %t:%d:%s %t:%d:%s\n",
			len(scope), scope, exp.Valid, len(exp.String), exp.String, dream.Valid, len(dream.String), dream.String)
	}
	return rows.Err()
}
