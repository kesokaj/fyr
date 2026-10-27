package eval

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// afterSession is steps 11-14. A sandbox the session replaced is not
// followed: no removal through it, no check run in it.
func (w *row) afterSession(ctx context.Context, tok string) {
	w.unlink(tok) // through the held root, whatever the paths lead to now
	if !w.intact() {
		return
	}
	// Step 12: a denial or a grant the CLI did not take fails the row.
	if n := countLines(filepath.Join(w.dir, "err.txt"), "auto-denied"); n > 0 {
		w.note("auto-denied %d", n)
		w.fail(ReasonDenied)
	}
	if log := newestLog(filepath.Join(w.sb, ".gemini", "antigravity-cli", "log")); log != "" {
		if n := countLines(log, "invalid allow entry"); n > 0 {
			w.note("invalid allow entries %d", n)
			w.fail(ReasonInvalid)
		}
	}
	if ctx.Err() != nil {
		return
	}
	// Step 13: the row's check.
	if !w.bash(ctx, w.r.Check, w.checkEnv(), "check.log") {
		w.fail(ReasonCheck)
	}
	// Step 14: the agent the row names was invoked.
	if w.r.Agent != "" {
		ok, notes := invokedNotes(w.sb, w.r.Agent)
		for _, n := range notes {
			w.note("%s", n)
		}
		if !ok {
			w.fail(ReasonAgent(w.r.Agent))
		}
	}
}

// unlink is step 11: warn when the token link was replaced or the real token
// changed kind, and remove the link and settings.json by name.
func (w *row) unlink(tok string) {
	if root, done, err := w.sandboxRoot(); err == nil && w.linked {
		if st, err := root.Lstat(filepath.FromSlash(tokenRel)); err != nil || st.Mode()&os.ModeSymlink == 0 {
			w.note("WARNING the token link was replaced; the real login may be stale")
		}
		done()
	}
	w.removeInSandbox(tokenRel)
	w.removeInSandbox(".gemini/antigravity-cli/settings.json")
	if st, err := os.Lstat(tok); err != nil || !st.Mode().IsRegular() {
		w.note("WARNING %s is no longer a regular file", tok)
	}
}

// exitChecks is step 15: leftover processes, then the real config, then new
// credentials, and last a SIGKILL to every agy group as a sweep.
func (w *row) exitChecks() {
	if w.root != nil {
		defer w.root.Close()
	}
	if w.linked {
		w.removeInSandbox(tokenRel) // gone even when a step after 9 returned early
	}
	// The approved strings, never a fresh resolution, and whether or not the
	// clone still exists: a clone the agent turned into a link to the real
	// home must not aim the SIGTERM there, and one it deleted must not skip
	// the check. Before step 2b no agent has run, so a fresh resolution is
	// the clone eval just made.
	roots := []string{}
	for _, p := range []string{w.app, w.sb} {
		if r, ok := w.resolved[p]; ok {
			roots = append(roots, r)
		} else if r, err := filepath.EvalSymlinks(p); err == nil && w.resolved == nil {
			roots = append(roots, r)
		}
	}
	if len(roots) > 0 {
		if found := leftoverProcs(w.out, w.sb, roots...); len(found) > 0 {
			w.note("processes left by the run (BUG-163); each was sent SIGTERM: %s", strings.Join(found, ", "))
			w.fail(ReasonProcs)
		}
	}
	if w.realBefore != nil {
		if changed := changedPaths(w.realBefore, fingerprint(w.o.RealHome)); len(changed) > 0 {
			w.note("the real home's watched config changed (BUG-107, BUG-123): %s", strings.Join(changed, ", "))
			w.fail(ReasonRealConfig)
		}
	}
	// After the leftover processes are gone: one of them could have replaced
	// the sandbox since afterSession looked.
	if w.credsBefore != nil && (w.resolved == nil || w.intact()) {
		if left := newCreds(w.credsBefore, scanCreds(w.dir)); len(left) > 0 {
			w.note("credentials left in the sandbox (BUG-160): %s", strings.Join(left, ", "))
			w.fail(ReasonCredentials)
		}
	}
	for _, g := range w.pgids {
		syscall.Kill(-g, syscall.SIGKILL)
	}
}

// countLines is grep -c: the lines of path holding s.
func countLines(path, s string) int {
	f, err := openRegular(path, os.O_RDONLY)
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
