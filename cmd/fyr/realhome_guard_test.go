package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
)

// BUG-507: a development build (`go run`, a scratch `go build`) run with no
// --home wrote the real home's stores. The guard refuses every verb but
// install, sync, update, version, list, render and diff unless the running
// fyr is the home's installed binary or --home was given (ADR-505).

// guardHome is a temporary $HOME with an installed binary, and the running
// fyr set to self: "installed" for that binary, "" for a separate dev build.
func guardHome(t *testing.T, self string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	installed := filepath.Join(home, harness.BinPath)
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("installed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if self == "installed" {
		self = installed
	} else if self == "" {
		self = filepath.Join(t.TempDir(), "fyr")
		if err := os.WriteFile(self, []byte("dev build"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := guardSelf
	guardSelf = func() (string, error) { return self, nil }
	t.Cleanup(func() { guardSelf = old })
	return home
}

// asInstalled makes the running fyr the installed binary of home, for a
// test of a verb's own behaviour on an unflagged home, past the guard.
func asInstalled(t *testing.T, home string) {
	t.Helper()
	installed := filepath.Join(home, harness.BinPath)
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("installed"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := guardSelf
	guardSelf = func() (string, error) { return installed, nil }
	t.Cleanup(func() { guardSelf = old })
}

// guardFor parses args as run does and returns the guard's verdict.
func guardFor(t *testing.T, args ...string) (error, string) {
	t.Helper()
	var out bytes.Buffer
	c := newCtx(args[0], verbs[args[0]], &out)
	if stop, err := c.parse(verbs[args[0]], args[1:]); stop {
		t.Fatalf("%v: parse stopped: %v %s", args, err, out.String())
	}
	if err := c.resolveHome(); err != nil {
		t.Fatal(err)
	}
	return c.refuseRealHomeWrite(), out.String()
}

func isGuardRefusal(err error, out string) bool {
	var code exitCode
	return errors.As(err, &code) && code == 2 && strings.Contains(out, "BUG-507")
}

// Writing invocations, all guarded.
var guardedInvocations = [][]string{
	{"record", "list"},
	{"record", "get", "BUG-1"},
	{"record", "add", "bug", "--title", "x"},
	{"memory", "add", "--type", "project", "--name", "n", "--description", "d"},
	{"metrics", "ingest"},
	{"index"},
	{"index", "--global"},
	{"search", "words"},
	{"dream"},
	{"auto", "on"},
	{"auto", "run"},
	{"work", "task.md"},
	{"eval", "row"},
	{"uninstall"},
	{"adopt", "skill"},
}

func TestRealHomeGuardRefusesDevBuild(t *testing.T) {
	guardHome(t, "")
	for _, args := range guardedInvocations {
		if err, out := guardFor(t, args...); !isGuardRefusal(err, out) {
			t.Errorf("%v: err = %v, out %q; want the BUG-507 refusal", args, err, out)
		}
	}
}

func TestRealHomeGuardPassesInstalledBinary(t *testing.T) {
	guardHome(t, "installed")
	for _, args := range guardedInvocations {
		if err, out := guardFor(t, args...); err != nil {
			t.Errorf("%v: err = %v, out %q; the installed binary passes", args, err, out)
		}
	}
}

// A link to the installed binary is that binary (file identity, not path).
func TestRealHomeGuardPassesLinkToInstalled(t *testing.T) {
	home := guardHome(t, "")
	link := filepath.Join(t.TempDir(), "fyr")
	if err := os.Symlink(filepath.Join(home, harness.BinPath), link); err != nil {
		t.Fatal(err)
	}
	guardSelf = func() (string, error) { return link, nil }
	if err, out := guardFor(t, "record", "list"); err != nil {
		t.Errorf("err = %v, out %q", err, out)
	}
}

// An explicit --home is the opt-in, the real home included.
func TestRealHomeGuardPassesExplicitHome(t *testing.T) {
	home := guardHome(t, "")
	for _, h := range []string{home, t.TempDir()} {
		for _, args := range guardedInvocations {
			args = append(append([]string{}, args...), "--home", h)
			if err, out := guardFor(t, args...); err != nil {
				t.Errorf("%v: err = %v, out %q", args, err, out)
			}
		}
	}
}

// install, sync and update place the binary, so a build that is not it yet
// runs them, dry runs and --check included; version, list, render and diff
// open no store under the home.
func TestRealHomeGuardExemptions(t *testing.T) {
	guardHome(t, "")
	for _, args := range [][]string{
		{"install"}, {"sync"}, {"update"},
		{"install", "--dry-run"}, {"update", "--check"},
		{"list"}, {"render"}, {"diff"}, {"version"},
	} {
		if err, out := guardFor(t, args...); err != nil {
			t.Errorf("%v: err = %v, out %q; not guarded", args, err, out)
		}
	}
}

// Reads and dry runs are guarded too: a read opens a store, and opening one
// migrates it forward (the review of BUG-507's fix, finding 1).
func TestRealHomeGuardRefusesReads(t *testing.T) {
	guardHome(t, "")
	for _, args := range [][]string{
		{"status"}, {"memory", "show"}, {"metrics", "show"}, {"metrics", "report"}, {"report"},
		{"metrics", "evals"}, {"record", "import", "--dry-run"}, {"auto", "status"},
		{"auto", "run", "--dry-run"}, {"dream", "--dry-run"}, {"uninstall", "--dry-run"},
		{"work", "--check"}, {"eval", "--list"}, {"resume"}, {"git-check"},
	} {
		if err, out := guardFor(t, args...); !isGuardRefusal(err, out) {
			t.Errorf("%v: err = %v, out %q; want the BUG-507 refusal", args, err, out)
		}
	}
}

// A store at schema 0 (an empty file is an empty SQLite database) is what
// a read by a newer build migrates: refused through run, both stay empty.
func TestRealHomeGuardReadsLeaveOlderStores(t *testing.T) {
	home := guardHome(t, "")
	stores := []string{metrics.Path(home), brain.Path(home)}
	for _, p := range stores {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"metrics", "show"}, {"memory", "show"}, {"status"}} {
		var out bytes.Buffer
		if err := run(args, &out); !isGuardRefusal(err, out.String()) {
			t.Errorf("%v: err = %v, out %q", args, err, out.String())
		}
	}
	for _, p := range stores {
		if fi, err := os.Stat(p); err != nil || fi.Size() != 0 {
			t.Errorf("%s changed: %v %v", p, fi, err)
		}
	}
}

// The check failing is not evidence the running fyr is the installed one:
// no installed binary, or an unreadable executable path, refuses.
func TestRealHomeGuardRefusesWhenUnsure(t *testing.T) {
	home := guardHome(t, "installed")
	guardSelf = func() (string, error) { return "", errors.New("no executable") }
	if err, out := guardFor(t, "record", "list"); !isGuardRefusal(err, out) {
		t.Errorf("unreadable self: err = %v, out %q", err, out)
	}
	guardSelf = func() (string, error) { return filepath.Join(home, harness.BinPath), nil }
	if err := os.Remove(filepath.Join(home, harness.BinPath)); err != nil {
		t.Fatal(err)
	}
	if err, out := guardFor(t, "record", "list"); !isGuardRefusal(err, out) {
		t.Errorf("no installed binary: err = %v, out %q", err, out)
	}
}

// End to end through run: the refusal comes before the state directory is
// migrated or any store opened, so the real home is untouched.
func TestRealHomeGuardRunWritesNothing(t *testing.T) {
	home := guardHome(t, "")
	old := filepath.Join(home, harness.OldStateDir)
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"metrics", "ingest"}, {"record", "list"}, {"memory", "add", "--type", "project", "--name", "n", "--description", "d"}} {
		var out bytes.Buffer
		err := run(args, &out)
		if !isGuardRefusal(err, out.String()) {
			t.Errorf("%v: err = %v, out %q", args, err, out.String())
		}
	}
	if _, err := os.Lstat(filepath.Join(home, harness.StateDir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s exists after the refusals: %v", harness.StateDir, err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("the old state directory moved: %v", err)
	}
}
