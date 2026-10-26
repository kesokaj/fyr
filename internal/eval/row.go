package eval

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
)

// row is one sandbox's state as the steps fill it.
type row struct {
	o            Options
	r            Row
	dir, app, sb string
	out          io.Writer
	reasons      []string
	pgids        []int
	realBefore   map[string]string
	credsBefore  map[string]credFinding
	linked       bool
	stamp        string // {{STAMP}}: the row's start, %F-%H%M
	// resolved holds dir, app and sb as guard a approved them. The agent can
	// write in dir, so every later step that follows those paths first checks
	// they still resolve here (intact), and check e matches these strings.
	resolved map[string]string
	// root is SB, opened once guard a approved it. A held handle follows the
	// directory through a rename, so the token link is removed from the home
	// it was made in, whatever the path leads to by then.
	root *os.Root
}

func (w *row) fail(reason string) { w.reasons = append(w.reasons, reason) }

func (w *row) note(format string, a ...any) {
	fmt.Fprintln(w.out, printable(fmt.Sprintf("eval: "+w.r.ID+": "+format, a...)))
}

func (w *row) has(reason string) bool {
	for _, r := range w.reasons {
		if r == reason {
			return true
		}
	}
	return false
}

// intact reports whether the sandbox directory, the clone and the sandbox
// home still resolve to what guard a approved, and fails the row once when
// they do not: the agent can replace any of them with a link to the real
// home, and eval removes, scans and ingests through them.
func (w *row) intact() bool {
	for p, want := range w.resolved {
		st, err := os.Lstat(p)
		got, rerr := filepath.EvalSymlinks(p)
		if err != nil || rerr != nil || !st.IsDir() || got != want {
			w.replaced("%s no longer resolves to %s; nothing is removed, run or scanned through it", p, want)
			return false
		}
	}
	return true
}

// replaced notes what the agent changed and fails the row sandbox replaced,
// once.
func (w *row) replaced(format string, a ...any) {
	w.note(format, a...)
	if !w.has(ReasonReplaced) {
		w.fail(ReasonReplaced)
	}
}

// sandboxRoot is the held os.Root on SB, or one opened now when prepare did
// not get that far. An os.Root refuses a path that a link leads out of the
// home (the real token sits at the other end of one). done closes only a
// root opened here.
func (w *row) sandboxRoot() (root *os.Root, done func(), err error) {
	if w.root != nil {
		return w.root, func() {}, nil
	}
	root, err = os.OpenRoot(w.sb)
	if err != nil {
		return nil, nil, err
	}
	return root, func() { root.Close() }, nil
}

// removeInSandbox removes rel inside the sandbox home, never through a link
// that leads out of it; one that does fails the row sandbox replaced.
func (w *row) removeInSandbox(rel string) {
	root, done, err := w.sandboxRoot()
	if err == nil {
		err = root.Remove(filepath.FromSlash(rel))
		done()
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		w.replaced("not removed: %s: %v", rel, err)
	}
}

// openRegular opens path with flag, refusing a link (O_NOFOLLOW) and
// anything that is not a regular file, without blocking on a FIFO the agent
// left at a name eval reads or writes.
func openRegular(path string, flag int) (*os.File, error) {
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return f, nil
}

// printable is search.Printable's filter, which eval cannot import (D10):
// control and format characters, the line separators and the ignorables
// become '?', so a row id or a path never forges a line. A test pins the two
// equal.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) ||
			unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || unicode.Is(unicode.Cf, r) ||
			unicode.Is(unicode.Variation_Selector, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) {
			return '?'
		}
		return r
	}, s)
}
