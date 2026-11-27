package isolate

import (
	"context"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kesokaj/fyr/internal/trail"
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
		// Keep Node 22+/26+ from writing npm's built-in PEM private-key help
		// docstring into $TMPDIR/node-compile-cache so fail-closed credential
		// scans stay clean without weakening creds.go.
		"NODE_DISABLE_COMPILE_CACHE=1",
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

func effectiveRealHome(realHome string) string {
	if realHome != "" {
		return realHome
	}
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return ""
}

// confinedPATH builds a sandbox-safe PATH from optional firstDir, explicitly
// allowed host PATH directories, existing GOROOT/bin (allowed by policyPaths),
// and remaining non-excluded host PATH directories, deduplicated in order.
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
	explicitDirs, hostDirs := safeHostPATHDirs(realHome, allowPaths)
	for _, dir := range explicitDirs {
		add(dir)
	}
	if gr := resolveGoRoot(); gr != "" {
		goBin := filepath.Join(gr, "bin")
		if st, err := os.Stat(goBin); err == nil && st.IsDir() {
			add(goBin)
		}
	}
	for _, dir := range hostDirs {
		add(dir)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

func safeHostPATHDirs(realHome string, allowPaths []string) (explicitDirs, hostDirs []string) {
	realHome = effectiveRealHome(realHome)
	tmpRoot := filepath.Clean(os.TempDir())
	resTmp, _ := resolve(tmpRoot)
	var resHome string
	if realHome != "" {
		resHome, _ = resolve(realHome)
	}
	resBounds, rawBounds := buildAllowBounds(allowPaths)
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		abs := filepath.Clean(dir)
		resAbs, err := resolve(abs)
		if err != nil || resAbs == "" {
			continue
		}
		if inAllowBounds(resAbs, resBounds) {
			if inAllowBounds(abs, rawBounds) {
				explicitDirs = append(explicitDirs, abs)
			} else {
				explicitDirs = append(explicitDirs, resAbs)
			}
			continue
		}
		if !isExcludedPATHDir(abs, resAbs, tmpRoot, resTmp, realHome, resHome) {
			hostDirs = append(hostDirs, abs)
		}
	}
	return explicitDirs, hostDirs
}

func buildAllowBounds(paths []string) (resBounds, rawBounds []string) {
	resBounds = make([]string, 0, len(paths))
	rawBounds = make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		cleanAbs := filepath.Clean(abs)
		res, err := resolve(cleanAbs)
		if err != nil || res == "" {
			continue
		}
		rawBounds = append(rawBounds, cleanAbs)
		resBounds = append(resBounds, filepath.Clean(res))
	}
	return resBounds, rawBounds
}

func inAllowBounds(resAbs string, bounds []string) bool {
	if resAbs == "" {
		return false
	}
	for _, b := range bounds {
		if b != "" && within(resAbs, b) {
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

var (
	goRootMu    sync.Mutex
	goRootCache = map[string]string{}
)

// resolveGoRoot locates GOROOT via the "go" binary on PATH and caches the
// result per resolved binary path and GOROOT env setting (SA1019).
func resolveGoRoot() string {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return ""
	}
	key := filepath.Clean(goBin) + "\x00" + os.Getenv("GOROOT")
	goRootMu.Lock()
	if cached, ok := goRootCache[key]; ok {
		goRootMu.Unlock()
		return cached
	}
	goRootMu.Unlock()

	root := queryGoRoot(goBin)
	goRootMu.Lock()
	goRootCache[key] = root
	goRootMu.Unlock()
	return root
}

func queryGoRoot(goBin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "env", "GOROOT")
	out := trail.NewCappedBuffer(4096, nil)
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || out.Truncated() {
		return ""
	}
	root := strings.TrimSpace(out.String())
	if root == "" || strings.ContainsAny(root, "\r\n\x00") || !filepath.IsAbs(root) {
		return ""
	}
	return filepath.Clean(root)
}

// ShellQuote is s as one single-quoted bash word.
func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
