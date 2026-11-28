package main

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/kesokaj/fyr/internal/auto"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// now is the clock `index --register` dates the marker with; a test replaces it.
var now = time.Now

// runRegister is `fyr index --register`: the operator's consent that this
// checkout reaches the cross-project consumers (ADR-068). It skips ephemeral
// worktree/smoke/sandbox/scratch paths (ADR-532), writes the marker in the
// checkout's git common directory, then indexes as a plain `fyr index`, whose
// prepare registers the checkout in the global search index now that the marker
// exists. The marker comes first, so a failed index leaves a valid one, and the
// error says so.
func runRegister(c *runtimeCtx, o search.Options) error {
	if o.Global {
		fmt.Fprintln(c.out, "fyr index: --register and --global do not go together")
		return exitCode(2)
	}
	main, top, err := trail.Roots(o.Dir)
	if errors.Is(err, trail.ErrNotCheckout) {
		fmt.Fprintln(c.out, "fyr index: --register needs a git checkout")
		return exitCode(2)
	}
	if err != nil {
		return fmt.Errorf("index --register: %w", err)
	}
	jsonOut := indexOptsOf(c).jsonOut
	var buf bytes.Buffer
	out := c.out
	if jsonOut {
		out = &buf
	}
	if eph := ephemeralRegisterTarget(o.Dir, top, main); eph != "" {
		fmt.Fprintln(out, search.Printable("note: skipping registration for ephemeral path "+eph))
		if jsonOut {
			return writeIndexJSON(c, o.Dir, buf.String())
		}
		return nil
	}
	if _, err = trail.Register(filepath.Join(c.home, harness.StateDir), o.Dir, now()); err != nil {
		return fmt.Errorf("index --register: %w", err)
	}
	if err := search.Index(o, out); err != nil {
		return fmt.Errorf("registered %s, but the index failed: %w", main, err)
	}
	fmt.Fprintf(out, "registered %s\n", main)
	// The marker is consent, not yet a reader: say so when nothing takes it.
	root := search.RegistryRoot(o.Dir)
	if ok, err := search.HasScopeRecords(c.home, root); err == nil && !ok {
		fmt.Fprintln(out, search.Printable("note: "+root+" has no .agents/trail.jsonl, so no cross-project read takes it until it has records (fyr record import, or the first fyr record add)"))
	}
	if jsonOut {
		return writeIndexJSON(c, o.Dir, buf.String())
	}
	return nil
}

func ephemeralRegisterTarget(dir, top, main string) string {
	for _, p := range []string{top, main, dir} {
		if auto.IsEphemeralPath(p) {
			return p
		}
	}
	return ""
}
