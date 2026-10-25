package metrics

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

// agyTools maps Antigravity's tool names to the store's vocabulary, the
// names the summary counts by (Bash, Read, Grep; spec § Shared tool
// vocabulary), which the historical rows share. An unmapped name is kept as
// agy:<name>.
var agyTools = map[string]string{
	"run_command": "Bash", "view_file": "Read", "write_to_file": "Write",
	"replace_file_content": "Edit", "multi_replace_file_content": "Edit",
	"grep_search": "Grep", "find_by_name": "Glob", "list_dir": "LS",
	"invoke_subagent": "Agent", "send_message": "SendMessage",
}

// Antigravity step types and statuses (plan D35).
const (
	agyUser   = 14
	agyReply  = 15
	agyTool   = 132
	agyFailed = 7
)

// skillPath is a read of an installed skill's SKILL.md; only the name is kept.
var skillPath = regexp.MustCompile(`/skills/([A-Za-z0-9_.-]+)/SKILL\.md$`)

// agyArgs is the only part of a tool call's arguments read, in memory: a
// command line (normalised, hashed) and a path (classified). D28.
type agyArgs struct {
	CommandLine  string `json:"CommandLine"`
	AbsolutePath string `json:"AbsolutePath"`
}

type agyConv struct {
	id, root, agent, parent string
	modified                int64
}

// ingestAgy reads every Antigravity conversation that changed since its last
// ingest, from copies: the live databases are never opened (D24 of the dream
// plan; spec § Invariants). step_payload, the tool output, is never read.
func (s *Store) ingestAgy(o Options, r *Result, log io.Writer) {
	src := o.agyRoot()
	if _, err := os.Stat(filepath.Join(src, "conversation_summaries.db")); errors.Is(err, os.ErrNotExist) {
		return
	}
	fail := func(what string, err error) {
		r.Failed = append(r.Failed, what)
		fmt.Fprintln(log, printable(fmt.Sprintf("metrics: %s: %v", what, err)))
	}
	sweepCopies(filepath.Dir(s.path), time.Now())
	tmp, err := os.MkdirTemp(filepath.Dir(s.path), "metrics-agy-")
	if err != nil {
		fail(src, err)
		return
	}
	defer os.RemoveAll(tmp)

	convs, err := agyConversations(src, tmp)
	if err != nil {
		fail(filepath.Join(src, "conversation_summaries.db"), err)
		return
	}
	for _, c := range convs {
		source := filepath.Join(src, "conversations", c.id+".db")
		var mtime int64
		err := s.db.QueryRow(`SELECT mtime FROM ingest_state WHERE source = ?`, source).Scan(&mtime)
		if err == nil && mtime == c.modified {
			continue
		}
		if err := s.ingestAgyConversation(src, c, source, tmp, o); err != nil {
			fail(source, err)
		}
	}
}

func agyConversations(src, tmp string) ([]agyConv, error) {
	db, err := search.OpenCopy(src, "conversation_summaries.db", tmp)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT conversation_id, last_modified_time, workspace_uris, agent_name, parent_conversation_id FROM conversation_summaries`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agyConv
	for rows.Next() {
		var c agyConv
		var modified any
		var uris string
		if err := rows.Scan(&c.id, &modified, &uris, &c.agent, &c.parent); err != nil {
			return nil, err
		}
		if c.id == "" || strings.ContainsAny(c.id, `/\`) || c.id == "." || c.id == ".." {
			continue
		}
		if t, ok := search.AgyTime(modified); ok {
			c.modified = t.UnixMilli()
		}
		c.root = firstWorkspace(uris)
		out = append(out, c)
	}
	return out, rows.Err()
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

	type step struct {
		idx, typ, status int
		meta             []byte
	}
	var steps []step
	rows, err := db.Query(`SELECT idx, step_type, status, metadata FROM steps ORDER BY idx`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var st step
		if err := rows.Scan(&st.idx, &st.typ, &st.status, &st.meta); err != nil {
			rows.Close()
			return err
		}
		steps = append(steps, st)
	}
	rows.Close()
	var gens [][]byte
	if rows, err := db.Query(`SELECT data FROM gen_metadata ORDER BY idx`); err == nil {
		for rows.Next() {
			var b []byte
			if rows.Scan(&b) == nil {
				gens = append(gens, b)
			}
		}
		rows.Close()
	}

	salt := s.salt()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	w := writer{tx: tx, salt: salt, label: o.Label, outcome: o.Outcome}
	w.exec(`INSERT INTO sessions(id, vendor, project_root, parent_id, agent_type, label, outcome, ended_at)
		VALUES (?, 'agy', NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, 0))
		ON CONFLICT(id) DO UPDATE SET
			project_root = COALESCE(sessions.project_root, excluded.project_root),
			parent_id = COALESCE(sessions.parent_id, excluded.parent_id),
			agent_type = COALESCE(sessions.agent_type, excluded.agent_type),
			label = COALESCE(sessions.label, excluded.label),
			outcome = COALESCE(sessions.outcome, excluded.outcome),
			ended_at = MAX(COALESCE(sessions.ended_at, 0), COALESCE(excluded.ended_at, 0))`,
		c.id, c.root, c.parent, identifier(c.agent), w.label, w.outcome, c.modified)

	var replyTimes []int64
	for _, st := range steps {
		f := fieldMap(st.meta)
		ts := int64(f.varint(1, 1)) * 1000
		ext := fmt.Sprintf("%s:%d", c.id, st.idx)
		switch st.typ {
		case agyUser:
			w.step(c.id, ext, ts, "user")
		case agyReply:
			w.step(c.id, ext, ts, "assistant")
			replyTimes = append(replyTimes, ts)
		case agyTool:
			w.agyTool(c, ext, ts, st.status == agyFailed, f)
		default:
			continue
		}
		if ts > 0 {
			w.exec(`UPDATE sessions SET started_at = MIN(COALESCE(started_at, ?), ?), ended_at = MAX(COALESCE(ended_at, ?), ?) WHERE id = ?`,
				ts, ts, ts, ts, c.id)
		}
	}
	for i, g := range gens {
		f := fieldMap(g)
		var ts any
		if i < len(replyTimes) && replyTimes[i] > 0 {
			ts = replyTimes[i]
		}
		var ctx any
		if used := f.varint(1, 9, 10, 1); used > 0 {
			ctx = used
		}
		w.exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tok_in, tok_out, cache_read, context)
			VALUES (?, ?, ?, 'generation', ?, ?, ?, ?) ON CONFLICT(session_id, ext_id) DO UPDATE SET
				ts = excluded.ts, tok_in = excluded.tok_in, tok_out = excluded.tok_out,
				cache_read = excluded.cache_read, context = excluded.context`,
			c.id, fmt.Sprintf("%s:gen:%d", c.id, i), ts, f.varint(1, 4, 2), f.varint(1, 4, 3), f.varint(1, 4, 5), ctx)
		if m := identifier(f.text(1, 19)); m != "" {
			w.exec(`UPDATE sessions SET model = COALESCE(model, ?) WHERE id = ?`, m, c.id)
		}
	}
	fi, _ := os.Stat(source)
	var size int64
	if fi != nil {
		size = fi.Size()
	}
	w.exec(`INSERT INTO ingest_state(source, size, mtime, offset) VALUES (?, ?, ?, 0)
		ON CONFLICT(source) DO UPDATE SET size = excluded.size, mtime = excluded.mtime`, source, size, c.modified)
	if w.err != nil {
		return w.err
	}
	return tx.Commit()
}

