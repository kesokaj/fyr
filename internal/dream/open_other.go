//go:build !unix

package dream

import (
	"fmt"
	"os"
)

// openRegular is a plain open with the regular-file check where O_NONBLOCK
// does not exist; the release targets are unix (DEPLOY-remote § Releases).
func openRegular(p string, follow bool) (*os.File, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	return f, nil
}
