//go:build linux

package isolate

import (
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestABIMaskAndCleanIsolateEnv(t *testing.T) {
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

	if !slices.Contains(cmd.Env, "GOCOVERDIR="+covDir) || !slices.Contains(cmd.Env, IsolateInitEnv+"=1") {
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
	if !slices.Contains(got.RWPaths, covDir) {
		t.Errorf("policy RWPaths missing covDir %s: %v", covDir, got.RWPaths)
	}
	if hasEnvKey(got.Env, "GOCOVERDIR") {
		t.Errorf("GOCOVERDIR leaked into child Policy.Env: %v", got.Env)
	}
}

func TestWithCoverDirForInit(t *testing.T) {
	t.Setenv("GOCOVERDIR", "")
	gotPol, gotEnv := withCoverDirForInit(
		Policy{Env: []string{"PATH=/bin", "GOCOVERDIR=rel/cov"}},
		[]string{"PATH=/bin", "GOCOVERDIR=rel/cov", IsolateInitEnv + "=1"},
	)
	wantRel, err := filepath.Abs("rel/cov")
	if err != nil {
		t.Fatal(err)
	}
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
	gotAbsPol, gotAbsEnv := withCoverDirForInit(Policy{Env: []string{"GOCOVERDIR=" + absCov}}, nil)
	if !slices.Contains(gotAbsPol.RWPaths, absCov) || !slices.Contains(gotAbsEnv, "GOCOVERDIR="+absCov) {
		t.Errorf("abs pol.Env GOCOVERDIR: rw=%v env=%v want=%s", gotAbsPol.RWPaths, gotAbsEnv, absCov)
	}

	t.Setenv("GOCOVERDIR", "hostrel/cov")
	wantHost, err := filepath.Abs("hostrel/cov")
	if err != nil {
		t.Fatal(err)
	}
	hostPol, hostEnv := withCoverDirForInit(Policy{Env: []string{"PATH=/bin"}}, []string{"PATH=/bin"})
	if !slices.Contains(hostPol.RWPaths, wantHost) || !slices.Contains(hostEnv, "GOCOVERDIR="+wantHost) {
		t.Errorf("relative host GOCOVERDIR: rw=%v env=%v want=%s", hostPol.RWPaths, hostEnv, wantHost)
	}

	emptyPol, emptyEnv := withCoverDirForInit(Policy{Env: []string{"GOCOVERDIR="}}, []string{"GOCOVERDIR="})
	if len(emptyPol.RWPaths) != 0 || slices.Contains(emptyEnv, "GOCOVERDIR=hostrel/cov") {
		t.Errorf("empty pol.Env GOCOVERDIR fell back to host: rw=%v env=%v", emptyPol.RWPaths, emptyEnv)
	}
}
