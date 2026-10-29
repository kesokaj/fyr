package sandbox

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// FyrEnv is the caller's environment for the sandbox's fyr calls, minus
// what would choose another harness or repository (D16).
func FyrEnv() []string {
	var out []string
	for _, kv := range WithoutGit(os.Environ()) {
		if k, _, _ := strings.Cut(kv, "="); k != "FYR_REPO" && k != "FYR_CHILD" {
			out = append(out, kv)
		}
	}
	return out
}

// agyEnv is exactly the allowlist of run.sh:220-222 (D27), with the git
// identity of Config.
func (b *Box) agyEnv() []string {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return []string{
		"PATH=" + filepath.Join(b.sb, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TERM=" + envOr("TERM", "dumb"),
		"LANG=" + envOr("LANG", "C.UTF-8"),
		"USER=" + envOr("USER", name),
		"LOGNAME=" + envOr("LOGNAME", name),
		"SHELL=/bin/bash",
		// Read by this box's credential broker, outside this repository: off,
		// so a session behaves here as the kit did (D27).
		"STYRMAN_SKIP_CRED_PULL=1",
		"HOME=" + b.sb,
		"GIT_AUTHOR_NAME=" + b.c.GitName, "GIT_AUTHOR_EMAIL=" + b.c.GitEmail,
		"GIT_COMMITTER_NAME=" + b.c.GitName, "GIT_COMMITTER_EMAIL=" + b.c.GitEmail,
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ShellQuote is s as one single-quoted bash word.
func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
