package isolate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Intact reports whether the sandbox directory, the clone and the sandbox
// home still resolve to what guard a approved, and fails the box once when
// they do not, or when a file system is mounted at, under or above them: the agent
// can replace any of them with a link to the real home, or mount over them,
// and the steps remove, scan and ingest through them.
func (b *Box) Intact() bool {
	for p, want := range b.resolved {
		st, err := os.Lstat(p)
		got, rerr := filepath.EvalSymlinks(p)
		if err != nil || rerr != nil || !st.IsDir() || got != want {
			b.replaced("%s no longer resolves to %s; nothing is removed, run or scanned through it", p, want)
			return false
		}
	}
	if len(b.resolved) == 0 {
		return true
	}
	roots := make([]string, 0, len(b.resolved))
	for _, want := range b.resolved {
		roots = append(roots, want)
	}
	found, err := mountsUnder(roots...)
	if err != nil {
		b.replaced("the mount table could not be read: %v; nothing is removed, run or scanned through the sandbox", err)
		return false
	}
	if len(found) > 0 {
		for i, m := range found {
			found[i] = Printable(m)
		}
		b.replaced("a file system is mounted under the sandbox: %s; nothing is removed, run or scanned through it", strings.Join(found, ", "))
		return false
	}
	chains, err := mountChains(roots...)
	if err != nil {
		b.replaced("the mount table could not be read: %v; nothing is removed, run or scanned through the sandbox", err)
		return false
	}
	for r, chain := range b.mounts {
		if chains[r] != chain {
			b.replaced("a file system is mounted above %s; nothing is removed, run or scanned through it", Printable(r))
			return false
		}
	}
	return true
}

// replaced notes what the agent changed and fails the box sandbox replaced,
// once.
func (b *Box) replaced(format string, a ...any) {
	b.Note(format, a...)
	b.failOnce(ReasonReplaced)
}

// fileFailed fails the box for a file of its own it could not open or
// remove: sandbox write failed when the machine is answerable (machineErr)
// and no session has started, so no agent ran that could have caused it,
// and eval does not grade it; else sandbox replaced, the graded direction:
// a planted link, FIFO, directory or hard link is what a run leaves, and
// after a session the agent, as the operator's uid, can exhaust descriptors
// or memory or mount a file system of its own (PLAN.md items 1 and 48; P6's
// security review, finding 1). Once each.
func (b *Box) fileFailed(err error, format string, a ...any) {
	if b.Started() || !machineErr(err) {
		b.replaced(format, a...)
		return
	}
	b.Note(format, a...)
	b.failOnce(ReasonWrite)
}

// machineErrs are the errors of the machine, not of what is at the path.
var machineErrs = []error{syscall.ENOSPC, syscall.EDQUOT, syscall.EIO, syscall.EROFS, syscall.EMFILE, syscall.ENFILE, syscall.ENOMEM}

func machineErr(err error) bool {
	for _, e := range machineErrs {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// sandboxRoot is the held os.Root on SB, or one opened now when Clone did
// not get that far. An os.Root refuses a path that a link leads out of the
// home (the real token sits at the other end of one). done closes only a
// root opened here.
func (b *Box) sandboxRoot() (root *os.Root, done func(), err error) {
	if b.root != nil {
		return b.root, func() {}, nil
	}
	root, err = os.OpenRoot(b.sb)
	if err != nil {
		return nil, nil, err
	}
	return root, func() { root.Close() }, nil
}

// removeInSandbox removes rel inside the sandbox home, never through a link
// that leads out of it; one that does fails the box sandbox replaced.
func (b *Box) removeInSandbox(rel string) {
	root, done, err := b.sandboxRoot()
	if err == nil {
		err = root.Remove(filepath.FromSlash(rel))
		done()
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		b.fileFailed(err, "not removed: %s: %v", rel, err)
	}
}
