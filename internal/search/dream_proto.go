package search

// The decoder of an Antigravity step's protobuf payload, which
// conversationText runs on every step it reads.

import (
	"encoding/binary"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// maxDepth bounds the nesting the decoder follows.
const maxDepth = 32

// proseLeaves decodes one step's protobuf payload by its wire format, with no
// schema, and returns its prose: every length-delimited field that is
// printable UTF-8 with three spaces or more, except JSON, which is a tool
// call's arguments (a command, a file written) and goes whole. A field that
// is not text is tried as a nested message. Decoding replaces a scan for byte
// runs, which leaked argument pieces glued to a printable length byte or split
// at a multi-byte character (security review of the release range, D30).
func proseLeaves(msg []byte) []string {
	var out []string
	walk(msg, 0, &out)
	return out
}

// walk appends msg's prose leaves to out and reports whether msg parsed as a
// whole message.
func walk(msg []byte, depth int, out *[]string) bool {
	if depth > maxDepth {
		return false
	}
	var leaves []string
	for len(msg) > 0 {
		key, n := binary.Uvarint(msg)
		if n <= 0 || key>>3 == 0 {
			return false
		}
		var ok bool
		if key&7 == 2 { // length-delimited: text, or a nested message
			msg, ok = lengthDelimited(msg[n:], depth, &leaves)
		} else {
			msg, ok = skipScalar(msg[n:], key&7)
		}
		if !ok {
			return false
		}
	}
	*out = append(*out, leaves...)
	return true
}

// skipScalar steps over one varint, 64-bit or 32-bit value at the front of
// msg; any other wire type (groups, unknown types) is not a message here.
func skipScalar(msg []byte, wire uint64) ([]byte, bool) {
	switch wire {
	case 0: // varint
		_, n := binary.Uvarint(msg)
		if n <= 0 {
			return nil, false
		}
		return msg[n:], true
	case 1: // 64-bit
		if len(msg) < 8 {
			return nil, false
		}
		return msg[8:], true
	case 5: // 32-bit
		if len(msg) < 4 {
			return nil, false
		}
		return msg[4:], true
	}
	return nil, false
}

// lengthDelimited takes the length-delimited field at the front of msg,
// appends its prose to leaves, and returns the rest of msg.
func lengthDelimited(msg []byte, depth int, leaves *[]string) ([]byte, bool) {
	l, n := binary.Uvarint(msg)
	if n <= 0 || l > uint64(len(msg)-n) {
		return nil, false
	}
	field := msg[n : n+int(l)]
	// A field that parses as a whole message IS one: tried as text
	// first, a string-only sub-message (a tool call's arguments)
	// passed as prose with its key and length bytes glued in front,
	// which the JSON guard cannot see (audit 2026-09-30, SD1).
	var nested []string
	if walk(field, depth+1, &nested) {
		*leaves = append(*leaves, nested...)
	} else if text, ok := textField(field); ok && prose(text) {
		*leaves = append(*leaves, text)
	}
	return msg[n+int(l):], true
}

// textField is field as text when it is valid UTF-8 with no control
// character other than a newline or a tab.
func textField(field []byte) (string, bool) {
	if len(field) == 0 || !utf8.Valid(field) {
		return "", false
	}
	for _, r := range string(field) {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return "", false
		}
	}
	return string(field), true
}

// prose is a text leaf worth sending: three spaces or more, and not JSON.
func prose(text string) bool {
	t := strings.TrimSpace(text)
	if strings.Count(t, " ") < 3 || len(t) < 20 {
		return false
	}
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") || json.Valid([]byte(t)) {
		return false
	}
	return true
}
