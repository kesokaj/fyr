package harness

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInventoryAndRender(t *testing.T) {
	repo := newRepo(t)
	skills, err := loadSkills(DirSource(repo))
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 || skills[0].Name != "alpha" || skills[1].Name != "beta" {
		t.Fatalf("skills = %+v", skills)
	}
	if !skills[1].SlashOnly || skills[0].SlashOnly {
		t.Errorf("SlashOnly wrong: %+v", skills)
	}
	inv, err := LoadInventory(DirSource(repo))
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Commands) != 1 || inv.Commands[0].Name != "deploy" || inv.Commands[0].Description != "Deploy the thing" {
		t.Errorf("commands = %+v", inv.Commands)
	}
	if len(inv.Rules) != 1 || inv.Rules[0].Description != "Go style" {
		t.Errorf("rules = %+v", inv.Rules)
	}
	ctx, err := RenderContext(DirSource(repo), inv)
	if err != nil {
		t.Fatal(err)
	}
	s := string(ctx)
	if !strings.HasPrefix(s, Marker+"\n# AGENTS.md") {
		t.Errorf("context should start with the marker and AGENTS.md:\n%s", s[:80])
	}
	for _, want := range []string{"## Environment & Stack", "`alpha`", "`/beta`", "`reviewer` (pro)", "`/deploy`", "`go-style`"} {
		if !strings.Contains(s, want) {
			t.Errorf("context missing %q", want)
		}
	}
	if strings.Contains(s, "Alpha does things") {
		t.Errorf("deployed context should carry names only, not descriptions")
	}
	// The canary is the LAST line: a check that reads it proves the whole
	// file loaded (BUG-085). Anything after it would be unproven.
	if !strings.HasSuffix(s, "\n"+Canary+"\n") {
		t.Errorf("context should end with the canary line %q, ends with %q", Canary, s[max(0, len(s)-80):])
	}
	full := RenderInventory(inv)
	for _, want := range []string{"| `alpha` | auto |", "| `beta` | `/beta` |", "| `reviewer` | pro |", "| `/deploy` |", "| `go-style` |", "Beta: slash only"} {
		if !strings.Contains(full, want) {
			t.Errorf("full inventory missing %q", want)
		}
	}
}

func TestInstallStatusResyncUninstall(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	self := []byte("#!/bin/sh\necho fyr\n")

	// A file the operator keeps under ~/.claude is not the installer's: it
	// survives install and uninstall, and no file is written beside it.
	claudeMD := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(claudeMD), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeMD, []byte("my own notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := Plan(DirSource(repo), self)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &out); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		".gemini/config/hooks.json",
		".gemini/config/rules/AGENTS.md",
		".gemini/config/skills/alpha/SKILL.md",
		".gemini/config/skills/alpha/references/notes.md",
		".gemini/config/skills/beta/SKILL.md",
		".gemini/config/agents/reviewer/agent.md",
		".gemini/config/workflows/deploy.md",
		".gemini/config/rules/go-style.md",
		".local/bin/fyr",
		StateDir + "/state.json",
	} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("expected %s after install: %v", rel, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".claude")); len(entries) != 1 {
		t.Errorf("install wrote under ~/.claude: %v", entries)
	}
	if got, _ := os.ReadFile(claudeMD); string(got) != "my own notes\n" {
		t.Errorf("the operator's CLAUDE.md was touched: %q", got)
	}
	if info, _ := os.Stat(filepath.Join(home, BinPath)); info.Mode().Perm()&0o100 == 0 {
		t.Errorf("binary should be executable, mode %v", info.Mode())
	}

	report, err := Status(home, files)
	if err != nil {
		t.Fatal(err)
	}
	if report.Drift() || report.Counts["foreign"] != 0 {
		t.Errorf("fresh install should be clean: %+v", report.Counts)
	}
	// status and diff plan without the binary; the installed one is not stale.
	noSelf, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if report, _ := Status(home, noSelf); report.Drift() {
		t.Errorf("installed binary must not count as stale: %+v", report.Counts)
	}

	// Edit a deployed file, delete another: both show up as drift.
	if err := os.WriteFile(filepath.Join(home, ".gemini/config/skills/alpha/SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, ".gemini/config/agents/reviewer/agent.md")); err != nil {
		t.Fatal(err)
	}
	report, _ = Status(home, files)
	if report.Counts["differs"] != 1 || report.Counts["missing"] != 1 {
		t.Errorf("drift not detected: %+v", report.Counts)
	}

	// Remove a skill from the repo and reinstall: the deployed copy goes away.
	if err := os.RemoveAll(filepath.Join(repo, "skills", "beta")); err != nil {
		t.Fatal(err)
	}
	files, err = Plan(DirSource(repo), self)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Install(DirSource(repo), home, files, false, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini/config/skills/beta")); err == nil {
		t.Error("stale .gemini/config/skills/beta should have been removed")
	}
	if !strings.Contains(out.String(), "1 removed") {
		t.Errorf("resync should report removals:\n%s", out.String())
	}
	report, _ = Status(home, files)
	if report.Drift() {
		t.Errorf("after resync there should be no drift: %+v", report.Counts)
	}

	// Dry-run changes nothing.
	if err := Uninstall(home, true, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini/config/rules/AGENTS.md")); err != nil {
		t.Errorf("dry-run uninstall removed a file")
	}

	if err := Uninstall(home, false, &out); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".gemini/config/skills", ".gemini/config/rules", ".gemini/config/agents", ".local/bin/fyr", StateDir} {
		if _, err := os.Lstat(filepath.Join(home, rel)); err == nil {
			t.Errorf("%s should be gone after uninstall", rel)
		}
	}
	for _, rel := range []string{".gemini/config", ".claude/CLAUDE.md"} {
		if _, err := os.Lstat(filepath.Join(home, rel)); err != nil {
			t.Errorf("%s must survive uninstall: %v", rel, err)
		}
	}
}

