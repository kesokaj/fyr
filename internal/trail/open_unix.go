//go:build unix

package trail

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// OpenRegular opens path for reading without following a final symlink or
// blocking on a FIFO, and only when it is a regular file.
func OpenRegular(path string) (*os.File, error) {
	return OpenRegularFollow(path, false)
}

// OpenRegularFollow opens path for reading without blocking on a FIFO, and
// only when it is a regular file. With follow false, a final symlink is refused.
func OpenRegularFollow(path string, follow bool) (*os.File, error) {
	flag := os.O_RDONLY | syscall.O_NONBLOCK
	if !follow {
		flag |= syscall.O_NOFOLLOW
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		if !follow && errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("%w: %s", ErrStampSymlink, path)
		}
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrStampNotRegular, path)
	}
	return f, nil
}

// OpenNonBlocking opens path for reading without blocking on a FIFO and
// without following a final symlink.
func OpenNonBlocking(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
