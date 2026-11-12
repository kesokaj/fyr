package sandbox

import (
	"bytes"
	"crypto/sha256"
)

const maxPrivBodyHash = 2048

func recordWindowCreds(b []byte, safeEnd int, kinds, unparsed map[string]int, digests map[string]map[[32]byte]int) {
	recordWindowCredsSkip(b, safeEnd, nil, kinds, unparsed, digests)
}

func recordWindowCredsSkip(b []byte, safeEnd int, skip [][2]int, kinds, unparsed map[string]int, digests map[string]map[[32]byte]int) {
	record := func(kind string, tok []byte, ok bool) {
		kinds[kind]++
		if !ok {
			unparsed[kind]++
			return
		}
		if digests[kind] == nil {
			digests[kind] = map[[32]byte]int{}
		}
		digests[kind][sha256.Sum256(tok)]++
	}
	for _, c := range credText {
		for _, loc := range c.re.FindAllIndex(b, -1) {
			if loc[0] < safeEnd && !inCoveredSpan(loc[0], skip) {
				tok, ok := canonicalCredToken(c.kind, b, loc[0], loc[1])
				record(c.kind, tok, ok)
			}
		}
	}
	for _, loc := range credAWS.FindAllIndex(b, -1) {
		if loc[0] < safeEnd && !inCoveredSpan(loc[0], skip) {
			m := b[loc[0]:loc[1]]
			if !bytes.Equal(m, credExample) {
				record("aws-key", m, true)
			}
		}
	}
}

// canonicalCredToken returns a canonical token slice for cross-file baseline
// digesting when a full secret value, PEM body/span, or normalized full-line
// source/prose context can be extracted; otherwise ok is false so bare markers
// remain per-file count-bounded and never enter cross-file known[k] (BUG-542, BUG-544).
func canonicalCredToken(kind string, b []byte, start, end int) ([]byte, bool) {
	switch kind {
	case "refresh-token":
		return canonicalRefreshToken(b, start, end)
	case "private-key":
		return canonicalPrivKeyToken(b, start, end)
	}
	return b[start:end], true
}

func canonicalRefreshToken(b []byte, start, end int) ([]byte, bool) {
	tail := b[end:min(len(b), end+300)]
	if m := credRefreshVal.FindSubmatch(tail); m != nil {
		if len(bytes.Trim(bytes.TrimSpace(m[1]), "\"'`")) > 0 {
			return append(append([]byte("refresh-token:"), m[1]...), 0), true
		}
		return nil, false
	}
	if line, afterEnd, ok := peelLogicalLine(b, start, end, true); ok && !followedByAssign(afterEnd) {
		return prefixToken("refresh-token-line:", b[start:end], line), true
	}
	return nil, false
}

func followedByAssign(afterEnd []byte) bool {
	norm := bytes.TrimSpace(normalizeSpan(afterEnd[:min(len(afterEnd), 128)]))
	return len(norm) > 0 && (norm[0] == ':' || norm[0] == '=')
}

func canonicalPrivKeyToken(b []byte, start, end int) ([]byte, bool) {
	hdr := b[start:end]
	endTag := append([]byte("-----END "), hdr[11:]...)
	tail := b[end:min(len(b), end+4096)]
	if next := credPrivBegin.FindIndex(tail); next != nil {
		tail = tail[:next[0]]
	}
	tail = tail[:beforeBinaryControl(tail)]
	if body, ok := extractPrivKeyBody(tail, endTag); ok {
		return prefixToken("private-key-body:", hdr, body), true
	}
	if span, ok := extractPrivKeySpan(tail, endTag); ok {
		return prefixToken("private-key-span:", hdr, span), true
	}
	if line, afterEnd, ok := peelLogicalLine(b, start, end, false); ok {
		if next := credPrivBegin.FindIndex(afterEnd); next != nil {
			afterEnd = afterEnd[:next[0]]
		}
		afterEnd = afterEnd[:beforeBinaryControl(afterEnd)]
		if !followedByKeyContinuation(afterEnd) {
			return prefixToken("private-key-line:", hdr, line), true
		}
	}
	return nil, false
}

func prefixToken(tag string, hdr, body []byte) []byte {
	out := make([]byte, 0, len(tag)+len(hdr)+1+len(body))
	return append(append(append(out, tag...), hdr...), append([]byte{':'}, body...)...)
}

func extractPrivKeyBody(tail, endTag []byte) ([]byte, bool) {
	norm := normalizeSpan(tail)
	completeEnd := false
	if idx := bytes.Index(norm, endTag); idx >= 0 {
		norm, completeEnd = norm[:idx], true
	}
	body := make([]byte, 0, maxPrivBodyHash)
	for _, rawLine := range bytes.Split(norm, []byte{'\n'}) {
		line := stripLineNumPrefix(bytes.TrimSpace(rawLine))
		if len(line) == 0 || isPEMHeaderLine(line) {
			continue
		}
		if !isBase64Line(line) {
			completeEnd = false
			break
		}
		body = append(body, line[:min(len(line), maxPrivBodyHash-len(body))]...)
		if len(body) >= maxPrivBodyHash {
			break
		}
	}
	return body, (completeEnd && len(body) >= 4) || len(body) >= 64
}

