//go:build !unix

package graph

import (
	"os"
	"path/filepath"
)

func openTargetNoFollow(path string, createExcl bool) (*os.File, error) {
	flags := os.O_WRONLY | os.O_CREATE
	if createExcl {
		flags |= os.O_EXCL
	}
	r, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.OpenFile(filepath.Base(path), flags, 0o600)
}
