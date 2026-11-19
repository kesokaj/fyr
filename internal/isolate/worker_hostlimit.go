package isolate

import (
	"bytes"
	"fmt"
	"os/exec"
	"time"
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

// cappedBuffer retains up to limit bytes written to it and records overflow.
// Keeping buf as a named field avoids io.Copy bypassing Write via ReadFrom.
type cappedBuffer struct {
	limit int
	buf   bytes.Buffer
	over  bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	room := c.limit - c.buf.Len()
	if room <= 0 {
		if len(p) > 0 {
			c.over = true
		}
		return len(p), nil
	}
	if len(p) > room {
		c.over = true
		c.buf.Write(p[:room])
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

// branchOutput is cmd's Output for a git read of a session's history, under
// the deadline and the output bound.
func branchOutput(cmd *exec.Cmd) ([]byte, error) {
	out := cappedBuffer{limit: maxBranchOutput}
	cmd.Stdout = &out
	err := runGroup(cmd, branchLimit)
	if err != nil {
		return nil, err
	}
	if out.over {
		return nil, fmt.Errorf("output over %d bytes", maxBranchOutput)
	}
	return out.buf.Bytes(), nil
}
