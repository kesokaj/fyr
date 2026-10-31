package work

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/trail"
)

// memRig is a repository holding one loose blob of size bytes of zeros: git
// add of a big file writes a pack, whose compressed bytes pack-objects copies
// without reading the blob.
func memRig(t *testing.T, size int) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}} {
		if out, err := exec.Command("git", append([]string{"-C", src}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	hash := exec.Command("sh", "-c", fmt.Sprintf("head -c %d /dev/zero | git -c core.bigFileThreshold=100g hash-object -w --stdin", size))
	hash.Dir = src
	oid, err := hash.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	for _, args := range [][]string{
		{"update-index", "--add", "--cacheinfo", "100644," + strings.TrimSpace(string(oid)) + ",zero"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "blob"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", src}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return src
}

// memFetch is step 6's fetch from src into a fresh repository.
func memFetch(t *testing.T, src, runner string, fsck bool) error {
	t.Helper()
	dst := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dst).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	args := []string{"fetch", "-q", "--no-tags", "--upload-pack", "git upload-pack --strict", filepath.Join(src, ".git"), "main:main"}
	if fsck {
		args = append([]string{"-c", "fetch.fsckObjects=true"}, args...)
	}
	out, err := runBounded(capMemory(trail.Command(dst, args...), runner), fetchLimit)
	if err != nil {
		t.Logf("fetch: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return err
}

// TestFetchMemoryLimit: under docker a clone's core.bigFileThreshold stops at
// the limit; the same fetch passes on the host, so the failure is the limit's,
// and a small blob passes under it, so the limit alone does not fail a fetch.
// The receiving index-pack is the same command's child, under the same limit;
// no test isolates it, since a blob it holds whole the serving side holds too.
func TestFetchMemoryLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 1 GiB blob")
	}
	old := hostMemKiB
	hostMemKiB = 512 << 10
	t.Cleanup(func() { hostMemKiB = old })
	if err := memFetch(t, memRig(t, 1<<20), RunnerDocker, true); err != nil {
		t.Fatalf("the limit failed a small fetch: %v", err)
	}
	big := memRig(t, 1<<30)
	cmd := exec.Command("git", "-C", big, "config", "core.bigFileThreshold", "100g")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}
	if err := memFetch(t, big, RunnerHost, false); err != nil {
		t.Fatalf("the host fetch failed: %v", err)
	}
	if err := memFetch(t, big, RunnerDocker, false); err == nil {
		t.Error("upload-pack held a 1 GiB blob under a 512 MiB limit")
	}
}

// TestCapMemoryForm: only docker is wrapped, and the wrapper's && means a
// limit that cannot be set runs nothing.
func TestCapMemoryForm(t *testing.T) {
	host := exec.Command("git", "status")
	if got := capMemory(host, RunnerHost); got != host {
		t.Errorf("host: %v", got.Args)
	}
	host.Dir, host.Env = "/d", []string{"A=1"}
	c := capMemory(host, RunnerDocker)
	if c.Dir != "/d" || !slices.Equal(c.Env, host.Env) || !strings.Contains(strings.Join(c.Args, " "), `ulimit -v "$0" && exec "$@"`) || c.Args[len(c.Args)-1] != "status" {
		t.Errorf("docker: %q dir %q env %v", c.Args, c.Dir, c.Env)
	}
}
