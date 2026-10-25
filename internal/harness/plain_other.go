//go:build !unix

package harness

import "os"

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
