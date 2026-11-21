package isolate

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
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

func baseSandboxEnv(pathVal, scratchHome, tmpDir string) []string {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return []string{
		"PATH=" + pathVal,
		"TERM=" + envOr("TERM", "dumb"),
		"LANG=" + envOr("LANG", "C.UTF-8"),
		"USER=" + envOr("USER", name),
		"LOGNAME=" + envOr("LOGNAME", name),
		"SHELL=/bin/bash",
		"HOME=" + scratchHome,
		"TMPDIR=" + tmpDir,
	}
}

// agyEnv is the allowlist of run.sh:220-222 (the eval plan's D27), with
// the git identity of Config, plus TZ when Config.Zone names one (workers
// D144); for eval, which sets no Zone, it is exactly the allowlist.
func (b *Box) agyEnv() []string {
	env := baseSandboxEnv(b.agyPATH(), b.sb, filepath.Join(b.dir, "tmp"))
	// Read by this box's credential broker, outside this repository: off,
	// so a session behaves here as the kit did (D27).
	env = append(env,
		"STYRMAN_SKIP_CRED_PULL=1",
		"GIT_AUTHOR_NAME="+b.c.GitName, "GIT_AUTHOR_EMAIL="+b.c.GitEmail,
		"GIT_COMMITTER_NAME="+b.c.GitName, "GIT_COMMITTER_EMAIL="+b.c.GitEmail,
	)
	if b.c.Zone != "" {
		env = append(env, "TZ="+b.c.Zone)
	}
	return env
}

func (b *Box) agyPATH() string {
	first := filepath.Join(b.sb, "bin")
	if !b.isolated() {
		return first + string(os.PathListSeparator) + os.Getenv("PATH")
	}
	allow := make([]string, 0, 3+len(b.c.TestRO)+len(b.c.TestRW))
	if b.dir != "" {
		allow = append(allow, b.sb, b.app, filepath.Join(b.dir, "tmp"))
	} else {
		if filepath.IsAbs(b.sb) {
			allow = append(allow, b.sb)
		}
		if filepath.IsAbs(b.app) {
			allow = append(allow, b.app)
		}
	}
	allow = append(allow, b.c.TestRO...)
	allow = append(allow, b.c.TestRW...)
	return confinedPATH(first, b.c.RealHome, allow)
}

type pathBound struct {
	abs string
	res string
}

// confinedPATH builds a sandbox-safe PATH from optional firstDir, existing
// GOROOT/bin (allowed by policyPaths), and host PATH directories that are not
// excluded or fall inside explicit allowPaths, deduplicated in order.
func confinedPATH(firstDir, realHome string, allowPaths []string) string {
	var out []string
	seen := make(map[string]bool)
	add := func(dir string) {
		if dir == "" {
			return
		}
		clean := filepath.Clean(dir)
		if !seen[clean] {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	add(firstDir)
	if gr := runtime.GOROOT(); gr != "" {
		goBin := filepath.Join(gr, "bin")
		if st, err := os.Stat(goBin); err == nil && st.IsDir() {
			add(goBin)
		}
	}
	for _, dir := range safeHostPATHDirs(realHome, allowPaths) {
		add(dir)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

func safeHostPATHDirs(realHome string, allowPaths []string) []string {
	tmpRoot := filepath.Clean(os.TempDir())
	resTmp, _ := resolve(tmpRoot)
	var resHome string
	if realHome != "" {
		resHome, _ = resolve(realHome)
	}
	bounds := buildAllowBounds(allowPaths)
	var out []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		resAbs, err := resolve(abs)
		allowed := err == nil && !isExcludedPATHDir(abs, resAbs, tmpRoot, resTmp, realHome, resHome)
		if !allowed && !inAllowBounds(abs, resAbs, bounds) {
			continue
		}
		out = append(out, abs)
	}
	return out
}

func buildAllowBounds(paths []string) []pathBound {
	bounds := make([]pathBound, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		res, _ := resolve(abs)
		if res != "" {
			res = filepath.Clean(res)
		}
		bounds = append(bounds, pathBound{abs: abs, res: res})
	}
	return bounds
}

func inAllowBounds(abs, resAbs string, bounds []pathBound) bool {
	for _, b := range bounds {
		if b.abs != "" && within(abs, b.abs) {
			return true
		}
		if resAbs != "" && b.res != "" && within(resAbs, b.res) {
			return true
		}
	}
	return false
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ShellQuote is s as one single-quoted bash word.
func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
