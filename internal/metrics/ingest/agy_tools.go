package ingest

import (
	"encoding/json"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/sqliteutil"
)

// AgyTools maps Antigravity's tool names to the store's vocabulary.
var AgyTools = map[string]string{
	"run_command": "Bash", "view_file": "Read", "write_to_file": "Write",
	"replace_file_content": "Edit", "multi_replace_file_content": "Edit",
	"read_file": "Read", "write_file": "Write", "edit_file": "Edit",
	"grep_search": "Grep", "find_by_name": "Glob", "list_dir": "LS",
	"invoke_subagent": "Agent", "send_message": "SendMessage",
}

// skillPath anchors SKILL.md reads to installed skill roots (v9.10.0 F-043).
var skillPath = regexp.MustCompile(`(?:/\.gemini/config/skills/|/\.agents/skills/|/builtin/skills/)([A-Za-z0-9_.-]+)/SKILL\.md$`)

// AgyArgs holds the subset of tool call arguments inspected in memory.
type AgyArgs struct {
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

func (a AgyArgs) path() string {
	for _, p := range []string{a.AbsolutePath, a.Path, a.TargetFile} {
		if p != "" {
			return p
		}
	}
	return ""
}

func (w *Writer) targetPathHash(root, p, tool string, args AgyArgs) string {
	norm := normalizeTargetPath(root, p)
	h := w.hash(norm)
	if tool == "Read" {
		if start, end, ranged := ExtractReadRange(args); ranged {
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

func resolveAgyToolName(name string) string {
	if tool := AgyTools[name]; tool != "" {
		return tool
	}
	if name == "" {
		return ""
	}
	return ToolName("agy:" + name)
}

func (w *Writer) agyTool(c AgyConv, ext string, ts int64, st agyStep, f Fields, started, ended, wait any) {
	tool := resolveAgyToolName(f.Text(4, 2))
	var args AgyArgs
	_ = json.Unmarshal(f.Bytes(4, 3), &args)
	skill, kind, hash := w.extractToolMeta(c.Root, tool, args)
	denialKind := ClassifyHookDenial(st.status, f, st.meta, st.errDetails)
	denied := denialKind != ""
	isErr := st.status == AgyFailed && !denied
	w.exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, skill, target_kind, is_error, denied, cmd_hash, started_ms, ended_ms, wait_ms)
		VALUES (?, ?, ?, 'tool', ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(session_id, ext_id) DO UPDATE SET
			ts = excluded.ts, tool = excluded.tool, skill = excluded.skill, target_kind = excluded.target_kind,
			is_error = excluded.is_error, denied = excluded.denied, cmd_hash = excluded.cmd_hash,
			started_ms = excluded.started_ms, ended_ms = excluded.ended_ms, wait_ms = excluded.wait_ms`,
		c.ID, ext, ts, tool, skill, kind, sqliteutil.BoolInt(isErr), sqliteutil.BoolInt(denied), hash, started, ended, wait)
	w.syncHookBlockEvent(c.ID, ext, ts, denialKind)
	if tool == "Bash" {
		w.insertBashVerbs(c.ID, ext, args.CommandLine)
	}
}

func (w *Writer) extractToolMeta(root, tool string, args AgyArgs) (skill, kind, hash any) {
	if p := args.path(); p != "" {
		skill = installedSkillName(tool, p)
		if tool == "Read" || tool == "Write" || tool == "Edit" {
			kind = w.targetKind(root, p)
			hash = w.targetPathHash(root, p, tool, args)
		}
	}
	if tool == "Bash" && args.CommandLine != "" {
		hash = w.hash(args.CommandLine)
		if k := RecordRead(args.CommandLine); k != "" {
			kind = k
		}
	}
	return skill, kind, hash
}

func installedSkillName(tool, p string) any {
	if tool != "Read" {
		return nil
	}
	m := skillPath.FindStringSubmatch(filepath.ToSlash(p))
	if m == nil {
		return nil
	}
	if id := Identifier(m[1]); id != "" {
		return id
	}
	return nil
}

func (w *Writer) syncHookBlockEvent(sid, ext string, ts int64, denialKind string) {
	if denialKind != "" {
		w.exec(`INSERT INTO events(session_id, ts, kind, ref, value) VALUES (?, ?, 'hook_block', ?, ?)
			ON CONFLICT(session_id, kind, ref) DO UPDATE SET ts = excluded.ts, value = excluded.value`,
			sid, ts, ext, denialKind)
		return
	}
	w.exec(`DELETE FROM events WHERE session_id = ? AND kind = 'hook_block' AND ref = ?`, sid, ext)
}

func (w *Writer) insertBashVerbs(sid, ext, cmd string) {
	for n, v := range Verb(cmd) {
		w.exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, sid, ext, n, v)
	}
}

// RecordRead returns the trail kind a command line reads through `fyr record`
// verbs, for first reads (C3, plan D8, D16).
func RecordRead(cmd string) string {
	for _, seg := range segments(StripHeredocs(cmd)) {
		words, ok := fyrSubargs(seg)
		if !ok {
			continue
		}
		return recordSubcommandKind(words)
	}
	return ""
}

func fyrSubargs(seg []string) ([]string, bool) {
	i := programAt(seg)
	if i < 0 {
		return nil, false
	}
	words := seg[i:]
	switch {
	case path.Base(words[0]) == "fyr":
		return words[1:], true
	case words[0] == "go" && len(words) >= 3 && words[1] == "run" && path.Clean(words[2]) == "cmd/fyr":
		return words[3:], true
	default:
		return nil, false
	}
}

func recordSubcommandKind(words []string) string {
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