func extractPrivKeySpan(tail, endTag []byte) ([]byte, bool) {
	idx := bytes.Index(tail, endTag)
	if idx < 0 {
		return nil, false
	}
	var lines [][]byte
	for _, rawLine := range bytes.Split(normalizeSpan(tail[:idx]), []byte{'\n'}) {
		if line := stripLineNumPrefix(bytes.TrimSpace(rawLine)); len(line) > 0 {
			lines = append(lines, line)
		}
	}
	joined := bytes.Join(lines, []byte{'\n'})
	return joined[:min(len(joined), maxPrivBodyHash)], len(joined) > 0
}

func normalizeSpan(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == '\r' {
			if i+1 < len(b) && b[i+1] == '\n' {
				i++
			}
			out = append(out, '\n')
			continue
		}
		if b[i] != '\\' {
			out = append(out, b[i])
			continue
		}
		out, i = consumeEscapeRun(out, b, i)
	}
	return out
}

func consumeEscapeRun(out, b []byte, i int) ([]byte, int) {
	j := i
	for j < len(b) && b[j] == '\\' {
		j++
	}
	run := j - i
	if j >= len(b) {
		return append(out, '\\'), j - 1
	}
	switch b[j] {
	case 'r':
		k := j + 1
		for k < len(b) && k-(j+1) < run && b[k] == '\\' {
			k++
		}
		if k-(j+1) == run && k < len(b) && b[k] == 'n' {
			return append(out, '\n'), k
		}
		return append(out, '\n'), j
	case 'n':
		return append(out, '\n'), j
	case 't':
		return append(out, '\t'), j
	case '"':
		return append(out, '"'), j
	case 'u':
		if dec, step, ok := decodeUnicodeEscape(b, j); ok {
			return append(out, dec...), j + step
		}
		return append(out, '\\'), j - 1
	default:
		return append(out, '\\'), j - 1
	}
}

func followedByKeyContinuation(afterEnd []byte) bool {
	lines := bytes.Split(normalizeSpan(afterEnd[:min(len(afterEnd), 300)]), []byte{'\n'})
	checked := 0
	for _, rawLine := range lines {
		for _, p := range splitContParts(rawLine) {
			cand := cleanContToken(p)
			if len(cand) == 0 || isPEMHeaderLine(cand) {
				continue
			}
			if isKeyBodyToken(cand) {
				return true
			}
			if checked++; checked >= 8 {
				return false
			}
		}
	}
	return false
}

func splitContParts(line []byte) [][]byte {
	var parts [][]byte
	start := 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		isConcat := c == '+' && (i == 0 || !isBase64Byte(line[i-1]) || i+1 >= len(line) || !isBase64Byte(line[i+1]))
		if c == ',' || c == '"' || c == '\'' || isConcat {
			parts = append(parts, line[start:i])
			start = i + 1
		}
	}
	return append(parts, line[start:])
}

func cleanContToken(p []byte) []byte {
	s := stripLineNumPrefix(bytes.TrimSpace(p))
	return bytes.Trim(s, " \t,:;+-\\[]{}()\"'`")
}

func isKeyBodyToken(cand []byte) bool {
	if len(cand) >= 12 && isBase64Line(cand) {
		return true
	}
	if idx := bytes.IndexAny(cand, ":="); idx >= 0 {
		val := cleanContToken(cand[idx+1:])
		if len(val) >= 12 && isBase64Line(val) {
			return true
		}
	}
	return false
}

func beforeBinaryControl(b []byte) int {
	for i, c := range b {
		if isLineBreakOrBin(c) && c != '\n' && c != '\r' {
			return i
		}
	}
	return len(b)
}

func isLineBreakOrBin(c byte) bool {
	return (c < 0x20 && c != '\t') || c == 0x7f
}

func stripLineNumPrefix(line []byte) []byte {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(line) || line[i] != ':' {
		return line
	}
	for i++; i < len(line) && (line[i] == ' ' || line[i] == '\t'); i++ {
	}
	return line[i:]
}

func isPEMHeaderLine(line []byte) bool {
	colon := bytes.IndexByte(line, ':')
	if colon <= 0 || colon+1 >= len(line) || (line[colon+1] != ' ' && line[colon+1] != '\t') {
		return false
	}
	for i := 0; i < colon; i++ {
		c := line[i]
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9' && i > 0) || ((c == '-' || c == '_') && i > 0)
		if !ok {
			return false
		}
	}
	return true
}

func isBase64Byte(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '='
}

func isBase64Line(line []byte) bool {
	for _, c := range line {
		if !isBase64Byte(c) {
			return false
		}
	}
	return len(line) > 0
}
