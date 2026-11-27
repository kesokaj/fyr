package trail

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8(t *testing.T) {
	t.Parallel()

	// Positive cases and multi-byte/boundary cases.
	cases := []struct {
		name     string
		in       string
		maxBytes int
		want     string
	}{
		{name: "empty_positive", in: "", maxBytes: 5, want: ""},
		{name: "empty_zero", in: "", maxBytes: 0, want: ""},
		{name: "zero_limit", in: "hello", maxBytes: 0, want: ""},
		{name: "negative_limit", in: "hello", maxBytes: -3, want: ""},
		{name: "ascii_under_limit", in: "hello", maxBytes: 10, want: "hello"},
		{name: "ascii_exact_limit", in: "hello", maxBytes: 5, want: "hello"},
		{name: "ascii_truncated", in: "hello", maxBytes: 3, want: "hel"},
		// 2-byte rune: "é" = c3 a9
		{name: "two_byte_exact", in: "aéb", maxBytes: 3, want: "aé"},
		{name: "two_byte_mid_split", in: "aéb", maxBytes: 2, want: "a"},
		{name: "two_byte_at_start_split", in: "éx", maxBytes: 1, want: ""},
		// 3-byte rune: "€" = e2 82 ac
		{name: "three_byte_exact", in: "a€b", maxBytes: 4, want: "a€"},
		{name: "three_byte_split_after_2", in: "a€b", maxBytes: 3, want: "a"},
		{name: "three_byte_split_after_1", in: "a€b", maxBytes: 2, want: "a"},
		{name: "three_byte_at_start_split", in: "€x", maxBytes: 2, want: ""},
		// 4-byte rune: U+1F680 (f0 9f 9a 80)
		{name: "four_byte_exact", in: "a\U0001F680b", maxBytes: 5, want: "a\U0001F680"},
		{name: "four_byte_split_after_3", in: "a\U0001F680b", maxBytes: 4, want: "a"},
		{name: "four_byte_split_after_2", in: "a\U0001F680b", maxBytes: 3, want: "a"},
		{name: "four_byte_split_after_1", in: "a\U0001F680b", maxBytes: 2, want: "a"},
		{name: "four_byte_at_start_split", in: "\U0001F680x", maxBytes: 3, want: ""},
		// Stray continuation bytes and malformed leads at the cut boundary.
		{name: "stray_continuation_bytes", in: "a\x80\x80\x80\x80", maxBytes: 3, want: "a"},
		{name: "stray_continuation_preserves_ascii", in: "ab\x80\x80", maxBytes: 3, want: "ab"},
		{name: "stray_continuation_preserves_replacement", in: "a\ufffd\x80", maxBytes: 4, want: "a\ufffd"},
		{name: "truncated_lead_byte", in: "a\xf0\x9f\x8e", maxBytes: 3, want: "a"},
		{name: "all_continuation_bytes", in: "\x80\x80\x80\x80", maxBytes: 2, want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := TruncateUTF8(tc.in, tc.maxBytes)
			if got != tc.want {
				t.Fatalf("TruncateUTF8(%q, %d) = %q, want %q", tc.in, tc.maxBytes, got, tc.want)
			}
			if tc.maxBytes >= 0 && len(got) > tc.maxBytes {
				t.Fatalf("TruncateUTF8(%q, %d) len=%d exceeds maxBytes", tc.in, tc.maxBytes, len(got))
			}
			if utf8.ValidString(tc.in) && !utf8.ValidString(got) {
				t.Fatalf("TruncateUTF8(%q, %d) produced invalid UTF-8: %q", tc.in, tc.maxBytes, got)
			}
		})
	}
}

