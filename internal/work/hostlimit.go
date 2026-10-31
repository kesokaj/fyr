package work

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// hostMemKiB bounds the address space of each git process that reads what a
// docker session wrote, in KiB: 8 GiB, the worker container's own --memory.
// It is per process, so a fetch's two sides together can hold twice that.
var hostMemKiB = 8 << 20

// capMemory runs cmd under that limit under the docker runner, through a sh
// that exits when it cannot set it, so a limit not set fails the command. A
// clone's config can make the host's pack-objects hold a blob whole, and a
// few KiB of shared trees make diff-tree list a million paths (the v5.5.0
// gate). Children inherit it. A host session runs as the operator already.
func capMemory(cmd *exec.Cmd, runner string) *exec.Cmd {
	if runner != RunnerDocker {
		return cmd
	}
	args := append([]string{"-c", `ulimit -v "$0" && exec "$@"`, strconv.Itoa(hostMemKiB)}, cmd.Args...)
	c := exec.Command("sh", args...)
	c.Dir, c.Env, c.Stdin = cmd.Dir, cmd.Env, cmd.Stdin
	return c
}

// fetchCmd is step 6's fetch of ref from git, a clone's .git.
func fetchCmd(root, git, ref, runner string) *exec.Cmd {
	return capMemory(leadGit(root, "-c", "fetch.fsckObjects=true", "fetch", "-q", "--no-tags",
		"--no-recurse-submodules", "--no-write-fetch-head", "--upload-pack", "git upload-pack --strict",
		git, ref+":"+ref), runner)
}

// blobCmd prints a blob of the branch.
func blobCmd(root, oid, runner string) *exec.Cmd {
	return capMemory(leadGit(root, "cat-file", "blob", oid), runner)
}

// branchLimit bounds one read of the branch after the fetch, which holds the
// collect lock as the fetch does.
const branchLimit = 10 * time.Minute

// maxBranchOutput bounds what one such read returns. Over it the read fails:
// a truncated path list would pass the scope check.
var maxBranchOutput = 64 << 20

// overflow keeps up to its bound and remembers going past it.
type overflow struct {
	kept bytes.Buffer
	over bool
}

func (o *overflow) Write(p []byte) (int, error) {
	if room := maxBranchOutput - o.kept.Len(); room >= len(p) {
		o.kept.Write(p)
	} else {
		o.over = true
	}
	return len(p), nil
}

// branchOutput is cmd's Output for a git read of a session's history, under
// the memory limit, the deadline and the output bound.
func branchOutput(cmd *exec.Cmd, runner string) ([]byte, error) {
	cmd = capMemory(cmd, runner)
	var out overflow
	cmd.Stdout = &out
	err := runGroup(cmd, branchLimit)
	if err != nil {
		return nil, err
	}
	if out.over {
		return nil, fmt.Errorf("output over %d bytes", maxBranchOutput)
	}
	return out.kept.Bytes(), nil
}