func TestInstallMarksDirtyTree(t *testing.T) {
	repo := newRepo(t)
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "fixture")
	short := gitIn(t, repo, "rev-parse", "--short", "HEAD")
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &out); err != nil {
		t.Fatal(err)
	}
	st, _, err := LoadState(home)
	if err != nil {
		t.Fatal(err)
	}
	if st.Commit != short {
		t.Errorf("clean tree: commit = %q, want %q", st.Commit, short)
	}
	if err := os.WriteFile(filepath.Join(repo, "context", "AGENTS.md"), []byte("# AGENTS.md\n\nBe honest and brief.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err = Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &out); err != nil {
		t.Fatal(err)
	}
	st, _, err = LoadState(home)
	if err != nil {
		t.Fatal(err)
	}
	if st.Commit != short+"+dirty" {
		t.Errorf("dirty tree: commit = %q, want %q", st.Commit, short+"+dirty")
	}
}

// TestReinstallBuildsAndInstalls runs the real rebuild path against this
// checkout: go build into the repo's scratch space, then that build installs
// into a scratch home and copies itself there, stamped with the revision.
func TestReinstallBuildsAndInstalls(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	var out bytes.Buffer
	if err := Reinstall(repo, []string{"--home", home}, &out); err != nil {
		t.Fatalf("Reinstall: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "files managed") {
		t.Errorf("child install output missing:\n%s", out.String())
	}
	bin := filepath.Join(home, BinPath)
	ver, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Fatalf("installed binary does not run: %v", err)
	}
	if v := strings.TrimSpace(string(ver)); strings.Contains(v, "(devel)") || strings.Contains(v, "unknown") {
		t.Errorf("installed binary is unstamped: %q", v)
	}
	// Reinstall names its build directory after this process's pid (repo.go), so
	// only that path is ours to check: a glob over fyr-build-* also sees a
	// concurrent `go test` process's in-flight build and flakes (v1.12.0 gate).
	own := filepath.Join(repo, ".agents", "scratch", fmt.Sprintf("fyr-build-%d", os.Getpid()))
	if _, err := os.Stat(own); err == nil {
		t.Errorf("build directory left behind: %s", own)
	}
}

// TestReinstallNeverNeedsToDownloadAToolchain exercises Reinstall in the
// environment that broke it: a module whose go.mod asks for a toolchain that
// is not present, with no proxy to fetch it from. Under the default
// GOTOOLCHAIN=auto that build exits 1 with "toolchain not available"; the
// same build must succeed through Reinstall, which pins GOTOOLCHAIN=local.
// Asserted by running it, not by grepping the source (round-2 release review).
func TestReinstallNeverNeedsToDownloadAToolchain(t *testing.T) {
	for _, bin := range []string{"go", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s is not on PATH", bin)
		}
	}
	repo := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A toolchain that will never exist locally, so auto must try to fetch it.
	write("go.mod", "module fixture\n\ngo 1.24.4\n\ntoolchain go1.99.0\n")
	write("cmd/fyr/main.go", "package main\n\nfunc main() {}\n")
	// A real repository: IsCheckout needs .git, and `go build` stamps VCS
	// information, which fails on a .git that is not one.
	for _, args := range [][]string{
		{"init", "--quiet", "-b", "main"},
		{"config", "user.email", "fixture@example.invalid"},
		{"config", "user.name", "fixture"},
		{"add", "-A"},
		{"commit", "--quiet", "-m", "fixture"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "")

	// Must-fail: the same build under the default auto.
	t.Setenv("GOTOOLCHAIN", "auto")
	probe := exec.Command("go", "build", "-o", filepath.Join(t.TempDir(), "probe"), "./cmd/fyr")
	probe.Dir = repo
	if out, err := probe.CombinedOutput(); err == nil {
		t.Skipf("this environment resolved go1.99.0, so the failure cannot be staged: %s", out)
	}

	// Must-pass: through Reinstall, which forces the local toolchain. The
	// child install is the fixture's own no-op main, so success means the
	// BUILD succeeded, which is what this test is about.
	var out bytes.Buffer
	if err := Reinstall(repo, []string{"--home", t.TempDir()}, &out); err != nil {
		t.Fatalf("Reinstall must not need to download a toolchain: %v\n%s", err, out.String())
	}
}
