package eval

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// checkEnv is run.sh's check environment (:237): env -i, eval's own PATH
// (the wrapper directory is never on it), HOME=SB, and the row's paths.
func (w *row) checkEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + w.sb,
		"SB=" + w.sb,
		"OUT=" + filepath.Join(w.dir, "out.txt"),
		"OUT2=" + filepath.Join(w.dir, "out2.txt"),
		"FIXDIR=" + w.o.Fixture,
		"CLONE=" + w.app,
	}
}

// setupEnv is the check's, plus the git identity run.sh's gi() used.
func (w *row) setupEnv() []string {
	return append(w.checkEnv(),
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=f@localhost",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=f@localhost")
}

// fyrEnv is eval's own environment for the sandbox's fyr calls, minus what
// would choose another harness or repository (D16).
func (w *row) fyrEnv() []string {
	var out []string
	for _, kv := range withoutGit(os.Environ()) {
		if k, _, _ := strings.Cut(kv, "="); k != "FYR_REPO" && k != "FYR_CHILD" {
			out = append(out, kv)
		}
	}
	return out
}

// agyEnv is exactly the allowlist of run.sh:220-222 (D27).
func (w *row) agyEnv() []string {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return []string{
		"PATH=" + filepath.Join(w.sb, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TERM=" + envOr("TERM", "dumb"),
		"LANG=" + envOr("LANG", "C.UTF-8"),
		"USER=" + envOr("USER", name),
		"LOGNAME=" + envOr("LOGNAME", name),
		"SHELL=/bin/bash",
		// Read by this box's credential broker, outside this repository: off,
		// so eval behaves here as the kit did (D27).
		"STYRMAN_SKIP_CRED_PULL=1",
		"HOME=" + w.sb,
		"GIT_AUTHOR_NAME=agy-rel", "GIT_AUTHOR_EMAIL=agy-rel@localhost",
		"GIT_COMMITTER_NAME=agy-rel", "GIT_COMMITTER_EMAIL=agy-rel@localhost",
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// shellQuote is s as one single-quoted bash word.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
