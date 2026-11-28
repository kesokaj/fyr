package creds

import (
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	goPkgDefName     = "__.PKGDEF"
	goPkgDefPrefix   = "go object "
	arMemberHdrBytes = 60
	maxGoPkgArchive  = 256 << 20
)

// validGoPkgArchive verifies that f is a regular Unix ar archive ("!<arch>\n")
// within maxGoPkgArchive whose first member is "__.PKGDEF" starting with
// "go object " and whose subsequent 60-byte member headers account cleanly to
// exact EOF.
func validGoPkgArchive(f *os.File) bool {
	size, ok := readArchiveMagic(f)
	if !ok {
		return false
	}
	pos := int64(len(arMagic))
	first := true
	for pos < size {
		next, ok := nextArchiveMember(f, pos, size, first)
		if !ok {
			return false
		}
		first = false
		pos = next
	}
	return !first && pos == size
}

func readArchiveMagic(f *os.File) (int64, bool) {
	st, err := f.Stat()
	minSize := int64(len(arMagic) + arMemberHdrBytes + len(goPkgDefPrefix))
	if err != nil || !st.Mode().IsRegular() || st.Size() < minSize || st.Size() > maxGoPkgArchive {
		return 0, false
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, false
	}
	var magic [len(arMagic)]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil || string(magic[:]) != arMagic {
		return 0, false
	}
	return st.Size(), true
}

func nextArchiveMember(f *os.File, pos, size int64, first bool) (int64, bool) {
	if pos+arMemberHdrBytes > size {
		return 0, false
	}
	var hdr [arMemberHdrBytes]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return 0, false
	}
	name, msize, ok := parseArHeader(hdr[:])
	if !ok || pos+arMemberHdrBytes+msize > size {
		return 0, false
	}
	if first && !verifyFirstPkgDef(f, name, msize) {
		return 0, false
	}
	next := pos + arMemberHdrBytes + msize + (msize & 1)
	if next > size {
		return 0, false
	}
	if _, err := f.Seek(next, io.SeekStart); err != nil {
		return 0, false
	}
	return next, true
}

func verifyFirstPkgDef(f *os.File, name string, msize int64) bool {
	if name != goPkgDefName || msize < int64(len(goPkgDefPrefix)) {
		return false
	}
	var pfx [len(goPkgDefPrefix)]byte
	_, err := io.ReadFull(f, pfx[:])
	return err == nil && string(pfx[:]) == goPkgDefPrefix
}

func parseArHeader(hdr []byte) (name string, size int64, ok bool) {
	if len(hdr) != arMemberHdrBytes || hdr[58] != '`' || hdr[59] != '\n' {
		return "", 0, false
	}
	for _, c := range hdr[:58] {
		if c < 0x20 || c > 0x7e {
			return "", 0, false
		}
	}
	name = strings.TrimRight(string(hdr[0:16]), " ")
	if name == "" {
		return "", 0, false
	}
	rawSize := strings.TrimSpace(string(hdr[48:58]))
	if rawSize == "" {
		return "", 0, false
	}
	n, err := strconv.ParseInt(rawSize, 10, 64)
	if err != nil || n < 0 {
		return "", 0, false
	}
	return name, n, true
}
