package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// oldRelease builds the v2.7.0 binary from `git archive v2.7.0`, the last
// release under the old name, so the crossing below starts from what a real
// place holds rather than from a hand-made manifest (plan fyr D14).
func oldRelease(t *testing.T) (bin, src string) {
	t.Helper()
	if testing.Short() {
		t.Skip("-short: builds v2.7.0 from git archive")
	}
	for _, tool := range []string{"go", "git", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH: the v2.7.0 fixture cannot be built", tool)
		}
	}
	if err := exec.Command("git", "-C", checkoutRoot, "cat-file", "-e", "v2.7.0^{commit}").Run(); err != nil {
		t.Skip("no v2.7.0 tag in this checkout (a plain copy or a shallow clone): the fixture cannot be built")
	}
	dir := t.TempDir()
	src = filepath.Join(dir, "v2.7.0")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := exec.Command("git", "-C", checkoutRoot, "archive", "v2.7.0")
	untar := exec.Command("tar", "-x", "-C", src)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	untar.Stdin = pipe
	if err := untar.Start(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Run(); err != nil {
		t.Fatalf("git archive v2.7.0: %v", err)
	}
	if err := untar.Wait(); err != nil {
		t.Fatalf("tar: %v", err)
	}
	bin = filepath.Join(dir, "sh-agent")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "./cmd/sh-agent")
	build.Dir = src
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building v2.7.0: %v\n%s", err, out)
	}
	return bin, src
}

// runOld runs the v2.7.0 binary with nothing of this process's harness
// environment: an inherited FYR_* or SH_AGENT_* would steer it.
func runOld(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "SH_AGENT_") && !strings.HasPrefix(kv, "FYR_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("v2.7.0 %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestCrossingFromV270 is E2E criterion 2's local half: a home installed by
// v2.7.0 — state dir, binary, manifest, a dream unit — is moved to fyr by
// one `fyr install`. The migration runs before the resync, which reads the
// old manifest from the moved directory, removes ~/.local/bin/sh-agent by
// today's rule and keeps every ~/.claude file byte for byte, a symlinked
// entry included (F2, D32: fyr never removes Claude Code's files); the dream
// unit is replaced under its new name (plan fyr D13, D14).
func TestCrossingFromV270(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	t.Setenv("FYR_REPO", "")
	bin, src := oldRelease(t)
	fakeBin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	if err := os.WriteFile(filepath.Join(fakeBin, "systemctl"), []byte("#!/bin/sh\necho \"$*\" >> \""+calls+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	home := t.TempDir()
	runOld(t, bin, "install", "--repo", src, "--home", home)
	runOld(t, bin, "dream", "--install-timer", "--home", home)
	oldDir := filepath.Join(home, harness.OldStateDir)
	if err := os.WriteFile(filepath.Join(oldDir, "feedback.jsonl"), []byte(`{"kept":true}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var old struct{ Files []string }
	data, err := os.ReadFile(filepath.Join(oldDir, "state.json"))
	if err != nil {
		t.Fatalf("v2.7.0 wrote no manifest: %v", err)
	}
	if err := json.Unmarshal(data, &old); err != nil {
		t.Fatal(err)
	}
	var claudeFiles []string
	listed := map[string]bool{}
	for _, rel := range old.Files {
		listed[rel] = true
		if strings.HasPrefix(rel, ".claude/") {
			claudeFiles = append(claudeFiles, rel)
		}
	}
	if !listed[".local/bin/sh-agent"] || len(claudeFiles) == 0 {
		t.Fatalf("fixture: the v2.7.0 manifest lists sh-agent %v and %d ~/.claude files; want both", listed[".local/bin/sh-agent"], len(claudeFiles))
	}
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	units, _ := filepath.Glob(filepath.Join(unitDir, "sh-agent-dream-*"))
	if len(units) != 2 {
		t.Fatalf("fixture: v2.7.0 --install-timer left %v, want its .service and .timer", units)
	}
	// F2: an entry the operator has since made a symlink is theirs and stays.
	linked := filepath.Join(home, claudeFiles[0])
	if err := os.Remove(linked); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "mine"), linked); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(calls); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, rel := range claudeFiles[1:] {
		b, err := os.ReadFile(filepath.Join(home, rel))
		if err != nil {
			t.Fatal(err)
		}
		before[rel] = string(b)
	}

	var out bytes.Buffer
	if err := run([]string{"install", "--repo", checkoutRoot, "--home", home}, &out); err != nil {
		t.Fatalf("fyr install: %v\n%s", err, out.String())
	}
	got := out.String()
	if !strings.HasPrefix(got, "migrated ~/.config/sh-agent → ~/.config/fyr\n") {
		t.Errorf("the migration is not the first thing install says:\n%.400s", got)
	}
	if _, err := os.Lstat(oldDir); !os.IsNotExist(err) {
		t.Errorf("~/.config/sh-agent still there: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(home, harness.StateDir, "feedback.jsonl")); err != nil || !strings.Contains(string(b), "kept") {
		t.Errorf("the feedback log did not move: %q, %v", b, err)
	}
	st, found, err := harness.LoadState(home)
	if err != nil || !found {
		t.Fatalf("no fyr manifest: found %v, err %v", found, err)
	}
	for _, rel := range st.Files {
		if strings.HasPrefix(rel, ".claude/") || rel == ".local/bin/sh-agent" {
			t.Errorf("the new manifest lists %s", rel)
		}
	}
	for _, rel := range claudeFiles[1:] {
		if b, err := os.ReadFile(filepath.Join(home, rel)); err != nil || string(b) != before[rel] {
			t.Errorf("~/%s removed or changed by the crossing: %v", rel, err)
		}
	}
	if !strings.Contains(got, fmt.Sprintf("keep    ~/.claude: %d files", len(claudeFiles))) {
		t.Errorf("install does not name the %d kept ~/.claude files:\n%.2000s", len(claudeFiles), got)
	}
	if fi, err := os.Lstat(linked); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlinked entry was removed or replaced: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".local", "bin", "sh-agent")); !os.IsNotExist(err) {
		t.Errorf("~/.local/bin/sh-agent left: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, harness.BinPath)); err != nil {
		t.Errorf("~/.local/bin/fyr not installed: %v", err)
	}
	// The operator's own file under ~/.claude, which v2.7.0 created but never
	// listed, and the directory itself stay (D2, pruneEmptyDirs' stop map).
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err != nil {
		t.Errorf("~/.claude/settings.json touched: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(unitDir, "sh-agent-dream-*")); len(left) != 0 {
		t.Errorf("old dream units left: %v", left)
	}
	if fresh, _ := filepath.Glob(filepath.Join(unitDir, "fyr-dream-*")); len(fresh) != 2 {
		t.Errorf("fyr-dream units: %v, want the .service and .timer", fresh)
	}
	if b, _ := os.ReadFile(calls); !strings.Contains(string(b), "enable --now fyr-dream-") || !strings.Contains(string(b), "disable --now sh-agent-dream-") {
		t.Errorf("systemctl calls:\n%s", b)
	}
	if !strings.Contains(got, "dream timer sh-agent-dream-") {
		t.Errorf("install did not say it replaced the dream timer:\n%s", got)
	}

	out.Reset()
	if err := run([]string{"status", "--repo", checkoutRoot, "--home", home}, &out); err != nil {
		t.Fatalf("status after the crossing: %v\n%s", err, out.String())
	}
	if s := out.String(); !strings.Contains(s, "drift      none") || !strings.Contains(s, "timer installed") || strings.Contains(s, "sh-agent") {
		t.Errorf("status after the crossing:\n%s", s)
	}
}
