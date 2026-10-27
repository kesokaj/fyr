package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The arms of runRecord that its split moves into helpers, pinned before
// the split (PLAN-2026-10-27-funclen Phase 6, D13). Exempt (D12): the
// os.Getwd error, which needs getcwd to fail.

// get of two IDs separates them with a blank line; edit --title changes the
// title and keeps the body; a wrong argument count is a usage error.
func TestRecordGetEditArms(t *testing.T) {
	recordRepo(t)
	home := t.TempDir()
	for _, title := range []string{"first", "second"} {
		if _, err := recordCmd(t, title+" body\n", "record", "add", "learning", "--title", title, "--home", home); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := recordCmd(t, "", "record", "list", "--home", home)
	if err != nil {
		t.Fatal(err)
	}
	var both []string
	for _, line := range strings.Split(strings.TrimSpace(ids), "\n") {
		both = append(both, strings.Fields(line)[0])
	}
	out, err := recordCmd(t, "", append(append([]string{"record", "get"}, both...), "--home", home)...)
	if err != nil || strings.Count(out, "\n\n") != 1 {
		t.Errorf("get of two: %v\n%q", err, out)
	}
	before, _ := recordCmd(t, "", "record", "get", both[0], "--home", home)
	_, body, _ := strings.Cut(before, "\n")
	if _, err := recordCmd(t, "", "record", "edit", both[0], "--title", "renamed", "--home", home); err != nil {
		t.Fatal(err)
	}
	out, _ = recordCmd(t, "", "record", "get", both[0], "--home", home)
	if !strings.Contains(out, "renamed") || !strings.HasSuffix(out, body) || !strings.Contains(body, " body") {
		t.Errorf("edit --title: %q, body before %q", out, body)
	}
	for _, args := range [][]string{{"edit"}, {"list", "x"}, {"import", "a", "b"}, {"import", "a", "--global"}} {
		if out, err := recordCmd(t, "", append(append([]string{"record"}, args...), "--home", home)...); exitOf(err) != 2 || !strings.HasPrefix(out, "usage: ") {
			t.Errorf("record %v: exit %d, %q", args, exitOf(err), out)
		}
	}
}

// import takes a docs root by name, and --global imports the home's old
// logs instead of a project's trail.
func TestRecordImportArms(t *testing.T) {
	root := recordRepo(t)
	home := t.TempDir()
	p := filepath.Join(root, "notes", "plans", "2026-01-01-elsewhere.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("# Elsewhere\n\n- [ ] left\n"), 0o644)
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "docs"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	out, err := recordCmd(t, "", "record", "import", "--dry-run", "notes", "--home", home)
	if err != nil || !strings.Contains(out, "PLAN-2026-01-01-elsewhere") {
		t.Errorf("import of a named root: %v\n%s", err, out)
	}
	out, err = recordCmd(t, "", "record", "import", "--global", "--dry-run", "--home", home)
	if err != nil || strings.Contains(out, "PLAN-") {
		t.Errorf("import --global: %v\n%s", err, out)
	}
}
