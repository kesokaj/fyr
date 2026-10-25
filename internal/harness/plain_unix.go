//go:build unix

package harness

import (
	"io"
	"os"
	"syscall"
)

// openPlain opens p without following a symlink and without blocking on a
// FIFO, then judges the file it holds, not the name: a path swapped between
// a check and a read can no longer redirect or stall it.
func openPlain(p string) ([]byte, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if isLoop(err) {
			return nil, errNotRegular
		}
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotRegular
	}
	return io.ReadAll(f)
}

// isLoop reports the error O_NOFOLLOW gives for a symlink (ELOOP;
// EMLINK on some BSDs).
func isLoop(err error) bool {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err == syscall.ELOOP || pe.Err == syscall.EMLINK
	}
	return false
}
