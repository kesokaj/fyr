package trail

import (
	"bytes"
	"unicode/utf8"
)

// TruncateUTF8 returns the longest prefix of s that is at most maxBytes bytes
// without splitting a multi-byte UTF-8 rune. Returns "" when maxBytes <= 0.
func TruncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	n := maxBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	if n < maxBytes {
		if r, size := utf8.DecodeRuneInString(s[n:]); (r != utf8.RuneError || size > 1) && n+size <= maxBytes {
			return s[:n+size]
		}
	}
	return s[:n]
}

// TruncateUTF8Bytes returns the longest prefix of b that is at most maxBytes
// bytes without splitting a multi-byte UTF-8 rune. Returns nil when maxBytes <= 0.
// Truncated slices clip capacity so callers appending cannot overwrite b.
func TruncateUTF8Bytes(b []byte, maxBytes int) []byte {
	if maxBytes <= 0 {
		return nil
	}
	if len(b) <= maxBytes {
		return b
	}
	n := maxBytes
	for n > 0 && !utf8.RuneStart(b[n]) {
		n--
	}
	if n < maxBytes {
		if r, size := utf8.DecodeRune(b[n:]); (r != utf8.RuneError || size > 1) && n+size <= maxBytes {
			end := n + size
			return b[:end:end]
		}
	}
	return b[:n:n]
}

// CappedWriter is an io.Writer that retains at most limit bytes in memory.
// Once exceeded, it records truncation and either returns errOnExceed (error
// mode) or silently discards excess bytes (discard mode, when errOnExceed == nil).
// Keeping buf as a named field prevents io.Copy from bypassing Write via ReadFrom.
type CappedWriter struct {
	limit       int
	errOnExceed error
	buf         bytes.Buffer
	truncated   bool
}

// NewCappedBuffer returns a CappedWriter capped at limit bytes (negative limits
// clamp to 0). When errOnExceed is nil, excess bytes are discarded and Write
// returns (len(p), nil); when non-nil, Write writes up to the remaining capacity
// and returns (written, errOnExceed).
func NewCappedBuffer(limit int, errOnExceed error) *CappedWriter {
	if limit < 0 {
		limit = 0
	}
	return &CappedWriter{
		limit:       limit,
		errOnExceed: errOnExceed,
	}
}

// Write appends up to the remaining byte allowance into the buffer.
func (c *CappedWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	room := max(c.limit-c.buf.Len(), 0)
	if len(p) <= room {
		return c.buf.Write(p)
	}
	if room > 0 {
		_, _ = c.buf.Write(p[:room])
	}
	c.truncated = true
	if c.errOnExceed != nil {
		return room, c.errOnExceed
	}
	return len(p), nil
}

// Bytes returns the retained bytes.
func (c *CappedWriter) Bytes() []byte { return c.buf.Bytes() }

// String returns the retained bytes as a string.
func (c *CappedWriter) String() string { return c.buf.String() }

// Truncated reports whether any written byte exceeded the configured limit.
func (c *CappedWriter) Truncated() bool { return c.truncated }

// Len returns the number of retained bytes.
func (c *CappedWriter) Len() int { return c.buf.Len() }
