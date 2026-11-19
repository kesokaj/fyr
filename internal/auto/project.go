package auto

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

// gitPath is git's own answer for a path in root's git directory, so a linked
// worktree reads its own.
func gitPath(root, name string) (string, error) {
	out, err := trail.Command(root, "rev-parse", "--path-format=absolute", "--git-path", name).Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gateClean is G8: why root is not clean, or "". A question git cannot answer
// stops the project.
func gateClean(root string) string {
	stop := func(why string) string { return "G8 clean: " + why }
	for _, name := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply"} {
		path, err := gitPath(root, name)
		if err != nil {
			return stop(oneLine(err.Error()))
		}
		if _, err := os.Lstat(path); err == nil {
			return stop("an operation is in progress (" + name + ")")
		}
	}
	if err := trail.Command(root, "symbolic-ref", "-q", "HEAD").Run(); err != nil {
		return stop("HEAD is not a branch")
	}
	if err := trail.Command(root, "rev-parse", "--verify", "-q", "@{upstream}").Run(); err != nil {
		return stop("the branch has no upstream")
	}
	ahead, err := trail.Command(root, "rev-list", "--count", "@{upstream}..HEAD").Output()
	if err != nil {
		return stop("git rev-list: " + oneLine(err.Error()))
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(ahead)))
	if err != nil {
		return stop("git rev-list: " + oneLine(err.Error()))
	}
	if n > 0 {
		return stop(fmt.Sprintf("the branch is %d ahead of its upstream", n))
	}
	dirty, err := trail.Dirty(root)
	if err != nil {
		return stop(oneLine(err.Error()))
	}
	if dirty {
		return stop("the tree is dirty")
	}
	return ""
}
