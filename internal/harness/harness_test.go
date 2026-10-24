package harness

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newRepo builds a minimal harness checkout: two skills, one agent, two
// context files. Tests never touch the real $HOME.
func newRepo(t *testing.T) string {
	t.Helper()
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
	write("context/AGENTS.md", "# AGENTS.md\n\nBe honest.\n")
	write("context/dev-setup.md", "## Environment & Stack\n\n- GCP only.\n")
	write("skills/alpha/SKILL.md", "---\nname: alpha\ndescription: |\n  Alpha does things.\n  Use it for alpha work.\nmetadata:\n  version: \"1\"\n---\nbody\n")
	write("skills/alpha/references/notes.md", "notes\n")
	write("skills/beta/SKILL.md", "---\nname: beta\ndescription: \"Beta: slash only\"\ndisable-model-invocation: true\n---\nbody\n")
	write("skills/VENDORED.md", "not a skill\n")
	write("commands/deploy.md", "---\ndescription: Deploy the thing\n---\nSteps\n")
	write("rules/go-style.md", "# Go style\n\nUse slog.\n")
	write("agents/reviewer/agent.md", reviewerAgent)
	return repo
}

func TestParseFrontmatter(t *testing.T) {
	front := "name: x\ndescription: |\n  line one\n  line two\nquoted: \"a: b\"\nmetadata:\n  version: \"1.2\"\n  author: me\ntools: [Read, Grep]\nskills:\n  - alpha\n  - beta\nplain: first\n  second\nempty:\n"
	fm := Parse(front)
	want := map[string]string{
		"name":        "x",
		"description": "line one line two",
		"quoted":      "a: b",
		"tools":       "[Read, Grep]",
		"skills":      "[alpha, beta]",
		"plain":       "first second",
		"empty":       "",
	}
	for k, v := range want {
		if fm[k] != v {
			t.Errorf("%s = %q, want %q", k, fm[k], v)
		}
	}
	if _, ok := fm["metadata"]; ok {
		t.Errorf("nested map should be skipped, got %q", fm["metadata"])
	}
	if got := SplitList("[Read, Grep]"); strings.Join(got, ",") != "Read,Grep" {
		t.Errorf("SplitList brackets = %v", got)
	}
	if got := SplitList("Read Grep"); strings.Join(got, ",") != "Read,Grep" {
		t.Errorf("SplitList spaces = %v", got)
	}
}

func TestLintRejectsUnquotedComment(t *testing.T) {
	// A plain scalar with " #" is a YAML comment start: strict parsers
	// truncate it or, inside a flow sequence, fail the whole file. The
	// Antigravity CLI dropped the `bug` and `feature` skills that way.
	bad := []string{
		"name: x\nargument-hint: [feature description or #issue]\n",
		"name: x\ndescription: fixes #1 and #2\n",
		"name: x\ndescription: #comment only\n",
		"name: x\ndescription: first line\n  wraps with #issue\n",
		"name: x\ndescription: value\t#tab-comment\n",
		"name: x\ndescription: \"quoted\" # trailing\n",
	}
	for _, front := range bad {
		if _, err := ParseStrict(front); err == nil {
			t.Errorf("Lint accepted %q", front)
		} else if !strings.Contains(err.Error(), "#") {
			t.Errorf("error should name the marker: %v", err)
		}
	}
	good := []string{
		"name: x\nargument-hint: \"[feature description or #issue]\"\n",
		"name: x\nargument-hint: '[#issue]'\n",
		"name: x\ndescription: |\n  a #b\n  c #d\nmetadata:\n  tag: \"#1\"\n",
		"name: x\ntools: [Read, Grep]\ncolor: \"#ff0000\"\n",
	}
	for _, front := range good {
		if _, err := ParseStrict(front); err != nil {
			t.Errorf("Lint rejected %q: %v", front, err)
		}
	}
}

