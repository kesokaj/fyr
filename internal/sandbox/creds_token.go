package sandbox

import (
	"bytes"
	"crypto/sha256"
)

const maxPrivBodyHash = 512

func recordWindowCreds(b []byte, safeEnd int, kinds, unparsed map[string]int, digests map[string]map[[32]byte]int) {
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
			if loc[0] < safeEnd {
				tok, ok := canonicalCredToken(c.kind, b, loc[0], loc[1])
				record(c.kind, tok, ok)
			}
		}
	}
	for _, loc := range credAWS.FindAllIndex(b, -1) {
		if loc[0] < safeEnd {
			m := b[loc[0]:loc[1]]
			if !bytes.Equal(m, credExample) {
				record("aws-key", m, true)
			}
		}
	}
}

// canonicalCredToken returns a canonical token slice for cross-file baseline
// digesting when a full secret value or PEM body (or benign Go struct tag /
// quoted header literal) can be extracted; otherwise ok is false so bare
// markers remain per-file count-bounded and never enter cross-file known[k]
// (BUG-542).
func canonicalCredToken(kind string, b []byte, start, end int) ([]byte, bool) {
	switch kind {
	case "refresh-token":
		tail := b[end:min(len(b), end+300)]
		if m := credRefreshVal.FindSubmatch(tail); m != nil {
			if len(bytes.Trim(bytes.TrimSpace(m[1]), `"'`)) > 0 {
				return append(append([]byte("refresh-token:"), m[1]...), 0), true
			}
			return nil, false
		}
		if start >= 5 && string(b[start-5:start]) == "json:" && end < len(b) && b[end] == '`' {
			return []byte("refresh-token-struct-tag"), true
		}
		return nil, false
	case "private-key":
		tail := b[end:min(len(b), end+4096)]
		if body, ok := extractPrivKeyBody(tail); ok {
			out := make([]byte, 0, 17+(end-start)+1+len(body))
			out = append(out, "private-key-body:"...)
			out = append(out, b[start:end]...)
			out = append(out, ':')
			return append(out, body...), true
		}
		if lit, ok := quotedHeaderLiteral(b, start, end); ok {
			return append([]byte("private-key-literal:"), lit...), true
		}
		return nil, false
	}
	return b[start:end], true
}

// extractPrivKeyBody parses up to maxPrivBodyHash base64 body characters after
// a PEM private-key header, normalizing JSONL-escaped newlines and line-number
// prefixes and skipping RFC 1421 header lines up to -----END (BUG-542).
func extractPrivKeyBody(tail []byte) ([]byte, bool) {
	norm := normalizePEMNewlines(tail)
	hasEnd := false
	if idx := bytes.Index(norm, []byte("-----END")); idx >= 0 {
		norm = norm[:idx]
		hasEnd = true
	}
	body := make([]byte, 0, maxPrivBodyHash)
	for _, rawLine := range bytes.Split(norm, []byte{'\n'}) {
		line := stripLineNumPrefix(bytes.TrimSpace(rawLine))
		if len(line) == 0 || isPEMHeaderLine(line) {
			continue
		}
		if !isBase64Line(line) {
			break
		}
		rem := maxPrivBodyHash - len(body)
		if len(line) > rem {
			line = line[:rem]
		}
		body = append(body, line...)
		if len(body) >= maxPrivBodyHash {
			break
		}
	}
	if (hasEnd && len(body) >= 16) || len(body) >= 64 {
		return body, true
	}
	return nil, false
}

func normalizePEMNewlines(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == '\r' {
			out = append(out, '\n')
			continue
		}
		if b[i] == '\\' && i+1 < len(b) && (b[i+1] == 'n' || b[i+1] == 'r') {
			out = append(out, '\n')
			i++
			continue
		}
		out = append(out, b[i])
	}
	return out
}

func stripLineNumPrefix(line []byte) []byte {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(line) || line[i] != ':' {
		return line
	}
	i++
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
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

func isBase64Line(line []byte) bool {
	for _, c := range line {
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '='
		if !ok {
			return false
		}
	}
	return len(line) > 0
}

// quotedHeaderLiteral matches a header-only test string literal enclosed in
// double quotes on a single source line (for example `"-----BEGIN ...-----"` or
// `"...-----BEGIN ...-----\n"`) with no PEM body following it.
func quotedHeaderLiteral(b []byte, start, end int) ([]byte, bool) {
	after := end
	for after+1 < len(b) && b[after] == '\\' && (b[after+1] == 'n' || b[after+1] == 'r') {
		after += 2
	}
	if after >= len(b) || b[after] != '"' {
		return nil, false
	}
	ls := start
	for ls > 0 && b[ls-1] != '\n' && b[ls-1] != '\r' && b[ls-1] != 0 {
		ls--
	}
	if !bytes.ContainsRune(b[ls:start], '"') {
		return nil, false
	}
	le := after + 1
	for le < len(b) && b[le] != '\n' && b[le] != '\r' && b[le] != 0 && le-ls < 256 {
		le++
	}
	return stripLineNumPrefix(bytes.TrimSpace(b[ls:le])), true
}
