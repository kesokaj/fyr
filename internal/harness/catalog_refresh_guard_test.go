package harness

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// destGuard refuses an empty --dest and a directory that is not a catalog.
func (r *refreshRig) destGuard() {
	t, script, tmp, agents, gskills, work, pinA, pinG := r.t, r.script, r.tmp, r.agents, r.gskills, r.work, r.pinA, r.pinG
	// The destination guard: an empty --dest, and a directory that is not a
	// catalog, are refused before anything is deleted (security review S1).
	guardDir := filepath.Join(tmp, "not-a-catalog")
	if err := os.MkdirAll(filepath.Join(guardDir, "precious"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"", guardDir} {
		cmd := exec.Command("bash", script, "--dest", d, "--work", work, pinA, pinG)
		cmd.Dir = guardDir
		cmd.Env = fixtureEnv("CATALOG_AGENTS_CLI_REPO="+agents, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("--dest %q was accepted:\n%s", d, out)
		}
		if _, err := os.Stat(filepath.Join(guardDir, "precious")); err != nil {
			t.Fatalf("--dest %q: a directory that is not a catalog entry was deleted", d)
		}
	}
}

// guardPlaces refuses each place the guard names, with its own message.
func (r *refreshRig) guardPlaces() {
	t, repo, script, tmp, work, pinA, pinG := r.t, r.repo, r.script, r.tmp, r.work, r.pinA, r.pinG
	// The places the guard names, each refused by its own message before the
	// not-a-catalog check is reached: /, the home directory, the checkout,
	// and an ancestor of either. HOME is a fixture directory here.
	fakeHome := filepath.Join(tmp, "home", "user")
	r.fakeHome = fakeHome
	r.put(fakeHome, map[string]string{"keep.txt": "kept\n"})
	repoLink := filepath.Join(tmp, "link-to-the-checkout")
	if err := os.Symlink(repo, repoLink); err != nil {
		repoLink = repo // no symlinks here: the case repeats the plain one
	}
	for _, c := range []struct{ dest, want string }{
		{"/", "is not a catalog directory"},
		{fakeHome, "is not a catalog directory"},
		{fakeHome + "/", "is not a catalog directory"},
		{repo, "is not a catalog directory"},
		{filepath.Dir(repo), "contains the checkout"},
		{filepath.Dir(fakeHome), "contains the home directory"},
		// Another spelling of the same place is the same place: a path
		// through `..` and a symlink (security review of the v2.1.0 gate).
		{filepath.Join(repo, "skills") + "/..", "is not a catalog directory"},
		{repoLink, "is not a catalog directory"},
		// `..` below a directory that does not exist cannot be resolved, and
		// `mkdir -p` would later make it real: the home directory, the
		// checkout or / reached that way (code review of 7dd0645..f62513b).
		{fakeHome + "/no-such-dir/..", "below a directory that does not exist"},
		{repo + "/no-such-dir/..", "below a directory that does not exist"},
		{"/no-such-dir-for-this-test/..", "below a directory that does not exist"},
		{fakeHome + "/no-such-dir/./catalog", "below a directory that does not exist"},
		// A leading run of slashes is the same place (the v2.1.0 gate's L2).
		{"/" + fakeHome, "is not a catalog directory"},
		{"//" + fakeHome, "is not a catalog directory"},
	} {
		cmd := exec.Command("bash", script, "--dest", c.dest, "--work", work, pinA, pinG)
		cmd.Dir = tmp
		// The sources name nothing: if the guard and the not-a-catalog check
		// both regressed, the run stops at the clone and writes nowhere
		// (code review round 3).
		nowhere := filepath.Join(tmp, "no-such-repository")
		cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+nowhere, "CATALOG_GOOGLE_SKILLS_REPO="+nowhere, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), c.want) {
			t.Errorf("--dest %s: want a refusal saying %q, got err %v\n%s", c.dest, c.want, err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(fakeHome, "keep.txt")); err != nil {
		t.Fatalf("the guard fixture's home directory was touched: %v", err)
	}
}

// guardFooting refuses a --dest below a directory it cannot enter.
func (r *refreshRig) guardFooting() {
	t, tmp, fakeHome := r.t, r.tmp, r.fakeHome
	// Two ways the guard itself could lose its footing (code review of
	// 7dd0645..b4e3d79, round 2): a parent directory it cannot enter, which
	// once resolved to a path under /, and no HOME to compare against.
	if os.Geteuid() != 0 {
		locked := filepath.Join(tmp, "locked")
		if err := os.Mkdir(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o755) })
		if out, err := r.guardRun(filepath.Join(locked, "x"), fixtureEnv("HOME="+fakeHome)); err == nil || !strings.Contains(out, "cannot resolve") {
			t.Errorf("--dest below a directory that cannot be entered: want a refusal saying \"cannot resolve\", got err %v\n%s", err, out)
		}
	}
}

