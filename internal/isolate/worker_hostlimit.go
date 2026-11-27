package isolate

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/kesokaj/fyr/internal/trail"
)

// fetchCmd is step 6's fetch of ref from git, a clone's .git.
func fetchCmd(root, git, ref string) *exec.Cmd {
	return leadGit(root, "-c", "fetch.fsckObjects=true", "fetch", "-q", "--no-tags",
		"--no-recurse-submodules", "--no-write-fetch-head",
		"--upload-pack", "git -c core.hooksPath=/dev/null -c core.fsmonitor=false upload-pack --strict",
		git, ref+":"+ref)
}

// blobCmd prints a blob of the branch.
func blobCmd(root, oid string) *exec.Cmd {
	return leadGit(root, "cat-file", "blob", oid)
}

// branchLimit bounds one read of the branch after the fetch, which holds the
// collect lock as the fetch does.
const branchLimit = 10 * time.Minute

// maxBranchOutput bounds what one such read returns. Over it the read fails:
// a truncated path list would pass the scope check.
var maxBranchOutput = 64 << 20

// branchOutput is cmd's Output for a git read of a session's history, under
// the deadline and the output bound.
func branchOutput(cmd *exec.Cmd) ([]byte, error) {
	out := trail.NewCappedBuffer(maxBranchOutput, nil)
	cmd.Stdout = out
	err := runGroup(cmd, branchLimit)
	if err != nil {
		return nil, err
	}
	if out.Truncated() {
		return nil, fmt.Errorf("output over %d bytes", maxBranchOutput)
	}
	return out.Bytes(), nil
}
