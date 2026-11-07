package check

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

// GitCheck runs the three read-only post-dispatch git checks on the checkout
// holding dir: `git status --short --untracked-files=all`,
// `git log --branches --not --remotes --oneline`, and `git tag`.
func GitCheck(dir string) (string, error) {
	_, top, err := trail.Roots(dir)
	if err != nil {
		if errors.Is(err, trail.ErrNotCheckout) {
			return "", refusal{dir + " is not a git checkout"}
		}
		return "", refusal{err.Error()}
	}
	statusOut, err := trail.Command(top, "status", "--short", "--untracked-files=all").Output()
	if err != nil {
		return "", fmt.Errorf("git status in %s: %w", top, err)
	}
	logOut, err := trail.Log(top, "--branches", "--not", "--remotes", "--oneline")
	if err != nil {
		return "", fmt.Errorf("git log in %s: %w", top, err)
	}
	tagOut, err := trail.Command(top, "tag").Output()
	if err != nil {
		return "", fmt.Errorf("git tag in %s: %w", top, err)
	}
	var b strings.Builder
	writeGitSection(&b, "git status --short --untracked-files=all", string(statusOut))
	b.WriteByte('\n')
	writeGitSection(&b, "git log --branches --not --remotes --oneline", string(logOut))
	b.WriteByte('\n')
	writeGitSection(&b, "git tag", string(tagOut))
	return b.String(), nil
}

func writeGitSection(b *strings.Builder, header, out string) {
	b.WriteString("## " + header + "\n")
	out = strings.TrimRight(out, "\r\n")
	if out == "" {
		b.WriteString("(none)\n")
		return
	}
	b.WriteString(out + "\n")
}
