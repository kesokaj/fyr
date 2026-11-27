package dream

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/trail"
)

// checkAgy is the one refusal a run makes about the Antigravity CLI before
// it creates anything: agy on PATH or in sibling/~/.local/bin. The login
// belongs to the branch (the visible one reads it under --home, the private
// one under $HOME, plan D12), so a missing one fails a project, not the run.
// The dry run calls it too, so it refuses what the run refuses (SD27).
func checkAgy(home string) error {
	name := command()[0]
	if _, ok := ResolveBinary(name, home); !ok {
		return fmt.Errorf("no %s on PATH: the dream job runs the Antigravity CLI", name)
	}
	return nil
}

// findLogin finds the operator's Antigravity login under the real $HOME,
// never --home: the CLI's directory, and on macOS the Keychain folder agy
// finds the login through ("" elsewhere). It reads and creates nothing else.
func findLogin(goos string) (src, keychains string, err error) {
	real := os.Getenv("HOME")
	if !filepath.IsAbs(real) {
		return "", "", fmt.Errorf("HOME is %q, not an absolute path: the dream job reads the Antigravity login from it", real)
	}
	return loginIn(goos, real)
}

// loginIn is findLogin's rule under any home: the visible branch asks it of
// --home, where the login is used in place (plan D12).
func loginIn(goos, real string) (src, keychains string, err error) {
	src = filepath.Join(real, ".gemini", "antigravity-cli")
	token := filepath.Join(src, "antigravity-oauth-token")
	_, tokenErr := os.Stat(token)
	if goos == "darwin" {
		if info, err := os.Stat(filepath.Join(real, "Library", "Keychains")); err == nil && info.IsDir() {
			keychains = filepath.Join(real, "Library", "Keychains")
		}
	}
	if tokenErr != nil && keychains == "" {
		if goos == "darwin" {
			return "", "", fmt.Errorf("no Antigravity login: neither %s nor a Keychain folder at %s: run agy once and log in", token, filepath.Join(real, "Library", "Keychains"))
		}
		return "", "", fmt.Errorf("no Antigravity login at %s: run agy once and log in", token)
	}
	return src, keychains, nil
}

