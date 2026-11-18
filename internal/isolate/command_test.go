package isolate

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
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
	evilGoPath := filepath.Join(realHome, "evil-gopath")
	secretsDir := filepath.Join(realHome, "secrets")
	tmpModDir := filepath.Join(base, "tmpmod")
	goModCache := filepath.Join(realHome, "go", "pkg", "mod")
	for _, d := range []string{realHome, scratchHome, tmpDir, workDir, evilGoPath, secretsDir, tmpModDir, goModCache} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	resolvedGoModCache, err := filepath.EvalSymlinks(goModCache)
	if err != nil {
		t.Fatal(err)
	}

	secretKey := "FYR_TEST_" + "VAL"
	t.Setenv(secretKey, "gh"+"p_1234567890abcdef")
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(realHome, "ssh.sock"))
	t.Setenv("HOME", realHome)
	t.Setenv("GOPATH", evilGoPath)
	t.Setenv("GOMODCACHE", "")

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

	gotEnv := envToMap(cmd.Env)
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
	wantGoPath := filepath.Join(tmpDir, "gopath")
	if gotEnv["GOPATH"] != wantGoPath || gotEnv["GOMODCACHE"] != resolvedGoModCache || gotEnv["FYR_EXTRA"] != "ok" {
		t.Errorf("optional go/extra env mismatch: got=%v want GOPATH=%q GOMODCACHE=%q", gotEnv, wantGoPath, resolvedGoModCache)
	}

	pol := buildCommandPolicy(opts, "/bin/bash", cmd.Env, resolvedGoModCache)
	if pol.Network {
		t.Error("Command policy unexpectedly allowed Network")
	}
	if !slices.Contains(pol.ROPaths, resolvedGoModCache) {
		t.Errorf("Command policy ROPaths missing gomodcache %s: %v", resolvedGoModCache, pol.ROPaths)
	}
	for _, rw := range []string{workDir, scratchHome, tmpDir} {
		if !slices.Contains(pol.RWPaths, rw) {
			t.Errorf("Command policy RWPaths missing %s: %v", rw, pol.RWPaths)
		}
	}

	for _, badMC := range []string{secretsDir, tmpModDir, realHome} {
		t.Setenv("GOMODCACHE", badMC)
		badCmd, badCleanup, err := Command(opts)
		if err != nil {
			t.Fatalf("Command with rejected GOMODCACHE=%q: %v", badMC, err)
		}
		badCleanup()
		badEnv := envToMap(badCmd.Env)
		if v, ok := badEnv["GOMODCACHE"]; ok {
			t.Errorf("untrusted GOMODCACHE=%q was not rejected (got %q)", badMC, v)
		}
		badPol := buildCommandPolicy(opts, "/bin/bash", badCmd.Env, hostGoDirs(realHome))
		if slices.Contains(badPol.ROPaths, badMC) {
			t.Errorf("Command policy ROPaths leaked untrusted GOMODCACHE %q: %v", badMC, badPol.ROPaths)
		}
	}

	t.Setenv("GOMODCACHE", goModCache)
	okCmd, okCleanup, err := Command(opts)
	if err != nil {
		t.Fatalf("Command with realHome/go/pkg/mod GOMODCACHE: %v", err)
	}
	okCleanup()
	if got := envToMap(okCmd.Env)["GOMODCACHE"]; got != resolvedGoModCache {
		t.Errorf("explicit realHome/go/pkg/mod GOMODCACHE = %q, want %q", got, resolvedGoModCache)
	}

	noTmpOpts := opts
	noTmpOpts.TmpDir = ""
	noTmpEnv, _ := buildCommandEnv(noTmpOpts)
	if v, ok := envToMap(noTmpEnv)["GOPATH"]; ok {
		t.Errorf("GOPATH unexpectedly inherited when TmpDir empty: %q", v)
	}
}

func envToMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

