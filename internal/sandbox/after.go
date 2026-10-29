package sandbox

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// After is steps 11-12. It reports whether the sandbox is intact: one the
// session replaced is not followed, so the caller runs nothing in it.
func (b *Box) After() bool {
	b.unlink() // through the held root, whatever the paths lead to now
	if !b.Intact() {
		return false
	}
	// Step 12: a denial or a grant the CLI did not take fails the box.
	if n := countLines(filepath.Join(b.dir, "err.txt"), "auto-denied"); n > 0 {
		b.Note("auto-denied %d", n)
		b.Fail(ReasonDenied)
	}
	if log := newestLog(filepath.Join(b.sb, ".gemini", "antigravity-cli", "log")); log != "" {
		if n := countLines(log, "invalid allow entry"); n > 0 {
			b.Note("invalid allow entries %d", n)
			b.Fail(ReasonInvalid)
		}
	}
	return true
}

// unlink is step 11: warn when the token link was replaced or the real token
// changed kind, and remove the link and settings.json by name.
func (b *Box) unlink() {
	if root, done, err := b.sandboxRoot(); err == nil && b.linked {
		if st, err := root.Lstat(filepath.FromSlash(TokenRel)); err != nil || st.Mode()&os.ModeSymlink == 0 {
			b.Note("WARNING the token link was replaced; the real login may be stale")
		}
		done()
	}
	b.removeInSandbox(TokenRel)
	b.removeInSandbox(".gemini/antigravity-cli/settings.json")
	if st, err := os.Lstat(b.tok); err != nil || !st.Mode().IsRegular() {
		b.Note("WARNING %s is no longer a regular file", b.tok)
	}
}

// ExitChecks is step 14: leftover processes, then the real config, then new
// credentials, and last a SIGKILL to every agy group as a sweep.
func (b *Box) ExitChecks() {
	if b.root != nil {
		defer b.root.Close()
	}
	if b.linked {
		b.removeInSandbox(TokenRel) // gone even when a step after 9 returned early
	}
	// The approved strings, never a fresh resolution, and whether or not the
	// clone still exists: a clone the agent turned into a link to the real
	// home must not aim the SIGTERM there, and one it deleted must not skip
	// the check. Before step 2's resolution no agent has run, so a fresh resolution is
	// the clone the box just made.
	roots := []string{}
	for _, p := range []string{b.app, b.sb} {
		if r, ok := b.resolved[p]; ok {
			roots = append(roots, r)
		} else if r, err := filepath.EvalSymlinks(p); err == nil && b.resolved == nil {
			roots = append(roots, r)
		}
	}
	if len(roots) > 0 {
		if found := leftoverProcs(b.c.Out, b.verb(), b.sb, roots...); len(found) > 0 {
			b.Note("processes left by the run (BUG-163); each was sent SIGTERM: %s", strings.Join(found, ", "))
			b.Fail(ReasonProcs)
		}
	}
	if b.realBefore != nil {
		if changed := changedPaths(b.realBefore, fingerprint(b.c.RealHome, b.c.SkipStores)); len(changed) > 0 {
			b.Note("the real home's watched config changed (BUG-107, BUG-123): %s", strings.Join(changed, ", "))
			b.Fail(ReasonRealConfig)
		}
	}
	// After the leftover processes are gone: one of them could have replaced
	// the sandbox since After looked.
	if b.credsBefore != nil && (b.resolved == nil || b.Intact()) {
		if left := newCreds(b.credsBefore, scanCreds(b.dir)); len(left) > 0 {
			b.Note("credentials left in the sandbox (BUG-160): %s", strings.Join(left, ", "))
			b.Fail(ReasonCredentials)
		}
	}
	for _, g := range b.pgids {
		syscall.Kill(-g, syscall.SIGKILL)
	}
}

// countLines is grep -c: the lines of path holding s.
func countLines(path, s string) int {
	f, err := OpenRegular(path, os.O_RDONLY)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		if strings.Contains(sc.Text(), s) {
			n++
		}
	}
	return n
}

// newestLog is the newest cli-*.log of dir by mtime, or "".
func newestLog(dir string) string {
	logs, _ := filepath.Glob(filepath.Join(dir, "cli-*.log"))
	sort.Slice(logs, func(i, j int) bool {
		a, _ := os.Stat(logs[i])
		b, _ := os.Stat(logs[j])
		return a != nil && b != nil && a.ModTime().After(b.ModTime())
	})
	if len(logs) == 0 {
		return ""
	}
	return logs[0]
}

// Remove is `fyr work`'s step 9: the sandbox directory removed through the
// path guard a approved, only while it is intact; os.RemoveAll removes a
// link inside it, never the link's target. A directory the session left
// read-only (a Go module cache) is made writable first, never through a
// link: WalkDir reports a link as a link. False keeps the sandbox: one the
// session replaced fails sandbox replaced, as Intact does.
func (b *Box) Remove() bool {
	if b.resolved == nil || !b.Intact() {
		return false
	}
	_ = filepath.WalkDir(b.resolved[b.dir], func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			os.Chmod(p, 0o700)
		}
		return nil
	})
	if err := os.RemoveAll(b.resolved[b.dir]); err != nil {
		b.Note("the sandbox was not removed: %v", err)
		return false
	}
	return true
}
