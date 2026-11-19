package redact

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type jsonFrame struct {
	delim     byte
	expectKey bool
	key       string
}

// decoded extracts every unescaped string key, string value, and key:value
// pair from a JSONL line using a streaming Token() walker. It recovers from
// leading non-JSON bytes, BOM, syntax errors, and truncated trailing strings
// and never overwrites duplicate keys.
func decoded(line string) string {
	var b strings.Builder
	pendingKey := ""
	for s := line; len(s) > 0; {
		adv, done := walkJSONChunk(s, &b, &pendingKey)
		if done || adv <= 0 || adv >= len(s) {
			break
		}
		s = s[adv:]
	}
	return b.String()
}

func walkJSONChunk(s string, b *strings.Builder, pendingKey *string) (int, bool) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var frames []jsonFrame
	lastStr := ""
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return len(s), true
		}
		off := min(max(int(dec.InputOffset()), 0), len(s))
		if err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				emitTruncatedJSONString(s[off:], frames, *pendingKey, b)
				return len(s), true
			}
			return recoverJSONError(s, off, err, lastStr, pendingKey), false
		}
		lastStr = handleJSONToken(tok, &frames, pendingKey, b)
	}
}

func handleJSONToken(tok any, frames *[]jsonFrame, pendingKey *string, b *strings.Builder) string {
	switch v := tok.(type) {
	case json.Delim:
		*pendingKey = ""
		updateJSONFrames(byte(v), frames)
		return ""
	case string:
		b.WriteString(v)
		b.WriteString(",\n")
		if len(*frames) > 0 && (*frames)[len(*frames)-1].delim == '{' {
			top := &(*frames)[len(*frames)-1]
			if top.expectKey {
				top.key = v
				top.expectKey = false
				return v
			}
			emitStringPair(b, top.key, v)
			top.expectKey, top.key = true, ""
			return v
		}
		if *pendingKey != "" {
			emitStringPair(b, *pendingKey, v)
			*pendingKey = ""
		}
		return v
	default:
		emitScalarToken(v, *frames, pendingKey, b)
		return ""
	}
}

func updateJSONFrames(d byte, frames *[]jsonFrame) {
	switch d {
	case '{':
		*frames = append(*frames, jsonFrame{delim: '{', expectKey: true})
	case '[':
		*frames = append(*frames, jsonFrame{delim: '['})
	case '}', ']':
		if len(*frames) > 0 {
			*frames = (*frames)[:len(*frames)-1]
		}
		if len(*frames) > 0 && (*frames)[len(*frames)-1].delim == '{' {
			(*frames)[len(*frames)-1].expectKey = true
			(*frames)[len(*frames)-1].key = ""
		}
	}
}

func emitScalarToken(v any, frames []jsonFrame, pendingKey *string, b *strings.Builder) {
	val := "null"
	if v != nil {
		val = fmt.Sprint(v)
	}
	if len(frames) > 0 && frames[len(frames)-1].delim == '{' {
		top := &frames[len(frames)-1]
		if !top.expectKey && top.key != "" {
			emitRawPair(b, top.key, val)
		}
		top.expectKey, top.key = true, ""
		return
	}
	if *pendingKey != "" {
		emitRawPair(b, *pendingKey, val)
		*pendingKey = ""
	}
}

func emitStringPair(b *strings.Builder, key, val string) {
	if key == "" {
		return
	}
	b.WriteByte('"')
	b.WriteString(strings.ReplaceAll(key, `"`, `'`))
	b.WriteString(`":"`)
	b.WriteString(strings.ReplaceAll(val, `"`, `'`))
	b.WriteString("\",\n")
}

func emitRawPair(b *strings.Builder, key, val string) {
	if key == "" {
		return
	}
	b.WriteByte('"')
	b.WriteString(strings.ReplaceAll(key, `"`, `'`))
	b.WriteString(`":`)
	b.WriteString(val)
	b.WriteString(",\n")
}

func emitTruncatedJSONString(tail string, frames []jsonFrame, pendingKey string, b *strings.Builder) {
	tail = strings.TrimLeft(tail, " \t\r\n:,")
	if !strings.HasPrefix(tail, `"`) {
		return
	}
	s, ok := unquoteTruncatedJSONString(tail)
	if !ok {
		return
	}
	b.WriteString(s)
	b.WriteString(",\n")
	key := pendingKey
	if len(frames) > 0 && frames[len(frames)-1].delim == '{' && !frames[len(frames)-1].expectKey {
		key = frames[len(frames)-1].key
	}
	emitStringPair(b, key, s)
}

func unquoteTruncatedJSONString(tail string) (string, bool) {
	var s string
	if json.Unmarshal([]byte(tail+`"`), &s) == nil {
		return s, true
	}
	candidates := []string{strings.TrimSuffix(tail, `\`)}
	if slash := strings.LastIndexByte(tail, '\\'); slash > 0 && slash >= len(tail)-6 {
		candidates = append(candidates, tail[:slash])
	}
	for _, c := range candidates {
		if json.Unmarshal([]byte(c+`"`), &s) == nil {
			return s, true
		}
	}
	return "", false
}

func recoverJSONError(s string, off int, err error, lastStr string, pendingKey *string) int {
	if lastStr != "" && strings.HasPrefix(strings.TrimLeft(s[off:], " \t"), ":") {
		*pendingKey = lastStr
	} else {
		*pendingKey = ""
	}
	pos := off + 1
	var se *json.SyntaxError
	if errors.As(err, &se) && int(se.Offset) > pos {
		pos = int(se.Offset)
	}
	if pos >= len(s) {
		return len(s)
	}
	idx := strings.IndexAny(s[pos:], "{[\"")
	if idx < 0 {
		return len(s)
	}
	return pos + idx
}
