package serve

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

const (
	dreamWindow = 256 << 10 // the most of dream.log read, from its end
	dreamLines  = 200       // the most lines shown
)

// readDreamLog is the tail of dream.log as /dream shows it: the last 200
// lines within its last 256 KiB, each printable, or the one line that says
// why there are none. The open never follows a link and never waits on a
// FIFO; the opened descriptor must be a regular file.
func readDreamLog(path string) string {
	f, err := openLog(path)
	if errors.Is(err, os.ErrNotExist) {
		return "no dream log yet"
	}
	if err != nil {
		if notRegular(err) {
			return "dream.log is not a regular file: not read"
		}
		return search.Printable("dream.log: " + err.Error())
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return search.Printable("dream.log: " + err.Error())
	}
	if !fi.Mode().IsRegular() {
		return "dream.log is not a regular file: not read"
	}
	start := int64(0)
	if fi.Size() > dreamWindow {
		start = fi.Size() - dreamWindow
	}
	b, err := io.ReadAll(io.NewSectionReader(f, start, dreamWindow))
	if err != nil {
		return search.Printable("dream.log: " + err.Error())
	}
	if start > 0 { // the window's first line is a partial one
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		} else {
			b = nil
		}
	}
	return tailLines(string(b), dreamLines)
}

// tailLines keeps the last n lines of s.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return printableText(strings.Join(lines, "\n") + "\n")
}

// printableText is verb text as the terminal shows it: each line through
// printable, which would turn the newline itself into '?', with its tabs
// kept, as brain.FormatGet keeps a body's.
func printableText(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		parts := strings.Split(l, "\t")
		for j := range parts {
			parts[j] = search.Printable(parts[j])
		}
		lines[i] = strings.Join(parts, "\t")
	}
	return strings.Join(lines, "\n")
}
