package harness

import (
	"errors"
	"os"
)

// dirInTheWay is the dry run's copy of the guard the real write meets: a
// directory at a file's path, which WriteAtomic's rename cannot replace.
// Without it a dry run printed an update and exited 0 where the run fails
// (IN41 of the 2026-10-27 audit; review-standards § 8).
func dirInTheWay(dest string) error {
	if fi, err := os.Lstat(dest); err == nil && fi.IsDir() {
		return errors.New("a directory is in the way; the install cannot replace it")
	}
	return nil
}
