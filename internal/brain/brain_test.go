package brain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fixed = time.Date(2026, 10, 25, 14, 30, 0, 0, time.UTC)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo is a fresh git checkout with no trail at all: a migrated project.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	return dir
}

// opts is a project-scope call in dir against a scratch home, never the
// real one (R6).
func opts(dir, home string) Options {
	return Options{Home: home, Dir: dir, Now: func() time.Time { return fixed }}
}

func add(t *testing.T, o Options, kind, title, body string, extra ...string) string {
	t.Helper()
	in := AddInput{Kind: kind, Title: title, Body: strings.NewReader(body)}
	for i := 0; i+1 < len(extra); i += 2 {
		switch extra[i] {
		case "status":
			in.Status = extra[i+1]
		case "id":
			in.ID = extra[i+1]
		case "project":
			in.Project = extra[i+1]
		}
	}
	id, err := Add(o, in)
	if err != nil {
		t.Fatalf("add %s %q: %v", kind, title, err)
	}
	return id
}

func trailFile(dir string) string { return filepath.Join(dir, ".agents", "trail.jsonl") }

func trailText(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(trailFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func str(s string) *string { return &s }
