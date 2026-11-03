package brain

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// OpenBodyFile opens a --body-file path for reading: Lstat and O_NOFOLLOW
// refuse a symlink, a directory, a FIFO or any other non-regular file, and
// Stat on the opened descriptor checks that the file was not swapped.
func OpenBodyFile(path string) (*os.File, error) {
	if path == "" {
		return nil, usage("--body-file needs a path")
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, refuse("--body-file %s: %v", path, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, refuse("--body-file %s is a symlink", path)
	}
	if !fi.Mode().IsRegular() {
		return nil, refuse("--body-file %s is not a regular file", path)
	}
	f, err := openNonBlocking(path)
	if err != nil {
		return nil, refuse("--body-file %s: %v", path, err)
	}
	fi2, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, refuse("--body-file %s: %v", path, err)
	}
	if !fi2.Mode().IsRegular() || !os.SameFile(fi, fi2) {
		f.Close()
		return nil, refuse("--body-file %s is not the same regular file", path)
	}
	return f, nil
}

// RefusePipedBodyFile is the refusal when --body-file is given together with
// piped or redirected stdin.
func RefusePipedBodyFile() error {
	return refuse("--body-file cannot be used with piped stdin")
}

// StdinPiped reports whether r carries piped or redirected input: a regular
// file redirected onto stdin, or a pipe/reader with at least one byte ready
// without blocking on an idle pipe or FIFO.
func StdinPiped(r io.Reader) bool {
	if f, ok := r.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil {
			return false
		}
		if fi.Mode()&os.ModeCharDevice != 0 {
			return false
		}
		if fi.Mode().IsRegular() {
			return true
		}
		return hasPipedByte(f)
	}
	var b [1]byte
	n, _ := r.Read(b[:])
	return n > 0
}

// CheckScratchOut verifies that outPath resolves to a single-link regular-file
// path under dir's checkout .agents/scratch/, with no .. segment and no
// existing symlink along .agents, .agents/scratch, any parent directory, or
// the target file.
func CheckScratchOut(dir, outPath string) (top string, segs []string, err error) {
	_, top, err = resolveProject(dir)
	if err != nil {
		return "", nil, err
	}
	sub, err := scratchRel(top, outPath)
	if err != nil {
		return "", nil, err
	}
	segs = strings.Split(sub, "/")
	if sub == "" || len(segs) == 0 {
		return "", nil, refuse("--out %s must name a file under .agents/scratch/", outPath)
	}
	for _, seg := range segs {
		if seg == "" || seg == "." || seg == ".." {
			return "", nil, refuse("--out %s is not a valid path under .agents/scratch/", outPath)
		}
	}
	cur := top
	for _, seg := range append([]string{".agents", "scratch"}, segs[:len(segs)-1]...) {
		cur = filepath.Join(cur, seg)
		fi, err := os.Lstat(cur)
		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			return "", nil, refuse("--out %s: %v", outPath, err)
		case fi.Mode()&os.ModeSymlink != 0:
			return "", nil, refuse("--out %s: %s is a symlink", outPath, cur)
		case !fi.IsDir():
			return "", nil, refuse("--out %s: %s is not a directory", outPath, cur)
		}
	}
	target := filepath.Join(top, ".agents", "scratch", filepath.FromSlash(sub))
	if fi, err := os.Lstat(target); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", nil, refuse("--out %s: %s is a symlink", outPath, target)
		}
		if !fi.Mode().IsRegular() {
			return "", nil, refuse("--out %s: %s is not a regular file", outPath, target)
		}
		if hasMultipleLinks(fi) {
			return "", nil, refuse("--out %s: %s is a hardlink", outPath, target)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, refuse("--out %s: %v", outPath, err)
	}
	return top, segs, nil
}