// agyTool stores one tool step's shape: the common tool name, and from its
// arguments only a command's verbs and hash, a path's trail kind, a record
// verb's kind (recordRead) and a skill's name.
func (w *writer) agyTool(c agyConv, ext string, ts int64, failed bool, f fields) {
	name := f.text(4, 2)
	tool := agyTools[name]
	if tool == "" && name != "" {
		tool = toolName("agy:" + name)
	}
	var args agyArgs
	json.Unmarshal(f.bytes(4, 3), &args)
	var skill, kind, hash any
	if args.AbsolutePath != "" {
		if m := skillPath.FindStringSubmatch(filepath.ToSlash(args.AbsolutePath)); m != nil && tool == "Read" {
			if id := identifier(m[1]); id != "" {
				skill = id
			}
		}
		if tool == "Read" || tool == "Write" || tool == "Edit" {
			kind = w.targetKind(c.root, args.AbsolutePath)
		}
	}
	if tool == "Bash" && args.CommandLine != "" {
		hash = w.hash(args.CommandLine)
		if k := recordRead(args.CommandLine); k != "" {
			kind = k
		}
	}
	// A conversation is re-read whole when it changes: a step first seen
	// while running gets its final status and time (review 2026-10-08).
	w.exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, skill, target_kind, is_error, cmd_hash)
		VALUES (?, ?, ?, 'tool', ?, ?, ?, ?, ?) ON CONFLICT(session_id, ext_id) DO UPDATE SET
			ts = excluded.ts, tool = excluded.tool, skill = excluded.skill, target_kind = excluded.target_kind,
			is_error = excluded.is_error, cmd_hash = excluded.cmd_hash`,
		c.id, ext, ts, tool, skill, kind, boolInt(failed), hash)
	if tool == "Bash" {
		for n, v := range Verb(args.CommandLine) {
			w.exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, c.id, ext, n, v)
		}
	}
}

// recordRead is the trail kind a command line reads through the record
// verbs, for first reads (C3, plan D8, D16): the kind of the ID `fyr record
// get` names, `record` when its prefix is unknown or no ID follows, `list`
// for `fyr record list`, and "" for anything else. Only the first segment
// whose program is fyr (or `go run ./cmd/fyr`) is read; nothing but the kind
// leaves this function (R4).
func recordRead(cmd string) string {
	for _, seg := range segments(stripHeredocs(cmd)) {
		i := programAt(seg)
		if i < 0 {
			continue
		}
		words := seg[i:]
		switch {
		case path.Base(words[0]) == "fyr":
			words = words[1:]
		case words[0] == "go" && len(words) >= 3 && words[1] == "run" && path.Clean(words[2]) == "cmd/fyr":
			words = words[3:]
		default:
			continue
		}
		words = recordWords(words)
		if len(words) == 0 || words[0] != "record" {
			return ""
		}
		words = recordWords(words[1:])
		if len(words) == 0 {
			return ""
		}
		switch words[0] {
		case "list":
			return "list"
		case "get":
			if ids := recordWords(words[1:]); len(ids) > 0 {
				if k := brain.KindOfID(ids[0]); k != "" {
					return k
				}
			}
			return "record"
		}
		return ""
	}
	return ""
}

// recordWords drops the flags at the front of words: --home and --repo with
// their value when it is not glued on with =, any other flag alone.
func recordWords(words []string) []string {
	for len(words) > 0 && strings.HasPrefix(words[0], "-") {
		name, _, glued := strings.Cut(strings.TrimLeft(words[0], "-"), "=")
		words = words[1:]
		if (name == "home" || name == "repo") && !glued && len(words) > 0 {
			words = words[1:]
		}
	}
	return words
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
