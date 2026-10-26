package brain

import (
	"bytes"
	"io"
	"time"
)

// The import's read caps, over the docs tree, the files that reference it
// and the old memory stores together: far above this repository's (16 MB,
// 1,700 files). Variables so a test can lower them (security review of the
// import, 2026-10-25, finding 6).
var (
	maxImportBytes int64 = 256 << 20
	maxImportFiles       = 50000
)

// budget is what one import may still read.
type budget struct {
	bytes int64
	files int
}

func newBudget() *budget { return &budget{bytes: maxImportBytes, files: maxImportFiles} }

func (b *budget) take(n int, what string) error {
	b.bytes -= int64(n)
	b.files--
	if b.bytes < 0 || b.files < 0 {
		return refuse("the import is over %d bytes or %d files (at %s); import a smaller docs root", maxImportBytes, maxImportFiles, what)
	}
	return nil
}

// readRegular reads a file whole, opened without following a link or
// blocking on a FIFO (openNonBlocking), refusing what is not a regular file
// or is over max bytes, by its size and while reading: a file swapped after
// a check is never read through (security review of the import, finding 7).
func readRegular(path string, max int64) ([]byte, error) {
	f, err := openNonBlocking(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, refuse("%s is not a regular file", path)
	}
	if fi.Size() > max {
		return nil, refuse("%s is over %d bytes", path, max)
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, refuse("%s is over %d bytes", path, max)
	}
	return data, nil
}

// checkRecord refuses a record the store's own rules would not take back:
// an exported kind goes through the load's parser itself, so the import's
// check and the load's are one function (security review of the import,
// finding 1); the others get the same field rules by hand.
func checkRecord(r Record) error {
	if !unexported[r.Kind] {
		line, err := encodeTrail([]Record{r})
		if err != nil {
			return err
		}
		_, err = parseLine(bytes.TrimSuffix(line, []byte("\n")), 1)
		return err
	}
	_, derr := time.Parse("2006-01-02", r.Date)
	_, uerr := time.Parse(time.RFC3339, r.Updated)
	switch {
	case !validID(r.Kind, r.ID):
		return refuse("id %q is not a %s id", r.ID, r.Kind)
	case !allowed(r.Kind, r.Status):
		return refuse("status %q is not one a %s takes", r.Status, r.Kind)
	case derr != nil || uerr != nil:
		return refuse("date %q or updated %q is not a date", r.Date, r.Updated)
	case r.Title == "" || len(r.Title) > maxTitle || !titleText(r.Title):
		return refuse("the title is not one line of text of at most %d bytes", maxTitle)
	case len(r.Body) > maxBody || !bodyText(r.Body):
		return refuse("the body is over 1 MiB or not text")
	}
	return nil
}
