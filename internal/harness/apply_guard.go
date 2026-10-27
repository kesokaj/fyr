package harness

import (
	"errors"
	"os"
)

// dirInTheWay is the guard both runs meet before a write: a directory at a
// file's path, which WriteAtomic's rename cannot replace. Without it a dry
// run printed an update and exited 0 where the run fails (IN41 of the
// 2026-10-27 audit; review-standards § 8), and the real run failed with a
// bare rename error that named no way out (BUG-189).
func dirInTheWay(dest string) error {
	if fi, err := os.Lstat(dest); err == nil && fi.IsDir() {
		return errors.New("a directory is in the way; the install cannot replace it: move or remove it, then run fyr install again")
	}
	return nil
}
