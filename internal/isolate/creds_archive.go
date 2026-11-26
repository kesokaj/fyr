package isolate

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
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() < int64(len(arMagic)+arMemberHdrBytes+len(goPkgDefPrefix)) || st.Size() > maxGoPkgArchive {
		return false
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false
	}
	var magic [len(arMagic)]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil || string(magic[:]) != arMagic {
		return false
	}
	size := st.Size()
	pos := int64(len(arMagic))
	first := true
	var hdr [arMemberHdrBytes]byte
	for pos < size {
		if pos+arMemberHdrBytes > size {
			return false
		}
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			return false
		}
		name, msize, ok := parseArHeader(hdr[:])
		if !ok || pos+arMemberHdrBytes+msize > size {
			return false
		}
		if first {
			if name != goPkgDefName || msize < int64(len(goPkgDefPrefix)) {
				return false
			}
			var pfx [len(goPkgDefPrefix)]byte
			if _, err := io.ReadFull(f, pfx[:]); err != nil || string(pfx[:]) != goPkgDefPrefix {
				return false
			}
			first = false
		}
		next := pos + arMemberHdrBytes + msize + (msize & 1)
		if next > size {
			return false
		}
		if _, err := f.Seek(next, io.SeekStart); err != nil {
			return false
		}
		pos = next
	}
	return !first && pos == size
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