// workGuard refuses the --work directories the script must not use.
func (r *refreshRig) workGuard() {
	t, script, tmp, pinA, pinG, fakeHome := r.t, r.script, r.tmp, r.pinA, r.pinG, r.fakeHome
	// --work is cloned into and its build directory removed, so it is a
	// directory of this script or a new one (the v2.1.0 gate's L7): never /,
	// the home directory or the checkout, never nested with --dest, and when
	// it holds files, only with the marker the script writes before its
	// first clone, or the origins.txt of an earlier completed run.
	// The three forbidden places are tried with a COPY of the script inside a
	// stand-in checkout, and with sources that cannot be cloned: when this
	// guard did not exist yet, the same cases run against the real checkout
	// with working sources cloned the fixtures into it (BUG-150).
	fakeRepo := filepath.Join(tmp, "fake-checkout")
	scriptCopy := filepath.Join(fakeRepo, "skills", "google-catalog", "scripts", "refresh-catalog.sh")
	scriptBytes, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	r.put(fakeRepo, map[string]string{"skills/google-catalog/scripts/refresh-catalog.sh": string(scriptBytes), "README.md": "a stand-in checkout\n"})
	places := []string{fakeHome, fakeRepo}
	if os.Geteuid() != 0 {
		places = append(places, "/") // as root a regressed guard could write a marker there
	}
	for _, w := range places {
		cmd := exec.Command("bash", scriptCopy, "--dest", filepath.Join(tmp, "dest-w"), "--work", w, pinA, pinG)
		cmd.Dir = tmp
		nowhere := filepath.Join(tmp, "no-such-repository")
		cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+nowhere, "CATALOG_GOOGLE_SKILLS_REPO="+nowhere, "CATALOG_DIR=", "CATALOG_WRITE=")
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "must not be /, the home directory or the checkout") {
			t.Errorf("--work %s: want a refusal, got err %v\n%s", w, err, out)
		}
	}
	for _, stray := range []string{filepath.Join(fakeRepo, "agents-cli"), filepath.Join(fakeRepo, ".refresh-catalog-work"), filepath.Join(fakeHome, "agents-cli"), filepath.Join(fakeHome, ".refresh-catalog-work")} {
		if _, err := os.Stat(stray); err == nil {
			t.Errorf("a refused --work was written into: %s", stray)
		}
	}
	nest := filepath.Join(tmp, "nest")
	for _, c := range [][2]string{{nest, filepath.Join(nest, "work")}, {filepath.Join(nest, "dest"), nest}} {
		if out, err := r.workRun(c[0], c[1], pinA, pinG); err == nil || !strings.Contains(out, "must not contain each other") {
			t.Errorf("--dest %s --work %s: want a refusal, got err %v\n%s", c[0], c[1], err, out)
		}
	}
	foreign := filepath.Join(tmp, "someone-elses-directory")
	r.put(foreign, map[string]string{"agents-cli/notes.txt": "the operator's own clone\n", "build/keep.txt": "kept\n"})
	if out, err := r.workRun(filepath.Join(tmp, "dest-f"), foreign, pinA, pinG); err == nil || !strings.Contains(out, "is not a work directory of this script") || !strings.Contains(out, "name an empty or new directory, or delete this one") {
		t.Errorf("a non-empty --work without the marker: want a refusal, got err %v\n%s", err, out)
	}
	for _, f := range []string{"agents-cli/notes.txt", "build/keep.txt"} {
		if _, err := os.Stat(filepath.Join(foreign, f)); err != nil {
			t.Errorf("the refused --work lost %s: %v", f, err)
		}
	}
}

