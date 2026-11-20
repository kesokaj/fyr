//go:build !unix

package graph

import "os"

func openTargetNoFollow(path string, force bool) (*os.File, error) {
	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	return os.OpenFile(path, flags, 0o600)
}