func TestLoadSkillsRejectsUnquotedComment(t *testing.T) {
	repo := newRepo(t)
	p := filepath.Join(repo, "skills", "gamma", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\nname: gamma\ndescription: Gamma\nargument-hint: [topic or #issue]\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSkills(DirSource(repo))
	if err == nil || !strings.Contains(err.Error(), "gamma") || !strings.Contains(err.Error(), "argument-hint") {
		t.Fatalf("LoadSkills should name the skill and the key, got %v", err)
	}
}

func TestSplitRejectsMissingFrontmatter(t *testing.T) {
	if _, _, err := Split("# no frontmatter\n"); err == nil {
		t.Fatal("expected an error")
	}
	if _, _, err := Split("---\nname: x\n"); err == nil {
		t.Fatal("expected an error for an unterminated block")
	}
}

func TestInventoryAndRender(t *testing.T) {
	repo := newRepo(t)
	skills, err := LoadSkills(DirSource(repo))
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
	self := []byte("#!/bin/sh\necho sh-agent\n")

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
		".gemini/config/rules/AGENTS.md",
		".gemini/config/skills/alpha/SKILL.md",
		".gemini/config/skills/alpha/references/notes.md",
		".gemini/config/skills/beta/SKILL.md",
		".gemini/config/agents/reviewer/agent.md",
		".gemini/config/workflows/deploy.md",
		".gemini/config/rules/go-style.md",
		".local/bin/sh-agent",
		".config/sh-agent/state.json",
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
	for _, rel := range []string{".gemini/config/skills", ".gemini/config/rules", ".gemini/config/agents", ".local/bin/sh-agent", ".config/sh-agent"} {
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

// TestPlanAntigravityOnly: every planned path is under the Antigravity CLI's
// directory or is the binary, and the manifest records the one vendor that a
// reader of state.json (the init-project skill) checks for.
func TestPlanAntigravityOnly(t *testing.T) {
	repo := newRepo(t)
	files, err := Plan(DirSource(repo), []byte("#!/bin/sh\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !strings.HasPrefix(f.Rel, AgyRoot+string(filepath.Separator)) && f.Rel != BinPath {
			t.Errorf("planned %s, outside ~/%s", f.Rel, AgyRoot)
		}
	}
	home := t.TempDir()
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Errorf("an install into an empty home made ~/.claude: %v", err)
	}
	st, found, err := LoadState(home)
	if err != nil || !found || st.Vendor != VendorAntigravity {
		t.Errorf("state = %+v, %v, %v; want vendor %q", st, found, err, VendorAntigravity)
	}
}

// TestResyncShedsOldClaudeFiles: a manifest from an install before ADR-062
// lists files under ~/.claude. The next install's resync removes each one
// and names it, by the one rule it applies to any file the plan no longer
// holds: a symlink, or a file under a linked directory, is the operator's and
// stays; ~/.claude itself and a file the manifest never listed stay; the
// vendor the old manifest recorded is ignored (plan fyr Phase 2, D14, D17).
func TestResyncShedsOldClaudeFiles(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	put := func(rel, body string) {
		t.Helper()
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := []string{
		".claude/AGENTS.md", ".claude/CLAUDE.md", ".claude/skills/alpha/SKILL.md",
		".claude/agents/reviewer.md", ".claude/commands/deploy.md", ".claude/rules/go-style.md",
		".claude/rules/linked.md",
	}
	for _, rel := range old[:6] {
		put(rel, Marker+"\nold\n")
	}
	put(".claude/settings.json", "{}\n") // the operator's, never in a manifest
	put("dotfiles/linked.md", "mine\n")
	if err := os.Symlink(filepath.Join(home, "dotfiles", "linked.md"), filepath.Join(home, ".claude", "rules", "linked.md")); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(home, State{Repo: repo, Vendor: "all", Files: old}); err != nil {
		t.Fatal(err)
	}

	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &out); err != nil {
		t.Fatal(err)
	}
	for _, rel := range old[:6] {
		if _, err := os.Lstat(filepath.Join(home, rel)); !os.IsNotExist(err) {
			t.Errorf("~/%s was kept: %v", rel, err)
		}
		if !strings.Contains(out.String(), "remove  ~/"+rel+" (no longer in the source)") {
			t.Errorf("the removal of ~/%s is not reported:\n%s", rel, out.String())
		}
	}
	for _, rel := range []string{".claude", ".claude/settings.json", ".claude/rules/linked.md", "dotfiles/linked.md"} {
		if _, err := os.Lstat(filepath.Join(home, rel)); err != nil {
			t.Errorf("~/%s must stay: %v", rel, err)
		}
	}
	if !strings.Contains(out.String(), "6 removed") {
		t.Errorf("install should count six removals:\n%s", out.String())
	}
	st, _, err := LoadState(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range st.Files {
		if strings.HasPrefix(rel, ".claude") {
			t.Errorf("the new manifest still lists ~/%s", rel)
		}
	}
	if st.Vendor != VendorAntigravity {
		t.Errorf("state vendor = %q, want %q", st.Vendor, VendorAntigravity)
	}
}

func TestAdopt(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	src := filepath.Join(home, ".agents", "skills", "gamma")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: gamma\ndescription: g\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink the way `npx skills add -g` leaves one in a vendor directory.
	link := filepath.Join(home, AgyRoot, "skills", "gamma")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, link); err != nil {
		t.Fatal(err)
	}

	dest, err := Adopt(repo, home, "gamma", false)
	if err != nil {
		t.Fatal(err)
	}
	if dest != filepath.Join(repo, "skills", "gamma") {
		t.Errorf("dest = %s", dest)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Error(err)
	}
	if _, err := Adopt(repo, home, "gamma", false); err == nil {
		t.Error("second adopt without --force should fail")
	}
	if _, err := Adopt(repo, home, "gamma", true); err != nil {
		t.Errorf("adopt --force: %v", err)
	}
	if _, err := Adopt(repo, home, "nope", false); err == nil {
		t.Error("unknown skill should fail")
	}
}

// gitInit makes a directory its own git checkout, which is what IsCheckout
// reads and what separates a tree update can move forward from a plain copy.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

func TestResolveSource(t *testing.T) {
	t.Setenv("SH_AGENT_REPO", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	// The walk starts at the working directory, so a temp directory INSIDE a
	// harness checkout (a TMPDIR under one) would be found and every case
	// below would read that tree instead (security review 2026-09-17).
	if _, err := ResolveSource("", t.TempDir(), ""); err == nil {
		t.Skip("the temp directory happens to be inside a harness repo")
	}
	repo := newRepo(t)
	home := t.TempDir()

	// A development build with no tree found keeps the error: only a release
	// binary may install the files it carries (D14).
	for _, v := range []string{"", "b64ffe886729", "b64ffe886729+dirty", "(devel)", "v1.2.3-rc.1", "v1.2.3-0.20260917090000-b64ffe886729", "1.2.3", "v1.2"} {
		_, err := ResolveSource("", home, v)
		if err == nil || !strings.Contains(err.Error(), "harness repo not found") {
			t.Errorf("version %q with no tree: err = %v, want harness repo not found", v, err)
			continue
		}
		// The message says WHY the embedded fallback was refused — this build
		// has no harness in it — and names the release-binary way out. Saying
		// only "run from inside the checkout" sent the operator looking for a
		// repository they had been told they would not need (2026-09-18).
		if !strings.Contains(err.Error(), "no harness built in") || !strings.Contains(err.Error(), "release binary") {
			t.Errorf("version %q: err = %v, want it to name the missing harness and the release-binary path", v, err)
		}
		if v != "" && v != "unknown" && !strings.Contains(err.Error(), v) {
			t.Errorf("version %q: err = %v, want it to quote the version", v, err)
		}
	}

	src, err := ResolveSource("", home, "v1.2.3")
	if err != nil {
		t.Fatalf("release binary with no tree: %v", err)
	}
	if src.Dir != "" || src.Version != "v1.2.3" || src.FS == nil {
		t.Errorf("release binary with no tree: %+v, want the embedded source", src)
	}
	if _, err := fs.Stat(src.FS, "context/AGENTS.md"); err != nil {
		t.Errorf("the embedded source has no context/AGENTS.md: %v", err)
	}

	// The working-directory walk, for a development build: standing inside a
	// harness tree, or anywhere under it, finds that tree. Nothing pinned this
	// before, so deleting the walk kept the suite green (code review
	// 2026-09-18).
	nested := filepath.Join(repo, "skills", "alpha", "references")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{repo, nested} {
		t.Chdir(dir)
		if src, err := ResolveSource("", t.TempDir(), ""); err != nil || src.Dir != repo {
			t.Errorf("development build in %s: %+v, %v — want the tree walked up to", dir, src, err)
		}
	}
	t.Chdir(cwd)

	// A development build takes the path it is given.
	if src, err := ResolveSource(repo, home, ""); err != nil || src.Dir != repo {
		t.Errorf("explicit repo, development build: %+v, %v", src, err)
	}
	if err := SaveState(home, State{Repo: repo}); err != nil {
		t.Fatal(err)
	}
	// The recorded tree comes before the embedded files for a DEVELOPMENT
	// build, which has nothing of its own to offer.
	if src, err := ResolveSource("", home, ""); err != nil || src.Dir != repo {
		t.Errorf("repo from state, development build: %+v, %v", src, err)
	}
	// A RELEASE binary takes NO tree on this machine: not the recorded one,
	// whether a plain copy or a real checkout, and not the working directory.
	// What it installs is the published repository at the tagged commit, which
	// it carries; `update` replaces the whole binary to change that (ADR-034 —
	// the operator, after a box installed months-old content from a recorded
	// plain copy while reporting no drift: "the binary should only use the
	// published git repo in github always and only").
	if src, err := ResolveSource("", home, "v1.2.3"); err != nil || src.Dir != "" || src.Version != "v1.2.3" {
		t.Errorf("recorded plain copy, release binary: %+v, %v — want the embedded files", src, err)
	}
	checkout := newRepo(t)
	gitInit(t, checkout)
	if err := SaveState(home, State{Repo: checkout}); err != nil {
		t.Fatal(err)
	}
	if src, err := ResolveSource("", home, "v1.2.3"); err != nil || src.Dir != "" {
		t.Errorf("recorded checkout, release binary: %+v, %v — want the embedded files", src, err)
	}
	// Standing inside a tree does not change it either.
	t.Chdir(checkout)
	if src, err := ResolveSource("", home, "v1.2.3"); err != nil || src.Dir != "" {
		t.Errorf("inside a checkout, release binary: %+v, %v — want the embedded files", src, err)
	}
	// A named path is REFUSED, not ignored: doing something other than what
	// was typed is what D15 exists to prevent.
	// The refusal names WHICH input carried the path: an exported
	// $SH_AGENT_REPO makes every command on the machine fail, and "unset it"
	// is unactionable without knowing which one (code review 2026-09-18).
	if _, err := ResolveSource(checkout, home, "v1.2.3"); err == nil ||
		!strings.Contains(err.Error(), "release binary") || !strings.Contains(err.Error(), "--repo="+checkout) {
		t.Errorf("--repo on a release binary: err = %v, want a refusal naming --repo and the path", err)
	}
	t.Setenv("SH_AGENT_REPO", checkout)
	if _, err := ResolveSource("", home, "v1.2.3"); err == nil ||
		!strings.Contains(err.Error(), "release binary") || !strings.Contains(err.Error(), "$SH_AGENT_REPO="+checkout) {
		t.Errorf("$SH_AGENT_REPO on a release binary: err = %v, want a refusal naming the variable", err)
	}
	t.Setenv("SH_AGENT_REPO", "")
	os.Unsetenv("SH_AGENT_REPO")
	t.Chdir(cwd)
	if err := SaveState(home, State{Repo: repo}); err != nil {
		t.Fatal(err)
	}

	// A manifest written by an embedded install records no path: its repo
	// value is never read as one, even when a directory of that name holding
	// a harness sits in the working directory.
	if err := os.CopyFS(filepath.Join(cwd, "embedded:v1.0.0"), os.DirFS(repo)); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(home, State{Repo: "embedded:v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if src, err := ResolveSource("", home, "v1.2.3"); err != nil || src.Dir != "" {
		t.Errorf("recorded embedded install: %+v, %v, want the embedded source", src, err)
	}
	if _, err := ResolveSource("", home, ""); err == nil {
		t.Error("a development build must not read a recorded embedded:<version> as a path")
	}
}

func TestBehindAndPull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	origin := filepath.Join(base, "origin.git")
	run(base, "init", "--bare", "-q", "-b", "main", origin)

	a := filepath.Join(base, "a")
	run(base, "clone", "-q", origin, a)
	if err := os.WriteFile(filepath.Join(a, "f"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(a, "add", "f")
	run(a, "commit", "-q", "-m", "one")
	run(a, "push", "-q", "-u", "origin", "main")

	b := filepath.Join(base, "b")
	run(base, "clone", "-q", origin, b)

	if n, err := Behind(b); err != nil || n != 0 {
		t.Fatalf("fresh clone behind = %d, %v", n, err)
	}

	if err := os.WriteFile(filepath.Join(a, "f"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(a, "commit", "-q", "-am", "two")
	run(a, "push", "-q")

	if n, err := Behind(b); err != nil || n != 1 {
		t.Fatalf("behind = %d, %v; want 1", n, err)
	}

	if err := os.WriteFile(filepath.Join(b, "f"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Pull(b, &bytes.Buffer{}); err == nil {
		t.Fatal("pull over a dirty tree must be refused")
	}
	run(b, "checkout", "--", "f")
	if err := Pull(b, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if n, _ := Behind(b); n != 0 {
		t.Errorf("after pull behind = %d", n)
	}
}

// TestNestedCopyIsNotACheckout: a harness-shaped tree inside another git
// repository must not inherit that repository's branch, commit, or dirty
// state, and git-driven update must refuse it.
func TestNestedCopyIsNotACheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := newRepo(t)
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "fixture")

	nested := filepath.Join(root, "mirror")
	for _, rel := range []string{"context", "skills"} {
		if err := os.MkdirAll(filepath.Join(nested, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(nested, "context", "AGENTS.md"), []byte("# copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsRepo(nested) {
		t.Fatal("fixture: nested copy should look like a harness")
	}

	if got := Inspect(nested); got != (RepoInfo{}) {
		t.Errorf("nested copy Inspect = %+v, want empty", got)
	}
	if _, err := Behind(nested); !errors.Is(err, errPlainCopy) {
		t.Errorf("nested copy Behind err = %v, want %v", err, errPlainCopy)
	}
	if err := Pull(nested, &bytes.Buffer{}); !errors.Is(err, errPlainCopy) {
		t.Errorf("nested copy Pull err = %v, want %v", err, errPlainCopy)
	}

	// An inherited GIT_DIR/GIT_WORK_TREE must not redirect the check to the
	// enclosing repository: the check looks for .git itself and never asks git.
	t.Setenv("GIT_DIR", filepath.Join(root, ".git"))
	t.Setenv("GIT_WORK_TREE", nested)
	if got := Inspect(nested); got != (RepoInfo{}) {
		t.Errorf("nested copy with GIT_DIR/GIT_WORK_TREE set: Inspect = %+v, want empty", got)
	}
	if _, err := Behind(nested); !errors.Is(err, errPlainCopy) {
		t.Errorf("nested copy with GIT_DIR set: Behind err = %v, want %v", err, errPlainCopy)
	}
	t.Setenv("GIT_DIR", "")
	t.Setenv("GIT_WORK_TREE", "")
	os.Unsetenv("GIT_DIR")
	os.Unsetenv("GIT_WORK_TREE")

	got := Inspect(root)
	if got.Branch == "" || got.Commit == "" || !got.Dirty {
		t.Errorf("repo root Inspect = %+v, want branch, commit, and dirty (the untracked mirror)", got)
	}
	if _, err := Behind(root); errors.Is(err, errPlainCopy) {
		t.Errorf("repo root Behind must not be refused as a plain copy: %v", err)
	}
}

func TestLastRetro(t *testing.T) {
	home := t.TempDir()
	if _, ok := LastRetro(home); ok {
		t.Fatal("no proposals yet")
	}
	dir := filepath.Join(home, ".config", "sh-agent", "proposals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-09-03-retro.md"), []byte("# retro\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, ok := LastRetro(home)
	if !ok {
		t.Fatal("proposal should be found")
	}
	// A project-level retro appends to the index without a proposal file;
	// that counts as a retro too, and its newer mtime wins.
	index := filepath.Join(home, ".config", "sh-agent", "retros.jsonl")
	if err := os.WriteFile(index, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := first.Add(48 * time.Hour)
	if err := os.Chtimes(index, later, later); err != nil {
		t.Fatal(err)
	}
	if got, ok := LastRetro(home); !ok || !got.Equal(later) {
		t.Fatalf("LastRetro = %v, %v; want %v", got, ok, later)
	}
	// A home path holding a glob metacharacter: the proposals are listed by
	// name, never through a pattern built from the home (BUG-109's class;
	// the golang skill's rule).
	odd := filepath.Join(t.TempDir(), "u[1]")
	oddDir := filepath.Join(odd, ".config", "sh-agent", "proposals")
	if err := os.MkdirAll(oddDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oddDir, "2026-09-30-retro.md"), []byte("# retro\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := LastRetro(odd); !ok {
		t.Fatal("a proposal under a home with [ in its path should be found")
	}
}

func TestSlashNoteOnlyWhenSomeSkillIsSlashOnly(t *testing.T) {
	repo := newRepo(t)
	inv, err := LoadInventory(DirSource(repo))
	if err != nil {
		t.Fatal(err)
	}
	const marked = "Skills marked `/name` are typed by the user"
	if !strings.Contains(RenderInventoryCompact(inv), marked) || !strings.Contains(RenderInventory(inv), marked) {
		t.Errorf("fixture has a slash-only skill; the note should be printed")
	}
	for i := range inv.Skills {
		inv.Skills[i].SlashOnly = false
	}
	for _, out := range []string{RenderInventoryCompact(inv), RenderInventory(inv)} {
		if strings.Contains(out, marked) {
			t.Errorf("no slash-only skill; the note must not be printed:\n%s", out)
		}
		if !strings.Contains(out, "can also be typed as `/name`") {
			t.Errorf("expected the all-model-invocable sentence:\n%s", out)
		}
	}
}

// gitIn runs git in dir and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	outb, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, outb)
	}
	return strings.TrimSpace(string(outb))
}

// An explicit --repo or $SH_AGENT_REPO is used or refused, never replaced by
// the embedded files, even in a release binary (D15).
func TestResolveSourceRefusesNamedNonRepo(t *testing.T) {
	home := t.TempDir()
	bogus := filepath.Join(t.TempDir(), "not-a-checkout")
	for _, v := range []string{"", "v1.2.3"} {
		t.Setenv("SH_AGENT_REPO", "")
		if _, err := ResolveSource(bogus, home, v); err == nil {
			t.Errorf("version %q: an explicit path that is not a checkout must be an error, not a fallback", v)
		}
		t.Setenv("SH_AGENT_REPO", bogus)
		if _, err := ResolveSource("", home, v); err == nil {
			t.Errorf("version %q: $SH_AGENT_REPO pointing at a non-checkout must be an error, not a fallback", v)
		}
	}
	t.Setenv("SH_AGENT_REPO", "")
	repo := newRepo(t)
	if got, err := ResolveSource(repo, home, ""); err != nil || got.Dir != repo {
		t.Errorf("explicit checkout: %+v, %v", got, err)
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
	// only that path is ours to check: a glob over sh-agent-build-* also sees a
	// concurrent `go test` process's in-flight build and flakes (v1.12.0 gate).
	own := filepath.Join(repo, ".agents", "scratch", fmt.Sprintf("sh-agent-build-%d", os.Getpid()))
	if _, err := os.Stat(own); err == nil {
		t.Errorf("build directory left behind: %s", own)
	}
}

// vendoredSkills reads skills/VENDORED.md and returns the skill directories
// listed there. Vendored text is never edited in place (AGENTS.md), so a
// prose check that walks it would report a defect nobody may fix; the fix
// for those is a re-fetch or a fork under another name.
func vendoredSkills(t *testing.T, repo string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, "skills", "VENDORED.md"))
	if err != nil {
		t.Fatalf("reading VENDORED.md: %v", err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cell := strings.TrimSpace(strings.Trim(strings.SplitN(strings.TrimSpace(line), "|", 3)[1], " `"))
		if cell == "" || strings.Contains(cell, " ") || strings.HasPrefix(cell, "-") {
			continue
		}
		if info, err := os.Stat(filepath.Join(repo, "skills", cell)); err == nil && info.IsDir() {
			out[cell] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("VENDORED.md named no skill directory; the parser or the file changed shape")
	}
	return out
}

// isVendoredPath reports whether rel, a slash-separated path relative to
// skills/, is a vendored key of VENDORED.md or lies under one: a skill
// vendored whole (`find-skills`) or a vendored subtree inside a harness skill
// (`google-catalog/catalog`), whose prose is upstream's and is not checked.
func isVendoredPath(vendored map[string]bool, rel string) bool {
	for key := range vendored {
		if rel == key || strings.HasPrefix(rel, key+"/") {
			return true
		}
	}
	return false
}

// TestNoTmpInHarnessProse guards the scratch rule at the source: the
// context, the agents, the skills — their SKILL.md AND their references,
// where the rule drifted unseen (BUG-019) — the commands, the rules, and the
// repo docs may mention /tmp only to forbid it. Four records (BUG-003,
// BUG-007, BUG-014, BUG-019) are the reason this is a test and not a
// reminder. Vendored skills are exempt: their text may not be edited here.
func TestNoTmpInHarnessProse(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{"context", "agents", "commands", "rules", "README.md", "AGENTS.md"}
	vendored := vendoredSkills(t, repo)
	dirs, _ := filepath.Glob(filepath.Join(repo, "skills", "*"))
	for _, d := range dirs {
		info, err := os.Stat(d)
		if err != nil || !info.IsDir() || vendored[filepath.Base(d)] {
			continue
		}
		rel, _ := filepath.Rel(repo, d)
		roots = append(roots, rel)
	}
	allowed := func(line string) bool {
		l := strings.ToLower(line)
		return strings.Contains(l, "never /tmp") || strings.Contains(l, "never `/tmp`") ||
			strings.Contains(l, "not /tmp") || strings.Contains(l, "not `/tmp`") || strings.Contains(l, "instead of `/tmp`")
	}
	for _, root := range roots {
		err := filepath.WalkDir(filepath.Join(repo, root), func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				// A vendored subtree inside a harness skill is upstream's text.
				if rel, rerr := filepath.Rel(filepath.Join(repo, "skills"), path); rerr == nil && isVendoredPath(vendored, filepath.ToSlash(rel)) {
					return filepath.SkipDir
				}
			}
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, "/tmp") && !allowed(line) {
					rel, _ := filepath.Rel(repo, path)
					t.Errorf("%s:%d mentions /tmp outside a 'never /tmp' clause: %s", rel, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// A source that resolves to the repo, to skills/, or to the destination must
// never become the deletion target: ".", ".." and a relative "skills/<name>"
// reached Base unchanged and named the deletion root itself (BUG-037).
func TestAdoptRefusesDestructiveSources(t *testing.T) {
	home := t.TempDir()
	skillDir := func(t *testing.T) string {
		t.Helper()
		d := filepath.Join(t.TempDir(), "delta")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\nname: delta\ndescription: d\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return d
	}

	t.Run("dot adopts the directory itself, never skills/", func(t *testing.T) {
		repo := newRepo(t)
		t.Chdir(skillDir(t))
		dest, err := Adopt(repo, home, ".", true)
		if err != nil {
			t.Fatalf("adopt . --force: %v", err)
		}
		if dest != filepath.Join(repo, "skills", "delta") {
			t.Errorf("dest = %s", dest)
		}
		if _, err := os.Stat(filepath.Join(repo, "skills", "alpha", "SKILL.md")); err != nil {
			t.Errorf("skills/ was removed: %v", err)
		}
	})

	t.Run("dotdot adopts the parent, never the repo", func(t *testing.T) {
		repo := newRepo(t)
		sub := filepath.Join(skillDir(t), "sub")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(sub)
		dest, err := Adopt(repo, home, "..", true)
		if err != nil {
			t.Fatalf("adopt .. --force: %v", err)
		}
		if dest != filepath.Join(repo, "skills", "delta") {
			t.Errorf("dest = %s", dest)
		}
		if _, err := os.Stat(filepath.Join(repo, "context", "AGENTS.md")); err != nil {
			t.Errorf("the repo was removed: %v", err)
		}
	})

	t.Run("a skill adopted onto itself", func(t *testing.T) {
		repo := newRepo(t)
		src := filepath.Join(repo, "skills", "alpha")
		if _, err := Adopt(repo, home, src, true); err == nil {
			t.Error("adopting a skill onto itself should fail")
		}
		if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
			t.Errorf("the source was removed: %v", err)
		}
	})

	t.Run("a relative path into the repo's own skills/", func(t *testing.T) {
		repo := newRepo(t)
		t.Chdir(repo)
		if _, err := Adopt(repo, home, filepath.Join("skills", "alpha"), true); err == nil {
			t.Error("adopting skills/alpha from the repo root should fail")
		}
		if _, err := os.Stat(filepath.Join(repo, "skills", "alpha", "SKILL.md")); err != nil {
			t.Errorf("the source was removed: %v", err)
		}
	})
}

func TestWithin(t *testing.T) {
	for _, c := range []struct {
		parent, child string
		want          bool
	}{
		{"/a/skills", "/a/skills", true},
		{"/a/skills", "/a/skills/", true},
		{"/a/skills", "/a/skills/delta", true},
		{"/a/skills/delta", "/a/skills", false},
		{"/a/skills", "/a/skills-old", false},
		{"/a/skills", "/a/skillsdelta", false},
		{"/a/skills", "/b/skills/delta", false},
		{"/a", "/a/skills/delta", true},
	} {
		if got := within(c.parent, c.child); got != c.want {
			t.Errorf("within(%q, %q) = %v, want %v", c.parent, c.child, got, c.want)
		}
	}
}

// TestWriteAtomicIsAtomicAndDurable covers the write every deployed file
// goes through: the bytes land whole under the asked-for mode, and no temp
// file is left beside them on either outcome. The Sync added on 2026-09-16
// is not directly observable from a test — a crash is what would show it —
// so what is asserted here is the contract it protects: after the call
// returns, the destination holds exactly the bytes and nothing else does.
func TestWriteAtomicIsAtomicAndDurable(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "nested", "context.md")
	data := []byte("# AGENTS.md\n\nwhole file\n")
	if err := WriteAtomic(dest, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("content = %q, want %q", got, data)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(dest))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".sh-agent-") {
			t.Errorf("temp file %s left beside the destination", e.Name())
		}
	}
	// Overwriting an existing file keeps the same guarantee.
	if err := WriteAtomic(dest, []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "second\n" {
		t.Errorf("after overwrite = %q", got)
	}
}

// TestRenderContextRefusesSymlinks is BUG-036's class, tested directly on
// RenderContext rather than only through Plan: a symlinked context directory
// or context file would let a tree outside the repo decide what every
// machine reads.
func TestRenderContextRefusesSymlinks(t *testing.T) {
	t.Run("context directory is a link", func(t *testing.T) {
		repo := newRepo(t)
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "AGENTS.md"), []byte("# hostile\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(repo, "context")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(repo, "context")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		inv, _ := LoadInventory(DirSource(repo))
		if _, err := RenderContext(DirSource(repo), inv); err == nil {
			t.Fatal("a symlinked context directory must be refused")
		} else if !strings.Contains(err.Error(), "symlink") {
			t.Errorf("the error should name the symlink, got %v", err)
		}
	})

	t.Run("a file inside context is a link", func(t *testing.T) {
		repo := newRepo(t)
		outside := filepath.Join(t.TempDir(), "extra.md")
		if err := os.WriteFile(outside, []byte("# hostile\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(repo, "context", "extra.md")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		inv, _ := LoadInventory(DirSource(repo))
		if _, err := RenderContext(DirSource(repo), inv); err == nil {
			t.Fatal("a symlinked context file must be refused")
		} else if !strings.Contains(err.Error(), "symlink") {
			t.Errorf("the error should name the symlink, got %v", err)
		}
	})
}

// TestAdoptFailedCopyKeepsTheExistingSkill: adopt --force used to delete the
// destination and then copy, so a copy that failed part-way left the operator
// with neither the old skill nor the new one. The copy is staged and renamed
// into place, so a refusal mid-walk changes nothing.
func TestAdoptFailedCopyKeepsTheExistingSkill(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()

	dest := filepath.Join(repo, "skills", "widget")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	original := "---\nname: widget\ndescription: the version already here\n---\nold body\n"
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// A source whose copy fails part-way: copyTree refuses symlinks.
	src := filepath.Join(t.TempDir(), "widget")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: widget\ndescription: replacement\n---\nnew body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "linked.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Adopt(repo, home, src, true); err == nil {
		t.Fatal("a source holding a symlink must be refused")
	}
	got, err := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if err != nil {
		t.Fatalf("the existing skill must survive a failed adopt: %v", err)
	}
	if string(got) != original {
		t.Errorf("the existing skill was modified: %q", got)
	}
	assertSkillsClean(t, repo, "after a failed adopt")

	// A rename that cannot complete must leave the ORIGINAL in place, never
	// delete both copies: dest is moved aside and put back (release review of
	// v1.0.0, finding 3). Forced here by making the destination's parent
	// read-only so the rename into it fails.
	if os.Geteuid() != 0 {
		if err := os.Remove(filepath.Join(src, "linked.md")); err != nil {
			t.Fatal(err)
		}
		skillsDir := filepath.Join(repo, "skills")
		if err := os.Chmod(skillsDir, 0o500); err != nil {
			t.Fatal(err)
		}
		_, err := Adopt(repo, home, src, true)
		if chmodErr := os.Chmod(skillsDir, 0o755); chmodErr != nil {
			t.Fatal(chmodErr)
		}
		if err == nil {
			t.Error("a rename into an unwritable skills/ should fail")
		}
		got, readErr := os.ReadFile(filepath.Join(dest, "SKILL.md"))
		if readErr != nil {
			t.Fatalf("the original skill must survive a failed rename: %v", readErr)
		}
		if string(got) != original {
			t.Errorf("the original skill changed: %q", got)
		}
		if err != nil && !strings.Contains(err.Error(), ".agents/scratch") {
			t.Errorf("the error should name where the prepared copy is, got %v", err)
		}
	}

	// The happy path still replaces the skill and leaves no staging behind.
	os.Remove(filepath.Join(src, "linked.md"))
	if _, err := Adopt(repo, home, src, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "SKILL.md")); !strings.Contains(string(got), "new body") {
		t.Errorf("the skill should have been replaced, got %q", got)
	}
	assertSkillsClean(t, repo, "after a successful adopt")

	// The staging directory lives in .agents/scratch and is removed either
	// way; if one survived there with a complete SKILL.md, LoadSkills would
	// not see it (it walks skills/ only), which is the point of the move.
	leftovers, _ := filepath.Glob(filepath.Join(repo, ".agents", "scratch", "adopt-*"))
	if len(leftovers) != 0 {
		t.Errorf("staging directories left in scratch: %v", leftovers)
	}
}

// assertSkillsClean fails if skills/ holds anything but real skill
// directories: a staging or partial copy there would be loaded as a second
// skill of the same name and installed over the real one (code review
// 2026-09-16).
func assertSkillsClean(t *testing.T, repo, when string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repo, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.Contains(e.Name(), "staging") || strings.Contains(e.Name(), "adopt-") || strings.HasPrefix(e.Name(), ".") {
			t.Errorf("%s: skills/ holds %q, which is not a skill", when, e.Name())
		}
	}
	skills, err := LoadSkills(DirSource(repo))
	if err != nil {
		t.Fatalf("%s: LoadSkills: %v", when, err)
	}
	seen := map[string]bool{}
	for _, s := range skills {
		if seen[s.Name] {
			t.Errorf("%s: two skills named %q are loaded", when, s.Name)
		}
		seen[s.Name] = true
	}
}

// TestLoadSkillsRefusesDuplicateNames: two directories whose frontmatter
// claims one name install to a single destination, so the walk order alone
// would decide which text every machine reads. That is what an interrupted
// `adopt` used to be able to leave behind (its staging now lives outside
// skills/), and what a hand-copied directory can still produce.
func TestLoadSkillsRefusesDuplicateNames(t *testing.T) {
	repo := newRepo(t)
	dup := filepath.Join(repo, "skills", "alpha-copy")
	if err := os.MkdirAll(dup, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: alpha\ndescription: a second directory claiming the same name\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dup, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSkills(DirSource(repo))
	if err == nil {
		t.Fatal("two skills named alpha must be refused")
	}
	for _, want := range []string{"alpha", "alpha-copy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name %s, got %v", want, err)
		}
	}
}

// TestPlanRefusesSecretShapedFiles: a credential that slipped past
// .gitignore into a skill directory is invisible to review (git never shows
// it) and install is the one step that would copy it into both vendor
// directories on every machine (security review 2026-09-16).
func TestPlanRefusesSecretShapedFiles(t *testing.T) {
	for _, name := range []string{".env", ".env.local", "server.key", "cert.pem", "creds.tfvars", "application_default_credentials.json", "sa-key.json"} {
		t.Run(name, func(t *testing.T) {
			repo := newRepo(t)
			if err := os.WriteFile(filepath.Join(repo, "skills", "alpha", name), []byte("secret\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Plan(DirSource(repo), nil); err == nil {
				t.Fatalf("%s must not be deployed", name)
			} else if !strings.Contains(err.Error(), name) {
				t.Errorf("the error should name the file, got %v", err)
			}
		})
	}
	t.Run("an ordinary reference file still plans", func(t *testing.T) {
		repo := newRepo(t)
		if err := os.WriteFile(filepath.Join(repo, "skills", "alpha", "keys-explained.md"), []byte("about keys\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		files, err := Plan(DirSource(repo), nil)
		if err != nil {
			t.Fatalf("a normal file must still plan: %v", err)
		}
		var found bool
		for _, f := range files {
			if strings.HasSuffix(f.Rel, "keys-explained.md") {
				found = true
			}
		}
		if !found {
			t.Error("the reference file should have been planned")
		}
	})
}

// TestInspectCountsUntrackedFiles: `status.showUntrackedFiles=no` used to
// make a tree holding a whole new untracked skill read as clean, which
// `update` would then deploy everywhere (security review 2026-09-16).
func TestInspectCountsUntrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := newRepo(t)
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--quiet", "-b", "main")
	git("config", "user.email", "fixture@example.invalid")
	git("config", "user.name", "fixture")
	git("add", "-A")
	git("commit", "--quiet", "-m", "fixture")
	if Inspect(repo).Dirty {
		t.Fatal("a freshly committed tree is clean")
	}

	// The operator's own setting, which Inspect must not inherit.
	git("config", "status.showUntrackedFiles", "no")
	if err := os.MkdirAll(filepath.Join(repo, "skills", "smuggled"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills", "smuggled", "SKILL.md"), []byte("---\nname: smuggled\ndescription: never committed\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Inspect(repo).Dirty {
		t.Error("an untracked skill directory makes the checkout dirty, whatever status.showUntrackedFiles says")
	}
}

// TestInspectFailsClosedOnUnknownStatus: "git could not answer" must never
// read as "the tree is clean" to a caller deciding whether to deploy
// (release review of v1.0.0, finding 4).
func TestInspectFailsClosedOnUnknownStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := newRepo(t)
	// A .git that is a file, not a directory: IsCheckout is satisfied, and
	// every git command then fails rather than reporting a clean tree.
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("not a git dir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info := Inspect(repo)
	if !info.StatusUnknown {
		t.Error("a git status that failed must be reported as unknown")
	}
	if err := Pull(repo, io.Discard); err == nil {
		t.Error("Pull must refuse a checkout whose status git cannot report")
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
	write("cmd/sh-agent/main.go", "package main\n\nfunc main() {}\n")
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
	probe := exec.Command("go", "build", "-o", filepath.Join(t.TempDir(), "probe"), "./cmd/sh-agent")
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

// TestPlanDropsGroupAndOtherWrite: a skill file that is group-writable in
// the repo must not land group-writable in ~/.gemini/config, where its contents are
// instructions every session reads (security review of v1.0.0).
func TestPlanDropsGroupAndOtherWrite(t *testing.T) {
	repo := newRepo(t)
	loose := filepath.Join(repo, "skills", "alpha", "loose.md")
	if err := os.WriteFile(loose, []byte("reference\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o666); err != nil {
		t.Fatal(err)
	}
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, f := range files {
		if f.Mode&0o022 != 0 {
			t.Errorf("%s would be deployed mode %o — group or other writable", f.Rel, f.Mode)
		}
		if strings.HasSuffix(f.Rel, "loose.md") {
			seen++
			if f.Mode.Perm() != 0o644 {
				t.Errorf("loose.md mode = %o, want 644", f.Mode.Perm())
			}
		}
	}
	if seen == 0 {
		t.Error("the loose file should still be planned")
	}
}

// TestInstallCorrectsTheModeOfAnUnchangedFile: same bytes is not the same
// file. A deployed copy left group-writable stays that way on the
// "unchanged" branch, so the machine that has the problem is the one that
// never gets the fix (security review of v1.0.0, round 2, which measured it
// on a real install).
func TestInstallCorrectsTheModeOfAnUnchangedFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root's umask games make the mode assertions meaningless")
	}
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &buf); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(home, AgyRoot, "skills", "alpha", "SKILL.md")
	if err := os.Chmod(victim, 0o666); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := Install(DirSource(repo), home, files, false, &buf); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(victim)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		t.Errorf("a second install left %s mode %o — group or other writable", victim, info.Mode().Perm())
	}
	// And a dry run changes nothing.
	if err := os.Chmod(victim, 0o666); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := Install(DirSource(repo), home, files, true, &buf); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(victim); info.Mode().Perm() != 0o666 {
		t.Errorf("--dry-run changed a mode: %o", info.Mode().Perm())
	}
}

// TestAdoptRefusesSecretShapedFiles guards the entrance: Plan refuses a
// credential on the way out, but adopt copies into skills/, where it would
// be committed and pushed before any consumer's install ever refused it.
func TestAdoptRefusesSecretShapedFiles(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	src := filepath.Join(t.TempDir(), "widget")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: widget\ndescription: has a key beside it\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "id_ed25519"), []byte("PRIVATE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Adopt(repo, home, src, false)
	if err == nil {
		t.Fatal("adopt must refuse a skill carrying a private key")
	}
	if !strings.Contains(err.Error(), "id_ed25519") {
		t.Errorf("the error should name the file, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "skills", "widget")); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("nothing should have been copied into skills/")
	}
}

// install names each file it changes, on every run: `add` for a file that
// did not exist, `update` for changed bytes (or a previous file it could not
// read), `mode` for a permission it corrects, `remove` for a file the source
// no longer has. Unchanged files print nothing; --dry-run prints the same
// lines and writes nothing (plan sh-agent-works-everywhere, Phase 1, D4, D6-D9).
func TestInstallNamesEveryChange(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	lines := func(out string, verb string) []string {
		var got []string
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, verb+" ") {
				got = append(got, l)
			}
		}
		return got
	}
	plan := func() []File {
		t.Helper()
		files, err := Plan(DirSource(repo), []byte("#!/bin/sh\necho sh-agent\n"))
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	install := func(files []File, dryRun bool) string {
		t.Helper()
		var out bytes.Buffer
		if err := Install(DirSource(repo), home, files, dryRun, &out); err != nil {
			t.Fatalf("install: %v\n%s", err, out.String())
		}
		return out.String()
	}

	// 1. First install: one `add` per managed file, nothing else.
	files := plan()
	out := install(files, false)
	if adds := lines(out, "add"); len(adds) != len(files) {
		t.Errorf("first install: %d `add` lines for %d files:\n%s", len(adds), len(files), out)
	}
	for _, verb := range []string{"update", "remove", "mode"} {
		if got := lines(out, verb); len(got) != 0 {
			t.Errorf("first install printed %s lines: %v", verb, got)
		}
	}
	if !strings.Contains(out, strconv.Itoa(len(files))+" written, 0 unchanged") {
		t.Errorf("first install summary:\n%s", out)
	}

	// A second, identical install: nothing named at all.
	out = install(files, false)
	for _, verb := range []string{"add", "update", "remove", "mode"} {
		if got := lines(out, verb); len(got) != 0 {
			t.Errorf("identical install printed %s lines: %v", verb, got)
		}
	}

	// 2. One changed source file: `update` for each deployed copy of it, nothing else.
	alpha := filepath.Join(repo, "skills", "alpha", "SKILL.md")
	if err := os.WriteFile(alpha, []byte("---\nname: alpha\ndescription: changed\n---\nnew body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files = plan()
	out = install(files, false)
	updates := lines(out, "update")
	if len(updates) == 0 {
		t.Errorf("a changed source file printed no `update` line:\n%s", out)
	}
	for _, l := range updates {
		if !strings.HasSuffix(l, "skills/alpha/SKILL.md") {
			t.Errorf("unexpected update line: %q", l)
		}
	}
	for _, verb := range []string{"add", "remove", "mode"} {
		if got := lines(out, verb); len(got) != 0 {
			t.Errorf("changed-file install printed %s lines: %v", verb, got)
		}
	}

	// 3. A removed source file: `remove … (no longer in the source)` on the real run.
	if err := os.RemoveAll(filepath.Join(repo, "skills", "beta")); err != nil {
		t.Fatal(err)
	}
	files = plan()
	out = install(files, false)
	removes := lines(out, "remove")
	if len(removes) == 0 {
		t.Errorf("a removed source file printed no `remove` line:\n%s", out)
	}
	for _, l := range removes {
		if !strings.Contains(l, "skills/beta/") || !strings.HasSuffix(l, "(no longer in the source)") {
			t.Errorf("unexpected remove line: %q", l)
		}
	}

	// 4. A mode-only difference: one `mode` line, still counted unchanged.
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file and its umask blurs the mode cases")
	}
	victim := filepath.Join(home, AgyRoot, "skills", "alpha", "SKILL.md")
	if err := os.Chmod(victim, 0o666); err != nil {
		t.Fatal(err)
	}
	out = install(files, false)
	modes := lines(out, "mode")
	if len(modes) != 1 || !strings.Contains(modes[0], "skills/alpha/SKILL.md (0666 -> 0644)") {
		t.Errorf("mode-only difference: want one `mode … (0666 -> 0644)` line, got %v:\n%s", modes, out)
	}
	if !strings.Contains(out, "0 written, ") {
		t.Errorf("a mode-only correction counted as written:\n%s", out)
	}
	if info, _ := os.Stat(victim); info.Mode().Perm() != 0o644 {
		t.Errorf("mode not corrected: %o", info.Mode().Perm())
	}

	// 5. A previous file that cannot be read is written over and the line says why (D6, D9).
	if err := os.Chmod(victim, 0o000); err != nil {
		t.Fatal(err)
	}
	out = install(files, false)
	updates = lines(out, "update")
	if len(updates) != 1 || !strings.Contains(updates[0], "skills/alpha/SKILL.md (previous file unreadable: ") {
		t.Errorf("unreadable previous file: want one `update … (previous file unreadable: …)` line, got %v:\n%s", updates, out)
	}
	if got, err := os.ReadFile(victim); err != nil || !strings.Contains(string(got), "new body") {
		t.Errorf("the unreadable file was not written over: %v %q", err, got)
	}

	// 6. and 7. --dry-run: the same lines for a changed source, a removed
	// source and a mode difference, and nothing written or chmodded.
	if err := os.WriteFile(alpha, []byte("---\nname: alpha\ndescription: changed again\n---\nthird body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(victim, 0o666); err != nil {
		t.Fatal(err)
	}
	gamma := filepath.Join(home, AgyRoot, "skills", "alpha", "references", "notes.md")
	if err := os.Chmod(gamma, 0o666); err != nil {
		t.Fatal(err)
	}
	files = plan()
	out = install(files, true)
	if got := lines(out, "update"); len(got) == 0 || !strings.HasSuffix(got[0], "skills/alpha/SKILL.md") {
		t.Errorf("--dry-run: want `update` lines for the changed skill, got %v:\n%s", got, out)
	}
	if got := lines(out, "mode"); len(got) != 1 || !strings.Contains(got[0], "references/notes.md (0666 -> 0644)") {
		t.Errorf("--dry-run: want one `mode` line for the untouched-but-chmodded file, got %v:\n%s", got, out)
	}
	if got, _ := os.ReadFile(victim); strings.Contains(string(got), "third body") {
		t.Errorf("--dry-run wrote a file")
	}
	if info, _ := os.Stat(gamma); info.Mode().Perm() != 0o666 {
		t.Errorf("--dry-run changed a mode: %o", info.Mode().Perm())
	}

	// 8. A directory at a destination cannot be replaced by the rename: the
	// install errors, and the line is not printed for a change that did not
	// happen (the line prints after the write).
	if err := os.Chmod(victim, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := Install(DirSource(repo), home, files, false, &buf)
	if err == nil {
		t.Fatalf("a directory at a destination did not fail the install:\n%s", buf.String())
	}
	for _, l := range lines(buf.String(), "update") {
		if strings.Contains(l, "skills/alpha/SKILL.md") {
			t.Errorf("a line claimed a write that failed: %q (err %v)", l, err)
		}
	}
}

// TestVendoredRowsCarryAPin: every skill VENDORED.md lists names the
// repository it came from and a pin (a commit or a digest), so the copy can
// be checked against upstream and a local edit told from upstream content.
// Two rows read "not recorded when vendored" for a year (project review
// 2026-09-30, security finding 4).
func TestVendoredRowsCarryAPin(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "skills", "VENDORED.md"))
	if err != nil {
		t.Fatal(err)
	}
	pin := regexp.MustCompile(`\b[0-9a-f]{7,40}\b|sha256:[0-9a-f]{6,}`)
	vendored := vendoredSkills(t, repo)
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cell := strings.TrimSpace(strings.Trim(strings.SplitN(strings.TrimSpace(line), "|", 3)[1], " `"))
		if !vendored[cell] {
			continue
		}
		seen[cell] = true
		if strings.Contains(line, "not recorded") || !strings.Contains(line, "github.com/") && !strings.Contains(line, "https://") {
			t.Errorf("%s: the row names no source repository: %s", cell, line)
		}
		if !pin.MatchString(line) {
			t.Errorf("%s: the row carries no commit or digest pin: %s", cell, line)
		}
	}
	for name := range vendored {
		if !seen[name] {
			t.Errorf("%s: listed by vendoredSkills but not seen in a row", name)
		}
	}
}

// TestAbandonNamesATempFileItCouldNotRemove: a failed write removes its temp
// file, and when even that fails the returned error names the file, so
// nothing is left behind unseen (the settings writer's own diagnostic, kept
// when it moved onto WriteAtomic; code review 2026-09-30).
func TestAbandonNamesATempFileItCouldNotRemove(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permissions this test relies on")
	}
	dir := t.TempDir()
	tmp, err := os.CreateTemp(dir, ".sh-agent-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	got := abandon(tmp, errors.New("disk full"))
	if got == nil || !strings.Contains(got.Error(), "disk full") || !strings.Contains(got.Error(), "left behind") || !strings.Contains(got.Error(), tmp.Name()) {
		t.Errorf("the cause and the leftover temp file should both be named, got %v", got)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	tmp2, err := os.CreateTemp(dir, ".sh-agent-*")
	if err != nil {
		t.Fatal(err)
	}
	if got := abandon(tmp2, errors.New("disk full")); got == nil || got.Error() != "disk full" {
		t.Errorf("a removed temp file adds nothing to the cause, got %v", got)
	}
	if _, err := os.Lstat(tmp2.Name()); !os.IsNotExist(err) {
		t.Errorf("the temp file should be removed, got %v", err)
	}
}

// TestWriteAtomicClosesBeforeRename pins the order no behavioural test can
// observe: the temp file is closed before it is renamed into place, so an
// error from WriteAtomic always means dest is unchanged, and a binary
// renamed into place is never still open (ETXTBSY on Linux). Round 2 of the
// 2026-09-30 review swapped the two lines and every test stayed green.
func TestWriteAtomicClosesBeforeRename(t *testing.T) {
	src, err := os.ReadFile("apply.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)[strings.Index(string(src), "func WriteAtomic("):]
	body = body[:strings.Index(body, "\n}\n")]
	// Code lines only: a comment naming the close above the rename must not
	// satisfy the check (v1.13.1 gate review).
	var code []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			code = append(code, line)
		}
	}
	body = strings.Join(code, "\n")
	closeAt, renameAt := strings.Index(body, "tmp.Close()"), strings.Index(body, "os.Rename(")
	if closeAt < 0 || renameAt < 0 || closeAt > renameAt {
		t.Errorf("WriteAtomic must close the temp file before renaming it: close at %d, rename at %d", closeAt, renameAt)
	}
}

// TestInstallMidFailureKeepsManifestComplete: files were written one by one
// and the manifest saved only at the end, so a write that failed midway left
// files on disk no manifest listed (audit of 2026-09-30, IN2). After a
// failure the manifest is (every old entry still on disk) ∪ (written so far).
func TestInstallMidFailureKeepsManifestComplete(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	old, _, _ := LoadState(home)
	// A new skill that sorts before alpha, and a change to alpha whose
	// directory is made unwritable: the new skill is written, alpha fails.
	if err := os.MkdirAll(filepath.Join(repo, "skills", "aaa-new"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "skills", "aaa-new", "SKILL.md"), []byte("---\nname: aaa-new\ndescription: new\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "skills", "alpha", "SKILL.md"), []byte("---\nname: alpha\ndescription: changed\n---\nnew body\n"), 0o644)
	alphaDir := filepath.Join(home, AgyRoot, "skills", "alpha")
	if err := os.Chmod(alphaDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(alphaDir, 0o755) })
	files, err = Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &out); err == nil {
		t.Fatalf("install did not fail on the unwritable directory:\n%s", out.String())
	}
	st, _, err := LoadState(home)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, rel := range st.Files {
		listed[rel] = true
	}
	newRel := filepath.Join(AgyRoot, "skills", "aaa-new", "SKILL.md")
	if _, err := os.Stat(filepath.Join(home, newRel)); err != nil {
		t.Fatalf("the new skill was not written before the failure: %v", err)
	}
	if !listed[newRel] {
		t.Errorf("manifest lacks the file written before the failure: %s", newRel)
	}
	for _, rel := range old.Files {
		if _, err := os.Lstat(filepath.Join(home, rel)); err == nil && !listed[rel] {
			t.Errorf("manifest dropped an old entry still on disk: %s", rel)
		}
	}
	os.Chmod(alphaDir, 0o755)
	if err := Uninstall(home, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, newRel)); !os.IsNotExist(err) {
		t.Error("uninstall left the file written before the failure")
	}
}

// deployedSkillFiles returns the planned skill paths, sorted.
func deployedSkillFiles(files []File) []string {
	var rels []string
	for _, f := range files {
		if strings.HasPrefix(f.Rel, filepath.Join(AgyRoot, "skills")+string(filepath.Separator)) {
			rels = append(rels, f.Rel)
		}
	}
	sort.Strings(rels)
	return rels
}

// TestInstallDoesNotFollowLinkedDest: the unchanged branch read, stat'ed
// and chmodded the destination through a symlink, and a changed file's
// rename replaced the link with a regular file (audit of 2026-09-30, IN3).
// A linked destination is kept, never written through, and never enters the
// manifest.
func TestInstallDoesNotFollowLinkedDest(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	rels := deployedSkillFiles(files)
	if len(rels) < 2 {
		t.Fatalf("fixture has %d deployed skill files, need 2", len(rels))
	}
	var planned = map[string]File{}
	for _, f := range files {
		planned[f.Rel] = f
	}
	targets := t.TempDir()
	// Link 1: the target holds the planned bytes but a different mode.
	same := filepath.Join(targets, "same")
	os.WriteFile(same, planned[rels[0]].Data, 0o600)
	// Link 2: the target holds other bytes.
	other := filepath.Join(targets, "other")
	os.WriteFile(other, []byte("the operator's own file\n"), 0o600)
	for i, target := range []string{same, other} {
		dest := filepath.Join(home, rels[i])
		os.Remove(dest)
		if err := os.Symlink(target, dest); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	for i, target := range []string{same, other} {
		dest := filepath.Join(home, rels[i])
		if fi, err := os.Lstat(dest); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is no longer a symlink (err %v)", rels[i], err)
		}
		if fi, err := os.Stat(target); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("the target of %s changed mode: %v", rels[i], fi.Mode())
		}
	}
	if got, _ := os.ReadFile(other); string(got) != "the operator's own file\n" {
		t.Errorf("the target of %s was written through: %q", rels[1], got)
	}
	if n := strings.Count(out.String(), "(a symlink; sh-agent never writes through a link)"); n != 2 {
		t.Errorf("want two keep lines for links, got %d:\n%s", n, out.String())
	}
	st, _, _ := LoadState(home)
	for _, rel := range st.Files {
		if rel == rels[0] || rel == rels[1] {
			t.Errorf("a linked destination entered the manifest: %s", rel)
		}
	}
	// Status and Diff report a link as foreign, never as drift or a diff.
	report, err := Status(home, files)
	if err != nil {
		t.Fatal(err)
	}
	if report.Drift() {
		t.Errorf("status reports drift for linked destinations: %+v", report.Counts)
	}
	if report.Counts["foreign"] < 2 {
		t.Errorf("status counts %d foreign, want the two links", report.Counts["foreign"])
	}
	var diff bytes.Buffer
	if err := Diff(home, files, &diff); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff.String(), rels[1]) {
		t.Errorf("diff printed the linked destination:\n%s", diff.String())
	}
}

// TestUninstallAndLinkedDirs: an old manifest may name a path that is the
// operator's link now, or a file under a linked directory; neither is
// removed, written or chmodded through (security review of IN3's fix).
func TestUninstallAndLinkedDirs(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	rels := deployedSkillFiles(files)
	// The whole skills directory becomes the operator's link to a
	// tree of their own; the manifest still lists every file under it.
	skillsDir := filepath.Join(home, AgyRoot, "skills")
	mine := filepath.Join(t.TempDir(), "mine")
	if err := os.Rename(skillsDir, mine); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mine, skillsDir); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(mine, strings.TrimPrefix(rels[0], filepath.Join(AgyRoot, "skills")+string(filepath.Separator))))
	// A reinstall with a changed skill writes nothing through the link.
	os.WriteFile(filepath.Join(repo, "skills", "alpha", "SKILL.md"), []byte("---\nname: alpha\ndescription: changed\n---\nnew body\n"), 0o644)
	files, _ = Plan(DirSource(repo), nil)
	var out bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	after, _ := os.ReadFile(filepath.Join(mine, "alpha", "SKILL.md"))
	if string(after) != string(before) {
		t.Errorf("install wrote through the linked skills directory")
	}
	if !strings.Contains(out.String(), "keep    ~/"+rels[0]+" (a symlink; sh-agent never writes through a link)") {
		t.Errorf("no keep line for a file under a linked directory:\n%s", out.String())
	}
	// Uninstall removes nothing under the link and keeps the link.
	out.Reset()
	if err := Uninstall(home, false, &out); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	if fi, err := os.Lstat(skillsDir); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("uninstall removed the operator's link (err %v)", err)
	}
	if _, err := os.Stat(filepath.Join(mine, "alpha", "SKILL.md")); err != nil {
		t.Errorf("uninstall removed a file through the link: %v", err)
	}
}

// TestUninstallKeepsMetricsStore: uninstall removes what its manifest lists
// and nothing else; the metrics store is the only copy of what expired
// transcripts held, and stays (R4, ADR-055).
func TestUninstallKeepsMetricsStore(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(home, ".config", "sh-agent", "metrics.db")
	if err := os.WriteFile(store, []byte("rows"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Uninstall(home, false, &out); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	if b, err := os.ReadFile(store); err != nil || string(b) != "rows" {
		t.Fatalf("uninstall touched the metrics store: %v", err)
	}
}

// TestUninstallSkipsLink: a manifest path that has become the operator's
// link is kept by uninstall (security review round 2 of IN3: the guard at
// the one line that answers it had no test).
func TestUninstallSkipsLink(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	rel := deployedSkillFiles(files)[0]
	target := filepath.Join(t.TempDir(), "mine")
	os.WriteFile(target, []byte("mine\n"), 0o600)
	dest := filepath.Join(home, rel)
	os.Remove(dest)
	if err := os.Symlink(target, dest); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Uninstall(home, false, &out); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	if fi, err := os.Lstat(dest); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("uninstall removed the operator's link at %s (err %v)", rel, err)
	}
	if !strings.Contains(out.String(), "keep    ~/"+rel) {
		t.Errorf("no keep line:\n%s", out.String())
	}
}

// TestUninstallSkipsFilesUnderLink: an old manifest lists files under a
// directory that has since become the operator's link; uninstall removes
// none of them through it (security review round 3: the underLink half of
// the guard had no test of its own).
func TestUninstallSkipsFilesUnderLink(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(home, AgyRoot, "skills")
	mine := filepath.Join(t.TempDir(), "mine")
	if err := os.Rename(skillsDir, mine); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mine, skillsDir); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(filepath.Join(mine, "alpha"))
	var out bytes.Buffer
	if err := Uninstall(home, false, &out); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	after, _ := os.ReadDir(filepath.Join(mine, "alpha"))
	if len(after) != len(before) || len(before) == 0 {
		t.Errorf("uninstall removed files through the linked directory: %d before, %d after", len(before), len(after))
	}
	if fi, err := os.Lstat(skillsDir); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("uninstall removed the operator's link (err %v)", err)
	}
}

// TestInstallResyncFailureKeepsManifest: the IN2 rule on the resync path: a
// stale file that cannot be removed leaves the files written before it
// listed (code review round 2, 2026-10-01).
func TestInstallResyncFailureKeepsManifest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	// beta leaves the repo (its deployed file becomes stale) and a new skill
	// arrives (written before the resync); beta's directory is unremovable.
	os.RemoveAll(filepath.Join(repo, "skills", "beta"))
	os.MkdirAll(filepath.Join(repo, "skills", "aaa-new"), 0o755)
	os.WriteFile(filepath.Join(repo, "skills", "aaa-new", "SKILL.md"), []byte("---\nname: aaa-new\ndescription: new\n---\nbody\n"), 0o644)
	betaDir := filepath.Join(home, AgyRoot, "skills", "beta")
	if err := os.Chmod(betaDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(betaDir, 0o755) })
	files, _ = Plan(DirSource(repo), nil)
	var out bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &out); err == nil {
		t.Fatalf("install did not fail on the unremovable stale file:\n%s", out.String())
	}
	st, _, _ := LoadState(home)
	newRel := filepath.Join(AgyRoot, "skills", "aaa-new", "SKILL.md")
	found := false
	for _, rel := range st.Files {
		found = found || rel == newRel
	}
	if !found {
		t.Errorf("manifest lacks the file written before the failed resync: %s", newRel)
	}
}

// TestResyncSkipsLink: the resync loop removed a path the old manifest named
// even when it had become a symlink (audit of 2026-09-30, IN3).
func TestResyncSkipsLink(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	rel := deployedSkillFiles(files)[0]
	skill := strings.Split(rel, string(filepath.Separator))[2]
	target := filepath.Join(t.TempDir(), "mine")
	os.WriteFile(target, []byte("mine\n"), 0o600)
	dest := filepath.Join(home, rel)
	os.Remove(dest)
	if err := os.Symlink(target, dest); err != nil {
		t.Fatal(err)
	}
	// The skill leaves the repo: the resync loop would remove its old path.
	if err := os.RemoveAll(filepath.Join(repo, "skills", skill)); err != nil {
		t.Fatal(err)
	}
	files, err = Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(dest); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the resync removed the operator's link at %s (err %v)", rel, err)
	}
}

// TestAgentDescriptionRules: an agent's description is one line and names
// neither "dispatch prompt" nor "harness binary" (AGENTS.md § Rules; audit
// 2026-09-30, AG12). Until now only inspection enforced it.
func TestAgentDescriptionRules(t *testing.T) {
	for name, front := range map[string]string{
		"two lines":       "name: x\ndescription: |\n  first line\n  second line\nsubagent: true\nmainAgent: false\n",
		"dispatch prompt": "name: x\ndescription: \"the dispatch prompt names the file\"\nsubagent: true\nmainAgent: false\n",
		"harness binary":  "name: x\ndescription: \"run by the harness binary\"\nsubagent: true\nmainAgent: false\n",
	} {
		repo := newRepo(t)
		writeAgent(t, repo, "x", "---\n"+front+"---\nbody\n")
		if _, err := LoadAgents(DirSource(repo)); err == nil {
			t.Errorf("%s: LoadAgents accepted the description", name)
		}
	}
	// The fixture's own agent (one line, plain words) and the real agents pass.
	if _, err := LoadAgents(DirSource(newRepo(t))); err != nil {
		t.Errorf("the fixture's agents: %v", err)
	}
	if _, err := LoadAgents(DirSource(filepath.Join("..", ".."))); err != nil {
		t.Errorf("the checkout's agents: %v", err)
	}
}

// TestLintDescriptionLimit: a skill or agent description over 1,024
// characters, the Agent Skills spec's limit, is refused; a multi-line
// description is measured after its continuation lines are joined (audit
// 2026-09-30, D19).
func TestLintDescriptionLimit(t *testing.T) {
	long := strings.Repeat("word ", 206) // 1,029 characters after the trailing space is trimmed
	if _, err := ParseStrict("name: x\ndescription: \"" + long + "\"\n"); err == nil {
		t.Error("a 1,025-character description passed Lint")
	}
	block := "name: x\ndescription: |\n"
	for i := 0; i < 11; i++ {
		block += "  " + strings.Repeat("word ", 20) + "\n" // 11 × 100 = 1,100 joined
	}
	if _, err := ParseStrict(block); err == nil {
		t.Error("a 1,100-character block description passed Lint")
	}
	if _, err := ParseStrict("name: x\ndescription: \"" + strings.Repeat("word ", 204) + "\"\n"); err != nil {
		t.Errorf("a 1,020-character description was refused: %v", err)
	}
	// A block of exactly 1,023 joined characters passes: the `|` indicator
	// and its space are not counted (code review of the fix).
	block = "name: x\ndescription: |\n"
	for i := 0; i < 10; i++ {
		block += "  " + strings.Repeat("word ", 20) + "\n"
	}
	block += "  " + strings.Repeat("w", 13) + "\n" // 10 × 99 (joined by spaces) + 1 + 13 = 1,004 + 19 → under the limit
	if _, err := ParseStrict(block); err != nil {
		t.Errorf("a block description under the limit was refused: %v", err)
	}
}

// TestLintRefusesAllowedTools: a skill's frontmatter is a permission surface;
// `allowed-tools` lets every named tool run unprompted while the skill is
// active, so a harness skill never sets it — the operator's permission mode
// decides (BUG-143, graduated here).
func TestLintRefusesAllowedTools(t *testing.T) {
	repo := newRepo(t)
	dir := filepath.Join(repo, "skills", "x")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\ndescription: a skill\nallowed-tools: Bash\n---\nbody\n"), 0o644)
	if _, err := LoadSkills(DirSource(repo)); err == nil || !strings.Contains(err.Error(), "allowed-tools") {
		t.Errorf("a skill with allowed-tools passed: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\ndescription: a skill\n---\nbody\n"), 0o644)
	if _, err := LoadSkills(DirSource(repo)); err != nil {
		t.Errorf("the same skill without the key was refused: %v", err)
	}
	if _, err := LoadSkills(DirSource(filepath.Join("..", ".."))); err != nil {
		t.Errorf("the checkout's skills: %v", err)
	}
}

// TestLintPermissionKeysByKind: the permission-granting frontmatter keys are
// refused per file kind, and a top-level line the installer's key grammar
// cannot read is refused rather than skipped — a quoted or spaced key is the
// same key to a YAML parser (security review 2026-10-01; one rule per kind).
func TestLintPermissionKeysByKind(t *testing.T) {
	skill := func(front string) error {
		repo := newRepo(t)
		dir := filepath.Join(repo, "skills", "x")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\ndescription: a skill\n"+front+"---\nbody\n"), 0o644)
		_, err := LoadSkills(DirSource(repo))
		return err
	}
	for name, front := range map[string]string{
		"double-quoted key": "\"allowed-tools\": Bash\n",
		"single-quoted key": "'allowed-tools': Bash\n",
		"spaced colon":      "allowed-tools : Bash\n",
		"block list":        "allowed-tools:\n  - Bash\n",
		"hooks":             "hooks:\n  PreToolUse: []\n",
	} {
		if err := skill(front); err == nil {
			t.Errorf("skill with %s loaded", name)
		}
	}
	for name, front := range map[string]string{
		"metadata map":  "metadata:\n  version: \"1.0\"\n",
		"license":       "license: Apache-2.0\n",
		"comment line":  "# a comment\n",
		"column-0 list": "compatibility:\n- agy\n- gemini\n",
	} {
		if err := skill(front); err != nil {
			t.Errorf("skill with %s refused: %v", name, err)
		}
	}
	agent := func(front string) error {
		repo := newRepo(t)
		writeAgent(t, repo, "x", "---\nname: x\ndescription: an agent\nsubagent: true\nmainAgent: false\ntools: [view_file]\n"+front+"---\nbody\n")
		_, err := LoadAgents(DirSource(repo))
		return err
	}
	// mcpServers: an inline MCP server is a command line the CLI starts
	// when the agent is used, outside the permission mode (project review
	// 2026-10-09, security row 1; left open by the 2026-10-01 gate).
	// An agent key outside the allow-list is refused, whatever it does:
	// `memory` and `initialPrompt` passed the deny-list (the project review's
	// security round 1, finding 5), and a vendor's next key would too
	// (ADR-056).
	for _, front := range []string{"permissionMode: bypassPermissions\n", "hooks:\n  Stop: []\n", "mcpServers:\n  x:\n    command: sh\n", "mcpServers: {x: {command: sh}}\n",
		"memory: project\n", "initialPrompt: run the tests\n", "\"memory\": user\n", "isolation: worktree\n", "futureKey: x\n",
		// A restriction Antigravity does not honour would leave the agent
		// unrestricted while reading as restricted (ADR-056a).
		"disallowedTools: Write\n",
		// The old format's keys: Antigravity has no equivalent, so a file
		// carrying one was written for another CLI (ADR-062, F6).
		"maxTurns: 10\n", "skills: [golang]\n", "effort: high\n", "color: blue\n"} {
		if err := agent(front); err == nil {
			t.Errorf("agent with %q loaded", front)
		}
	}
	for _, front := range []string{"model: pro\n", "model: flash\n"} {
		if err := agent(front); err != nil {
			t.Errorf("agent with %q refused: %v", front, err)
		}
	}
	repo := newRepo(t)
	os.MkdirAll(filepath.Join(repo, "commands"), 0o755)
	os.WriteFile(filepath.Join(repo, "commands", "x.md"), []byte("---\ndescription: a command\nallowed-tools: Bash\n---\nbody\n"), 0o644)
	if _, err := LoadInventory(DirSource(repo)); err == nil {
		t.Error("a command with allowed-tools loaded")
	}
	if _, err := LoadInventory(DirSource(filepath.Join("..", ".."))); err != nil {
		t.Errorf("the checkout: %v", err)
	}
}

// TestLintRefusesUnreadableFrontmatterInDocs: a command or rule whose first
// line opens a frontmatter block that Split cannot read was once treated as
// having none, so the permission-key lint never saw it and the file deployed
// as written (the v2.1.0 gate's L5). A file with no frontmatter still loads.
func TestLintRefusesUnreadableFrontmatterInDocs(t *testing.T) {
	load := func(kind, text string) error {
		repo := newRepo(t)
		os.MkdirAll(filepath.Join(repo, kind), 0o755)
		os.WriteFile(filepath.Join(repo, kind, "x.md"), []byte(text), 0o644)
		_, err := LoadInventory(DirSource(repo))
		return err
	}
	for _, kind := range []string{"commands", "rules"} {
		for name, text := range map[string]string{
			"a byte-order mark":           "\ufeff---\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"spaces after the opening":    "---  \ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"a tab after the opening":     "---\t\ndescription: d\nhooks:\n  Stop: []\n---\nbody\n",
			"an unterminated block":       "---\ndescription: d\nallowed-tools: Bash\nbody with no closing line\n",
			"a carriage return and space": "--- \r\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			// Spellings a lenient reader could still take for a fence
			// (security review of v2.1.0..a62eb9d).
			"a no-break space after":   "---\u00a0\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"a form feed after":        "---\f\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"an ideographic space":     "---\u3000\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"two byte-order marks":     "\ufeff\ufeff---\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"a leading blank line":     "\n---\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"a tag on the fence":       "---yaml\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"a comment on the fence":   "--- # c\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"a zero-width space first": "\u200b---\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
		} {
			err := load(kind, text)
			if err == nil || !strings.Contains(err.Error(), "frontmatter starts but cannot be read") {
				t.Errorf("%s with %s: want the unreadable-frontmatter refusal, got %v", kind, name, err)
			}
		}
		for name, text := range map[string]string{
			"no frontmatter":             "# A title\n\nbody\n",
			"a rule line further down":   "A first line.\n\n---\n\nmore\n",
			"a readable block":           "---\ndescription: d\n---\nbody\n",
			"four dashes (a rule, text)": "----\nbody\n",
		} {
			if err := load(kind, text); err != nil {
				t.Errorf("%s with %s refused: %v", kind, name, err)
			}
		}
	}
}

// TestSplitFenceIsOneLine: a fence is `---` and at most one carriage return.
// A line of `---` and two carriage returns once closed the block, so a key
// written after it (and before a real closing line a lenient reader would
// take) was never checked (security read of a62eb9d..768efce).
func TestSplitFenceIsOneLine(t *testing.T) {
	if _, _, err := Split("---\r\nname: x\r\n---\r\nbody\r\n"); err != nil {
		t.Errorf("a CRLF file is not read: %v", err)
	}
	front, _, err := Split("---\nname: x\n---\r\r\nallowed-tools: Bash\n---\nbody\n")
	if err == nil && !strings.Contains(front, "allowed-tools") {
		t.Errorf("the block closed at a fence with two carriage returns; front = %q", front)
	}
	if _, _, err := Split("---\r\r\nname: x\n---\nbody\n"); err == nil {
		t.Error("an opening fence with two carriage returns was accepted")
	}
	repo := newRepo(t)
	dir := filepath.Join(repo, "skills", "x")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\ndescription: a skill\n---\r\r\nallowed-tools: Bash\n---\nbody\n"), 0o644)
	if _, err := LoadSkills(DirSource(repo)); err == nil {
		t.Error("a skill with a permission key behind a two-carriage-return fence loaded")
	} else if !strings.Contains(err.Error(), "line break") || strings.Contains(err.Error(), "quoted or spaced key") {
		// The refusal names the real problem (a line break that is not a
		// newline), not a key that cannot be read.
		t.Errorf("the refusal blames the wrong thing: %v", err)
	}
	// A `---` with a trailing space is not a fence to Split either; inside
	// the block it is refused as a fence that is not one.
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\ndescription: a skill\n--- \nallowed-tools: Bash\n---\nbody\n"), 0o644)
	if _, err := LoadSkills(DirSource(repo)); err == nil || !strings.Contains(err.Error(), "not a fence") {
		t.Errorf("a skill with a spaced fence inside its block: want the not-a-fence refusal, got %v", err)
	}
}

// TestLintSeesEveryKeyAYAMLReaderSees: three shapes hid a permission key
// from the lint while a YAML reader still read it, or may (security reviews
// of v2.2.0..1fc0d4c and 1fc0d4c..4165f81): a block whose lines are all
// indented (YAML reads a top-level map; the key grammar took the lines for
// continuations of nothing), a line break the lint does not split on inside a
// key line (a lone carriage return, NEL, the Unicode line and paragraph
// separators), and a line that starts with a tab (no YAML indentation: a
// reader errors, nests it, or reads a top-level key). Refused in every kind,
// whatever else the file gets right.
func TestLintSeesEveryKeyAYAMLReaderSees(t *testing.T) {
	const hidden = "allowed-tools: Bash(*)"
	// An agent sets only the keys of its allow-list (ADR-056), so its
	// keep-loading shapes use them; none of them holds a map.
	other := [3]string{"license", "compatibility", "metadata"}
	kinds := map[string]struct {
		keys  []string  // the kind's own required lines
		extra [3]string // a key the kind loads with a value, a list and a map ("" for none)
		load  func(front string) error
	}{
		"skill": {[]string{"name: x", "description: d"}, other, func(front string) error {
			repo := newRepo(t)
			dir := filepath.Join(repo, "skills", "x")
			os.MkdirAll(dir, 0o755)
			os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\n"+front+"---\nbody\n"), 0o644)
			_, err := LoadSkills(DirSource(repo))
			return err
		}},
		"agent": {[]string{"name: x", "description: d", "subagent: true", "mainAgent: false"}, [3]string{"model", "", ""}, func(front string) error {
			repo := newRepo(t)
			writeAgent(t, repo, "x", "---\n"+front+"---\nbody\n")
			_, err := LoadAgents(DirSource(repo))
			return err
		}},
		"command": {[]string{"description: d"}, other, func(front string) error {
			repo := newRepo(t)
			os.MkdirAll(filepath.Join(repo, "commands"), 0o755)
			os.WriteFile(filepath.Join(repo, "commands", "x.md"), []byte("---\n"+front+"---\nbody\n"), 0o644)
			_, err := LoadInventory(DirSource(repo))
			return err
		}},
		"rule": {[]string{"description: d"}, other, func(front string) error {
			repo := newRepo(t)
			os.MkdirAll(filepath.Join(repo, "rules"), 0o755)
			os.WriteFile(filepath.Join(repo, "rules", "x.md"), []byte("---\n"+front+"---\nbody\n"), 0o644)
			_, err := LoadInventory(DirSource(repo))
			return err
		}},
	}
	for kind, k := range kinds {
		plain := strings.Join(k.keys, "\n") + "\n"
		refused := map[string]string{
			"an indented block":      "  " + strings.Join(k.keys, "\n  ") + "\n  " + hidden + "\n",
			"an indented first line": "  " + hidden + "\n" + plain,
			// A tab is no YAML indentation: a strict reader errors on the
			// line, a lenient one nests it, an older one reads a top-level
			// key (security review of 1fc0d4c..4165f81).
			"a tab-indented first line":        "\t" + hidden + "\n" + plain,
			"a tab-led key after a key":        plain + "\t" + hidden + "\n",
			"a tab-led key after a nested map": plain + "metadata:\n  version: \"1.0\"\n\t" + hidden + "\n",
			"a tab-led key after a list":       plain + "compatibility:\n  - agy\n\t" + hidden + "\n",
			"a tab-led key after an empty key": plain + "metadata:\n\t" + hidden + "\n",
		}
		for name, sep := range map[string]string{
			"a lone carriage return":         "\r",
			"a next-line character (U+0085)": "\u0085",
			"a line separator (U+2028)":      "\u2028",
			"a paragraph separator (U+2029)": "\u2029",
		} {
			refused[name] = strings.TrimSuffix(plain, "\n") + sep + hidden + "\n"
		}
		for name, front := range refused {
			err := k.load(front)
			if err == nil {
				t.Errorf("%s with %s loaded", kind, name)
				continue
			}
			// The refusal is the lint's, not a side effect such as a
			// missing description.
			if msg := err.Error(); !strings.Contains(msg, "indented") && !strings.Contains(msg, "line break") && !strings.Contains(msg, "starts with a tab") {
				t.Errorf("%s with %s refused for another reason: %v", kind, name, err)
			}
		}
		// What must keep loading: a nested map, a list, a CRLF file.
		value, list, nested := k.extra[0], k.extra[1], k.extra[2]
		for name, front := range map[string]string{
			"a nested map":     plain + nested + ":\n  version: \"1.0\"\n",
			"an indented list": plain + list + ":\n  - agy\n",
			"a CRLF file":      strings.ReplaceAll(plain, "\n", "\r\n") + value + ": MIT\r\n",
			"a comment first":  "# a comment\n" + plain,
			// A tab inside a value, and a line that is only a tab, are not
			// tab indentation.
			"a tab inside a value": plain + value + ": MIT\tor Apache\n",
			"a tab-only line":      plain + "\t\n" + value + ": MIT\n",
		} {
			if nested == "" && name == "a nested map" || list == "" && name == "an indented list" {
				continue
			}
			if err := k.load(front); err != nil {
				t.Errorf("%s with %s refused: %v", kind, name, err)
			}
		}
	}
	// A folded value under its key is a continuation, not a hidden key
	// (skills allow one; agents want a one-line description).
	if err := kinds["skill"].load("name: x\ndescription: >-\n  a description\n  over two lines\n"); err != nil {
		t.Errorf("a skill with a folded description refused: %v", err)
	}
}
