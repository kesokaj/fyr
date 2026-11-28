package ingest

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
	AgyUser       = 14
	AgyReply      = 15
	AgyCheckpoint = 23
	AgyTool       = 132
	AgyFailed     = 7
	AgyWaiting    = 9
)

// AgyConv is one Antigravity conversation summary row.
type AgyConv struct {
	ID, Root, Agent, Parent string
	Modified                int64
}

func (d Deps) ingestAgy(o Options, r *Result, log io.Writer) {
	src := o.agyRoot()
	sumPath := filepath.Join(src, "conversation_summaries.db")
	fail := func(what string, err error) {
		r.Failed = append(r.Failed, what)
		fmt.Fprintln(log, search.Printable(fmt.Sprintf("metrics: %s: %v", what, err)))
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
	SweepCopies(filepath.Dir(d.StorePath), time.Now())
	tmp, err := os.MkdirTemp(filepath.Dir(d.StorePath), "metrics-agy-")
	if err != nil {
		fail(src, err)
		return
	}
	defer os.RemoveAll(tmp)

	convs, err := d.agyConversations(src, tmp, o.dreamWork())
	if err != nil {
		fail(sumPath, err)
		return
	}
	unchanged, changed := d.agyConvSyncState(src, convs)
	if d.ResolveWorktreeRoots != nil {
		d.ResolveWorktreeRoots(convs, d.DB, changed)
	}
	home := o.Home
	if d.ResolveMetricsHome != nil {
		home = d.ResolveMetricsHome(o.Home, d.StorePath)
	}
	overrides := models.ContextWindowOverrides(home)
	d.ingestChangedAgyConvs(src, tmp, convs, unchanged, o, overrides, fail)
}

func (d Deps) agyConvSyncState(src string, convs []AgyConv) ([]bool, map[string]bool) {
	unchanged := make([]bool, len(convs))
	changed := make(map[string]bool, len(convs))
	for i, c := range convs {
		source := filepath.Join(src, "conversations", c.ID+".db")
		fi, lerr := os.Lstat(source)
		var curSize int64 = -1
		if lerr == nil && fi.Mode().IsRegular() {
			curSize = fi.Size()
		}
		var mtime, size int64
		err := d.DB.QueryRow(`SELECT mtime, size FROM ingest_state WHERE source = ?`, source).Scan(&mtime, &size)
		if err == nil && c.Modified != 0 && mtime == c.Modified && curSize >= 0 && size == curSize {
			unchanged[i] = true
			continue
		}
		changed[c.ID] = true
	}
	return unchanged, changed
}

func (d Deps) ingestChangedAgyConvs(src, tmp string, convs []AgyConv, unchanged []bool, o Options, overrides map[string]int64, fail func(string, error)) {
	for i, c := range convs {
		source := filepath.Join(src, "conversations", c.ID+".db")
		if unchanged[i] {
			if err := d.propagateResolvedRoot(c); err != nil {
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
		if err := d.IngestAgyConversationWithOverrides(src, c, source, tmp, o, overrides); err != nil {
			fail(source, err)
		}
	}
}

func (d Deps) propagateResolvedRoot(c AgyConv) error {
	if c.Root == "" || (d.IsAgyWorktreePath != nil && d.IsAgyWorktreePath(c.Root)) {
		return nil
	}
	repoID := d.resolveRepoID(c.Root)
	_, err := d.DB.Exec(`UPDATE sessions SET
		project_root = CASE
			WHEN project_root IS NULL OR project_root = '' OR project_root LIKE '%/.gemini/antigravity-cli/worktrees/%'
			THEN ? ELSE project_root END,
		repo_id = CASE
			WHEN ? != '' AND (repo_id = '' OR project_root IS NULL OR project_root = '' OR project_root LIKE '%/.gemini/antigravity-cli/worktrees/%')
			THEN ? ELSE repo_id END
		WHERE id = ? AND (repo_id = '' OR project_root IS NULL OR project_root = '' OR project_root LIKE '%/.gemini/antigravity-cli/worktrees/%')`,
		c.Root, repoID, repoID, c.ID)
	return err
}

func (d Deps) agyConversations(src, tmp string, exclude []string) ([]AgyConv, error) {
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
	var out []AgyConv
	for rows.Next() {
		var c AgyConv
		var modified any
		var uris, title, preview string
		if err := rows.Scan(&c.ID, &modified, &uris, &c.Agent, &c.Parent, &title, &preview); err != nil {
			return nil, err
		}
		if c.ID == "" || strings.ContainsAny(c.ID, `/\`) || c.ID == "." || c.ID == ".." {
			continue
		}
		if t, ok := search.AgyTime(modified); ok {
			c.Modified = t.UnixMilli()
		}
		c.Root = firstWorkspace(uris)
		isProbe := d.IsUsageProbeSummary != nil && d.IsUsageProbeSummary(c.Root, title, preview)
		if (c.Root != "" && harness.InDreamWork(c.Root, exclude)) || isProbe {
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
			switch name {
			case "title":
				hasTitle = true
			case "preview":
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

// IngestAgyConversationWithOverrides ingests a single conversation copy into d.DB.
func (d Deps) IngestAgyConversationWithOverrides(src string, c AgyConv, source, tmp string, o Options, overrides map[string]int64) error {
	db, err := search.OpenCopy(filepath.Join(src, "conversations"), c.ID+".db", tmp)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(filepath.Join(tmp, c.ID+".db"+suffix))
		}
	}()

	steps, gens, err := readAgySteps(db)
	if err != nil {
		return err
	}

	salt := d.salt()
	tx, err := d.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	w := Writer{tx: tx, salt: salt, label: o.Label, outcome: o.Outcome, run: o.Run, resolveRepoID: d.ResolveRepoID}
	w.AgySession(c)
	w.AgyGensWithOverrides(c, gens, w.agySteps(c, steps), overrides)
	w.agyIngestState(source, c.Modified)
	if w.err != nil {
		return w.err
	}
	return tx.Commit()
}

// SweepCopies removes metrics-agy-* directories older than an hour under dir.
func SweepCopies(dir string, now time.Time) {
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

// Fields indexes a protobuf message's fields by path (search.ProtoFields).
type Fields map[string]search.Field

// FieldMap decodes b into a path-indexed field map.
func FieldMap(b []byte) Fields {
	m := Fields{}
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

// Varint reads the varint field at path p, or 0 when absent.
func (m Fields) Varint(p ...int) uint64 {
	if f, ok := m[pathKey(p...)]; ok && f.Wire == 0 {
		return f.Varint
	}
	return 0
}

// Bytes reads the length-delimited field at path p, or nil when absent.
func (m Fields) Bytes(p ...int) []byte {
	if f, ok := m[pathKey(p...)]; ok && f.Wire == 2 {
		return f.Bytes
	}
	return nil
}

// Text reads the UTF-8 string field at path p.
func (m Fields) Text(p ...int) string { return string(m.Bytes(p...)) }

// TimeMS returns Unix milliseconds for a protobuf Timestamp at field n.
func (m Fields) TimeMS(n int) int64 {
	sec := m.Varint(n, 1)
	if sec == 0 {
		return 0
	}
	return int64(sec)*1000 + int64(m.Varint(n, 2))/1_000_000
}