func TestRejectsAncestorPathsForGoModCacheAndPATH(t *testing.T) {
	base := t.TempDir()
	homeParent := filepath.Join(base, "home-parent")
	realHome := filepath.Join(homeParent, "real")
	tmpParent := filepath.Join(base, "tmp-parent")
	sepTmp := filepath.Join(tmpParent, "tmp")
	scratchHome := filepath.Join(base, "scratch")
	workDir := filepath.Join(base, "work")
	safeDir := filepath.Join(base, "safebin")
	goModCache := filepath.Join(realHome, "go", "pkg", "mod")
	for _, d := range []string{realHome, sepTmp, scratchHome, workDir, safeDir, goModCache} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	resSafeDir, err := filepath.EvalSymlinks(safeDir)
	if err != nil {
		t.Fatal(err)
	}
	resGoModCache, err := filepath.EvalSymlinks(goModCache)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("TMPDIR", sepTmp)
	t.Setenv("HOME", realHome)

	ancHome := filepath.Dir(realHome)
	ancTmp := filepath.Dir(sepTmp)
	opts := CommandOpts{
		Dir:         workDir,
		ScratchHome: scratchHome,
		TmpDir:      sepTmp,
		RealHome:    realHome,
		Argv:        []string{"bash", "-c", "true"},
		Unconfined:  true,
	}

	root := string(filepath.Separator)
	for _, tc := range []struct {
		name string
		mc   string
	}{
		{name: "root", mc: root},
		{name: "ancestor_of_real_home", mc: ancHome},
		{name: "ancestor_of_tmpdir", mc: ancTmp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOMODCACHE", tc.mc)
			if got := hostGoDirs(realHome); got != "" {
				t.Errorf("hostGoDirs(%q) with GOMODCACHE=%q = %q, want empty", realHome, tc.mc, got)
			}
			cmd, cleanup, err := Command(opts)
			if err != nil {
				t.Fatalf("Command with GOMODCACHE=%q: %v", tc.mc, err)
			}
			cleanup()
			if v, ok := envToMap(cmd.Env)["GOMODCACHE"]; ok {
				t.Errorf("Command env leaked GOMODCACHE=%q for %q", v, tc.mc)
			}
			pol := buildCommandPolicy(opts, "/bin/bash", cmd.Env, hostGoDirs(realHome))
			resMC, _ := filepath.EvalSymlinks(tc.mc)
			for _, p := range []string{tc.mc, resMC} {
				if p != "" && slices.Contains(pol.ROPaths, p) {
					t.Errorf("buildCommandPolicy ROPaths leaked %q for GOMODCACHE=%q: %v", p, tc.mc, pol.ROPaths)
				}
			}
		})
	}

	t.Setenv("GOMODCACHE", "")
	if got := hostGoDirs(realHome); got != resGoModCache {
		t.Errorf("default hostGoDirs(%q) = %q, want %q", realHome, got, resGoModCache)
	}
	defaultCmd, defaultCleanup, err := Command(opts)
	if err != nil {
		t.Fatalf("Command with default GOMODCACHE: %v", err)
	}
	defaultCleanup()
	if got := envToMap(defaultCmd.Env)["GOMODCACHE"]; got != resGoModCache {
		t.Errorf("default Command GOMODCACHE = %q, want %q", got, resGoModCache)
	}
	defaultPol := buildCommandPolicy(opts, "/bin/bash", defaultCmd.Env, hostGoDirs(realHome))
	if !slices.Contains(defaultPol.ROPaths, resGoModCache) {
		t.Errorf("default buildCommandPolicy ROPaths missing %q: %v", resGoModCache, defaultPol.ROPaths)
	}

	t.Setenv("PATH", strings.Join([]string{root, ancHome, ancTmp, safeDir}, string(os.PathListSeparator)))
	gotPATH := appendSafePATHDirs(nil, realHome)
	if !slices.Contains(gotPATH, resSafeDir) {
		t.Fatalf("appendSafePATHDirs missing safe dir %q: got %v", resSafeDir, gotPATH)
	}
	for _, blocked := range []string{root, ancHome, ancTmp} {
		resBlocked, _ := filepath.EvalSymlinks(blocked)
		for _, p := range []string{blocked, resBlocked} {
			if p != "" && slices.Contains(gotPATH, p) {
				t.Errorf("appendSafePATHDirs leaked excluded dir %q: got %v", p, gotPATH)
			}
		}
	}

	gotEmptyHome := appendSafePATHDirs(nil, "")
	if !slices.Contains(gotEmptyHome, resSafeDir) {
		t.Fatalf("appendSafePATHDirs(empty realHome) missing safe dir %q: got %v", resSafeDir, gotEmptyHome)
	}
	for _, blocked := range []string{root, ancTmp} {
		resBlocked, _ := filepath.EvalSymlinks(blocked)
		for _, p := range []string{blocked, resBlocked} {
			if p != "" && slices.Contains(gotEmptyHome, p) {
				t.Errorf("appendSafePATHDirs(empty realHome) leaked excluded dir %q: got %v", p, gotEmptyHome)
			}
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

func TestCommandLandlockIsolatesNetwork(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Landlock network namespace isolation test runs on Linux")
	}
	if err := CheckAvailable(); err != nil {
		t.Fatalf("Landlock unavailable on Linux test host: %v", err)
	}
	hostLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen host: %v", err)
	}
	defer hostLn.Close()
	go func() {
		for {
			c, err := hostLn.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

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
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd, cleanup, err := Command(CommandOpts{
		Dir:         workDir,
		ScratchHome: scratchHome,
		TmpDir:      tmpDir,
		RealHome:    realHome,
		Argv:        []string{exe, "-test.run=^TestCommandLandlockIsolatesNetworkHelper$"},
		ExtraEnv:    []string{"FYR_NET_PROBE_TARGET=" + hostLn.Addr().String()},
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	out, runErr := cmd.CombinedOutput()
	cleanup()
	if runErr != nil {
		t.Fatalf("network isolation probe failed: %v\n%s", runErr, out)
	}
}

func TestCommandLandlockIsolatesNetworkHelper(t *testing.T) {
	target := os.Getenv("FYR_NET_PROBE_TARGET")
	if target == "" {
		return
	}
	if c, err := net.DialTimeout("tcp", target, 2*time.Second); err == nil {
		_ = c.Close()
		t.Fatalf("dialed host loopback %s from Network=false sandbox", target)
	} else if !errors.Is(err, syscall.ECONNREFUSED) && !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("dial host loopback %s = %v, want ECONNREFUSED", target, err)
	}

	if c, err := net.DialTimeout("tcp", "192.0.2.1:80", 2*time.Second); err == nil {
		_ = c.Close()
		t.Fatalf("dialed 192.0.2.1:80 from Network=false sandbox")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("self-loopback listen inside sandbox: %v", err)
	}
	defer ln.Close()
	errCh := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer c.Close()
		_, err = c.Write([]byte("ok"))
		errCh <- err
	}()
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("self-loopback dial inside sandbox: %v", err)
	}
	defer conn.Close()
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ok" {
		t.Fatalf("self-loopback read = (%q, %v), want ok", buf, err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("self-loopback accept/write: %v", err)
	}
}
