package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// recordRepo is a git checkout with the scratch directory ignored, as
// /init-project leaves it, and the working directory moved into it.
func recordRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.invalid"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".agents/scratch/*\n"), 0o644)
	t.Chdir(root)
	return root
}

func recordCmd(t *testing.T, body string, args ...string) (string, error) {
	t.Helper()
	stdin = strings.NewReader(body)
	defer func() { stdin = os.Stdin }()
	var out bytes.Buffer
	err := run(args, &out)
	return out.String(), err
}

func exitOf(err error) int {
	if err == nil {
		return 0
	}
	var code exitCode
	if errors.As(err, &code) {
		return int(code)
	}
	return 1
}

// The verbs end to end through run(): add prints the ID, get prints the
// header and body, edit without --body keeps the body, list prints one line
// per record; and nothing touches the real home (R6): $HOME is a decoy that
// must stay empty.
func TestRecordVerb(t *testing.T) {
	root := recordRepo(t)
	home := t.TempDir()
	decoy := t.TempDir()
	t.Setenv("HOME", decoy)

	out, err := recordCmd(t, "Symptom: x.\nRule: y.\n", "record", "add", "bug", "--title", "a trailer slipped in", "--home", home)
	if err != nil || out != "BUG-001\n" {
		t.Fatalf("add: %q %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir, "brain.db")); err != nil {
		t.Fatalf("brain.db not under --home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", "trail.jsonl")); err != nil {
		t.Fatalf("no export: %v", err)
	}
	out, err = recordCmd(t, "", "record", "edit", "BUG-001", "--status", "fixed", "--home", home)
	if err != nil {
		t.Fatalf("edit: %q %v", out, err)
	}
	out, err = recordCmd(t, "", "record", "get", "BUG-001", "--home", home)
	if err != nil || !strings.HasPrefix(out, "BUG-001  bug  fixed  ") || !strings.HasSuffix(out, "a trailer slipped in\nSymptom: x.\nRule: y.\n") {
		t.Fatalf("get: %q %v", out, err)
	}
	out, err = recordCmd(t, "", "record", "list", "--home", home)
	if err != nil || !strings.HasPrefix(out, "BUG-001  bug  fixed  ") || strings.Count(out, "\n") != 1 {
		t.Fatalf("list: %q %v", out, err)
	}
	out, err = recordCmd(t, "", "record", "list", "--open", "--home", home)
	if err != nil || out != "" {
		t.Errorf("list --open with nothing open: %q %v, want no output and exit 0", out, err)
	}
	out, err = recordCmd(t, "feedback body", "record", "add", "feedback", "--global", "--project", root, "--title", "global fb", "--home", home)
	if err != nil || !strings.HasPrefix(out, "FEEDBACK-") {
		t.Fatalf("add --global feedback: %q %v", out, err)
	}
	if entries, _ := os.ReadDir(decoy); len(entries) != 0 {
		t.Errorf("the real home was touched: %v", entries)
	}
}

// get prints through printable: a record cannot drive the terminal.
func TestRecordGetIsPrintable(t *testing.T) {
	recordRepo(t)
	home := t.TempDir()
	if out, err := recordCmd(t, "evil \x1b[2J\x1b]0;pwned\x07 body\n", "record", "add", "learning", "--title", "t‮itle", "--home", home); err != nil {
		t.Fatalf("add: %q %v", out, err)
	}
	list, _ := recordCmd(t, "", "record", "list", "--home", home)
	id := strings.Fields(list)[0]
	out, err := recordCmd(t, "", "record", "get", id, "--home", home)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out, "\x1b\x07‮") {
		t.Errorf("get printed control characters: %q", out)
	}
}

// Exit codes: 0 with or without output, 1 a refusal (its reason on the
// writer), 2 a usage error.
func TestRecordExitCodes(t *testing.T) {
	recordRepo(t)
	home := t.TempDir()
	for name, tc := range map[string]struct {
		body string
		args []string
		code int
		want string
	}{
		"no subverb":                  {"", []string{"record"}, 2, "usage: fyr record"},
		"unknown subverb":             {"", []string{"record", "drop"}, 2, "usage: fyr record"},
		"add without a kind":          {"b", []string{"record", "add", "--title", "t"}, 2, "usage"},
		"add without a title":         {"b", []string{"record", "add", "bug"}, 2, "--title"},
		"add of an unknown kind":      {"b", []string{"record", "add", "note", "--title", "t"}, 2, "kind"},
		"a bad status":                {"b", []string{"record", "add", "bug", "--title", "t", "--status", "done"}, 2, "status"},
		"a flag add does not take":    {"b", []string{"record", "add", "bug", "--title", "t", "--open"}, 2, "--open"},
		"edit with nothing to change": {"", []string{"record", "edit", "BUG-001"}, 2, "usage"},
		"get without an id":           {"", []string{"record", "get"}, 2, "usage"},
		"--project without --global":  {"b", []string{"record", "add", "learning", "--title", "t", "--project", "/x"}, 2, "--global"},
		"an empty body":               {"", []string{"record", "add", "bug", "--title", "t"}, 1, "empty"},
		"feedback in a project":       {"b", []string{"record", "add", "feedback", "--title", "t"}, 1, "global"},
		"get of a missing id":         {"", []string{"record", "get", "BUG-404"}, 1, "BUG-404"},
		"list on an empty store":      {"", []string{"record", "list"}, 0, ""},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := recordCmd(t, tc.body, append(tc.args, "--home", home)...)
			if got := exitOf(err); got != tc.code {
				t.Fatalf("%v: exit %d (%v), want %d\n%s", tc.args, got, err, tc.code, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("%v: %q lacks %q", tc.args, out, tc.want)
			}
		})
	}
}

// D16 through the verb: an unmigrated project refuses with exit 1 and names
// the import command.
func TestRecordUnmigratedRefusal(t *testing.T) {
	root := recordRepo(t)
	os.MkdirAll(filepath.Join(root, ".agents", "docs", "plans"), 0o755)
	out, err := recordCmd(t, "", "record", "list", "--home", t.TempDir())
	if exitOf(err) != 1 || !strings.Contains(out, "fyr record import") {
		t.Fatalf("list in an unmigrated project: %q exit %d", out, exitOf(err))
	}
}

// fyr search prints record hits first, then file hits: a record outranks a
// file however well the file matches.
func TestSearchRanksARecordAboveAFile(t *testing.T) {
	root := recordRepo(t)
	home := t.TempDir()
	os.WriteFile(filepath.Join(root, "notes.md"), []byte("# Marmot trailer\n\nmarmot trailer marmot trailer marmot trailer\n"), 0o644)
	if out, err := recordCmd(t, "Rule: no marmot trailer.\n", "record", "add", "bug", "--title", "the marmot trailer", "--home", home); err != nil {
		t.Fatalf("add: %q %v", out, err)
	}
	out, err := recordCmd(t, "", "search", "marmot", "trailer", "--home", home)
	if err != nil {
		t.Fatalf("search: %v\n%s", err, out)
	}
	rec, file := strings.Index(out, "BUG-001  bug open  "), strings.Index(out, "notes.md:")
	if rec < 0 || file < 0 || rec > file {
		t.Fatalf("want the record hit before the file hit:\n%s", out)
	}
	if !strings.HasPrefix(out, "records: 1 hits") {
		t.Errorf("no records header:\n%s", out)
	}
	// A usage error is still exit 2 with no record printed first.
	out, err = recordCmd(t, "", "search", "--limit", "99", "marmot", "--home", home)
	if exitOf(err) != 2 || strings.Contains(out, "BUG-001") {
		t.Errorf("search --limit 99: exit %d\n%s", exitOf(err), out)
	}
	// No store and no trail: search prints only what it printed before.
	other := recordRepo(t)
	os.WriteFile(filepath.Join(other, "notes.md"), []byte("marmot\n"), 0o644)
	fresh := t.TempDir()
	out, err = recordCmd(t, "", "search", "marmot", "--home", fresh)
	if err != nil || strings.Contains(out, "records:") {
		t.Errorf("search with no records: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(fresh, harness.StateDir, "brain.db")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a search with no records created brain.db")
	}
}

// fyr help lists the verb and its four subverbs.
func TestHelpListsRecord(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"help"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\n  record add ", "\n  record get ", "\n  record edit ", "\n  record list "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q", want)
		}
	}
}
