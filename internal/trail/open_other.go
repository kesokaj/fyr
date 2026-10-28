//go:build !unix

package trail

import (
	"fmt"
	"os"
)

// openRegular opens path for reading when it is a regular file; O_NOFOLLOW
// and O_NONBLOCK do not exist here, and the release targets are unix
// (DEPLOY-remote § Releases).
func openRegular(path string) (*os.File, error) {
	if fi, err := os.Lstat(path); err != nil {
		return nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return os.Open(path)
}
