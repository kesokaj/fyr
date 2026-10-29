//go:build unix

package serve

import (
	"errors"
	"os"
	"syscall"
)

// openLog opens dream.log without following a link and without waiting for
// a FIFO's writer (internal/brain's openNonBlocking is the same guard for
// trail.jsonl).
func openLog(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

// notRegular reports the error O_NOFOLLOW gives for a link.
func notRegular(err error) bool { return errors.Is(err, syscall.ELOOP) }
