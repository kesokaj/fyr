package trail

import "strings"

// AllowEnv returns the entries of env whose name is one of names, in env's
// order. A name ending in `*` is a prefix (`LC_*`). It builds the environment
// of a child that reads input fyr did not write, which gets only what it
// needs, never fyr's whole environment minus a deny-list (ADR-067).
func AllowEnv(env []string, names ...string) []string {
	out := env[:0:0]
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if ok && allowed(name, names) {
			out = append(out, kv)
		}
	}
	return out
}

func allowed(name string, names []string) bool {
	for _, n := range names {
		if p, ok := strings.CutSuffix(n, "*"); ok {
			if strings.HasPrefix(name, p) {
				return true
			}
		} else if name == n {
			return true
		}
	}
	return false
}
