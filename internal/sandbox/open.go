package sandbox

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unicode"
)

// OpenRegular opens path with flag, refusing a link (O_NOFOLLOW) and
// anything that is not a regular file, without blocking on a FIFO the agent
// left at a name the box reads or writes. A write open also refuses a file of
// more than one link, before anything is truncated: an agent's hard link to
// an operator file at check.log would otherwise empty that file (IN49 of the
// 2026-10-27 audit, plan D15); O_TRUNC is applied only after the check.
func OpenRegular(path string, flag int) (*os.File, error) {
	trunc := flag&os.O_TRUNC != 0
	f, err := os.OpenFile(path, flag&^os.O_TRUNC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	// The descriptor's inode must be the name's only link and still the one
	// the name holds: a leftover process that unlinks its own name between
	// the open and this check would otherwise drop the count to 1 (security
	// review of the 2026-10-27 fix, finding 1).
	if flag&(os.O_WRONLY|os.O_RDWR) != 0 && !soleLink(path, st) {
		f.Close()
		return nil, fmt.Errorf("%s: has other hard links, or changed while it was opened", path)
	}
	if trunc {
		if err := f.Truncate(0); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

// soleLink reports whether opened, the Stat of an open descriptor, has one
// link and is the inode path names now.
func soleLink(path string, opened os.FileInfo) bool {
	fd, ok := opened.Sys().(*syscall.Stat_t)
	named, err := os.Lstat(path)
	if !ok || err != nil {
		return false
	}
	nd, ok := named.Sys().(*syscall.Stat_t)
	// Both counts: a process that unlinks the name before the descriptor's
	// stat and links it to the operator's file again before the Lstat leaves
	// the Lstat's count at 2 (security review of the fix, round 2).
	return ok && fd.Nlink == 1 && nd.Nlink == 1 && fd.Dev == nd.Dev && fd.Ino == nd.Ino
}

// Printable is search.Printable's filter, which this package cannot import
// (D10): control and format characters, the line separators and the
// ignorables become '?', so a row id or a path never forges a line. A test
// pins the two equal.
func Printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) ||
			unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || unicode.Is(unicode.Cf, r) ||
			unicode.Is(unicode.Variation_Selector, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) {
			return '?'
		}
		return r
	}, s)
}
