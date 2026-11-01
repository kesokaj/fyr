package harness

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The review skill's restatement grep runs in EVERY project, not only in the
// harness checkout: a project without skills/VENDORED.md must still get its
// hits. With pipefail, an empty vendored list once made the whole command
// substitution fail and the script exit 1 — silent, the same as "no hits".
func TestRestatementGrepRunsOutsideHarness(t *testing.T) {
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "skills", "review", "scripts", "restatement-grep.sh"))
	if err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// run returns stdout and stderr separately: a hit is asserted on stdout alone,
	// so an error message on stderr can never pass for one.
	run := func(dir string, args ...string) (stdout, stderr string, code int) {
		t.Helper()
		cmd := exec.Command("bash", append([]string{script}, args...)...)
		cmd.Dir = dir
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return out.String(), errb.String(), code
	}

	t.Run("project without VENDORED.md", func(t *testing.T) {
		dir := t.TempDir()
		git(dir, "init", "-q")
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("GET /hello?name=<who> greets name.\nsecond line, no match\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(dir, "add", "README.md")
		git(dir, "commit", "-qm", "init")
		// Hit: exit 0, path:line: text even for a single file (-H).
		out, errs, code := run(dir, "-i", "--", "name")
		if code != 0 || out != "README.md:1:GET /hello?name=<who> greets name.\n" || errs != "" {
			t.Fatalf("hit: want exit 0 and one prefixed line, got exit %d stdout %q stderr %q", code, out, errs)
		}
		// No hit: exit 1, nothing printed, no error either.
		out, errs, code = run(dir, "--", "zzznomatch")
		if code != 1 || out != "" || errs != "" {
			t.Fatalf("miss: want exit 1 and no output, got exit %d stdout %q stderr %q", code, out, errs)
		}
		// --all: every word on the line; the same -H prefix feeds its path strip.
		out, _, code = run(dir, "--all", "--", "hello", "greets")
		if code != 0 || out != "README.md:1:GET /hello?name=<who> greets name.\n" {
			t.Fatalf("--all hit: want exit 0 and one prefixed line, got exit %d stdout %q", code, out)
		}
		out, _, code = run(dir, "--all", "--", "hello", "second")
		if code != 1 || out != "" {
			t.Fatalf("--all miss (words on different lines): want exit 1 and no output, got exit %d stdout %q", code, out)
		}
		// macOS ships /bin/bash 3.2 without mapfile/readarray; disable both
		// via BASH_ENV to prove the script runs under Bash 3.2.
		noMapfile := filepath.Join(t.TempDir(), "nomapfile.sh")
		if err := os.WriteFile(noMapfile, []byte("enable -n mapfile readarray\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("BASH_ENV", noMapfile)
		out, errs, code = run(dir, "-i", "--", "name")
		if code != 0 || out != "README.md:1:GET /hello?name=<who> greets name.\n" || errs != "" {
			t.Fatalf("without mapfile: want exit 0 and one prefixed line, got exit %d stdout %q stderr %q", code, out, errs)
		}
	})

	t.Run("harness-shaped checkout excludes vendored skills", func(t *testing.T) {
		dir := t.TempDir()
		git(dir, "init", "-q")
		for p, body := range map[string]string{
			"skills/VENDORED.md":   "| Skill | Source |\n|---|---|\n| foo | somewhere |\n",
			"skills/foo/SKILL.md":  "the vendored word\n",
			"skills/mine/SKILL.md": "the living word\n",
			".agents/trail.jsonl":  "the record word\n",
			// A file under an old docs root is an ordinary file now (ADR-087).
			".agents/docs/changes/x": "the old word\n",
		} {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		git(dir, "add", ".")
		git(dir, "commit", "-qm", "init")
		out, _, code := run(dir, "--", "word")
		if code != 0 || !strings.Contains(out, "skills/mine/SKILL.md:1:") {
			t.Fatalf("want exit 0 with the living hit, got exit %d:\n%s", code, out)
		}
		if !strings.Contains(out, ".agents/docs/changes/x:1:") {
			t.Fatalf("a file under an old docs root is excluded:\n%s", out)
		}
		if strings.Contains(out, "skills/foo/") || strings.Contains(out, ".agents/trail.jsonl") {
			t.Fatalf("vendored or record file reported:\n%s", out)
		}
	})
}

// A VENDORED.md row whose first cell is a path — a vendored catalog inside a
// harness skill — excludes that subtree from the living files, and only it:
// the skill's own SKILL.md beside the catalog stays in.
func TestRestatementGrepSkipsVendoredCatalog(t *testing.T) {
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "skills", "review", "scripts", "restatement-grep.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("skills/VENDORED.md", "| Catalog | Source | Pin |\n|---|---|---|\n| x/catalog | github.com/o/r | abc1234 |\n")
	write("skills/x/catalog/e/SKILL.md", "an entry that says zebraword\n")
	write("skills/x/SKILL.md", "the router says zebraword too\n")
	write("skills/y/SKILL.md", "a living skill that says zebraword\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-qm", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	cmd := exec.Command("bash", script, "--", "zebraword")
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("script: %v\nstderr: %s", err, errb.String())
	}
	got := out.String()
	for _, want := range []string{"skills/x/SKILL.md:1:", "skills/y/SKILL.md:1:"} {
		if !strings.Contains(got, want) {
			t.Errorf("a living file is missing from the hits (%s):\n%s", want, got)
		}
	}
	if strings.Contains(got, "skills/x/catalog/") {
		t.Errorf("the vendored catalog is in the hits:\n%s", got)
	}
}
