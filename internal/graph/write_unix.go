//go:build unix

package graph

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func openTargetNoFollow(path string, force bool) (*os.File, error) {
	flags := os.O_WRONLY | os.O_CREATE | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	if !force {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if errors.Is(err, syscall.ELOOP) {
		return nil, fmt.Errorf("refusing symlink target %s", path)
	}
	return f, err
}
