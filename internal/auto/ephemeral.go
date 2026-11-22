package auto

import (
	"path"
	"strings"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/search"
)

// IsEphemeralPath reports whether raw names a transient worktree, smoke test,
// eval/auto sandbox, or scratch directory that must neither be registered for
// cross-project reads nor retained/swept as a workspace by fyr auto (ADR-532).
func IsEphemeralPath(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == search.GlobalWorkspace {
		return false
	}
	clean := path.Clean(strings.ReplaceAll(trimmed, "\\", "/"))
	segs := strings.Split(clean, "/")
	for i, seg := range segs {
		if matchEphemeralSegment(segs, i, seg) {
			return true
		}
	}
	return false
}

func matchEphemeralSegment(segs []string, i int, seg string) bool {
	switch {
	case seg == "worktrees":
		return i+1 < len(segs) && segs[i+1] != ""
	case strings.HasPrefix(seg, "real-home-smoke-"), strings.HasPrefix(seg, "fyr-smoke-"):
		return true
	case strings.HasPrefix(seg, "smoke-"):
		return hasEphemeralSmokeAncestor(segs[:i])
	case seg == "sandbox":
		return i+1 < len(segs) && (segs[i+1] == "fyr-eval" || segs[i+1] == "fyr-auto")
	case seg == ".agents":
		return i+1 < len(segs) && segs[i+1] == "scratch"
	case seg == ".config":
		return hasConfigFyrEphemeralChild(segs[i:])
	}
	return false
}

func hasEphemeralSmokeAncestor(ancestors []string) bool {
	for j, a := range ancestors {
		if a == "sandbox" {
			return true
		}
		if a == ".config" && j+2 < len(ancestors) && ancestors[j+1] == "fyr" && ancestors[j+2] == "dev" {
			return true
		}
	}
	return false
}

func hasConfigFyrEphemeralChild(tail []string) bool {
	if len(tail) < 3 || tail[0] != ".config" || tail[1] != "fyr" {
		return false
	}
	switch tail[2] {
	case "auto", "eval", "sandbox":
		return true
	}
	return false
}

// filterSweepTargets removes ephemeral worktree/smoke/sandbox/scratch paths
// from a sweep's discovered targets so dead or transient directories are not
// swept or emitted as "skipped: gone" noise.
func filterSweepTargets(targets []dream.Target) (kept []dream.Target, skipped int) {
	kept = make([]dream.Target, 0, len(targets))
	for _, t := range targets {
		if IsEphemeralPath(t.Root) {
			skipped++
			continue
		}
		kept = append(kept, t)
	}
	return kept, skipped
}
