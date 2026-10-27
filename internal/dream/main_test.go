package dream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain drops every GIT_* variable, as the other packages' TestMain does:
// the collector runs git in fixture checkouts. It also gives the package a
// HOME with an Antigravity login and an `agy` on PATH, so a dry run's agy
// checks (checkAgy) pass on a box without the CLI and never read the real
// login; a test that needs otherwise sets HOME or PATH itself.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	tmp, err := os.MkdirTemp("", "dream-test-")
	if err != nil {
		panic(err)
	}
	home, bin := filepath.Join(tmp, "home"), filepath.Join(tmp, "bin")
	token := filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	if err := os.MkdirAll(filepath.Dir(token), 0o700); err != nil {
		panic(err)
	}
	os.WriteFile(token, []byte("token"), 0o600)
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "agy"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	os.Setenv("HOME", home)
	os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}
