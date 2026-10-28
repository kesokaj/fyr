package harness

import (
	"path/filepath"
	"strings"
)

// DreamWork is the dream's empty working directory under a home. A
// conversation whose workspace is this directory is the dream's own, never a
// project's: neither the metrics ingest nor any project's dream reads it
// (plan 2026-10-28 visible dream, D5).
func DreamWork(home string) string {
	return filepath.Join(home, StateDir, "dream-work")
}

// DreamWorkPaths is DreamWork(home) as written and, when it differs, its
// form with the symlinks followed: agy may store either (D15). The deepest
// part of the path that exists is resolved and the rest joined back, so a
// symlinked home or config directory still matches a dream conversation
// recorded before dream-work was removed.
func DreamWorkPaths(home string) []string {
	work := filepath.Clean(DreamWork(home))
	out := []string{work}
	if real, ok := resolveExisting(work); ok && real != work {
		out = append(out, real)
	}
	return out
}

// resolveExisting is p with the symlinks of its deepest existing ancestor
// followed and the missing rest joined back.
func resolveExisting(p string) (string, bool) {
	rest := ""
	for d := p; ; d = filepath.Dir(d) {
		if real, err := filepath.EvalSymlinks(d); err == nil {
			return filepath.Clean(filepath.Join(real, rest)), true
		}
		if filepath.Dir(d) == d {
			return "", false
		}
		rest = filepath.Join(filepath.Base(d), rest)
	}
}

// InDreamWork reports whether p is one of dirs or below one of them.
func InDreamWork(p string, dirs []string) bool {
	p = filepath.Clean(p)
	for _, d := range dirs {
		if p == d || strings.HasPrefix(p, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
