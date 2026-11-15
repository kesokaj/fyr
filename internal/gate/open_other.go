//go:build !unix

package gate

import (
	"os"
)

func openRegularNoFollow(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, refusef("stamp path %s is not a regular file", path)
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
	if !os.SameFile(info, finfo) || !finfo.Mode().IsRegular() {
		_ = f.Close()
		return nil, refusef("stamp path %s changed during open", path)
	}
	return f, nil
}