func TestTruncateUTF8Bytes(t *testing.T) {
	t.Parallel()

	// Negative and boundary cases on nil/zero/negative limits.
	if got := TruncateUTF8Bytes([]byte("hello"), 0); len(got) != 0 {
		t.Fatalf("TruncateUTF8Bytes(hello, 0) = %q, want empty", got)
	}
	if got := TruncateUTF8Bytes([]byte("hello"), -4); len(got) != 0 {
		t.Fatalf("TruncateUTF8Bytes(hello, -4) = %q, want empty", got)
	}
	if got := TruncateUTF8Bytes(nil, 5); got != nil {
		t.Fatalf("TruncateUTF8Bytes(nil, 5) = %v, want nil", got)
	}

	// Positive cases and multi-byte boundary splits.
	cases := []struct {
		name     string
		in       []byte
		maxBytes int
		want     []byte
	}{
		{name: "under_limit", in: []byte("abc"), maxBytes: 5, want: []byte("abc")},
		{name: "exact_limit", in: []byte("abc"), maxBytes: 3, want: []byte("abc")},
		{name: "two_byte_split", in: []byte("aéb"), maxBytes: 2, want: []byte("a")},
		{name: "three_byte_split", in: []byte("a€b"), maxBytes: 3, want: []byte("a")},
		{name: "three_byte_fit", in: []byte("a€b"), maxBytes: 4, want: []byte("a€")},
		{name: "four_byte_split", in: []byte("a\U0001F680b"), maxBytes: 4, want: []byte("a")},
		{name: "four_byte_fit", in: []byte("a\U0001F680b"), maxBytes: 5, want: []byte("a\U0001F680")},
		{name: "four_byte_start_split", in: []byte("\U0001F680"), maxBytes: 3, want: nil},
		{name: "stray_continuation_preserves_ascii", in: []byte("ab\x80\x80"), maxBytes: 3, want: []byte("ab")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := TruncateUTF8Bytes(tc.in, tc.maxBytes)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("TruncateUTF8Bytes(%q, %d) = %q, want %q", tc.in, tc.maxBytes, got, tc.want)
			}
			if len(got) > tc.maxBytes {
				t.Fatalf("TruncateUTF8Bytes len=%d exceeds %d", len(got), tc.maxBytes)
			}
			if utf8.Valid(tc.in) && !utf8.Valid(got) {
				t.Fatalf("TruncateUTF8Bytes produced invalid UTF-8: %q", got)
			}
		})
	}

	// Appending to a truncated slice must not mutate the original backing array.
	orig := []byte("abcdef")
	trunc := TruncateUTF8Bytes(orig, 3)
	_ = append(trunc, 'Z')
	if string(orig) != "abcdef" {
		t.Fatalf("append to truncated slice mutated original: got %q, want %q", orig, "abcdef")
	}
}

func TestCappedWriterDiscardMode(t *testing.T) {
	t.Parallel()

	// Positive case: writes up to exact limit do not mark truncated.
	cw := NewCappedBuffer(5, nil)
	n, err := cw.Write([]byte("ab"))
	if err != nil || n != 2 || cw.Truncated() || cw.Len() != 2 || cw.String() != "ab" {
		t.Fatalf("first write: n=%d err=%v trunc=%v len=%d str=%q", n, err, cw.Truncated(), cw.Len(), cw.String())
	}
	n, err = cw.Write([]byte("cde"))
	if err != nil || n != 3 || cw.Truncated() || cw.Len() != 5 || string(cw.Bytes()) != "abcde" {
		t.Fatalf("exact limit write: n=%d err=%v trunc=%v len=%d bytes=%q", n, err, cw.Truncated(), cw.Len(), cw.Bytes())
	}
	// Empty write at exact limit must not flip Truncated.
	n, err = cw.Write(nil)
	if err != nil || n != 0 || cw.Truncated() {
		t.Fatalf("empty write at exact limit: n=%d err=%v trunc=%v", n, err, cw.Truncated())
	}
	// Negative/overflow case: excess bytes are silently discarded and Truncated becomes true.
	n, err = cw.Write([]byte("fgh"))
	if err != nil || n != 3 || !cw.Truncated() || cw.Len() != 5 || cw.String() != "abcde" {
		t.Fatalf("overflow write: n=%d err=%v trunc=%v len=%d str=%q", n, err, cw.Truncated(), cw.Len(), cw.String())
	}

	// Single partial-overflow write retains prefix and reports full len(p), nil.
	partial := NewCappedBuffer(4, nil)
	n, err = partial.Write([]byte("abcdef"))
	if err != nil || n != 6 || !partial.Truncated() || partial.Len() != 4 || partial.String() != "abcd" {
		t.Fatalf("partial overflow: n=%d err=%v trunc=%v len=%d str=%q", n, err, partial.Truncated(), partial.Len(), partial.String())
	}

	// io.Copy must not bypass Write via bytes.Buffer.ReadFrom.
	copied := NewCappedBuffer(16, nil)
	written, err := io.Copy(copied, strings.NewReader(strings.Repeat("x", 1024)))
	if err != nil || written != 1024 || !copied.Truncated() || copied.Len() != 16 || copied.String() != strings.Repeat("x", 16) {
		t.Fatalf("io.Copy discard: written=%d err=%v trunc=%v len=%d", written, err, copied.Truncated(), copied.Len())
	}
}

