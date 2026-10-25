package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLintAllowListPerKind: skill, command and rule frontmatter is checked
// against an allow list per kind, as an agent's already was (IN23, D7). A
// permission key in any spelling the old deny list missed is refused, and
// every skill, command and rule in the tree is accepted.
func TestLintAllowListPerKind(t *testing.T) {
	if _, err := LoadInventory(DirSource(filepath.Join("..", ".."))); err != nil {
		t.Fatalf("the checkout's skills, commands and rules: %v", err)
	}
	refused := []string{"Allowed-Tools: Bash\n", "permissionMode: bypassPermissions\n", "mcpServers:\n  x:\n    command: sh\n", "allowed_tools: Bash\n", "compatibility: agy\n"}
	skill := func(front string) error {
		repo := newRepo(t)
		writeFile(t, filepath.Join(repo, "skills", "x", "SKILL.md"), "---\nname: x\ndescription: a skill\n"+front+"---\nbody\n")
		_, err := loadSkills(DirSource(repo))
		return err
	}
	doc := func(dir, front string) error {
		repo := newRepo(t)
		writeFile(t, filepath.Join(repo, dir, "x.md"), "---\ndescription: a doc\n"+front+"---\nbody\n")
		_, err := LoadInventory(DirSource(repo))
		return err
	}
	for _, front := range refused {
		key := front[:strings.IndexByte(front, ':')]
		if err := skill(front); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("skill with %s: err = %v, want a refusal naming it", key, err)
		}
		for _, dir := range []string{"commands", "workflows", "rules"} {
			if err := doc(dir, front); err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("%s with %s: err = %v, want a refusal naming it", dir, key, err)
			}
		}
	}
	for _, front := range []string{"license: Apache-2.0\n", "metadata:\n  version: \"1\"\n", "argument-hint: <name>\n", "disable-model-invocation: true\n"} {
		if err := skill(front); err != nil {
			t.Errorf("skill with %q refused: %v", front, err)
		}
	}
	if err := doc("rules", "trigger: always_on\n"); err != nil {
		t.Errorf("rule with trigger refused: %v", err)
	}
	if err := doc("commands", "trigger: always_on\n"); err == nil {
		t.Error("a command with a rule's trigger key loaded")
	}
}

// TestLintRefusesBadDocNames: a command or rule takes its name from its file
// name, which is rendered in backticks into the deployed context and joined
// into a path: it passes validName like a skill's or an agent's (IN25).
func TestLintRefusesBadDocNames(t *testing.T) {
	for _, name := range []string{"a`b", "a\nb", ".hidden", "-dash"} {
		for _, dir := range []string{"commands", "rules"} {
			repo := newRepo(t)
			p := filepath.Join(repo, dir, name+".md")
			if err := os.WriteFile(p, []byte("# x\n"), 0o644); err != nil {
				t.Skipf("this file system refuses %q: %v", name, err)
			}
			if _, err := LoadInventory(DirSource(repo)); err == nil {
				t.Errorf("%s/%q.md loaded", dir, name)
			}
		}
	}
}

// TestDiffWritesNothing: diff renders and compares in memory and pipes the
// plan's bytes to the system diff on stdin; on a home with no state dir it
// creates none, and nothing appears under the system temp (IN24, D8).
func TestDiffWritesNothing(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &out); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(home, AgyRoot, "skills", "alpha", "SKILL.md")
	if err := os.WriteFile(victim, []byte("drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	out.Reset()
	if err := Diff(home, files, &out); err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(out.String(), "-drifted") || !strings.Contains(out.String(), "installed ~/"+filepath.Join(AgyRoot, "skills", "alpha", "SKILL.md")) || !strings.Contains(out.String(), "+  Alpha does things.") {
		t.Errorf("Diff printed no labelled hunk for the drifted file:\n%s", out.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
		t.Errorf("Diff created ~/.config (err %v)", err)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("Diff left %d entries under the system temp: %v", len(left), left)
	}
}

// TestLoadStateKeepsManagedRootsOnly: a manifest entry outside the roots fyr
// manages is dropped when the state is read, so no install, uninstall or
// status ever removes or reads it (IN26, D12).
func TestLoadStateKeepsManagedRootsOnly(t *testing.T) {
	home := t.TempDir()
	keep := []string{".gemini/config/a.md", ".claude/agents/x.md", "./.claude/rules/y.md", ".local/bin/fyr", ".local/bin/sh-agent"}
	drop := []string{".ssh/authorized_keys", ".", ".gemini/config", ".gemini", ".claude", ".gemini/../.ssh/x", ".gemini/config/../../.bashrc", ".local/bin/other", StateDir + "/state.json"}
	if err := SaveState(home, State{Repo: "r", Files: append(append([]string{}, keep...), drop...)}); err != nil {
		t.Fatal(err)
	}
	s, ok, err := LoadState(home)
	if err != nil || !ok {
		t.Fatalf("LoadState: ok=%v err=%v", ok, err)
	}
	if strings.Join(s.Files, ",") != strings.Join(keep, ",") {
		t.Errorf("Files = %v, want %v", s.Files, keep)
	}
}

// TestStatusCountsKeptClaudeFiles: a pre-ADR-062 manifest's ~/.claude entry
// still on disk is kept, never removed (plan fyr D32), so status classes it
// kept, not stale, and it is no drift (IN32).
func TestStatusCountsKeptClaudeFiles(t *testing.T) {
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
	claude := filepath.Join(".claude", "agents", "reviewer.md")
	writeFile(t, filepath.Join(home, claude), "old\n")
	st.Files = append(st.Files, claude)
	if err := SaveState(home, st); err != nil {
		t.Fatal(err)
	}
	r, err := Status(home, files)
	if err != nil {
		t.Fatal(err)
	}
	if r.Drift() || r.Counts["stale"] != 0 || r.Counts["kept"] != 1 {
		t.Errorf("drift=%v counts=%v, want no drift, 0 stale, 1 kept", r.Drift(), r.Counts)
	}
}

// TestReadSumsRefusesTwoMarkers: SHA256SUMS names one release; a file
// carrying two different markers is refused, not read by its first (IN28).
func TestReadSumsRefusesTwoMarkers(t *testing.T) {
	dir := t.TempDir()
	line := strings.Repeat("a", 64) + "  fyr-linux-amd64\n"
	writeFile(t, filepath.Join(dir, "SHA256SUMS"), "# fyr v1.2.3\n"+line+"# fyr v9.9.9\n")
	if _, _, err := readSums(dir, "release v1.2.3"); err == nil || !strings.Contains(err.Error(), "two") {
		t.Errorf("two markers: err = %v, want a refusal", err)
	}
	writeFile(t, filepath.Join(dir, "SHA256SUMS"), "# fyr v1.2.3\n"+line+"# fyr v1.2.3\n")
	if _, tag, err := readSums(dir, "release v1.2.3"); err != nil || tag != "v1.2.3" {
		t.Errorf("one marker twice: tag=%q err=%v, want v1.2.3", tag, err)
	}
}
