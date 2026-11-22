package dream

import (
	"os"
	"path"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

// IsEphemeralPath reports whether raw names a refused system root, transient
// worktree, smoke test, eval/auto/dream sandbox, or scratch directory that
// must neither be registered nor swept by fyr dream or fyr auto (ADR-532,
// ADR-534).
func IsEphemeralPath(raw string) bool {
	return IsRefusedWorkspace(raw)
}

// IsRefusedWorkspace reports whether raw is an ephemeral directory or a
// refused system root (`/`, `/tmp`, `/private/tmp`, exact `$HOME`, or exact
// match to any explicit home directory passed in homes).
func IsRefusedWorkspace(raw string, homes ...string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == search.GlobalWorkspace {
		return false
	}
	clean := cleanSlashPath(trimmed)
	if isRefusedSystemRoot(clean, homes) {
		return true
	}
	segs := strings.Split(clean, "/")
	for i, seg := range segs {
		if matchEphemeralSegment(segs, i, seg) {
			return true
		}
	}
	return false
}

func cleanSlashPath(p string) string {
	return path.Clean(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"))
}

func isRefusedSystemRoot(clean string, homes []string) bool {
	if clean == "/" || clean == "/tmp" || clean == "/private/tmp" {
		return true
	}
	for _, h := range homes {
		if ch := cleanHomeRoot(h); ch != "" && clean == ch {
			return true
		}
	}
	if envHome := cleanHomeRoot(os.Getenv("HOME")); envHome != "" && clean == envHome {
		return true
	}
	if userHome, err := os.UserHomeDir(); err == nil {
		if uh := cleanHomeRoot(userHome); uh != "" && clean == uh {
			return true
		}
	}
	return false
}

func cleanHomeRoot(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}
	c := cleanSlashPath(h)
	if c == "" || c == "." {
		return ""
	}
	return c
}

func matchEphemeralSegment(segs []string, i int, seg string) bool {
	switch {
	case seg == "worktrees":
		return i+1 < len(segs) && segs[i+1] != ""
	case strings.HasPrefix(seg, "real-home-smoke"), strings.HasPrefix(seg, "fyr-smoke-"):
		return true
	case strings.HasPrefix(seg, "smoke-"):
		return hasEphemeralSmokeAncestor(segs[:i])
	case seg == "sandbox":
		return i+1 < len(segs) && (segs[i+1] == "fyr-eval" || segs[i+1] == "fyr-auto")
	case seg == ".agents":
		return i+1 < len(segs) && segs[i+1] == "scratch"
	case seg == ".config":
		return hasConfigFyrEphemeralChild(segs[i:])
	case seg == ".gemini":
		return hasGeminiBrainScratch(segs[i:])
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
	case "auto", "eval", "sandbox", "dev":
		return true
	}
	return strings.HasPrefix(tail[2], "dream-agy-home-")
}

func hasGeminiBrainScratch(tail []string) bool {
	return len(tail) >= 5 && tail[0] == ".gemini" && tail[1] != "" && tail[2] == "brain" && tail[3] != "" && tail[4] == "scratch"
}

func filterRefusedProjects(projects []search.Project, home string) []search.Project {
	kept := make([]search.Project, 0, len(projects))
	for _, p := range projects {
		if IsRefusedWorkspace(p.Root, home) {
			continue
		}
		kept = append(kept, p)
	}
	return kept
}
