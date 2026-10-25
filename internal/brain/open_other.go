//go:build !unix

package brain

import "os"

// openNonBlocking is a plain open where O_NONBLOCK does not exist; the
// release targets are unix (DEPLOY-remote § Releases).
func openNonBlocking(p string) (*os.File, error) { return os.Open(p) }
