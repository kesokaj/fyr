package metrics

import (
	"encoding/json"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

// agyTools maps Antigravity's tool names to the store's vocabulary, the
// names the summary counts by (Bash, Read, Grep; spec § Shared tool
// vocabulary), which the historical rows share. An unmapped name is kept as
// agy:<name>. read_file, write_file and edit_file are agy 1.4.7's names
// beside the older ones (BUG-184).
var agyTools = map[string]string{
	"run_command": "Bash", "view_file": "Read", "write_to_file": "Write",
	"replace_file_content": "Edit", "multi_replace_file_content": "Edit",
	"read_file": "Read", "write_file": "Write", "edit_file": "Edit",
	"grep_search": "Grep", "find_by_name": "Glob", "list_dir": "LS",
	"invoke_subagent": "Agent", "send_message": "SendMessage",
}

// skillPath is a read of an installed skill's SKILL.md; only the name is kept.
var skillPath = regexp.MustCompile(`/skills/([A-Za-z0-9_.-]+)/SKILL\.md$`)

// agyArgs is the only part of a tool call's arguments read, in memory: a
// command line (normalised, hashed) and a path (classified). D28. Each file
// tool sends its path under its own key: view_file AbsolutePath, the 1.4.7
// tools Path, write_to_file and replace_file_content TargetFile (BUG-184).
type agyArgs struct {
	CommandLine       string `json:"CommandLine"`
	AbsolutePath      string `json:"AbsolutePath"`
	Path              string `json:"Path"`
	TargetFile        string `json:"TargetFile"`
	StartLine         any    `json:"StartLine"`
	EndLine           any    `json:"EndLine"`
	StartLineLower    any    `json:"start_line"`
	EndLineLower      any    `json:"end_line"`
	StartLineNumber   any    `json:"StartLineNumber"`
	EndLineNumber     any    `json:"EndLineNumber"`
	StartLineCamel    any    `json:"startLine"`
	EndLineCamel      any    `json:"endLine"`
	StartLineNumCamel any    `json:"startLineNumber"`
	EndLineNumCamel   any    `json:"endLineNumber"`
	Offset            any    `json:"Offset"`
	Limit             any    `json:"Limit"`
	OffsetLower       any    `json:"offset"`
	LimitLower        any    `json:"limit"`
	LineCount         any    `json:"LineCount"`
	LineCountCamel    any    `json:"lineCount"`
}

// path is the file the call names, whichever key carried it.
func (a agyArgs) path() string {
	for _, p := range []string{a.AbsolutePath, a.Path, a.TargetFile} {
		if p != "" {
			return p
		}
	}
	return ""
}

func (w *writer) targetPathHash(root, p, tool string, args agyArgs) string {
	norm := normalizeTargetPath(root, p)
	h := w.hash(norm)
	if tool == "Read" {
		if start, end, ranged := extractReadRange(args); ranged {
			return "r:" + h + ":" + strconv.Itoa(start) + "-" + strconv.Itoa(end)
		}
	}
	return "f:" + h
}

func normalizeTargetPath(root, p string) string {
	clean := filepath.Clean(strings.TrimSpace(p))
	if root != "" {
		if rel, err := filepath.Rel(filepath.Clean(root), clean); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return filepath.ToSlash(rel)
		}
	}
	return strings.TrimPrefix(filepath.ToSlash(clean), "./")
}

// agyTool stores one tool step's shape: the common tool name, and from its
// arguments only a command's verbs and hash, a path's trail kind, a record
// verb's kind (recordRead) and a skill's name.
func (w *writer) agyTool(c agyConv, ext string, ts int64, st agyStep, f fields, started, ended, wait any) {
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
			hash = w.targetPathHash(c.root, p, tool, args)
		}
	}
	if tool == "Bash" && args.CommandLine != "" {
		hash = w.hash(args.CommandLine)
		if k := recordRead(args.CommandLine); k != "" {
			kind = k
		}
	}
	denialKind := classifyHookDenial(st.status, f, st.meta, st.errDetails)
	denied := denialKind != ""
	isErr := st.status == agyFailed && !denied
	// A conversation is re-read whole when it changes: a step first seen
	// while running gets its final status and time (review 2026-10-08).
	w.exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, skill, target_kind, is_error, denied, cmd_hash, started_ms, ended_ms, wait_ms)
		VALUES (?, ?, ?, 'tool', ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(session_id, ext_id) DO UPDATE SET
			ts = excluded.ts, tool = excluded.tool, skill = excluded.skill, target_kind = excluded.target_kind,
			is_error = excluded.is_error, denied = excluded.denied, cmd_hash = excluded.cmd_hash,
			started_ms = excluded.started_ms, ended_ms = excluded.ended_ms, wait_ms = excluded.wait_ms`,
		c.id, ext, ts, tool, skill, kind, search.BoolInt(isErr), search.BoolInt(denied), hash, started, ended, wait)
	if denied {
		w.exec(`INSERT INTO events(session_id, ts, kind, ref, value) VALUES (?, ?, 'hook_block', ?, ?)
			ON CONFLICT(session_id, kind, ref) DO UPDATE SET ts = excluded.ts, value = excluded.value`,
			c.id, ts, ext, denialKind)
	} else {
		w.exec(`DELETE FROM events WHERE session_id = ? AND kind = 'hook_block' AND ref = ?`, c.id, ext)
	}
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
