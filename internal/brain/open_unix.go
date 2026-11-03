//go:build unix

package brain

import (
	"os"
	"syscall"
)

// openNonBlocking opens p for reading without blocking: a FIFO swapped in
// between a check and the open returns at once instead of waiting for a
// writer (security review of v2.6.0, part 1, round 3); trail.jsonl is read
// through it (internal/metrics has the same helper). O_NOFOLLOW: a symlink
// swapped in after the guard's Lstat is refused, never read (security
// review 2026-10-25, finding 4).
func openNonBlocking(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}

// hasMultipleLinks reports whether fi has more than one hard link.
func hasMultipleLinks(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Nlink > 1
}

// openScratchFile creates a new scratch file inside dir without following a
// symlink, truncating an existing inode, or blocking on a FIFO.
func openScratchFile(dir *os.Root, name string) (*os.File, error) {
	return dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0o600)
}

// hasPipedByte probes a non-regular, non-tty descriptor (such as a pipe or
// FIFO) for a ready byte without blocking when a writer is open and idle.
func hasPipedByte(f *os.File) bool {
	fd := int(f.Fd())
	if err := syscall.SetNonblock(fd, true); err != nil {
		return false
	}
	defer syscall.SetNonblock(fd, false)
	var b [1]byte
	n, err := syscall.Read(fd, b[:])
	return err == nil && n > 0
}
