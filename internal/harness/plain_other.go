//go:build !unix

package harness

import (
	"fmt"
	"io/fs"
	"os"
)

// openPlain on a platform without O_NOFOLLOW keeps the Lstat check.
func openPlain(p string) ([]byte, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotRegular
	}
	return os.ReadFile(p)
}

// chmodPlain checks Lstat before chmod on platforms without O_NOFOLLOW.
func chmodPlain(p string, mode fs.FileMode) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errNotRegular
	}
	return os.Chmod(p, mode)
}

// openNonBlock verifies p is a non-symlink regular file before and after opening on !unix platforms.
func openNonBlock(p string) (*os.File, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	fStat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !os.SameFile(fi, fStat) || !fStat.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	return f, nil
}
