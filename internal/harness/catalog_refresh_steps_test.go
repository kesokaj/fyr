package harness

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// git runs git in dir as the fixture author and returns its trimmed output.
func (r *refreshRig) git(dir string, args ...string) string {
	t := r.t
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = fixtureEnv("GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// put writes files under dir.
func (r *refreshRig) put(dir string, files map[string]string) {
	t := r.t
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// commit commits everything in dir and returns the commit id.
func (r *refreshRig) commit(dir string) string {
	t := r.t
	t.Helper()
	r.git(dir, "add", "-A")
	r.git(dir, "commit", "-qm", "c", "--allow-empty")
	return r.git(dir, "rev-parse", "HEAD")
}

// run runs the script at the rig's destination and work directory, with the fixture sources.
func (r *refreshRig) run(args ...string) (string, int) {
	t, repo, script, agents, gskills, dest, work := r.t, r.repo, r.script, r.agents, r.gskills, r.dest, r.work
	t.Helper()
	cmd := exec.Command("bash", append([]string{script, "--dest", dest, "--work", work}, args...)...)
	cmd.Dir = repo
	cmd.Env = fixtureEnv("CATALOG_AGENTS_CLI_REPO="+agents, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), ee.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), 0
}

// snapshot is every file of the destination with its mode and a hash prefix.
func (r *refreshRig) snapshot() string {
	t, dest := r.t, r.dest
	t.Helper()
	var b strings.Builder
	filepath.WalkDir(dest, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(path)
		sum := sha256.Sum256(data)
		info, _ := d.Info()
		rel, _ := filepath.Rel(dest, path)
		fmt.Fprintf(&b, "%s %o %x\n", rel, info.Mode().Perm(), sum[:6])
		return nil
	})
	return b.String()
}

// guardRun runs the script with sources that cannot be cloned and env as its environment.
func (r *refreshRig) guardRun(dest string, env []string) (string, error) {
	script, tmp, work, pinA, pinG := r.script, r.tmp, r.work, r.pinA, r.pinG
	cmd := exec.Command("bash", script, "--dest", dest, "--work", work, pinA, pinG)
	cmd.Dir = tmp
	nowhere := filepath.Join(tmp, "no-such-repository")
	cmd.Env = append(env, "CATALOG_AGENTS_CLI_REPO="+nowhere, "CATALOG_GOOGLE_SKILLS_REPO="+nowhere, "CATALOG_DIR=", "CATALOG_WRITE=")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// workRun runs the script with the fixture sources and the fixture HOME.
func (r *refreshRig) workRun(destArg, workArg string, pins ...string) (string, error) {
	script, tmp, agents, gskills, fakeHome := r.script, r.tmp, r.agents, r.gskills, r.fakeHome
	cmd := exec.Command("bash", append([]string{script, "--dest", destArg, "--work", workArg}, pins...)...)
	cmd.Dir = tmp
	cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+agents, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// refuse commits a refused fixture to the google-skills source and runs the
// script at it.
// Refusals: each leaves the destination as it was and names the entry.
func (r *refreshRig) refuse(label, names string, files map[string]string, mutate func(), exclude string) {
	t, gskills, dest, pinA, pinG, good := r.t, r.gskills, r.dest, r.pinA, r.pinG, r.good
	t.Helper()
	r.put(gskills, files)
	if mutate != nil {
		mutate()
	}
	pin := r.commit(gskills)
	out, code := r.run(pinA, pin)
	if code != 1 || !strings.Contains(out, names) {
		t.Errorf("%s: want exit 1 naming %q, got exit %d\n%s", label, names, code, out)
	}
	if r.snapshot() != good {
		t.Errorf("%s: the refused run changed the destination", label)
	}
	if exclude != "" {
		if out, code := r.run("--exclude", exclude, pinA, pin); code != 0 {
			t.Errorf("%s: --exclude %s did not clear the refusal (exit %d)\n%s", label, exclude, code, out)
		}
		if _, err := os.Stat(filepath.Join(dest, exclude)); err == nil && exclude != "alpha" {
			t.Errorf("%s: the excluded entry %s was vendored", label, exclude)
		}
		// back to the good state for the next fixture
		if out, code := r.run(pinA, pinG); code != 0 || r.snapshot() != good {
			t.Fatalf("%s: could not restore the good catalog (exit %d)\n%s", label, code, out)
		}
	}
	r.git(gskills, "reset", "-q", "--hard", pinG)
	r.git(gskills, "clean", "-qfd")
}
