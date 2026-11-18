//go:build !unix

package trail

import (
	"fmt"
	"os"
)

// OpenRegular opens path for reading when it is a regular file without
// following a final symlink.
func OpenRegular(path string) (*os.File, error) {
	return OpenRegularFollow(path, false)
}

// OpenRegularFollow opens path for reading when it is a regular file; O_NOFOLLOW
// and O_NONBLOCK do not exist here, so !follow verifies Lstat+SameFile around Open.
func OpenRegularFollow(path string, follow bool) (*os.File, error) {
	var info os.FileInfo
	if !follow {
		var err error
		info, err = os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: %s", ErrStampSymlink, path)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%w: %s is not a regular file", ErrStampNotRegular, path)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	finfo, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !finfo.Mode().IsRegular() || (!follow && !os.SameFile(info, finfo)) {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrStampNotRegular, path)
	}
	return f, nil
}

// OpenNonBlocking is a plain open where O_NONBLOCK does not exist; release
// targets are unix.
func OpenNonBlocking(path string) (*os.File, error) {
	return os.Open(path)
}
