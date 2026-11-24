package check

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

// open lists what git tracks at top and opens the checkout as an os.Root.
func open(top string) (*checker, error) {
	out, err := trail.Command(top, "ls-files", "-z").Output()
	if err != nil {
		return nil, refusal{fmt.Sprintf("git ls-files in %s: %v", top, err)}
	}
	root, err := os.OpenRoot(top)
	if err != nil {
		return nil, refusal{err.Error()}
	}
	k := &checker{top: top, root: root, topDirs: map[string]bool{}, topFiles: map[string]bool{}, lines: map[string]int{}, links: map[string]bool{}, headings: map[string][]string{}}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		k.tracked = append(k.tracked, p)
		if first, _, nested := strings.Cut(p, "/"); nested {
			k.topDirs[first] = true
		} else {
			k.topFiles[first] = true
		}
	}
	return k, nil
}

// readFile reads a regular file of at most maxLineRead bytes inside the
// checkout; a symlink or anything larger is an error (os.Root has no
// ReadFile before Go 1.25, and the go line is 1.24).
func (k *checker) readFile(name string) ([]byte, error) {
	fi, err := k.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxLineRead {
		return nil, fmt.Errorf("%s: not a regular file of at most %d bytes", name, maxLineRead)
	}
	f, err := k.openRegular(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxLineRead))
}

// openRegular opens name without blocking and checks the opened file, not
// the name: what was swapped in after readFile's Lstat is refused unless it
// is a regular file of at most maxLineRead bytes.
func (k *checker) openRegular(name string) (*os.File, error) {
	f, err := k.root.OpenFile(name, os.O_RDONLY|nonBlock|noFollow, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err == nil && (!fi.Mode().IsRegular() || fi.Size() > maxLineRead) {
		err = fmt.Errorf("%s: not a regular file of at most %d bytes", name, maxLineRead)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (k *checker) git(args ...string) *exec.Cmd { return trail.Command(k.top, args...) }

// noIndexed returns the check of whether a directory or one above it holds
// a noindex marker, tracked or not; each answer is kept.
func (k *checker) noIndexed() func(dir string) bool {
	marked := map[string]bool{}
	for _, p := range k.tracked {
		if trail.IsNoIndexMarker(p) {
			marked[path.Dir(p)] = true
		}
	}
	return func(dir string) bool {
		for d := dir; ; d = path.Dir(d) {
			if _, seen := marked[d]; !seen {
				m := false
				for _, name := range trail.NoIndexMarkers {
					if _, err := k.root.Lstat(filepath.Join(filepath.FromSlash(d), name)); err == nil {
						m = true
					}
				}
				marked[d] = m
			}
			if marked[d] {
				return true
			}
			if d == "." {
				return false
			}
		}
	}
}
