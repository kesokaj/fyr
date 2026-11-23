package metrics

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/search"
)

// Antigravity step types and statuses (plan D35; Item 7; v9.10.0 F-003).
const (
	agyUser       = 14
	agyReply      = 15
	agyCheckpoint = 23
	agyTool       = 132
	agyFailed     = 7
	agyWaiting    = 9
)

type agyConv struct {
	id, root, agent, parent string
	modified                int64
}

// ingestAgy reads every Antigravity conversation that changed since its last
// ingest, from copies: the live databases are never opened (D24 of the dream
// plan; spec § Invariants). step_payload, the tool output, is never read.
func (s *Store) ingestAgy(o Options, r *Result, log io.Writer) {
	src := o.agyRoot()
	sumPath := filepath.Join(src, "conversation_summaries.db")
	fail := func(what string, err error) {
		r.Failed = append(r.Failed, what)
		fmt.Fprintln(log, printable(fmt.Sprintf("metrics: %s: %v", what, err)))
	}
	fi, err := os.Lstat(sumPath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		fail(sumPath, err)
		return
	}
	if !fi.Mode().IsRegular() {
		fail(sumPath, errors.New("not a regular file"))
		return
	}
	sweepCopies(filepath.Dir(s.path), time.Now())
	tmp, err := os.MkdirTemp(filepath.Dir(s.path), "metrics-agy-")
	if err != nil {
		fail(src, err)
		return
	}
	defer os.RemoveAll(tmp)

	convs, err := agyConversations(src, tmp, o.dreamWork())
	if err != nil {
		fail(sumPath, err)
		return
	}
	unchanged, changed := s.agyConvSyncState(src, convs)
	resolveWorktreeRootsFor(convs, s.db, changed)
	overrides := models.ContextWindowOverrides(resolveMetricsHome(ShowOptions{Home: o.Home}, s.path))
	s.ingestChangedAgyConvs(src, tmp, convs, unchanged, o, overrides, fail)
}

func (s *Store) agyConvSyncState(src string, convs []agyConv) ([]bool, map[string]bool) {
	unchanged := make([]bool, len(convs))
	changed := make(map[string]bool, len(convs))
	for i, c := range convs {
		source := filepath.Join(src, "conversations", c.id+".db")
		fi, lerr := os.Lstat(source)
		var curSize int64 = -1
		if lerr == nil && fi.Mode().IsRegular() {
			curSize = fi.Size()
		}
		var mtime, size int64
		err := s.db.QueryRow(`SELECT mtime, size FROM ingest_state WHERE source = ?`, source).Scan(&mtime, &size)
		if err == nil && c.modified != 0 && mtime == c.modified && curSize >= 0 && size == curSize {
			unchanged[i] = true
			continue
		}
		changed[c.id] = true
	}
	return unchanged, changed
}

func (s *Store) ingestChangedAgyConvs(src, tmp string, convs []agyConv, unchanged []bool, o Options, overrides map[string]int64, fail func(string, error)) {
	for i, c := range convs {
		source := filepath.Join(src, "conversations", c.id+".db")
		if unchanged[i] {
			if err := s.propagateResolvedRoot(c); err != nil {
				fail(source, err)
			}
			continue
		}
		if fi, lerr := os.Lstat(source); lerr != nil && !errors.Is(lerr, os.ErrNotExist) {
			fail(source, lerr)
			continue
		} else if lerr == nil && !fi.Mode().IsRegular() {
			fail(source, errors.New("not a regular file"))
			continue
		}
		if err := s.ingestAgyConversationWithOverrides(src, c, source, tmp, o, overrides); err != nil {
			fail(source, err)
		}
	}
}

func (s *Store) propagateResolvedRoot(c agyConv) error {
	if c.root == "" || isAgyWorktreePath(c.root) {
		return nil
	}
	repoID := ResolveRepoID(c.root)
	_, err := s.db.Exec(`UPDATE sessions SET
		project_root = CASE
			WHEN project_root IS NULL OR project_root = '' OR project_root LIKE '%/.gemini/antigravity-cli/worktrees/%'
			THEN ? ELSE project_root END,
		repo_id = CASE
			WHEN ? != '' AND (repo_id = '' OR project_root IS NULL OR project_root = '' OR project_root LIKE '%/.gemini/antigravity-cli/worktrees/%')
			THEN ? ELSE repo_id END
		WHERE id = ? AND (repo_id = '' OR project_root IS NULL OR project_root = '' OR project_root LIKE '%/.gemini/antigravity-cli/worktrees/%')`,
		c.root, repoID, repoID, c.id)
	return err
}

