package dream

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestMain drops every GIT_* variable, as the other packages' TestMain does:
// the collector runs git in fixture checkouts. It also gives the package a
// HOME with an Antigravity login and an `agy` on PATH, so a dry run's agy
// checks (checkAgy) pass on a box without the CLI and never read the real
// login; a test that needs otherwise sets HOME or PATH itself. The process
// check sees one interactive agy of this uid, so an --all run goes on; a
// test about the check replaces Options.Session, procRoot or psArgs.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	// Nor the operator's git config: a global hooksPath or signing key
	// never reaches a fixture commit (the v5.0.0 gate's security review).
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
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
	proc := filepath.Join(tmp, "proc", "1")
	os.MkdirAll(proc, 0o755)
	os.WriteFile(filepath.Join(proc, "status"), []byte("Uid:\t"+strconv.Itoa(os.Getuid())+"\n"), 0o644)
	os.WriteFile(filepath.Join(proc, "cmdline"), []byte("agy\x00"), 0o644)
	procRoot = filepath.Dir(proc)
	psArgs = func(int) ([]byte, error) { return []byte("agy\n"), nil }
	os.Setenv("HOME", home)
	os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}
