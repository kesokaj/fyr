//go:build unix

package gate

import (
	"errors"
	"os"
	"syscall"
)

func openRegularNoFollow(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, refusef("stamp path %s is a symlink", path)
		}
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, refusef("stamp path %s is not a regular file", path)
	}
	return f, nil
}