// agyConversations lists the conversations in src's summaries, without the
// dream's own or /usage probe sessions: one whose first workspace is in exclude
// is no project's session and gets no ingest_state row (visible dream plan D5).
func agyConversations(src, tmp string, exclude []string) ([]agyConv, error) {
	db, err := search.OpenCopy(src, "conversation_summaries.db", tmp)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(buildSummaryQuery(summaryProbeCols(db)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agyConv
	for rows.Next() {
		var c agyConv
		var modified any
		var uris, title, preview string
		if err := rows.Scan(&c.id, &modified, &uris, &c.agent, &c.parent, &title, &preview); err != nil {
			return nil, err
		}
		if c.id == "" || strings.ContainsAny(c.id, `/\`) || c.id == "." || c.id == ".." {
			continue
		}
		if t, ok := search.AgyTime(modified); ok {
			c.modified = t.UnixMilli()
		}
		c.root = firstWorkspace(uris)
		if (c.root != "" && harness.InDreamWork(c.root, exclude)) || isUsageProbeSummary(c.root, title, preview) {
			continue
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func summaryProbeCols(db *sql.DB) (hasTitle, hasPreview bool) {
	rows, err := db.Query(`PRAGMA table_info(conversation_summaries)`)
	if err != nil {
		return false, false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		if rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk) == nil {
			if name == "title" {
				hasTitle = true
			} else if name == "preview" {
				hasPreview = true
			}
		}
	}
	return hasTitle, hasPreview
}

func buildSummaryQuery(hasTitle, hasPreview bool) string {
	titleCol, previewCol := `''`, `''`
	if hasTitle {
		titleCol = `COALESCE(title, '')`
	}
	if hasPreview {
		previewCol = `COALESCE(preview, '')`
	}
	return `SELECT conversation_id, last_modified_time, workspace_uris, agent_name, parent_conversation_id, ` +
		titleCol + `, ` + previewCol + ` FROM conversation_summaries`
}

// firstWorkspace is the first file:// workspace URI's path, the session's
// project root.
func firstWorkspace(uris string) string {
	var list []string
	if json.Unmarshal([]byte(uris), &list) != nil {
		return ""
	}
	for _, u := range list {
		if p, err := url.Parse(u); err == nil && p.Scheme == "file" {
			return filepath.Clean(p.Path)
		}
	}
	return ""
}

func (s *Store) ingestAgyConversation(src string, c agyConv, source, tmp string, o Options) error {
	return s.ingestAgyConversationWithOverrides(src, c, source, tmp, o, nil)
}

func (s *Store) ingestAgyConversationWithOverrides(src string, c agyConv, source, tmp string, o Options, overrides map[string]int64) error {
	db, err := search.OpenCopy(filepath.Join(src, "conversations"), c.id+".db", tmp)
	if errors.Is(err, os.ErrNotExist) {
		return nil // listed but not written yet
	}
	if err != nil {
		return err
	}
	defer func() {
		db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(filepath.Join(tmp, c.id+".db"+suffix))
		}
	}()

	steps, gens, err := readAgySteps(db)
	if err != nil {
		return err
	}

	salt := s.salt()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	w := writer{tx: tx, salt: salt, label: o.Label, outcome: o.Outcome, run: o.Run}
	w.agySession(c)
	w.agyGensWithOverrides(c, gens, w.agySteps(c, steps), overrides)
	w.agyIngestState(source, c.modified)
	if w.err != nil {
		return w.err
	}
	return tx.Commit()
}

// sweepCopies removes metrics-agy-* directories older than an hour under dir:
// an ingest killed mid-copy leaves full conversation copies there, and the
// defer that removes them never ran. A fresh one may be another ingest's.
func sweepCopies(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "metrics-agy-") {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > time.Hour {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// fields indexes a protobuf message's fields by path (search.ProtoFields).
type fields map[string]search.Field

func fieldMap(b []byte) fields {
	m := fields{}
	for _, f := range search.ProtoFields(b) {
		k := pathKey(f.Path...)
		if _, seen := m[k]; !seen {
			m[k] = f
		}
	}
	return m
}

func pathKey(p ...int) string {
	var b strings.Builder
	for _, n := range p {
		fmt.Fprintf(&b, "/%d", n)
	}
	return b.String()
}

func (m fields) varint(p ...int) uint64 {
	if f, ok := m[pathKey(p...)]; ok && f.Wire == 0 {
		return f.Varint
	}
	return 0
}

func (m fields) bytes(p ...int) []byte {
	if f, ok := m[pathKey(p...)]; ok && f.Wire == 2 {
		return f.Bytes
	}
	return nil
}

func (m fields) text(p ...int) string { return string(m.bytes(p...)) }

// timeMS is the Unix milliseconds of a protobuf Timestamp at field n
// (seconds at n→1, nanoseconds at n→2), 0 when absent.
func (m fields) timeMS(n int) int64 {
	sec := m.varint(n, 1)
	if sec == 0 {
		return 0
	}
	return int64(sec)*1000 + int64(m.varint(n, 2))/1_000_000
}
