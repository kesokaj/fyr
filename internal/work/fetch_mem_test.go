package work

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/trail"
)

// TestUploadPackCapsMemory: a clone's core.bigFileThreshold makes the host's
// upload-pack hold a blob whole, at about 229 times its bytes on disk, past
// both GitIntact and check d (the v5.5.0 gate). Under docker the child runs
// under an address-space limit: the fetch fails instead of filling the host.
// The first run, with no such config, proves the limit alone does not fail a
// fetch.
func TestUploadPackCapsMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 1 GiB blob")
	}
	old := uploadPackMemKiB
	uploadPackMemKiB = 512 << 10
	t.Cleanup(func() { uploadPackMemKiB = old })
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", src, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	// A loose object: git add of a big file writes a pack, whose compressed
	// bytes pack-objects copies without reading the blob.
	const size = 1 << 30
	hash := exec.Command("sh", "-c", fmt.Sprintf("head -c %d /dev/zero | git -c core.bigFileThreshold=100g hash-object -w --stdin", size))
	hash.Dir = src
	oid, err := hash.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	git("update-index", "--add", "--cacheinfo", "100644,"+strings.TrimSpace(string(oid))+",zero")
	git("commit", "-qm", "blob")
	fetch := func(name string) error {
		dst := filepath.Join(dir, name)
		if out, err := exec.Command("git", "init", "-q", dst).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		out, err := runBounded(trail.Command(dst, "fetch", "-q", "--no-tags", "--upload-pack", uploadPack(RunnerDocker),
			filepath.Join(src, ".git"), "main:main"), fetchLimit)
		if err != nil {
			t.Logf("fetch %s: %v: %s", name, err, strings.TrimSpace(string(out)))
		}
		return err
	}
	if err := fetch("plain"); err != nil {
		t.Fatalf("the limit failed a fetch with no hostile config: %v", err)
	}
	git("config", "core.bigFileThreshold", "100g")
	if err := fetch("hostile"); err == nil {
		t.Error("upload-pack held a 1 GiB blob under a 512 MiB limit")
	}
}

// TestUploadPackForm: only the docker runner's fetch is wrapped, and a limit
// that cannot be set stops the fetch rather than running it unlimited.
func TestUploadPackForm(t *testing.T) {
	if got := uploadPack(RunnerHost); got != "git upload-pack --strict" {
		t.Errorf("host: %q", got)
	}
	if got := uploadPack(RunnerDocker); !strings.HasPrefix(got, "ulimit -v ") || !strings.Contains(got, " && exec git upload-pack --strict") {
		t.Errorf("docker: %q", got)
	}
}
