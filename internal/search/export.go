package search

// Exported for internal/metrics and internal/brain, the other packages that
// open a SQLite database (R4, ADR-055, ADR-063): the same open rules, the
// same read-from-a-copy discipline and the same printable output, so a
// security fix lands once.

import (
	"database/sql"
	"encoding/binary"
	"time"
)

// PrepareDBFiles refuses a database or companion file that is not a regular
// file and keeps all three 0600; it creates a missing database 0600.
func PrepareDBFiles(path string) error { return prepareFiles(path) }

// DSN is the escaped read-write DSN with WAL and the busy timeout.
func DSN(path string) string { return dsn(path) }

// Validate is the check Search makes of its options before it opens
// anything: `fyr search` runs it before it prints the record hits, so a usage
// error still prints nothing but its reason (BUG-168).
func Validate(o Options) error { return validate(&o, "search") }

// OpenCopy copies name (and its -wal, -shm) from dir into tmp and opens the
// copy read-only; the live file is never opened.
func OpenCopy(dir, name, tmp string) (*sql.DB, error) { return openCopy(dir, name, tmp) }

// AgyTime reads Antigravity's last_modified_time column, as a time or as its
// stored text.
func AgyTime(v any) (time.Time, bool) { return agyTime(v) }

// Printable replaces what a terminal would act on or hide with '?': control,
// format, line- and paragraph-separator characters, by Unicode class
// (BUG-152).
func Printable(s string) string { return printable(s) }

// CopyDB copies a SQLite database and its -wal and -shm files from dir into
// tmp and returns the copy's path: the dream's dry run reads the brain store
// from such a copy, never the live file.
func CopyDB(dir, name, tmp string) (string, error) { return copyDB(dir, name, tmp) }

// CleanSnippet folds an FTS5 snippet (match marks \x01 and \x02) into one
// printable line of at most 200 characters, the marks shown as »«; the
// brain store's record hits print through it (ADR-063).
func CleanSnippet(s string) string { return cleanSnippet(s) }

// FTSQuery quotes each word as an FTS5 string joined with op (" " for AND,
// " OR "), so no character of a word is query syntax.
func FTSQuery(words []string, op string) string { return ftsQuery(words, op) }

// Unticked counts a plan's unticked boxes (a line starting "- [ ]") outside
// fenced blocks: the rule that makes a plan open in the brain store.
func Unticked(text string) int {
	n := 0
	var f fence
	for _, l := range splitLines(text) {
		if f.step(l) || f.inside() {
			continue
		}
		if unticked.MatchString(l) {
			n++
		}
	}
	return n
}

// KindOf classifies a project-relative, slash-separated path against the
// project's docs root (bug, adr, plan, …, code, doc).
func KindOf(p, docsRoot string) string { return kindOf("", p, docsRoot) }

// Field is one protobuf field found by ProtoFields: its path of field
// numbers from the top message, and its value — Varint for wire type 0,
// Bytes for wire type 2 (also emitted when the bytes parse as a nested
// message, whose own fields follow with the longer path).
type Field struct {
	Path   []int
	Wire   int
	Varint uint64
	Bytes  []byte
}

// ProtoFields decodes msg by its wire format, with no schema, as walk does,
// but keeps field numbers so a reader can pick a field by its path (the
// metrics store's Antigravity tool names and token counts, ADR-055). It
// returns nil when msg is not a whole message.
func ProtoFields(msg []byte) []Field {
	var out []Field
	if !fields(msg, nil, &out) {
		return nil
	}
	return out
}

// fields appends msg's fields under path to out and reports whether msg
// parsed as a whole message; depth is bounded by maxDepth, as in walk.
func fields(msg []byte, path []int, out *[]Field) bool {
	if len(path) > maxDepth {
		return false
	}
	var got []Field
	for len(msg) > 0 {
		key, n := binary.Uvarint(msg)
		if n <= 0 || key>>3 == 0 {
			return false
		}
		msg = msg[n:]
		p := append(append([]int{}, path...), int(key>>3))
		switch key & 7 {
		case 0:
			v, n := binary.Uvarint(msg)
			if n <= 0 {
				return false
			}
			msg = msg[n:]
			got = append(got, Field{Path: p, Wire: 0, Varint: v})
		case 1:
			if len(msg) < 8 {
				return false
			}
			msg = msg[8:]
		case 5:
			if len(msg) < 4 {
				return false
			}
			msg = msg[4:]
		case 2:
			l, n := binary.Uvarint(msg)
			if n <= 0 || l > uint64(len(msg)-n) {
				return false
			}
			b := msg[n : n+int(l)]
			msg = msg[n+int(l):]
			got = append(got, Field{Path: p, Wire: 2, Bytes: b})
			var nested []Field
			if len(b) > 0 && fields(b, p, &nested) {
				got = append(got, nested...)
			}
		default:
			return false
		}
	}
	*out = append(*out, got...)
	return true
}
