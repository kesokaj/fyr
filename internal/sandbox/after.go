package sandbox

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
	denied := b.count(filepath.Join(b.dir, "err.txt"), "auto-denied")
	log := newestLog(filepath.Join(b.sb, ".gemini", "antigravity-cli", "log"))
	if denied == 0 && log != "" {
		denied = b.count(log, "soft-denying tool confirmation")
	}
	if denied > 0 {
		b.Note("auto-denied %d", denied)
		b.Fail(ReasonDenied)
	}
	if log != "" {
		if n := b.count(log, "invalid allow entry"); n > 0 {
			b.Note("invalid allow entries %d", n)
			b.Fail(ReasonInvalid)
		}
	}
	return true
}

// count is countLines for step 12. A file that cannot be read says nothing
// about what was denied, so it fails the box as fileFailed says (PLAN.md
// item 49), unless no session ran to write it.
func (b *Box) count(path, s string) int {
	n, err := countLines(path, s)
	if err != nil && (b.started || !errors.Is(err, fs.ErrNotExist)) {
		b.fileFailed(err, "not read: %v", err)
	}
	return n
}

// unlink is step 11: warn when a login link was replaced or the real login
// changed kind, and remove the link(s) and settings.json by name.
func (b *Box) unlink() {
	if root, done, err := b.sandboxRoot(); err == nil {
		if b.tokLinked {
			if st, err := root.Lstat(filepath.FromSlash(TokenRel)); err != nil || st.Mode()&os.ModeSymlink == 0 {
				b.Note("WARNING the token link was replaced; the real login may be stale")
			}
		}
		if b.keychainsLinked {
			if st, err := root.Lstat(filepath.FromSlash(KeychainsRel)); err != nil || st.Mode()&os.ModeSymlink == 0 {
				b.Note("WARNING the Keychains link was replaced; the real login may be stale")
			}
		}
		done()
	}
	b.removeLinks()
	b.removeInSandbox(".gemini/antigravity-cli/settings.json")
	if b.tok != "" {
		if st, err := os.Lstat(b.tok); err != nil || !st.Mode().IsRegular() {
			b.Note("WARNING %s is no longer a regular file", b.tok)
		}
	}
	b.checkKeychains()
}

func (b *Box) checkKeychains() {
	if b.keychains == "" {
		return
	}
	if st, err := os.Lstat(b.keychains); err != nil || !st.IsDir() {
		b.Note("WARNING %s is no longer a directory", b.keychains)
		return
	}
	for _, f := range b.keychainFiles {
		if st, err := os.Lstat(f); err != nil || !st.Mode().IsRegular() {
			b.Note("WARNING %s is no longer a regular file", f)
		}
	}
}

func (b *Box) removeLinks() {
	b.removeInSandbox(TokenRel)
	if b.keychains != "" || b.keychainsLinked {
		b.removeInSandbox(KeychainsRel)
	}
}

// ExitChecks is step 14: leftover processes (a docker-mode session checked
// its container at its end), then the real config, then new credentials,
// and last a SIGKILL to every agy group as a sweep.
func (b *Box) ExitChecks() {
	if b.root != nil {
		defer b.root.Close()
	}
	if b.tokLinked || b.keychainsLinked {
		b.removeLinks() // gone even when a step after 9 returned early
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
		found, err := leftoverProcs(b.c.Out, b.verb(), b.sb, roots...)
		if len(found) > 0 {
			b.Note("processes left by the run (BUG-163); each was sent SIGTERM: %s", strings.Join(found, ", "))
		}
		if err != nil {
			b.Note("the leftover-process check could not read /proc: %v", err)
		}
		if len(found) > 0 || err != nil {
			b.Fail(ReasonProcs)
		}
	}
	if b.realBefore != nil {
		if changed := changedPathsIn(b.c.RealHome, b.realBefore, fingerprintWith(b.c.RealHome, b.c.SkipStores, b.c.BrainWatch)); len(changed) > 0 {
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
func countLines(path, s string) (int, error) {
	f, err := OpenRegular(path, os.O_RDONLY)
	if err != nil {
		return 0, err
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
	return n, sc.Err()
}

// newestLog is the newest cli-*.log of dir by mtime, or "".
func newestLog(dir string) string {
	entries, _ := os.ReadDir(dir)
	var newest string
	var newestTime int64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "cli-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		if info, err := e.Info(); err == nil && (newest == "" || info.ModTime().UnixNano() > newestTime) {
			newest = filepath.Join(dir, name)
			newestTime = info.ModTime().UnixNano()
		}
	}
	return newest
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

// cleanName keeps the characters the preamble kept, at most 40 of them.
func cleanName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._+-", r)) && b.Len() < 40 {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "?"
	}
	return b.String()
}
