package creds

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanCredsStreamsOversizedGoBuildTestLog(t *testing.T) {
	t.Parallel()
	opts := Options{MaxScan: 256, MaxTestLogScan: 2048, TestLogChunk: 256, TestLogOverlap: 96}

	sb := t.TempDir()
	put(t, filepath.Join(sb, "app", "main.go"), "package main\n", 0o644)
	base := ScanWith(sb, opts)

	cacheDir := filepath.Join(sb, "home", ".cache", "go-build", "1e")
	entry := filepath.Join(cacheDir, strings.Repeat("a", 64)+"-d")

	var clean bytes.Buffer
	clean.WriteString("# test log\n")
	for clean.Len() < 900 {
		clean.WriteString("getenv GOEXPERIMENT\nstat /workspace/cmd/fyr/main.go\n")
	}
	put(t, entry, clean.String(), 0o644)
	if got := New(base, ScanWith(sb, opts)); len(got) != 0 {
		t.Fatalf("clean oversized go-build # test log flagged: %v", got)
	}

	// A novel credential straddling a streaming chunk boundary inside # test log
	// must still fail closed.
	var boundary bytes.Buffer
	boundary.WriteString("# test log\n")
	for boundary.Len() < opts.TestLogChunk+opts.TestLogOverlap-6 {
		boundary.WriteByte('x')
	}
	boundary.WriteByte('\n')
	boundary.WriteString(strings.Join([]string{"sk-", "ant", "-api03-streamedTestLogBoundaryKey000001"}, ""))
	boundary.WriteString("\nstat /workspace/go.mod\n")
	for boundary.Len() < 900 {
		boundary.WriteString("chdir /workspace/internal/isolate\n")
	}
	put(t, entry, boundary.String(), 0o644)
	if got := New(base, ScanWith(sb, opts)); len(got) != 1 || !strings.Contains(got[0], "(anthropic-key)") {
		t.Fatalf("boundary credential in # test log = %v, want anthropic-key failure", got)
	}

	// Exceeding maxTestLogScan fails closed even with a valid # test log header.
	var huge bytes.Buffer
	huge.WriteString("# test log\n")
	for huge.Len() <= int(opts.MaxTestLogScan)+64 {
		huge.WriteString("open /workspace/cmd/fyr/main.go\n")
	}
	put(t, entry, huge.String(), 0o644)
	if got := New(base, ScanWith(sb, opts)); len(got) != 1 || !strings.Contains(got[0], "larger than") {
		t.Fatalf("test log > maxTestLogScan = %v, want larger-than failure", got)
	}

	// A non-# test log text file in go-build > maxScan still fails closed.
	put(t, entry, strings.Repeat("plain text not a test log\n", 30), 0o644)
	if got := New(base, ScanWith(sb, opts)); len(got) != 1 || !strings.Contains(got[0], "larger than") {
		t.Fatalf("non-test-log oversized go-build file = %v, want larger-than failure", got)
	}

	// A symlink at the go-build entry path is never streamed as a trusted test log.
	outsideLog := filepath.Join(sb, "app", "real-log.txt")
	put(t, outsideLog, clean.String(), 0o644)
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideLog, entry); err != nil {
		t.Fatal(err)
	}
	if got := New(base, ScanWith(sb, opts)); len(got) == 0 {
		t.Fatalf("symlinked oversized # test log unexpectedly passed")
	}
}
