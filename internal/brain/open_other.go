//go:build !unix

package brain

import "os"

// openNonBlocking is a plain open where O_NONBLOCK does not exist; the
// release targets are unix (DEPLOY-remote § Releases).
func openNonBlocking(p string) (*os.File, error) { return os.Open(p) }

func hasMultipleLinks(_ os.FileInfo) bool { return false }

func openScratchFile(dir *os.Root, name string) (*os.File, error) {
	return dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func hasPipedByte(f *os.File) bool {
	var b [1]byte
	n, _ := f.Read(b[:])
	return n > 0
}
