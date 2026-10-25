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
