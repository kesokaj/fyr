package sandbox

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

func scanGoBuildTestLog(root, p string, d fs.DirEntry) (f credFinding, handled, ok bool) {
	if d == nil || !d.Type().IsRegular() {
		return credFinding{}, false, false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || !goBuildCacheRel.MatchString(filepath.ToSlash(rel)) || !resolvesUnder(root, p) {
		return credFinding{}, false, false
	}
	fd, err := OpenRegular(p, os.O_RDONLY)
	if err != nil {
		return credFinding{problem: "unreadable"}, true, true
	}
	defer fd.Close()
	st, err := fd.Stat()
	if err != nil {
		return credFinding{problem: "unreadable"}, true, true
	}
	return streamTestLogFile(fd, st.Size())
}

func streamTestLogFile(fd *os.File, statSize int64) (credFinding, bool, bool) {
	chunk := max(testLogChunk, 64)
	overlap := min(max(testLogOverlap, 32), chunk)
	lr := io.LimitReader(fd, maxTestLogScan+1)
	buf := make([]byte, overlap+chunk)

	n, err := io.ReadFull(lr, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return credFinding{problem: "unreadable"}, true, true
	}
	if !bytes.HasPrefix(buf[:n], []byte(testLogMagic)) {
		return credFinding{}, false, false
	}
	if statSize > maxTestLogScan || int64(n) > maxTestLogScan {
		return credFinding{problem: fmt.Sprintf("larger than %d bytes, not read", maxTestLogScan)}, true, true
	}

	kinds := map[string]int{}
	unparsed := map[string]int{}
	digests := map[string]map[[32]byte]int{}
	total, wlen := int64(n), n
	for {
		eof := err == io.EOF || err == io.ErrUnexpectedEOF
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
		m, err = io.ReadFull(lr, buf[overlap:])
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return credFinding{problem: "unreadable"}, true, true
		}
		total += int64(m)
		if total > maxTestLogScan {
			return credFinding{problem: fmt.Sprintf("larger than %d bytes, not read", maxTestLogScan)}, true, true
		}
		wlen = overlap + m
	}
	if len(kinds) == 0 {
		return credFinding{}, true, false
	}
	return credFinding{kinds: kinds, unparsed: unparsed, digests: digests}, true, true
}
