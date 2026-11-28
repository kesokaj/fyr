package creds

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const testLogMagic = "# test log\n"

// maxTestLogScan bounds streamed scanning of Go's action-invalidation test logs
// (home/.cache/go-build/<xx>/<hex>-d), which easily exceed maxScan (64 MiB) on
// package test runs while holding plain getenv/stat/open/chdir lines (BUG-539).
var (
	maxTestLogScan int64 = 512 << 20
	testLogChunk         = 1 << 20
	testLogOverlap       = 4096
)

func scanGoBuildTestLog(root, p string, d fs.DirEntry, opts Options) (f Finding, handled, ok bool) {
	if d == nil || !d.Type().IsRegular() {
		return Finding{}, false, false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || !goBuildCacheRel.MatchString(filepath.ToSlash(rel)) || !resolvesUnder(root, p) {
		return Finding{}, false, false
	}
	fd, err := opts.OpenRegular(p, os.O_RDONLY)
	if err != nil {
		return Finding{problem: "unreadable"}, true, true
	}
	defer fd.Close()
	st, err := fd.Stat()
	if err != nil {
		return Finding{problem: "unreadable"}, true, true
	}
	return streamTestLogFile(fd, st.Size(), opts)
}

func streamTestLogFile(fd *os.File, statSize int64, opts Options) (Finding, bool, bool) {
	chunk := max(opts.TestLogChunk, 64)
	overlap := min(max(opts.TestLogOverlap, 32), chunk)
	lr := io.LimitReader(fd, opts.MaxTestLogScan+1)
	buf := make([]byte, overlap+chunk)

	n, eof, err := readTestLogChunk(lr, buf)
	if err != nil {
		return Finding{problem: "unreadable"}, true, true
	}
	if !bytes.HasPrefix(buf[:n], []byte(testLogMagic)) {
		return Finding{}, false, false
	}
	if statSize > opts.MaxTestLogScan || int64(n) > opts.MaxTestLogScan {
		return Finding{problem: fmt.Sprintf("larger than %d bytes, not read", opts.MaxTestLogScan)}, true, true
	}
	return scanTestLogWindows(lr, buf, n, overlap, eof, opts.MaxTestLogScan)
}

func readTestLogChunk(lr io.Reader, dst []byte) (n int, eof bool, err error) {
	n, err = io.ReadFull(lr, dst)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return n, true, nil
	}
	return n, false, err
}

func scanTestLogWindows(lr io.Reader, buf []byte, wlen, overlap int, eof bool, limit int64) (Finding, bool, bool) {
	kinds := map[string]int{}
	unparsed := map[string]int{}
	digests := map[string]map[[32]byte]int{}
	total := int64(wlen)
	for {
		safeEnd := wlen
		if !eof {
			safeEnd = wlen - overlap
		}
		recordWindowCreds(buf[:wlen], safeEnd, kinds, unparsed, digests)
		if eof {
			break
		}
		copy(buf[:overlap], buf[safeEnd:wlen])
		var m int
		var err error
		m, eof, err = readTestLogChunk(lr, buf[overlap:])
		if err != nil {
			return Finding{problem: "unreadable"}, true, true
		}
		total += int64(m)
		if total > limit {
			return Finding{problem: fmt.Sprintf("larger than %d bytes, not read", limit)}, true, true
		}
		wlen = overlap + m
	}
	if len(kinds) == 0 {
		return Finding{}, true, false
	}
	return Finding{kinds: kinds, unparsed: unparsed, digests: digests}, true, true
}
