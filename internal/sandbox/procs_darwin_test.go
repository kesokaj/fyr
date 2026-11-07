//go:build darwin

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

type planted struct {
	pid  int
	done chan struct{}
}

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

func (p planted) exited(d time.Duration) bool {
	select {
	case <-p.done:
		return true
	case <-time.After(d):
		return false
	}
}

func copyAdhocSleep(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		t.Fatal(err)
	}
	if cs, err := exec.LookPath("codesign"); err == nil {
		if out, err := exec.Command(cs, "-f", "-s", "-", dst).CombinedOutput(); err != nil {
			t.Fatalf("codesign %s: %v\n%s", dst, err, out)
		}
	}
}

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
	copyAdhocSleep(t, sleep, copied)
	extSleep := filepath.Join(other, "sleep")
	copyAdhocSleep(t, sleep, extSleep)

	base := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + other}
	if found, err := leftoverProcs(io.Discard, "test", sb, clone, sb); err != nil || len(found) != 0 {
		t.Fatalf("a clean run reports %v, %v", found, err)
	}
	for name, c := range map[string]struct {
		bin, dir string
		env      []string
		kind     string
	}{
		"a server left in the clone":        {extSleep, clone, base, "cwd"},
		"an executable under the clone":     {copied, other, base, "exe"},
		"a process carrying HOME=<sandbox>": {extSleep, other, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + sb}, "home"},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := plant(t, c.bin, c.dir, c.env, 301)
			found, err := leftoverProcs(io.Discard, "test", sb, clone, sb)
			if err != nil {
				t.Fatalf("leftoverProcs: %v", err)
			}
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
			cmd := plant(t, extSleep, other, env, 302)
			if found, err := leftoverProcs(io.Discard, "test", sb, clone, sb); err != nil || len(found) != 0 {
				t.Errorf("found %v, %v", found, err)
			}
			if cmd.exited(300 * time.Millisecond) {
				t.Error("the process was killed")
			}
		})
	}
	t.Chdir(clone)
	if found, err := leftoverProcs(io.Discard, "test", sb, clone, sb); err != nil || len(found) != 0 {
		t.Errorf("eval matched itself: %v, %v", found, err)
	}
}

func TestProcsUnreadableFailsClosed(t *testing.T) {
	oldLsof, oldPs := procDir, psBin
	t.Cleanup(func() { procDir, psBin = oldLsof, oldPs })

	procDir = filepath.Join(t.TempDir(), "missing-lsof")
	if _, err := leftoverProcs(nil, "test", "/nowhere/sb", "/nowhere/sb"); err == nil {
		t.Error("an unreadable lsof passed")
	}

	procDir = oldLsof
	psBin = filepath.Join(t.TempDir(), "missing-ps")
	if _, err := leftoverProcs(nil, "test", "/nowhere/sb", "/nowhere/sb"); err == nil {
		t.Error("an unreadable ps passed")
	}
}
