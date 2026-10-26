package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResyncKeepsOldClaudeFiles: a manifest from an install before ADR-062
// lists files under ~/.claude. fyr never removes them (plan fyr D32: the
// operator keeps Claude Code's files where they still use it): the resync
// leaves each one as it is, drops it from fyr's manifest and says so in one
// line, while a stale entry outside ~/.claude still goes by the one rule.
// The next install is silent about them, and uninstall, before or after the
// first fyr install, never touches them either.
func TestResyncKeepsOldClaudeFiles(t *testing.T) {
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
	claude := []string{
		".claude/AGENTS.md", ".claude/CLAUDE.md", ".claude/skills/alpha/SKILL.md",
		".claude/agents/reviewer.md", ".claude/commands/deploy.md", ".claude/rules/go-style.md",
	}
	for _, rel := range claude {
		put(rel, Marker+"\nold "+rel+"\n")
	}
	put(".claude/settings.json", "{}\n") // the operator's, never in a manifest
	put(".local/bin/sh-agent", "#!/bin/sh\n")
	// A hand-edited manifest may spell an entry unclean: it names the same
	// file under ~/.claude and is kept too (security review of Phase 5b).
	put(".claude/rules/unclean-a.md", "a\n")
	put(".claude/rules/unclean-b.md", "b\n")
	old := append(append([]string{}, claude...), ".local/bin/sh-agent", "./.claude/rules/unclean-a.md", ".gemini/../.claude/rules/unclean-b.md")
	if err := SaveState(home, State{Repo: repo, Vendor: "all", Files: old}); err != nil {
		t.Fatal(err)
	}
	kept := func(t *testing.T, when string) {
		t.Helper()
		for _, rel := range claude {
			if got, err := os.ReadFile(filepath.Join(home, rel)); err != nil || string(got) != Marker+"\nold "+rel+"\n" {
				t.Errorf("%s: ~/%s removed or changed: %q, %v", when, rel, got, err)
			}
		}
		for _, rel := range []string{".claude/settings.json", ".claude/rules/unclean-a.md", ".claude/rules/unclean-b.md"} {
			if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
				t.Errorf("%s: ~/%s: %v", when, rel, err)
			}
		}
	}

	// uninstall straight after the migration, before any fyr install: the
	// manifest is still the old one, and its ~/.claude entries stay.
	var out bytes.Buffer
	if err := Uninstall(home, true, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "remove  ~/.claude") || !strings.Contains(out.String(), "keep    ~/.claude: 8 files") {
		t.Errorf("uninstall --dry-run over an old manifest:\n%s", out.String())
	}
	kept(t, "uninstall --dry-run")

	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Install(DirSource(repo), home, files, false, &out); err != nil {
		t.Fatal(err)
	}
	kept(t, "install")
	if strings.Contains(out.String(), "remove  ~/.claude") {
		t.Errorf("install reports removing a ~/.claude file:\n%s", out.String())
	}
	if n := strings.Count(out.String(), "~/.claude"); n != 1 || !strings.Contains(out.String(), "keep    ~/.claude: 8 files") {
		t.Errorf("install should name the kept ~/.claude files in one line (%d mentions):\n%s", n, out.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".local", "bin", "sh-agent")); !os.IsNotExist(err) {
		t.Errorf("~/.local/bin/sh-agent, a stale entry outside ~/.claude, was kept: %v", err)
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

	out.Reset()
	if err := Install(DirSource(repo), home, files, false, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), ".claude") {
		t.Errorf("the second install still talks about ~/.claude:\n%s", out.String())
	}
	out.Reset()
	if err := Uninstall(home, false, &out); err != nil {
		t.Fatal(err)
	}
	kept(t, "uninstall")
}

// TestInstallFailureKeepsClaudeFilesOutOfTheManifest: a write that fails
// midway saves the manifest as (old entries still on disk) ∪ (written so
// far), the IN2 rule; the old ~/.claude entries are not put back into it,
// and none of them is removed (plan fyr D32, security review of Phase 5b).
func TestInstallFailureKeepsClaudeFilesOutOfTheManifest(t *testing.T) {
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
	st, _, _ := LoadState(home)
	claude := filepath.Join(".claude", "AGENTS.md")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, claude), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st.Files = append(st.Files, claude)
	if err := SaveState(home, st); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "skills", "alpha", "SKILL.md"), []byte("---\nname: alpha\ndescription: changed\n---\nnew body\n"), 0o644)
	alphaDir := filepath.Join(home, AgyRoot, "skills", "alpha")
	if err := os.Chmod(alphaDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(alphaDir, 0o755) })
	if files, err = Plan(DirSource(repo), nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &out); err == nil {
		t.Fatalf("install did not fail on the unwritable directory:\n%s", out.String())
	}
	after, _, err := LoadState(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range after.Files {
		if strings.HasPrefix(rel, ".claude") {
			t.Errorf("the manifest saved on failure lists ~/%s", rel)
		}
	}
	if b, err := os.ReadFile(filepath.Join(home, claude)); err != nil || string(b) != "old\n" {
		t.Errorf("~/%s removed or changed: %q, %v", claude, b, err)
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
