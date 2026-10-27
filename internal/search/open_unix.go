//go:build unix

package search

import (
	"os"
	"syscall"
)

// openSource opens p for reading without blocking and without following a
// final symlink: a FIFO returns at once instead of waiting for a writer, and
// a symlink is refused (internal/brain and internal/metrics have the same
// open; the packages do not import each other).
func openSource(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
