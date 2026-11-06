package sandbox

import (
	"encoding/base64"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestCheckWorkLoginMatchesSync pins the docker runner's login check to the
// run's token step on each home layout: CheckWorkLogin refuses what
// SyncWorkToken refuses, in its words, or both pass with the one token path
// the run mounts, and the check changes nothing on disk (BUG-510). Paths are
// under the test's directory: real is the operator's home, home is --home.
func TestCheckWorkLoginMatchesSync(t *testing.T) {
	const tok = WorkTokenRel
	for _, c := range []struct {
		name, goos, kc string   // kc: the Keychain's answer, "" no Keychain folder
		home           string   // --home: "home", or "real" for the real home
		want           string   // "" both pass, else in both refusals
		path           string   // when both pass: the token path
		dirs, files    []string // made before the check
		link           []string // a symlink: its path, then its target
	}{
		{name: "keychain-all-real", goos: "darwin", kc: "good", home: "home", path: "home/" + tok, dirs: []string{"home/.config/fyr/work"}},
		{name: "keychain-config-symlink", goos: "darwin", kc: "good", home: "home", want: "home/.config is not a regular directory", dirs: []string{"home"}, link: []string{"home/.config", "elsewhere"}},
		{name: "keychain-fyr-symlink", goos: "darwin", kc: "good", home: "home", want: "home/.config/fyr is not a regular directory", dirs: []string{"home/.config"}, link: []string{"home/.config/fyr", "elsewhere"}},
		{name: "keychain-work-symlink", goos: "darwin", kc: "good", home: "home", want: "home/.config/fyr/work is not a regular directory", dirs: []string{"home/.config/fyr"}, link: []string{"home/.config/fyr/work", "elsewhere"}},
		{name: "keychain-token-symlink", goos: "darwin", kc: "good", home: "home", want: "home/" + tok, dirs: []string{"home/.config/fyr/work"}, link: []string{"home/" + tok, "elsewhere/antigravity-oauth-token"}},
		{name: "keychain-real-config-symlink", goos: "darwin", kc: "good", home: "real", want: "real/.config is not a regular directory", link: []string{"real/.config", "elsewhere"}},
		{name: "keychain-locked-home-token", goos: "darwin", kc: "locked", home: "home", path: "home/" + tok, files: []string{"home/" + tok}},
		{name: "keychain-bad-token", goos: "darwin", kc: "bad", home: "home", want: "home/" + tok + " (keychain entry", dirs: []string{"home"}},
		{name: "cli-token-first", goos: "darwin", kc: "good", home: "home", path: "real/" + TokenRel, files: []string{"real/" + TokenRel}, dirs: []string{"home"}, link: []string{"home/.config", "elsewhere"}},
		{name: "linux-work-symlink", goos: "linux", home: "home", want: "home/.config/fyr/work is not a regular directory", dirs: []string{"home/.config/fyr"}, link: []string{"home/.config/fyr/work", "elsewhere"}},
		// An existing fallback token is walked from its home as the Keychain
		// write is: a symlinked directory on the way refuses (BUG-512).
		{name: "linux-config-symlink-fallback", goos: "linux", home: "home", want: "home/.config is not a regular directory", dirs: []string{"home"}, link: []string{"home/.config", "elsewhere"}},
		{name: "linux-fyr-symlink-fallback", goos: "linux", home: "home", want: "home/.config/fyr is not a regular directory", dirs: []string{"home/.config"}, link: []string{"home/.config/fyr", "elsewhere"}},
		{name: "linux-real-config-symlink-fallback", goos: "linux", home: "home", want: "real/.config is not a regular directory", dirs: []string{"home"}, link: []string{"real/.config", "elsewhere"}},
		{name: "linux-real-token", goos: "linux", home: "real", path: "real/" + tok, files: []string{"real/" + tok}},
		{name: "linux-none", goos: "linux", home: "home", want: "no Antigravity login at ", dirs: []string{"home"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			tmp := workLoginLayout(t, c.kc, c.dirs, c.files, c.link)
			home, real := filepath.Join(tmp, c.home), filepath.Join(tmp, "real")
			before := treeOf(t, tmp)
			cpath, cerr := CheckWorkLogin(home, real, c.goos)
			if after := treeOf(t, tmp); !maps.Equal(before, after) {
				t.Errorf("CheckWorkLogin changed the tree:\nbefore %v\nafter  %v", before, after)
			}
			spath, serr := SyncWorkToken(home, real, c.goos)
			switch want := filepath.Join(tmp, filepath.FromSlash(c.path)); {
			case c.want == "" && (cerr != nil || serr != nil || cpath != want || spath != want):
				t.Errorf("CheckWorkLogin %q, %v; SyncWorkToken %q, %v; want both to pass with %s", cpath, cerr, spath, serr, want)
			case c.want != "" && (cerr == nil || serr == nil || cerr.Error() != serr.Error() || !strings.Contains(cerr.Error(), c.want)):
				t.Errorf("CheckWorkLogin %v; SyncWorkToken %v; want the same refusal, saying %q", cerr, serr, c.want)
			}
		})
	}
}

// workLoginLayout makes one case's directory: real, elsewhere with a regular
// token at every depth a symlink reaches, the case's dirs, files and symlink,
// and with kc a Keychain folder whose reader answers good (a token that
// decodes), bad (one that does not) or locked (an error).
func workLoginLayout(t *testing.T, kc string, dirs, files, link []string) string {
	t.Helper()
	tmp := t.TempDir()
	for _, d := range slices.Concat([]string{"real"}, dirs) {
		if err := os.MkdirAll(filepath.Join(tmp, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	elsewhere := []string{"elsewhere/antigravity-oauth-token", "elsewhere/work/antigravity-oauth-token", "elsewhere/fyr/work/antigravity-oauth-token"}
	for _, f := range slices.Concat(files, elsewhere) {
		put(t, filepath.Join(tmp, filepath.FromSlash(f)), `{"access_token":"t0"}`, 0o600)
	}
	if link != nil {
		if err := os.Symlink(filepath.Join(tmp, filepath.FromSlash(link[1])), filepath.Join(tmp, filepath.FromSlash(link[0]))); err != nil {
			t.Fatal(err)
		}
	}
	if kc == "" {
		return tmp
	}
	put(t, filepath.Join(tmp, "real", filepath.FromSlash(KeychainsRel), "login.keychain-db"), "keychain", 0o600)
	answer := map[string]string{
		"good": keychainPrefix + base64.StdEncoding.EncodeToString([]byte(`{"access_token":"tok-1"}`)),
		"bad":  keychainPrefix + "!!!",
	}[kc]
	t.Cleanup(SetKeychainReader(func() (string, error) {
		if kc == "locked" {
			return "", errors.New("locked")
		}
		return answer, nil
	}))
	return tmp
}
