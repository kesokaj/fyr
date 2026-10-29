package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// TestRunInstallStatusUninstall drives the CLI end to end against this
// checkout and a scratch home. ChildEnv is set so install takes the plain
// path: under `go test` the running binary is the test binary, and a rebuild
// here would be a real `go build` of the repo for nothing.
func TestRunInstallStatusUninstall(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	home := t.TempDir()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"install", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "files managed") {
		t.Errorf("install output:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, harness.BinPath)); err != nil {
		t.Errorf("binary not installed: %v", err)
	}
	out.Reset()
	if err := run([]string{"status", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("status: %v\n%s", err, out.String())
	}
	for _, want := range []string{"binary     ", "drift      none"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status missing %q:\n%s", want, out.String())
		}
	}
	// The binary line reads the installed file's build info: the test binary
	// was copied there, so the line must carry its version, never "unknown".
	if strings.Contains(out.String(), "binary     unknown") {
		t.Errorf("status could not read the installed binary's build info:\n%s", out.String())
	}
	out.Reset()
	if err := run([]string{"uninstall", "--home", home}, &out); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	// The vendor roots themselves are kept on purpose (pruneEmptyDirs stops
	// there); everything the manifest listed under them is gone.
	for _, rel := range []string{filepath.Join(harness.AgyRoot, "rules", "AGENTS.md"), filepath.Join(harness.AgyRoot, "skills"), filepath.Join(harness.AgyRoot, "agents"), harness.BinPath} {
		if _, err := os.Stat(filepath.Join(home, rel)); !os.IsNotExist(err) {
			t.Errorf("~/%s should be gone after uninstall, stat err = %v", rel, err)
		}
	}
	out.Reset()
	if err := run([]string{"status", "--repo", repo, "--home", home}, &out); err == nil {
		t.Error("status after uninstall should report exit 1")
	}
}

// TestInstallLeavesClaudeCodeAlone: the files an operator keeps under
// ~/.claude — the settings an install before ADR-062 wrote two keys into, a
// hand-written CLAUDE.md — are read by no verb: install and status leave
// their bytes as they are, write nothing beside them, and print no line about
// them (plan fyr Phase 2, D2).
func TestInstallLeavesClaudeCodeAlone(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	home := t.TempDir()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := map[string]string{
		"settings.json": `{"attribution": {"commit": ""}, "autoCompactWindow": 300000, "theme": "dark"}` + "\n",
		"CLAUDE.md":     "my own notes\n",
	}
	for name, body := range mine {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	for _, args := range [][]string{{"install"}, {"status"}} {
		if err := run(append(args, "--repo", repo, "--home", home), &out); err != nil {
			t.Fatalf("%s: %v\n%s", args[0], err, out.String())
		}
	}
	for name, body := range mine {
		if got, _ := os.ReadFile(filepath.Join(dir, name)); string(got) != body {
			t.Errorf("~/.claude/%s changed: %q", name, got)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != len(mine) {
		t.Errorf("a verb wrote under ~/.claude: %v", entries)
	}
	if s := strings.ReplaceAll(out.String(), repo, "<repo>"); strings.Contains(s, ".claude") || strings.Contains(s, "settings") { // the repo path of a worktree under .claude/ is not a line about ~/.claude
		t.Errorf("install or status printed a line about ~/.claude:\n%s", out.String())
	}
}

func TestSyncIsInstall(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"sync", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("sync: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "files managed") {
		t.Errorf("sync did not install:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir, "state.json")); err != nil {
		t.Errorf("sync wrote no manifest: %v", err)
	}
}

func TestDiffVerbShowsDrift(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"install", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	victim := filepath.Join(home, harness.AgyRoot, "rules", "AGENTS.md")
	if err := os.WriteFile(victim, []byte("drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	_ = run([]string{"diff", "--repo", repo, "--home", home}, &out)
	if !strings.Contains(out.String(), "-drifted") || !strings.Contains(out.String(), "AGENTS.md") {
		t.Errorf("diff printed no hunk for the drifted file:\n%s", out.String())
	}
}

// BUG-080: a relative --home beside --repo resolved against the repo
// directory on a real install (Reinstall runs the build's install with
// cmd.Dir = repo) and against the invoking directory on --dry-run. --home is
// made absolute before anything runs.
func TestRelativeHomeResolvesAgainstTheInvokingDirectory(t *testing.T) {
	repo := scratchClone(t) // the rebuild's child runs with cmd.Dir = repo
	cwd := t.TempDir()
	t.Chdir(cwd)
	want := filepath.Join(cwd, "rel-home")
	var out bytes.Buffer
	if err := run([]string{"install", "--repo", repo, "--home", "rel-home"}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(want, harness.StateDir, "state.json")); err != nil {
		t.Errorf("the install did not land under the invoking directory's rel-home: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "rel-home")); err == nil {
		t.Errorf("a rel-home directory appeared inside the repository (the child install resolved it against cmd.Dir)")
	}
}
