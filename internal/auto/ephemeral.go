package auto

import (
	"github.com/kesokaj/fyr/internal/dream"
)

// IsEphemeralPath reports whether raw names a refused system root, transient
// worktree, smoke test, eval/auto/dream sandbox, or scratch directory that
// must neither be registered for cross-project reads nor retained/swept as a
// workspace by fyr auto or fyr dream (ADR-532, ADR-534).
func IsEphemeralPath(raw string) bool {
	return dream.IsEphemeralPath(raw)
}

// filterSweepTargets removes ephemeral worktree/smoke/sandbox/scratch and
// refused system-root paths from a sweep's discovered targets so dead or
// transient directories are not swept or emitted as "skipped: gone" noise.
func filterSweepTargets(targets []dream.Target, homes ...string) (kept []dream.Target, skipped int) {
	kept = make([]dream.Target, 0, len(targets))
	for _, t := range targets {
		if dream.IsRefusedWorkspace(t.Root, homes...) {
			skipped++
			continue
		}
		kept = append(kept, t)
	}
	return kept, skipped
}
