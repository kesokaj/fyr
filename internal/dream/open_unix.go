//go:build unix

package dream

import (
	"fmt"
	"os"
	"syscall"
)

// openRegular opens p for reading without blocking, then checks the opened
// file: a FIFO swapped in after an earlier check is refused at once, never
// waited on (SD59b, SD60b of the 2026-10-27 re-audit; internal/search,
// brain and metrics have the same open, and the packages do not import
// each other). With follow false a final symlink is refused too; the
// operator's own ~/.config/fyr files are read through a dotfiles link, as
// metrics reads them.
func openRegular(p string, follow bool) (*os.File, error) {
	flag := os.O_RDONLY | syscall.O_NONBLOCK
	if !follow {
		flag |= syscall.O_NOFOLLOW
	}
	f, err := os.OpenFile(p, flag, 0)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	return f, nil
}
