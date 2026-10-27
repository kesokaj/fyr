//go:build !unix

package search

import "os"

// openSource is a plain open where O_NONBLOCK does not exist; the release
// targets are unix (DEPLOY-remote § Releases).
func openSource(p string) (*os.File, error) { return os.Open(p) }
