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
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

// Antigravity step types and statuses (plan D35; Item 7).
const (
	agyUser      = 14
	agyReply     = 15
	agyTool      = 132
	agyCancelled = 6
	agyFailed    = 7
	agyWaiting   = 9
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

	convs, err := agyConversations(src, tmp, o.dreamWork())
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

// agyConversations lists the conversations in src's summaries, without the
// dream's own: one whose first workspace is in exclude is no project's
// session and gets no ingest_state row (visible dream plan D5).
func agyConversations(src, tmp string, exclude []string) ([]agyConv, error) {
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
		if c.root != "" && harness.InDreamWork(c.root, exclude) {
			continue
		}
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
	w.agyGens(c, gens, w.agySteps(c, steps))
	w.agyIngestState(source, c.modified)
	if w.err != nil {
		return w.err
	}
	return tx.Commit()
}

// agyTool stores one tool step's shape: the common tool name, and from its
// arguments only a command's verbs and hash, a path's trail kind, a record
// verb's kind (recordRead) and a skill's name.
func (w *writer) agyTool(c agyConv, ext string, ts int64, failed bool, f fields, started, ended, wait any) {
	name := f.text(4, 2)
	tool := agyTools[name]
	if tool == "" && name != "" {
		tool = toolName("agy:" + name)
	}
	var args agyArgs
	json.Unmarshal(f.bytes(4, 3), &args)
	var skill, kind, hash any
	if p := args.path(); p != "" {
		if m := skillPath.FindStringSubmatch(filepath.ToSlash(p)); m != nil && tool == "Read" {
			if id := identifier(m[1]); id != "" {
				skill = id
			}
		}
		if tool == "Read" || tool == "Write" || tool == "Edit" {
			kind = w.targetKind(c.root, p)
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
	w.exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, skill, target_kind, is_error, cmd_hash, started_ms, ended_ms, wait_ms)
		VALUES (?, ?, ?, 'tool', ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(session_id, ext_id) DO UPDATE SET
			ts = excluded.ts, tool = excluded.tool, skill = excluded.skill, target_kind = excluded.target_kind,
			is_error = excluded.is_error, cmd_hash = excluded.cmd_hash,
			started_ms = excluded.started_ms, ended_ms = excluded.ended_ms, wait_ms = excluded.wait_ms`,
		c.id, ext, ts, tool, skill, kind, boolInt(failed), hash, started, ended, wait)
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

// timeMS is the Unix milliseconds of a protobuf Timestamp at field n
// (seconds at n→1, nanoseconds at n→2), 0 when absent.
func (m fields) timeMS(n int) int64 {
	sec := m.varint(n, 1)
	if sec == 0 {
		return 0
	}
	return int64(sec)*1000 + int64(m.varint(n, 2))/1_000_000
}
