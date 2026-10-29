//go:build !unix

package serve

import "os"

// openLog is a plain open where O_NOFOLLOW and O_NONBLOCK do not exist; the
// release targets are unix (DEPLOY-remote § Releases).
func openLog(p string) (*os.File, error) { return os.Open(p) }

func notRegular(error) bool { return false }