// agyHome is a private HOME for one run's Antigravity child: a copy of the
// operator's login and a settings file that grants nothing, so a tool call is
// refused whatever the operator allowed for interactive use (security review
// 2026-09-30, D28). Its conversations stay in it and go with it. The login
// is read from $HOME, the real one, never from --home. On macOS agy keeps the
// login in the Keychain and finds it only through $HOME/Library/Keychains, so
// that folder is linked in and the token file is optional (BUG-166).
func agyHome(conf, goos string) (string, func(), error) {
	src, keychains, err := findLogin(goos)
	if err != nil {
		return "", nil, err
	}
	home, err := os.MkdirTemp(conf, "dream-agy-home-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(home) }
	dst := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(dst, 0o700); err != nil {
		cleanup()
		return "", nil, err
	}
	for _, name := range []string{"antigravity-oauth-token", "installation_id"} {
		optional := name == "installation_id" || keychains != ""
		if err := copyPrivate(filepath.Join(src, name), filepath.Join(dst, name)); err != nil && (!optional || !errors.Is(err, os.ErrNotExist)) {
			cleanup()
			return "", nil, err
		}
	}
	if keychains != "" {
		// A link, never a copy: the Keychain stays where it is, and
		// os.RemoveAll removes the link without following it.
		if err := os.MkdirAll(filepath.Join(home, "Library"), 0o700); err != nil {
			cleanup()
			return "", nil, err
		}
		if err := os.Symlink(keychains, filepath.Join(home, "Library", "Keychains")); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	// Nothing allowed, and tool calls set to ask, which a headless run cannot:
	// explicit, never agy's default (security review round 2).
	settings := []byte(`{"toolPermission":"request-review","permissions":{"allow":[]},"trustedWorkspaces":[]}` + "\n")
	if err := os.WriteFile(filepath.Join(dst, "settings.json"), settings, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return home, cleanup, nil
}

func copyPrivate(from, to string) error {
	// The regular-file check is on the opened file: a FIFO swapped in after
	// a check would block the open (SD60b of the 2026-10-27 re-audit).
	in, err := trail.OpenRegularFollow(from, false)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// agyAuthHint names the one failure "invalid output" hides: agy found no
// login in where, the branch's home, and asked for a browser login nobody
// can give. Only agy's own lines count: a stream-json event (a "{" line)
// carries model text, which may quote the phrase from a transcript.
func agyAuthHint(stdout, stderr, where string) string {
	found := false
	for _, line := range strings.Split(stdout+"\n"+stderr, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "{") && strings.Contains(line, "Authentication required") {
			found = true
			break
		}
	}
	if !found {
		return ""
	}
	return "agy is not logged in inside " + where + " (run agy once and log in; on macOS the login Keychain must be unlocked, so the Mac's user must be logged in)"
}

// agyEnv is every variable agy gets from fyr's environment, HOME aside: it
// reads text fyr did not write, so a credential in the operator's shell
// stays out of its process (ADR-067). A name joins only after a run shows
// agy needs it, and only by the operator (plan audit-choices D8).
var agyEnv = []string{"PATH", "LANG", "TERM", "TMPDIR", "TZ",
	// The locale names, not LC_*: Debian's sshd accepts any LC_ variable a
	// client sends (AcceptEnv LANG LC_*), so the prefix carries arbitrary values.
	"LC_ALL", "LC_CTYPE", "LC_COLLATE", "LC_MESSAGES", "LC_MONETARY", "LC_NUMERIC", "LC_TIME",
	"LC_ADDRESS", "LC_IDENTIFICATION", "LC_MEASUREMENT", "LC_NAME", "LC_PAPER", "LC_TELEPHONE",
	"http_proxy", "https_proxy", "no_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
	"SSL_CERT_FILE", "SSL_CERT_DIR"}

// childEnv is agy's environment: agyEnv's names from env, then HOME.
func childEnv(env []string, home string) []string {
	return append(trail.AllowEnv(env, agyEnv...), "HOME="+home)
}

// removeCopies removes what a killed run left in conf: a private agy HOME
// with its login copy, and the unredacted database copies of the readers
// (security review 2026-09-30). A dry run takes no lock and makes copies
// too, so only copies older than an hour, far past any live run's, are
// removed (code review round 2).
func removeCopies(conf string, now time.Time) {
	entries, err := os.ReadDir(conf)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		// metrics-agy-* (Antigravity conversation copies) and dream-metrics-*
		// (a copy of the metrics store) hold the same kind of content.
		if !e.IsDir() || (!strings.HasPrefix(n, "dream-agy-") && !strings.HasPrefix(n, "dream-projects-") &&
			!strings.HasPrefix(n, "metrics-agy-") && !strings.HasPrefix(n, "dream-metrics-") && !strings.HasPrefix(n, "dream-brain-")) {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > time.Hour {
			os.RemoveAll(filepath.Join(conf, n))
		}
	}
}

// dreamExecutable is os.Executable; a test replaces it to verify sibling lookup.
var dreamExecutable = os.Executable

// ResolveBinary locates name by checking PATH first, then the directory beside
// the running executable, then home/.local/bin and $HOME/.local/bin.
func ResolveBinary(name, home string) (string, bool) {
	if name == "" {
		return "", false
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	if strings.ContainsRune(name, filepath.Separator) {
		return "", false
	}
	for _, dir := range localBinDirs(home) {
		cand := filepath.Join(dir, name)
		if isExecutableFile(cand) {
			return cand, true
		}
	}
	return "", false
}

// resolveFallbackBinary resolves name from local/sibling bin dirs only when
// name is absent from PATH, preserving bare command names on normal PATHs.
func resolveFallbackBinary(name, home string) (string, bool) {
	if name == "" {
		return "", false
	}
	if _, err := exec.LookPath(name); err == nil {
		return "", false
	}
	return ResolveBinary(name, home)
}

func localBinDirs(home string) []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" {
			return
		}
		d = filepath.Clean(d)
		if !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	if exe, err := dreamExecutable(); err == nil && exe != "" {
		add(filepath.Dir(exe))
		if realExe, err := filepath.EvalSymlinks(exe); err == nil && realExe != "" {
			add(filepath.Dir(realExe))
		}
	}
	if home != "" && filepath.IsAbs(home) {
		add(filepath.Join(home, ".local", "bin"))
	}
	if envHome := os.Getenv("HOME"); envHome != "" && filepath.IsAbs(envHome) {
		add(filepath.Join(envHome, ".local", "bin"))
	}
	return dirs
}

func isExecutableFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}
