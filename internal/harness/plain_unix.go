//go:build unix

package harness

import (
	"io"
	"io/fs"
	"os"
	"syscall"
)

// openVerifiedPlain opens p without following a symlink and without blocking
// on a FIFO, then verifies f.Stat().Mode().IsRegular().
func openVerifiedPlain(p string) (*os.File, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if isLoop(err) {
			return nil, errNotRegular
		}
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, errNotRegular
	}
	return f, nil
}

// openPlain opens p without following a symlink and without blocking on a
// FIFO, then judges the file it holds, not the name: a path swapped between
// a check and a read can no longer redirect or stall it.
func openPlain(p string) ([]byte, error) {
	f, err := openVerifiedPlain(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// chmodPlain sets mode on the verified non-symlink regular file at p via its
// open descriptor, preventing symlink-swap chmods.
func chmodPlain(p string, mode fs.FileMode) error {
	f, err := openVerifiedPlain(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Chmod(mode)
}

// openNonBlock opens p without blocking on a FIFO so the caller can inspect
// f.Stat() before reading.
func openNonBlock(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

// isLoop reports the error O_NOFOLLOW gives for a symlink (ELOOP;
// EMLINK on some BSDs).
func isLoop(err error) bool {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err == syscall.ELOOP || pe.Err == syscall.EMLINK
	}
	return false
}
