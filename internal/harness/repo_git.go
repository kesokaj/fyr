package harness

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// RepoInfo is the git state `fyr status` shows.
type RepoInfo struct {
	Branch string
	Commit string
	Dirty  bool
	// StatusUnknown is set when git could not report the tree's state at all.
	// A caller deciding whether to DEPLOY treats it as dirty: "git failed" is
	// not "the tree is clean" .
	StatusUnknown bool
}

// errPlainCopy is returned by Behind and Pull for a tree git cannot update.
var errPlainCopy = errors.New("not its own git checkout; a plain copy cannot be updated with git — `install` still deploys it as it is; to move forward, use a checkout (`git clone git@github.com:kesokaj/fyr.git`) or a release binary (README § Install without cloning)")

// IsCheckout reports whether repo is the top of its own git checkout: a
// `.git` entry sits directly in it (a directory, or the file a worktree
// carries). A harness-shaped tree nested inside another repository (a clone
// kept under review, a copy inside a project) has none and is a plain copy;
// asking git would answer for the enclosing repository, and git's answer
// can be redirected by its environment, so the check never asks git.
func IsCheckout(repo string) bool {
	_, err := os.Lstat(filepath.Join(repo, ".git"))
	return err == nil
}

// Inspect reads branch, commit, and cleanliness. A directory that is not its
// own git checkout yields empty fields rather than an error: the harness
// works from a plain copy too, only `update` needs git.
func Inspect(repo string) RepoInfo {
	if !IsCheckout(repo) {
		return RepoInfo{}
	}
	branch, _ := gitOut(repo, "rev-parse", "--abbrev-ref", "HEAD")
	commit, _ := gitOut(repo, "rev-parse", "--short", "HEAD")
	// --untracked-files=all, never the bare form: with `status.showUntrackedFiles
	// no`, a plain `--porcelain` reports a tree holding a wholly untracked
	// skill directory as clean, which `update` would then deploy.
	status, statusErr := gitOut(repo, "status", "--porcelain", "--untracked-files=all")
	return RepoInfo{Branch: branch, Commit: commit, Dirty: status != "", StatusUnknown: statusErr != nil}
}

// Behind fetches the upstream and reports how many commits HEAD lags it.
func Behind(repo string) (int, error) {
	if !IsCheckout(repo) {
		return 0, errPlainCopy
	}
	if _, err := gitOut(repo, "fetch", "--quiet"); err != nil {
		// Name the remote: git's text names a host and a port at most, not
		// which checkout's remote to fix (PLAN.md item 40, borken).
		remote := "the branch's upstream remote"
		if url, uerr := gitOut(repo, "ls-remote", "--get-url"); uerr == nil {
			if shown := remoteForDisplay(url); shown != "" {
				remote = shown
			}
		}
		return 0, fmt.Errorf("git fetch from %s failed; fyr runs git as you would, so fix it as for `git -C %s fetch` (on GitHub over SSH with port 22 blocked, ssh.github.com port 443 works): %s", remote, ShellArg(repo), urlQuery.ReplaceAllString(err.Error(), "$1"))
	}
	n, err := gitOut(repo, "rev-list", "--count", "HEAD..@{upstream}")
	if err != nil {
		return 0, fmt.Errorf("no upstream for the current branch: %w", err)
	}
	return strconv.Atoi(n)
}

// remoteForDisplay is a remote's URL fit for an error, or "" when no part of
// it can be shown safely. `ls-remote --get-url` prints it as configured or as
// an insteadOf rule rewrote it, a token included: a URL loses its userinfo,
// query and fragment, a URL that does not parse is not shown at all, and the
// scp-like `[user@]host:path` loses its user part. A control character is
// escaped.
func remoteForDisplay(remote string) string {
	shown := remote
	switch u, err := url.Parse(remote); {
	case err == nil && u.Scheme != "" && u.Host != "":
		u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
		shown = u.String()
	case strings.Contains(remote, "://"):
		return ""
	default:
		// The user part ends at the last @ before the path's first /.
		host := remote
		if slash := strings.Index(remote, "/"); slash >= 0 {
			host = remote[:slash]
		}
		if at := strings.LastIndex(host, "@"); at >= 0 {
			shown = remote[at+1:]
		}
	}
	if strings.IndexFunc(shown, notPrintable) >= 0 {
		return strconv.Quote(shown)
	}
	return shown
}

// urlQuery is a URL's query in git's own text: git prints it, and it can hold
// a token.
var urlQuery = regexp.MustCompile(`(://[^\s'"?]*)\?[^\s'"]*`)

// ShellArg quotes s for a command the operator may paste: in single quotes,
// so a space, a $ or a quote cannot change the command, or, when s holds a
// control character, in $'…' with that character and any quote escaped, which
// bash and zsh read back as s. A sh without $'…', dash among them, reads it as
// a literal string, since \x27 leaves no quote inside to close it: never in
// double quotes, inside which a shell runs $(…).
func ShellArg(s string) string {
	if strings.IndexFunc(s, notPrintable) < 0 {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	var b strings.Builder
	b.WriteString("$'")
	for _, r := range s {
		switch {
		case r == '\'':
			b.WriteString(`\x27`)
		case r == '\\':
			b.WriteString(`\\`)
		case notPrintable(r) && r < 0x80:
			fmt.Fprintf(&b, `\x%02x`, r)
		case notPrintable(r) && r <= 0xFFFF:
			fmt.Fprintf(&b, `\u%04x`, r)
		case notPrintable(r):
			fmt.Fprintf(&b, `\U%08x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

func notPrintable(r rune) bool { return !unicode.IsPrint(r) }

// Pull fast-forwards to upstream. It refuses a dirty tree: pulling over
// uncommitted edits to the harness is how local changes get lost. Git runs
// in the operator's own environment on purpose: GIT_* variables, git config,
// GIT_SSH_COMMAND and PATH are theirs to set, exactly as for a `git pull`
// typed by hand; only the plain-copy check above is kept independent of
// git so a typo or a nested copy can never redirect a build.
func Pull(repo string, out io.Writer) error {
	if !IsCheckout(repo) {
		return errPlainCopy
	}
	// `update` refuses a dirty checkout before it gets here, with a message
	// that also covers the up-to-date path (BUG-046); this one guards a
	// direct call to Pull. Both say the same thing: uncommitted work is never
	// pulled over and never deployed, and an unreadable status counts as
	// dirty rather than clean.
	if info := Inspect(repo); info.Dirty || info.StatusUnknown {
		return errors.New("the harness repo has uncommitted changes, or git could not report its state; commit or stash them before `fyr update`")
	}
	cmd := exec.Command("git", "-C", repo, "pull", "--ff-only", "--quiet")
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git pull --ff-only: %w", err)
	}
	return nil
}

func gitOut(repo string, args ...string) (string, error) {
	return gitRun(exec.Command("git", append([]string{"-C", repo}, args...)...), args)
}

func gitRun(cmd *exec.Cmd, args []string) (string, error) {
	data, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
