package sandbox

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// planted is a sleep the test started; done closes once it is reaped.
type planted struct {
	pid  int
	done chan struct{}
}

// plant starts a sleep in dir with env, killed at cleanup.
func plant(t *testing.T, bin, dir string, env []string, secs int) planted {
	t.Helper()
	cmd := exec.Command(bin, strconv.Itoa(secs))
	cmd.Dir, cmd.Env = dir, env
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := planted{cmd.Process.Pid, make(chan struct{})}
	go func() { cmd.Wait(); close(p.done) }()
	t.Cleanup(func() { cmd.Process.Kill(); <-p.done })
	return p
}

// exited reports whether p ended within d.
func (p planted) exited(d time.Duration) bool {
	select {
	case <-p.done:
		return true
	case <-time.After(d):
		return false
	}
}

// TestProcsLeftovers ports the kit test of the preamble's process check (the
// run kit's TestKitPreambleKillsLeftoverProcesses, in
// internal/harness/catalog_test.go at 8374f9c; BUG-163): a process whose cwd
// or executable is under the clone, or whose environment holds exactly
// HOME=<sandbox home>, is reported and sent SIGTERM; a HOME that only starts
// with it, and an unrelated process, are left alone; eval itself is never
// matched.
func TestProcsLeftovers(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep here")
	}
	tmp := t.TempDir()
	if r, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = r
	}
	clone, sb, other := filepath.Join(tmp, "app"), filepath.Join(tmp, "home"), filepath.Join(tmp, "other")
	for _, d := range []string{clone, sb, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	copied := filepath.Join(clone, "srv")
	b, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copied, b, 0o755); err != nil {
		t.Fatal(err)
	}
	base := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + other}
	if found, _ := leftoverProcs(io.Discard, "test", sb, clone, sb); len(found) != 0 {
		t.Fatalf("a clean run reports %v", found)
	}
	for name, c := range map[string]struct {
		bin, dir string
		env      []string
		kind     string
	}{
		"a server left in the clone":        {sleep, clone, base, "cwd"},
		"an executable under the clone":     {copied, other, base, "exe"},
		"a process carrying HOME=<sandbox>": {sleep, other, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + sb}, "home"},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := plant(t, c.bin, c.dir, c.env, 301)
			found, _ := leftoverProcs(io.Discard, "test", sb, clone, sb)
			want := strconv.Itoa(cmd.pid) + " (" + c.kind + ") "
			if len(found) != 1 || !strings.HasPrefix(found[0], want) {
				t.Errorf("found %v, want one entry starting %q", found, want)
			}
			if !cmd.exited(5 * time.Second) {
				t.Error("the process outlived its SIGTERM")
			}
		})
	}
	for name, env := range map[string][]string{
		"a HOME that only starts with the sandbox home": {"PATH=" + os.Getenv("PATH"), "HOME=" + sb + "-x"},
		"an unrelated process":                          base,
	} {
		t.Run(name, func(t *testing.T) {
			cmd := plant(t, sleep, other, env, 302)
			if found, _ := leftoverProcs(io.Discard, "test", sb, clone, sb); len(found) != 0 {
				t.Errorf("found %v", found)
			}
			if cmd.exited(300 * time.Millisecond) {
				t.Error("the process was killed")
			}
		})
	}
	// eval itself, sitting in a root, is never its own leftover.
	t.Chdir(clone)
	if found, _ := leftoverProcs(io.Discard, "test", sb, clone, sb); len(found) != 0 {
		t.Errorf("eval matched itself: %v", found)
	}
}

// TestProcsUnreadableFailsClosed is PLAN.md item 49: an agent can exhaust
// descriptors or memory so /proc cannot be read, and check e must not read
// that as "nothing left". A process that vanished is no failure.
func TestProcsUnreadableFailsClosed(t *testing.T) {
	old := procDir
	t.Cleanup(func() { procDir = old })

	procDir = filepath.Join(t.TempDir(), "missing")
	if _, err := leftoverProcs(nil, "test", "/nowhere/sb", "/nowhere/sb"); err == nil {
		t.Error("a /proc that cannot be listed passed")
	}

	// A pid whose status cannot be read for a reason other than its exit.
	procDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(procDir, "4242", "status"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := leftoverProcs(nil, "test", "/nowhere/sb", "/nowhere/sb"); err == nil {
		t.Error("a process that cannot be read passed")
	}

	// A pid that has gone: its directory holds nothing.
	procDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(procDir, "4242"), 0o755); err != nil {
		t.Fatal(err)
	}
	if found, err := leftoverProcs(nil, "test", "/nowhere/sb", "/nowhere/sb"); err != nil || len(found) != 0 {
		t.Errorf("a vanished process: %v, %v", found, err)
	}
}
