//go:build linux

package isolate

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestABIMaskAndCleanIsolateEnv(t *testing.T) {
	t.Parallel()
	if abiMask(0) != 0 || abiMask(1) != (1<<13)-1 || abiMask(2) != (1<<14)-1 || abiMask(3) != (1<<15)-1 || abiMask(5) != (1<<16)-1 {
		t.Fatalf("unexpected abiMask values: %d %d %d", abiMask(1), abiMask(3), abiMask(5))
	}
	cleaned := cleanIsolateEnv([]string{"A=1", IsolateInitEnv + "=1", "B=2"})
	if len(cleaned) != 2 || cleaned[0] != "A=1" || cleaned[1] != "B=2" {
		t.Fatalf("cleanIsolateEnv = %v", cleaned)
	}
}

func TestPrepareIsolatedCmdForwardsGOCOVERDIR(t *testing.T) {
	if err := CheckAvailable(); err != nil {
		t.Skipf("landlock unavailable: %v", err)
	}
	covDir := t.TempDir()
	wantCov := covDir
	if r, err := filepath.EvalSymlinks(covDir); err == nil {
		wantCov = r
	}
	t.Setenv("GOCOVERDIR", covDir)
	pol := Policy{
		Argv: []string{"/bin/true"},
		Dir:  t.TempDir(),
		Env:  []string{"PATH=/bin"},
	}
	cmd, cleanup, err := prepareIsolatedCmd(pol)
	if err != nil {
		t.Fatalf("prepareIsolatedCmd: %v", err)
	}
	defer cleanup()

	if !slices.Contains(cmd.Env, "GOCOVERDIR="+wantCov) || !slices.Contains(cmd.Env, IsolateInitEnv+"=1") {
		t.Fatalf("PID 1 cmd.Env missing expected vars: %v", cmd.Env)
	}
	raw, err := io.ReadAll(cmd.ExtraFiles[0])
	if err != nil {
		t.Fatalf("read policy pipe: %v", err)
	}
	var got Policy
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal policy: %v", err)
	}
	if !slices.Contains(got.RWPaths, wantCov) {
		t.Errorf("policy RWPaths missing covDir %s: %v", wantCov, got.RWPaths)
	}
	if hasEnvKey(got.Env, "GOCOVERDIR") {
		t.Errorf("GOCOVERDIR leaked into child Policy.Env: %v", got.Env)
	}
}

func TestWithCoverDirForInit(t *testing.T) {
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	t.Chdir(base)
	if err := os.MkdirAll(filepath.Join(base, "rel", "cov"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "hostrel", "cov"), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOCOVERDIR", "")
	gotPol, gotEnv := withCoverDirForInit(
		Policy{Env: []string{"PATH=/bin", "GOCOVERDIR=rel/cov"}},
		[]string{"PATH=/bin", "GOCOVERDIR=rel/cov", IsolateInitEnv + "=1"},
	)
	wantRel := filepath.Join(base, "rel", "cov")
	if !slices.Contains(gotPol.RWPaths, wantRel) {
		t.Errorf("RWPaths missing pol.Env relative GOCOVERDIR %s: %v", wantRel, gotPol.RWPaths)
	}
	if !slices.Contains(gotEnv, "GOCOVERDIR="+wantRel) {
		t.Errorf("initEnv missing normalized GOCOVERDIR=%s: %v", wantRel, gotEnv)
	}
	if got := slices.DeleteFunc(slices.Clone(gotEnv), func(s string) bool { return !strings.HasPrefix(s, "GOCOVERDIR=") }); len(got) != 1 {
		t.Errorf("expected single GOCOVERDIR in initEnv, got %v", gotEnv)
	}

	absCov := t.TempDir()
	wantAbs := absCov
	if r, err := filepath.EvalSymlinks(absCov); err == nil {
		wantAbs = r
	}
	gotAbsPol, gotAbsEnv := withCoverDirForInit(Policy{Env: []string{"GOCOVERDIR=" + absCov}}, nil)
	if !slices.Contains(gotAbsPol.RWPaths, wantAbs) || !slices.Contains(gotAbsEnv, "GOCOVERDIR="+wantAbs) {
		t.Errorf("abs pol.Env GOCOVERDIR: rw=%v env=%v want=%s", gotAbsPol.RWPaths, gotAbsEnv, wantAbs)
	}

	t.Setenv("GOCOVERDIR", "hostrel/cov")
	wantHost := filepath.Join(base, "hostrel", "cov")
	hostPol, hostEnv := withCoverDirForInit(Policy{Env: []string{"PATH=/bin"}}, []string{"PATH=/bin"})
	if !slices.Contains(hostPol.RWPaths, wantHost) || !slices.Contains(hostEnv, "GOCOVERDIR="+wantHost) {
		t.Errorf("relative host GOCOVERDIR: rw=%v env=%v want=%s", hostPol.RWPaths, hostEnv, wantHost)
	}

	emptyPol, emptyEnv := withCoverDirForInit(Policy{Env: []string{"GOCOVERDIR="}}, []string{"GOCOVERDIR="})
	if len(emptyPol.RWPaths) != 0 || slices.Contains(emptyEnv, "GOCOVERDIR=hostrel/cov") {
		t.Errorf("empty pol.Env GOCOVERDIR fell back to host: rw=%v env=%v", emptyPol.RWPaths, emptyEnv)
	}
}

func TestWithCoverDirForInitRejectsUnsafeTargets(t *testing.T) {
	base := t.TempDir()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("GOCOVERDIR", "")

	filePath := filepath.Join(base, "file.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{".ssh", ".config"} {
		if err := os.MkdirAll(filepath.Join(fakeHome, sub, "cov"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	linkHome := filepath.Join(base, "link-home")
	if err := os.Symlink(fakeHome, linkHome); err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(base, "link-root")
	if err := os.Symlink("/", linkRoot); err != nil {
		t.Fatal(err)
	}

	badDirs := []string{
		filepath.Join(base, "missing"),
		filePath,
		"/",
		os.TempDir(),
		"/etc",
		fakeHome,
		linkHome,
		linkRoot,
		filepath.Join(fakeHome, ".ssh"),
		filepath.Join(fakeHome, ".config", "cov"),
	}
	for _, bad := range badDirs {
		pol, env := withCoverDirForInit(
			Policy{Env: []string{"PATH=/bin", "GOCOVERDIR=" + bad}},
			[]string{"PATH=/bin", "GOCOVERDIR=" + bad, IsolateInitEnv + "=1"},
		)
		if len(pol.RWPaths) != 0 {
			t.Errorf("unsafe GOCOVERDIR=%q leaked into RWPaths: %v", bad, pol.RWPaths)
		}
		if hasEnvKey(env, "GOCOVERDIR") {
			t.Errorf("unsafe GOCOVERDIR=%q stayed in initEnv: %v", bad, env)
		}
	}
}

func hasEnvKey(env []string, key string) bool {
	_, ok := envValue(env, key)
	return ok
}
