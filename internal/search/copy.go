package search

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrNotRegular is a source that is not a regular file: a FIFO, a device, a
// directory or a symlink. It is refused before a byte is read, so a FIFO
// planted at a conversation path cannot hang ingest, eval or the nightly
// dream (SD48 of the 2026-10-27 audit).
var ErrNotRegular = errors.New("not a regular file")

// copyFileImpl copies the regular file from to a new file to. The source is
// opened without blocking and without following a final symlink
// (openSource), then checked on the open descriptor, so nothing swapped in
// after a check is read.
func copyFileImpl(from, to string) error {
	in, err := openSource(from)
	if err != nil {
		if fi, lerr := os.Lstat(from); lerr == nil && !fi.Mode().IsRegular() {
			return fmt.Errorf("%s: %w", from, ErrNotRegular)
		}
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", from, ErrNotRegular)
	}
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
