package isolate

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCommandValidationAndEnvAllowlist(t *testing.T) {
	if _, _, err := Command(CommandOpts{}); err == nil {
		t.Fatal("Command with empty argv succeeded")
	}
	if _, _, err := Command(CommandOpts{Argv: []string{""}}); err == nil {
		t.Fatal("Command with empty Argv[0] succeeded")
	}

	base := t.TempDir()
	realHome := filepath.Join(base, "real")
	scratchHome := filepath.Join(base, "scratch")
	tmpDir := filepath.Join(base, "tmp")
	workDir := filepath.Join(base, "work")
	goPath := filepath.Join(base, "gopath")
	goModCache := filepath.Join(goPath, "pkg", "mod")
	for _, d := range []string{realHome, scratchHome, tmpDir, workDir, goModCache} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	secretKey := "FYR_TEST_" + "VAL"
	t.Setenv(secretKey, "gh"+"p_1234567890abcdef")
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(realHome, "ssh.sock"))
	t.Setenv("HOME", realHome)
	t.Setenv("GOPATH", goPath)
	t.Setenv("GOMODCACHE", goModCache)

	opts := CommandOpts{
		Dir:         workDir,
		ScratchHome: scratchHome,
		TmpDir:      tmpDir,
		RealHome:    realHome,
		Argv:        []string{"bash", "-c", "true"},
		ExtraEnv:    []string{"FYR_EXTRA=ok"},
		Unconfined:  true,
	}
	cmd, cleanup, err := Command(opts)
	if err != nil {
		t.Fatalf("Command unconfined: %v", err)
	}
	defer cleanup()

	gotEnv := map[string]string{}
	for _, kv := range cmd.Env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			gotEnv[k] = v
		}
	}
	if _, leaked := gotEnv[secretKey]; leaked {
		t.Errorf("buildCommandEnv leaked %s", secretKey)
	}
	if _, leaked := gotEnv["SSH_AUTH_SOCK"]; leaked {
		t.Errorf("buildCommandEnv leaked SSH_AUTH_SOCK")
	}
	if gotEnv["HOME"] != scratchHome || gotEnv["TMPDIR"] != tmpDir {
		t.Errorf("HOME=%q TMPDIR=%q, want %q / %q", gotEnv["HOME"], gotEnv["TMPDIR"], scratchHome, tmpDir)
	}
	if gotEnv["GOCACHE"] != filepath.Join(tmpDir, "gocache") {
		t.Errorf("GOCACHE=%q, want %q", gotEnv["GOCACHE"], filepath.Join(tmpDir, "gocache"))
	}
	if gotEnv["GIT_CONFIG_GLOBAL"] != "/dev/null" || gotEnv["GIT_CONFIG_NOSYSTEM"] != "1" || gotEnv["GIT_TERMINAL_PROMPT"] != "0" {
		t.Errorf("git env not scrubbed: %v", gotEnv)
	}
	if gotEnv["GOPATH"] != goPath || gotEnv["GOMODCACHE"] != goModCache || gotEnv["FYR_EXTRA"] != "ok" {
		t.Errorf("optional go/extra env mismatch: %v", gotEnv)
	}

	pol := buildCommandPolicy(opts, "/bin/bash", cmd.Env, goModCache)
	if pol.Network {
		t.Error("Command policy unexpectedly allowed Network")
	}
	if !slices.Contains(pol.ROPaths, goModCache) {
		t.Errorf("Command policy ROPaths missing gomodcache %s: %v", goModCache, pol.ROPaths)
	}
	for _, rw := range []string{workDir, scratchHome, tmpDir} {
		if !slices.Contains(pol.RWPaths, rw) {
			t.Errorf("Command policy RWPaths missing %s: %v", rw, pol.RWPaths)
		}
	}
}

func TestCommandLandlockDeniesRealHomeWrite(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Landlock enforcement test runs on Linux")
	}
	if err := CheckAvailable(); err != nil {
		t.Fatalf("Landlock unavailable on Linux test host: %v", err)
	}
	base := t.TempDir()
	realHome := filepath.Join(base, "real")
	scratchHome := filepath.Join(base, "scratch")
	tmpDir := filepath.Join(base, "tmp")
	workDir := filepath.Join(base, "work")
	for _, d := range []string{realHome, scratchHome, tmpDir, workDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	escapeFile := filepath.Join(realHome, "escape.txt")
	script := "(printf 'bad' > " + ShellQuote(escapeFile) + ") 2>/dev/null; rc=$?; printf 'ok' > inside.txt; test $rc -ne 0"
	cmd, cleanup, err := Command(CommandOpts{
		Dir:         workDir,
		ScratchHome: scratchHome,
		TmpDir:      tmpDir,
		RealHome:    realHome,
		Argv:        []string{"bash", "-c", script},
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	out, runErr := cmd.CombinedOutput()
	cleanup()
	if runErr != nil {
		t.Fatalf("confined command failed: %v\n%s", runErr, out)
	}
	if _, err := os.Stat(escapeFile); !os.IsNotExist(err) {
		t.Errorf("confined command wrote %s: %v", escapeFile, err)
	}
	if got, err := os.ReadFile(filepath.Join(workDir, "inside.txt")); err != nil || string(got) != "ok" {
		t.Errorf("inside.txt = (%q, %v), want ok", got, err)
	}
}