// workAfterFailure is a run that fails after the clone, three ways, then the corrected run.
func (r *refreshRig) workAfterFailure() {
	t, script, tmp, agents, gskills, pinA, pinG, fakeHome := r.t, r.script, r.tmp, r.agents, r.gskills, r.pinA, r.pinG, r.fakeHome
	// A first run that fails after the checks (a pin that does not resolve)
	// must leave a work directory the corrected run accepts.
	freshWork, freshDest := filepath.Join(tmp, "fresh-work"), filepath.Join(tmp, "fresh-dest")
	if out, err := r.workRun(freshDest, freshWork, pinA, "0000000000000000000000000000000000000000"); err == nil || !strings.Contains(out, "does not resolve") {
		t.Fatalf("a first run with a bad pin: want the pin refusal, got err %v\n%s", err, out)
	} else if !strings.Contains(out, "delete that directory if the value is sensitive") {
		// The clones are on disk by then, and with them the override's
		// value: a run that fails says so too, not only one that succeeds
		// (security review of 1fc0d4c..4165f81).
		t.Errorf("a run that fails after the clone does not say an override's value stays in the work directory:\n%s", out)
	}
	// The same when no refusal of the script's own ends the run: a plain
	// command that fails under set -e (here the destination cannot be made,
	// its parent is read-only) still leaves the value on disk, and the run
	// still says so (review of 4165f81..7b3e969).
	roParent := filepath.Join(tmp, "read-only")
	if err := os.Mkdir(roParent, 0o555); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() == 0 {
		t.Log("running as root: a 0555 parent stops nothing, the failing-command case is not exercised")
	} else if out, err := r.workRun(filepath.Join(roParent, "dest"), filepath.Join(tmp, "ro-work"), pinA, pinG); err == nil {
		t.Errorf("a destination that cannot be created: want a failure\n%s", out)
	} else if !strings.Contains(out, "delete that directory if the value is sensitive") {
		t.Errorf("a run stopped by a failing command after the clone does not say an override's value stays in the work directory:\n%s", out)
	}
	// And when a signal ends it: TERM while the script waits for the writer
	// (a stub `go` that says it started and then sleeps). The whole process
	// group is signalled, as a terminal does, so the stub dies and the
	// script's TERM trap exits 143 through the note.
	{
		stub := filepath.Join(tmp, "stub-bin")
		started := filepath.Join(tmp, "stub-go-started")
		if err := os.MkdirAll(stub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stub, "go"), []byte("#!/bin/sh\n: > '"+started+"'\nexec sleep 120\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", script, "--dest", filepath.Join(tmp, "sig-dest"), "--work", filepath.Join(tmp, "sig-work"), pinA, pinG)
		cmd.Dir = tmp
		cmd.Env = fixtureEnv("HOME="+fakeHome, "PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"), "CATALOG_AGENTS_CLI_REPO="+agents, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(60 * time.Second)
		for {
			if _, err := os.Stat(started); err == nil {
				break
			}
			if time.Now().After(deadline) {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				cmd.Wait()
				t.Fatalf("the script never reached the writer:\n%s", buf.String())
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		err := cmd.Wait()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 143 || !strings.Contains(buf.String(), "delete that directory if the value is sensitive") {
			t.Errorf("a run ended by TERM after the clone: want exit 143 and the note that an override's value stays in the work directory, got %v\n%s", err, buf.String())
		}
	}
	if out, err := r.workRun(freshDest, freshWork, pinA, pinG); err != nil {
		t.Errorf("the corrected run on the same --work was refused: %v\n%s", err, out)
	} else if _, err := os.Stat(filepath.Join(freshDest, "gamma", "SKILL.md")); err != nil {
		t.Errorf("the corrected run vendored nothing: %v", err)
	}
}

// homeAndPin refuses a run with no HOME and a pin that is not a commit id.
func (r *refreshRig) homeAndPin() {
	t, tmp, pinA := r.t, r.tmp, r.pinA
	// The guard's second footing (guardFooting): no HOME to compare against.
	var noHome []string
	for _, kv := range fixtureEnv() {
		if !strings.HasPrefix(kv, "HOME=") {
			noHome = append(noHome, kv)
		}
	}
	if out, err := r.guardRun(filepath.Join(tmp, "dest-without-home"), noHome); err == nil || !strings.Contains(out, "HOME is not set") {
		t.Errorf("no HOME: want a refusal saying \"HOME is not set\", got err %v\n%s", err, out)
	}
	if out, code := r.run(pinA, "main"); code == 0 || !strings.Contains(out, "commit id") {
		t.Errorf("a pin that is not a commit id was accepted (exit %d)\n%s", code, out)
	}
}
