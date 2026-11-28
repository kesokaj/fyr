package creds

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeArMember(name string, payload []byte, padOdd bool) []byte {
	hdr := fmt.Sprintf("%-16s%-12s%-6s%-6s%-8s%-10d`\n",
		name, "0", "0", "0", "100644", len(payload))
	var b bytes.Buffer
	b.WriteString(hdr)
	b.Write(payload)
	if padOdd && len(payload)%2 == 1 {
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func writeArchiveTemp(t *testing.T, data []byte) *os.File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pkg.a")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestValidGoPkgArchiveBoundaryMatrix(t *testing.T) {
	t.Parallel()
	validSingle := append([]byte(arMagic), makeArMember("__.PKGDEF", []byte("go object linux amd64\n"), true)...)
	validMulti := append([]byte(arMagic), makeArMember("__.PKGDEF", []byte("go object odd"), true)...)
	validMulti = append(validMulti, makeArMember("_go_.o", []byte("even"), true)...)

	missingOddPad := append([]byte(arMagic), makeArMember("__.PKGDEF", []byte("go object odd"), false)...)

	badTrailer := append([]byte(nil), validSingle...)
	badTrailer[len(arMagic)+58] = '!'

	nonPrintable := append([]byte(nil), validSingle...)
	nonPrintable[len(arMagic)+20] = 0x01

	blankName := append([]byte(arMagic), makeArMember("   ", []byte("go object 1234"), true)...)
	wrongFirst := append([]byte(arMagic), makeArMember("_go_.o", []byte("go object 1234"), true)...)
	badPrefix := append([]byte(arMagic), makeArMember("__.PKGDEF", []byte("not-a-go-obj"), true)...)
	shortPkgDef := append([]byte(arMagic), makeArMember("__.PKGDEF", []byte("go obj"), true)...)
	truncHdr := append(append([]byte(nil), validSingle...), []byte("short-hdr")...)
	truncPayload := validSingle[:len(validSingle)-4]
	badSizeHdr := fmt.Sprintf("%-16s%-12s%-6s%-6s%-8s%-10s`\n", "__.PKGDEF", "0", "0", "0", "100644", "notnum")
	badSize := append([]byte(arMagic), append([]byte(badSizeHdr), []byte("go object 1234")...)...)
	negSizeHdr := fmt.Sprintf("%-16s%-12s%-6s%-6s%-8s%-10s`\n", "__.PKGDEF", "0", "0", "0", "100644", "-5")
	negSize := append([]byte(arMagic), append([]byte(negSizeHdr), []byte("go object 1234")...)...)

	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{name: "valid single even member", data: validSingle, want: true},
		{name: "valid multi member with odd pad", data: validMulti, want: true},
		{name: "empty file", data: nil, want: false},
		{name: "non-ar plain text", data: []byte("just some plain text longer than minimum header bytes " + strings.Repeat("a", 40)), want: false},
		{name: "wrong magic", data: append([]byte("!<badar>\n"), validSingle[len(arMagic):]...), want: false},
		{name: "first member not __.PKGDEF", data: wrongFirst, want: false},
		{name: "first member bad prefix", data: badPrefix, want: false},
		{name: "first member shorter than prefix", data: shortPkgDef, want: false},
		{name: "missing odd-length padding byte", data: missingOddPad, want: false},
		{name: "truncated trailing header", data: truncHdr, want: false},
		{name: "truncated member payload", data: truncPayload, want: false},
		{name: "bad member header trailer", data: badTrailer, want: false},
		{name: "non-printable byte in header", data: nonPrintable, want: false},
		{name: "blank member name", data: blankName, want: false},
		{name: "non-numeric member size", data: badSize, want: false},
		{name: "negative member size", data: negSize, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := writeArchiveTemp(t, tc.data)
			if got := validGoPkgArchive(f); got != tc.want {
				t.Errorf("validGoPkgArchive(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}

	t.Run("non-zero seek offset rewinds to start", func(t *testing.T) {
		f := writeArchiveTemp(t, validSingle)
		if _, err := f.Seek(12, 0); err != nil {
			t.Fatal(err)
		}
		if !validGoPkgArchive(f) {
			t.Errorf("validGoPkgArchive failed when starting at non-zero offset")
		}
	})

	t.Run("directory file descriptor rejected", func(t *testing.T) {
		d, err := os.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		if validGoPkgArchive(d) {
			t.Errorf("validGoPkgArchive accepted directory fd")
		}
	})
}
