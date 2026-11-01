package auto

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/trail"
)

// reflogTail is how much of a reflog's end G7 reads: more than any one entry.
const reflogTail = 64 << 10

// gateIdle is G7: what kept root busy within idle of now, or "". Each time is
// clamped to now, so a date ahead of the clock counts as now, toward not
// running. A reading that fails stops the project.
func gateIdle(env Env, home, root string, idle time.Duration, now time.Time) string {
	stop := func(what string, at time.Time) string {
		return fmt.Sprintf("G7 idle: %s at %s", what, at.UTC().Format(time.RFC3339))
	}
	cut := now.Add(-idle)
	busy := func(t time.Time) bool { return t.After(cut) }
	info, err := os.Stat(filepath.Join(root, ".agents", "trail.jsonl"))
	if err != nil {
		return "G7 idle: unreadable: " + oneLine(err.Error())
	}
	if busy(info.ModTime()) {
		return stop("the trail", info.ModTime())
	}
	last, err := lastReflog(root)
	if err != nil {
		return "G7 idle: unreadable: " + oneLine(err.Error())
	}
	if busy(last) {
		return stop("a git operation", last)
	}
	conv, err := env.LastConversation(home, root, cut)
	if err != nil {
		return "G7 idle: unreadable: " + oneLine(err.Error())
	}
	if busy(conv) {
		return stop("an Antigravity session", conv)
	}
	at, file, err := newestTracked(root, cut)
	if err != nil {
		return "G7 idle: unreadable: " + oneLine(err.Error())
	}
	if !at.IsZero() {
		return stop(oneLine(file), at)
	}
	return ""
}

// gitPath is git's own answer for a path in root's git directory, so a linked
// worktree reads its own.
func gitPath(root, name string) (string, error) {
	out, err := trail.Command(root, "rev-parse", "--path-format=absolute", "--git-path", name).Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

var reflogTime = regexp.MustCompile(`> ([0-9]+) [-+][0-9]{4}(\t|$)`)

// lastReflog is the time of the last entry of HEAD's reflog; zero with no
// reflog, which a fresh clone can have. A tail it cannot date is an error.
func lastReflog(root string) (time.Time, error) {
	path, err := gitPath(root, "logs/HEAD")
	if err != nil {
		return time.Time{}, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return time.Time{}, err
	}
	if !info.Mode().IsRegular() {
		return time.Time{}, errors.New(path + " is not a regular file")
	}
	if info.Size() == 0 {
		return time.Time{}, nil
	}
	if _, err := f.Seek(max(0, info.Size()-reflogTail), io.SeekStart); err != nil {
		return time.Time{}, err
	}
	data, err := io.ReadAll(io.LimitReader(f, reflogTail))
	if err != nil {
		return time.Time{}, err
	}
	data = bytes.TrimRight(data, "\n")
	line := data[bytes.LastIndexByte(data, '\n')+1:]
	m := reflogTime.FindSubmatch(line)
	if m == nil {
		return time.Time{}, errors.New("a reflog entry with no time: " + path)
	}
	sec, err := strconv.ParseInt(string(m[1]), 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
}

// newestTracked stops at the first tracked file modified after cut, by the
// file itself and not what a link names. A tracked file that is gone is G8's.
func newestTracked(root string, cut time.Time) (time.Time, string, error) {
	out, err := trail.Command(root, "ls-files", "-z").Output()
	if err != nil {
		return time.Time{}, "", fmt.Errorf("git ls-files: %w", err)
	}
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil {
			continue
		}
		if info.ModTime().After(cut) {
			return info.ModTime(), name, nil
		}
	}
	return time.Time{}, "", nil
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