func scratchRel(top, outPath string) (string, error) {
	if outPath == "" {
		return "", usage("--out needs a path under .agents/scratch/")
	}
	slashed := filepath.ToSlash(outPath)
	if strings.HasSuffix(slashed, "/") {
		return "", refuse("--out %s must name a file under .agents/scratch/", outPath)
	}
	for _, seg := range strings.Split(slashed, "/") {
		if seg == ".." {
			return "", refuse("--out %s must stay under .agents/scratch/", outPath)
		}
	}
	if !filepath.IsAbs(outPath) {
		for strings.HasPrefix(slashed, "./") {
			slashed = strings.TrimPrefix(slashed, "./")
		}
		sub, ok := strings.CutPrefix(slashed, ".agents/scratch/")
		if !ok || sub == "" {
			return "", refuse("--out %s is outside .agents/scratch/", outPath)
		}
		return sub, nil
	}
	prefix, sub, ok := strings.Cut(slashed, "/.agents/scratch/")
	if !ok || prefix == "" || sub == "" {
		return "", refuse("--out %s is outside .agents/scratch/", outPath)
	}
	realPrefix, err := filepath.EvalSymlinks(filepath.FromSlash(prefix))
	if err != nil || filepath.Clean(realPrefix) != top {
		return "", refuse("--out %s is outside .agents/scratch/", outPath)
	}
	return sub, nil
}

// WriteScratch writes body (with a trailing newline) to outPath under dir's
// checkout .agents/scratch/, walking held directory descriptors with *os.Root
// and creating a new single-link inode with O_CREATE|O_EXCL so neither a
// symlink nor a hardlink can redirect or truncate another file.
func WriteScratch(dir, outPath, body string) error {
	top, segs, err := CheckScratchOut(dir, outPath)
	if err != nil {
		return err
	}
	curRoot, err := os.OpenRoot(top)
	if err != nil {
		return refuse("--out %s: %v", outPath, err)
	}
	defer func() {
		if curRoot != nil {
			curRoot.Close()
		}
	}()
	for _, seg := range append([]string{".agents", "scratch"}, segs[:len(segs)-1]...) {
		fi, err := curRoot.Lstat(seg)
		if errors.Is(err, os.ErrNotExist) {
			if err := curRoot.Mkdir(seg, 0o755); err != nil {
				return refuse("--out %s: %v", outPath, err)
			}
			fi, err = curRoot.Lstat(seg)
		}
		if err != nil {
			return refuse("--out %s: %v", outPath, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return refuse("--out %s: %s is a symlink", outPath, seg)
		}
		if !fi.IsDir() {
			return refuse("--out %s: %s is not a directory", outPath, seg)
		}
		nextRoot, err := curRoot.OpenRoot(seg)
		if err != nil {
			return refuse("--out %s: %v", outPath, err)
		}
		fi2, err := nextRoot.Stat(".")
		if err != nil || !fi2.IsDir() || !os.SameFile(fi, fi2) {
			nextRoot.Close()
			return refuse("--out %s: %s changed during open", outPath, seg)
		}
		curRoot.Close()
		curRoot = nextRoot
	}
	leaf := segs[len(segs)-1]
	if fi, err := curRoot.Lstat(leaf); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return refuse("--out %s: %s is a symlink", outPath, leaf)
		}
		if !fi.Mode().IsRegular() {
			return refuse("--out %s: %s is not a regular file", outPath, leaf)
		}
		if hasMultipleLinks(fi) {
			return refuse("--out %s: %s is a hardlink", outPath, leaf)
		}
		if err := curRoot.Remove(leaf); err != nil {
			return refuse("--out %s: %v", outPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return refuse("--out %s: %v", outPath, err)
	}
	f, err := openScratchFile(curRoot, leaf)
	if err != nil {
		return refuse("--out %s: %v", outPath, err)
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil {
		return refuse("--out %s: %v", outPath, err)
	} else if !fi.Mode().IsRegular() || hasMultipleLinks(fi) {
		return refuse("--out %s: %s is not a single-link regular file", outPath, leaf)
	}
	if _, err := io.WriteString(f, body+"\n"); err != nil {
		return refuse("--out %s: %v", outPath, err)
	}
	return f.Close()
}
