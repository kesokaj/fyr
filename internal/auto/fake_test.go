package auto

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/dream"
)

// fakeEnv's zero value is a machine that lets every gate through.
type fakeEnv struct {
	supplies    []Supply
	suppliesErr error
	batt, all   string
	pmsetErr    error
	sessions    []map[string]string
	ptys        []time.Time
	hid, root   string
	ioregErr    error
	agy         func() (bool, error)
	usage       string
	usageErr    error
	usageCalls  int
	targets     []dream.Target
	targetsErr  error
	conv        time.Time
	convErr     error
	onConv      func()
	onTargets   func()
}

func (f *fakeEnv) Supplies() ([]Supply, error) { return f.supplies, f.suppliesErr }

func (f *fakeEnv) Pmset(args ...string) (string, error) {
	if len(args) == 2 {
		return f.batt, f.pmsetErr
	}
	return f.all, f.pmsetErr
}

func (f *fakeEnv) Sessions(int) ([]map[string]string, error) { return f.sessions, nil }
func (f *fakeEnv) PTYs(int) ([]time.Time, error)             { return f.ptys, nil }

func (f *fakeEnv) Ioreg(args ...string) (string, error) {
	if args[0] == "-c" {
		return f.hid, f.ioregErr
	}
	return f.root, nil
}

func (f *fakeEnv) AgySession(string, int) (bool, error) {
	if f.agy != nil {
		return f.agy()
	}
	return false, nil
}

func (f *fakeEnv) Usage(_, _ string) (string, error) {
	f.usageCalls++
	return f.usage, f.usageErr
}

func (f *fakeEnv) Targets(string) ([]dream.Target, error) {
	if f.onTargets != nil {
		f.onTargets()
	}
	return f.targets, f.targetsErr
}

func (f *fakeEnv) LastConversation(string, string, time.Time) (time.Time, error) {
	if f.onConv != nil {
		f.onConv()
	}
	return f.conv, f.convErr
}

var errFake = errors.New("fake failure")

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	stamp := runAt.Add(-8 * time.Hour).Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp,
		"GIT_AUTHOR_NAME=f", "GIT_AUTHOR_EMAIL=f@localhost", "GIT_COMMITTER_NAME=f", "GIT_COMMITTER_EMAIL=f@localhost",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// idleProject is a clean clone, pushed, with its trail, whose every date is
// eight hours before runAt.
func idleProject(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	remote, root := filepath.Join(base, "remote.git"), filepath.Join(base, "project")
	git(t, base, "init", "-q", "--bare", "-b", "main", remote)
	git(t, base, "clone", "-q", remote, root)
	git(t, root, "checkout", "-q", "-b", "main")
	writeTree(t, root, ".agents/trail.jsonl", "")
	writeTree(t, root, "main.go", "package main\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "first")
	git(t, root, "push", "-q", "-u", "origin", "main")
	backdate(t, root)
	return root
}

func writeTree(t *testing.T, root, name, text string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func backdate(t *testing.T, root string) {
	t.Helper()
	at := runAt.Add(-8 * time.Hour)
	for _, name := range strings.Split(git(t, root, "ls-files"), "\n") {
		if err := os.Chtimes(filepath.Join(root, name), at, at); err != nil {
			t.Fatal(err)
		}
	}
}
