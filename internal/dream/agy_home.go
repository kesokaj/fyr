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
)

// checkAgy is every refusal a run makes about the Antigravity CLI before
// it creates anything: agy on PATH, then findLogin's. The dry run calls it
// too, so it refuses what the run refuses (SD27 of the 2026-10-27 audit,
// plan D17).
func checkAgy(goos string) error {
	if name := command()[0]; !onPath(name) {
		return fmt.Errorf("no %s on PATH: the dream job runs the Antigravity CLI", name)
	}
	_, _, err := findLogin(goos)
	return err
}

// findLogin finds the operator's Antigravity login under the real $HOME,
// never --home: the CLI's directory, and on macOS the Keychain folder agy
// finds the login through ("" elsewhere). It reads and creates nothing else.
func findLogin(goos string) (src, keychains string, err error) {
	real := os.Getenv("HOME")
	if !filepath.IsAbs(real) {
		return "", "", fmt.Errorf("HOME is %q, not an absolute path: the dream job reads the Antigravity login from it", real)
	}
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
		if err := copyPrivate(filepath.Join(src, name), filepath.Join(dst, name)); err != nil && !(optional && errors.Is(err, os.ErrNotExist)) {
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
	in, err := openRegular(from, false)
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
// login in the private HOME and asked for a browser login nobody can give.
// Only agy's own lines count: a stream-json event (a "{" line) carries model
// text, which may quote the phrase from a transcript.
func agyAuthHint(stdout, stderr string) string {
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
	return "agy is not logged in inside the dream job's private HOME (run agy once and log in; on macOS the login Keychain must be unlocked, so the Mac's user must be logged in)"
}

// withHome is env with HOME replaced.
func withHome(env []string, home string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, "HOME=") {
			out = append(out, e)
		}
	}
	return append(out, "HOME="+home)
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
		if !e.IsDir() || !(strings.HasPrefix(n, "dream-agy-") || strings.HasPrefix(n, "dream-projects-") ||
			strings.HasPrefix(n, "metrics-agy-") || strings.HasPrefix(n, "dream-metrics-") || strings.HasPrefix(n, "dream-brain-")) {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > time.Hour {
			os.RemoveAll(filepath.Join(conf, n))
		}
	}
}

// onPath reports whether name resolves on PATH.
func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
