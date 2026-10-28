package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// registerClone registers a row's clone under the eval home before its
// sandbox is ingested, so the metrics join reads the clone's trail and its
// eval=<row> sessions get their handoff, testing, review and bug events
// (ledger item 76). The operator's run of fyr eval is the consent; the
// marker goes into the clone's own git directory, which dies with the
// sandbox, and the seal names the eval home's key, never the real home's.
//
// The agent ran with command(*) in the clone, so its git directory is
// checked first: a clone that is a link, or whose git directory resolves
// outside the clone (a .git file or commondir pointing at another
// repository), is not registered, because the marker would be written there.
// A --home that is the real home, or whose state directory is the real one
// through a link, is refused: the seal would take the real key, and the join
// would load the trail the agent wrote into the real brain store. Where eval
// cannot see the processes a row left (procsChecked false, every system but
// Linux), one still running could swap the git directory between that check
// and the write, so nothing is registered and the sessions get no record
// events; that is a known limit there, not a failed ingest, which would make
// every run exit 1 (the phase's security review, rounds 1 and 2).
func registerClone(home, sandbox string, procsChecked bool) error {
	if real, shared := sharesRealState(home); shared {
		return fmt.Errorf("--home is the real home %s, or shares its state directory; eval registers a clone only under a separate home", real)
	}
	if !procsChecked {
		return nil
	}
	app := filepath.Join(sandbox, "app")
	fi, err := os.Lstat(app)
	if os.IsNotExist(err) {
		return nil // the row stopped before its clone: nothing to join
	}
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", app)
	}
	out, err := trail.Command(app, "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("git rev-parse in %s: %w", app, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	realApp, err := filepath.EvalSymlinks(app)
	if err != nil || len(lines) != 2 {
		return fmt.Errorf("%s is not its own repository", app)
	}
	for _, p := range lines {
		r, err := filepath.EvalSymlinks(p)
		if err != nil || (r != realApp && !strings.HasPrefix(r, realApp+string(os.PathSeparator))) {
			return fmt.Errorf("%s's repository is not inside it", app)
		}
	}
	_, err = trail.Register(filepath.Join(home, harness.StateDir), app, time.Now())
	return err
}
