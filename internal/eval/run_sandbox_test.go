package eval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// TestRunExitChecks: what the run leaves on the machine fails the row even
// when its check passed (step 15): a child in agy's own group and one that
// called setsid (D18, BUG-163), and a change to the real config.
func TestRunExitChecks(t *testing.T) {
	t.Run("leftover processes", func(t *testing.T) {
		if !ProcsChecked {
			t.Skip("no /proc here (O1)")
		}
		if _, err := exec.LookPath("setsid"); err != nil {
			t.Skip("no setsid here")
		}
		r := newRig(t)
		r.on("child", "")
		r.on("setsid", "")
		x := one(t, r.run(named("p", "p", "true")))
		wantVerdict(t, x, "FAIL", ReasonProcs)
		pids := strings.Fields(r.read("pids"))
		if len(pids) != 2 {
			t.Fatalf("the fake planted %v", pids)
		}
		for _, pid := range pids {
			if !strings.Contains(r.out.String(), " "+pid+" (") {
				t.Errorf("pid %s is not named:\n%s", pid, r.out.String())
			}
			if alive(pid) {
				t.Errorf("pid %s outlived the check", pid)
			}
		}
	})
	t.Run("real config", func(t *testing.T) {
		r := newRig(t)
		r.on("realconfig", filepath.Join(r.real, harness.StateDir, "brain.db"))
		x := one(t, r.run(named("c", "p", "true")))
		wantVerdict(t, x, "FAIL", ReasonRealConfig)
		if !strings.Contains(r.out.String(), filepath.Join("fyr", "brain.db")) || strings.Contains(r.out.String(), "store") {
			t.Errorf("want the changed path and no content:\n%s", r.out.String())
		}
	})
}

// A setup that writes the real config is seen: the fingerprint is taken
// before setup runs (IN50 of the 2026-10-27 audit).
func TestRunSetupThatTouchesTheRealConfig(t *testing.T) {
	r := newRig(t)
	row := named("s", "p", "true")
	row.Setup = "mkdir -p '" + filepath.Join(r.real, harness.StateDir) + "' && echo x > '" + filepath.Join(r.real, harness.StateDir, "brain.db") + "'"
	x := one(t, r.run(row))
	wantVerdict(t, x, "FAIL", ReasonRealConfig)
}

// TestRunSandboxReplaced: the agent replaces the clone and the sandbox home
// with links to the real home. Nothing is removed, scanned or signalled
// through them: the real token stays, a credential-shaped file of the real
// home is not reported, a process whose cwd is the real home is not sent
// SIGTERM, and the row FAILs sandbox replaced.
func TestRunSandboxReplaced(t *testing.T) {
	r := newRig(t)
	put(t, filepath.Join(r.real, ".netrc"), "machine x login y password z\n", 0o600)
	r.on("swap", r.real)
	r.on("decoy", r.real)
	t.Cleanup(func() {
		b, _ := os.ReadFile(filepath.Join(r.rec, "decoy"))
		for _, f := range strings.Fields(string(b)) {
			if pid, err := strconv.Atoi(f); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	res, err := Run(context.Background(), r.opts(), []Row{named("s", "p", "true")})
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, one(t, res), "FAIL", ReasonReplaced)
	if b, err := os.ReadFile(filepath.Join(r.real, tokenRel)); err != nil || string(b) != "token" {
		t.Errorf("the real token: %q, %v", b, err)
	}
	if strings.Contains(r.out.String(), ".netrc") {
		t.Errorf("the real home was scanned:\n%s", r.out.String())
	}
	if ProcsChecked {
		decoy := strings.TrimSpace(r.read("decoy"))
		if decoy == "" || !alive(decoy) {
			t.Errorf("the process in the real home (%q) was signalled", decoy)
		}
	}
}

// TestRunCLIDirLinked: the agent links the sandbox's CLI directory to the
// real one. Removing the token link and settings.json never follows it.
func TestRunCLIDirLinked(t *testing.T) {
	r := newRig(t)
	realCLI := filepath.Dir(filepath.Join(r.real, tokenRel))
	put(t, filepath.Join(realCLI, "settings.json"), "{}", 0o600)
	r.on("link-cli", realCLI)
	res, err := Run(context.Background(), r.opts(), []Row{named("l", "p", "true")})
	if err != nil {
		t.Fatal(err)
	}
	x := one(t, res)
	if x.Verdict != "FAIL" || !slices.Contains(x.Reasons, ReasonReplaced) {
		t.Errorf("row %s: %s %v, want a FAIL naming %q", x.Row, x.Verdict, x.Reasons, ReasonReplaced)
	}
	for _, name := range []string{"antigravity-oauth-token", "settings.json"} {
		if _, err := os.Stat(filepath.Join(realCLI, name)); err != nil {
			t.Errorf("the real %s was removed: %v", name, err)
		}
	}
}

// TestRunFIFOs: FIFOs the agent left where eval reads err.txt, writes
// check.log and reads a conversation database never block the run.
func TestRunFIFOs(t *testing.T) {
	r := newRig(t)
	r.on("fifo", "")
	done := make(chan []Result, 1)
	go func() {
		res, _ := Run(context.Background(), r.opts(), []Row{{ID: "f", Prompt: "p", Check: "true", Agent: "reviewer", Grants: "named"}})
		done <- res
	}()
	select {
	case res := <-done:
		wantVerdict(t, one(t, res), "FAIL", ReasonCheck, ReasonAgent("reviewer"))
	case <-time.After(60 * time.Second):
		t.Fatal("Run blocked on a FIFO")
	}
}

// TestRunWrapperPinsHome: the sandbox's fyr refuses a --home of the
// agent's own in every spelling, and pins SB otherwise.
func TestRunWrapperPinsHome(t *testing.T) {
	r := newRig(t)
	x := one(t, r.run(named("w", "p", "true")))
	sb := filepath.Join(x.Dir, "home")
	wrapper := filepath.Join(sb, "bin", "fyr")
	out, err := exec.Command(wrapper, "search", "x").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "fyr search --home "+sb+" x" {
		t.Errorf("the wrapper without --home: %q, %v", out, err)
	}
	for _, args := range [][]string{{"search", "x", "--home", r.real}, {"search", "--home=" + r.real, "x"}, {"record", "list", "-home", r.real}, {"record", "-home=" + r.real}} {
		out, err := exec.Command(wrapper, args...).CombinedOutput()
		if err == nil || strings.Contains(string(out), "fyr "+args[0]) || !strings.Contains(string(out), "this sandbox pins --home") {
			t.Errorf("the wrapper with %v: %q, %v; want a refusal", args, out, err)
		}
	}
}