func TestCappedWriterErrorMode(t *testing.T) {
	t.Parallel()

	errOver := errors.New("limit exceeded")

	// Positive case: writes up to exact limit succeed without error.
	cw := NewCappedBuffer(5, errOver)
	if n, err := cw.Write([]byte("12345")); err != nil || n != 5 || cw.Truncated() {
		t.Fatalf("exact write: n=%d err=%v trunc=%v", n, err, cw.Truncated())
	}
	// Empty write at exact limit stays non-truncated and error-free.
	if n, err := cw.Write([]byte{}); err != nil || n != 0 || cw.Truncated() {
		t.Fatalf("empty write at exact limit: n=%d err=%v trunc=%v", n, err, cw.Truncated())
	}
	// Overflow when full returns (0, errOver) and marks truncated.
	if n, err := cw.Write([]byte("6")); !errors.Is(err, errOver) || n != 0 || !cw.Truncated() || cw.String() != "12345" {
		t.Fatalf("full overflow: n=%d err=%v trunc=%v str=%q", n, err, cw.Truncated(), cw.String())
	}

	// Partial overflow writes remaining room and returns (room, errOver).
	partial := NewCappedBuffer(4, errOver)
	if n, err := partial.Write([]byte("abcdef")); !errors.Is(err, errOver) || n != 4 || !partial.Truncated() || partial.String() != "abcd" {
		t.Fatalf("partial overflow: n=%d err=%v trunc=%v str=%q", n, err, partial.Truncated(), partial.String())
	}

	// io.Copy stops immediately on errOver and enforces the cap.
	copied := NewCappedBuffer(12, errOver)
	written, err := io.Copy(copied, strings.NewReader(strings.Repeat("y", 256)))
	if !errors.Is(err, errOver) || written != 12 || !copied.Truncated() || copied.Len() != 12 || copied.String() != strings.Repeat("y", 12) {
		t.Fatalf("io.Copy error mode: written=%d err=%v trunc=%v len=%d", written, err, copied.Truncated(), copied.Len())
	}
}

func TestCappedWriterZeroAndNegativeLimits(t *testing.T) {
	t.Parallel()

	errOver := errors.New("zero cap")

	for _, limit := range []int{0, -1, -64} {
		discard := NewCappedBuffer(limit, nil)
		if n, err := discard.Write(nil); err != nil || n != 0 || discard.Truncated() {
			t.Fatalf("limit=%d empty discard: n=%d err=%v trunc=%v", limit, n, err, discard.Truncated())
		}
		if n, err := discard.Write([]byte("a")); err != nil || n != 1 || !discard.Truncated() || discard.Len() != 0 || len(discard.Bytes()) != 0 {
			t.Fatalf("limit=%d non-empty discard: n=%d err=%v trunc=%v len=%d", limit, n, err, discard.Truncated(), discard.Len())
		}

		strict := NewCappedBuffer(limit, errOver)
		if n, err := strict.Write([]byte{}); err != nil || n != 0 || strict.Truncated() {
			t.Fatalf("limit=%d empty strict: n=%d err=%v trunc=%v", limit, n, err, strict.Truncated())
		}
		if n, err := strict.Write([]byte("a")); !errors.Is(err, errOver) || n != 0 || !strict.Truncated() || strict.Len() != 0 || strict.String() != "" {
			t.Fatalf("limit=%d non-empty strict: n=%d err=%v trunc=%v len=%d", limit, n, err, strict.Truncated(), strict.Len())
		}
	}

	// Zero-value CappedWriter defaults safely to limit 0 in discard mode.
	var zero CappedWriter
	if n, err := zero.Write([]byte("hi")); err != nil || n != 2 || !zero.Truncated() || zero.Len() != 0 {
		t.Fatalf("zero CappedWriter: n=%d err=%v trunc=%v len=%d", n, err, zero.Truncated(), zero.Len())
	}
}
