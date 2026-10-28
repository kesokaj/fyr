package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

// evalHomeRefused is why `fyr eval` refuses its --home, or "" (ledger D37):
// a run writes its sandboxes, its eval=<row> sessions and its testing record
// under --home, so a missing one, the real home, or a home that reaches the
// real state through a link would put them in the operator's own stores.
// writesState asks it too, so a refused run never moves the real home's old
// state directory, ~/.config/sh-agent, to ~/.config/fyr on its way to the
// refusal.
func evalHomeRefused(homeFlag, home string) string {
	if homeFlag == "" {
		return "--home is required: a run writes its sandboxes, metrics and testing record there; pass a home outside every checkout, never the real one"
	}
	if real, shared := sharesRealState(home); shared {
		return fmt.Sprintf("--home %s is the real home %s, or shares its state directory; pass a separate home", home, real)
	}
	return ""
}

// sharesRealState reports whether home is a real home, or reaches a real
// home's state through a link: home itself, home/.config, home/.config/fyr
// or any entry directly in it (the stores, their -wal and -shm files, the
// registration key, eval/), each compared by file identity (os.SameFile, so a
// case variant of the path on a case-insensitive disk and a bind mount count)
// with the same path, and the state directory also with the old
// ~/.config/sh-agent. The real homes are $HOME and the account's home from
// the account database, which a changed HOME does not move. A dangling link
// on the way counts as shared: where it leads cannot be told yet (the
// security review of D37).
func sharesRealState(home string) (string, bool) {
	for _, real := range realHomes() {
		if sharesHome(home, real) {
			return real, true
		}
	}
	return "", false
}

// realHomes is $HOME and the account's home, each once.
func realHomes() []string {
	var homes []string
	if h, err := os.UserHomeDir(); err == nil {
		homes = append(homes, h)
	}
	if h, err := accountHomeDir(); err == nil && h != "" && !samePath(h, firstOf(homes)) {
		homes = append(homes, h)
	}
	return homes
}

// accountHomeDir is the account's home from the account database (LookupId,
// never Current, which falls back to HOME); a test seam, so a test never
// names the operator's own home as --home.
var accountHomeDir = func() (string, error) {
	u, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		return "", err
	}
	return u.HomeDir, nil
}

func firstOf(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// sharesHome is sharesRealState for one real home.
func sharesHome(home, real string) bool {
	pairs := [][2]string{{home, real}}
	a, b := home, real
	for _, part := range strings.Split(harness.StateDir, "/") {
		a, b = filepath.Join(a, part), filepath.Join(b, part)
		pairs = append(pairs, [2]string{a, b})
	}
	pairs = append(pairs, [2]string{a, filepath.Join(real, harness.OldStateDir)})
	// Every entry the home's state directory holds, by name: a store, a
	// side file or the key, symlinked or hard-linked to the real one, would
	// send that write there (D37's security review, round 2).
	entries, _ := os.ReadDir(a)
	for _, e := range entries {
		pairs = append(pairs, [2]string{filepath.Join(a, e.Name()), filepath.Join(b, e.Name())})
	}
	for _, p := range pairs {
		if dangling(p[0]) || sameFile(p[0], p[1]) {
			return true
		}
	}
	return false
}

// sameFile reports whether a and b are one file or directory: by identity
// when both exist, else by their cleaned spelling.
func sameFile(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	if errA == nil && errB == nil {
		return os.SameFile(ia, ib)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// dangling reports whether p is a link whose target does not resolve.
func dangling(p string) bool {
	fi, err := os.Lstat(p)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	_, err = os.Stat(p)
	return err != nil
}
