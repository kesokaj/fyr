package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A frontmatter `name:` becomes a path component under ~/.gemini/config
// (plan.go); the parse boundary refuses anything that is not one
// local segment, naming the file, so `../x` or `a/b` never reaches
// filepath.Join (styrman-crew's 2026-09-14 review of the mirror refresh).
func TestSkillAndAgentNamesMustBeOneLocalSegment(t *testing.T) {
	for _, tc := range []struct {
		name string
		bad  bool
	}{
		{"ok-name", false},
		{"../evil", true},
		{"a/b", true},
		{"/abs", true},
		{"..", true},
		{`a\b`, true},
		{"a b", true},
		{"a`b", true},
		{"café", true},
		{".hidden", true},
		{"v1.2_x-y", false},
	} {
		repo := t.TempDir()
		writeFile(t, filepath.Join(repo, "skills", "dir", "SKILL.md"),
			"---\nname: "+tc.name+"\ndescription: \"a skill\"\n---\nbody\n")
		writeFile(t, filepath.Join(repo, "agents", "file.md"),
			"---\nname: "+tc.name+"\ndescription: \"an agent\"\n---\nbody\n")
		_, serr := LoadSkills(DirSource(repo))
		_, aerr := LoadAgents(DirSource(repo))
		if tc.bad {
			if serr == nil || !strings.Contains(serr.Error(), "dir/SKILL.md") {
				t.Errorf("name %q: skills loader err = %v, want a refusal naming dir/SKILL.md", tc.name, serr)
			}
			if aerr == nil || !strings.Contains(aerr.Error(), "file.md") {
				t.Errorf("name %q: agents loader err = %v, want a refusal naming file.md", tc.name, aerr)
			}
			continue
		}
		if serr != nil || aerr != nil {
			t.Errorf("name %q: skills err = %v, agents err = %v, want none", tc.name, serr, aerr)
		}
	}
}

// The saved manifest lives under the user's home and is read back on the
// next install to remove what the new plan no longer contains. An entry
// that is not local to the install root is skipped, never followed: the
// resync removes a stale planned-less file inside the home and leaves a
// `../` entry's target untouched.
func TestResyncNeverFollowsANonLocalManifestEntry(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	stale := filepath.Join(home, AgyRoot, "agents", "old", "agent.md")
	writeFile(t, stale, "stale\n")
	outside := filepath.Join(root, "escape.txt")
	writeFile(t, outside, "must survive\n")
	if err := SaveState(home, State{Repo: "r", Files: []string{".gemini/config/agents/old/agent.md", "../escape.txt"}}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Install(DirSource("r"), home, nil, false, &out); err != nil {
		t.Fatalf("install with an empty plan: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale planned-less file not removed: err = %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("the ../ manifest entry's target was touched: %v\n%s", err, out.String())
	}
}

// A manifest entry that is not local to the install root is dropped when
// the state is READ, so every reader — install's resync, uninstall, status —
// is guarded in one place (security review 2026-09-14).
func TestLoadStateDropsNonLocalManifestEntries(t *testing.T) {
	home := t.TempDir()
	if err := SaveState(home, State{Repo: "r", Files: []string{".gemini/config/a.md", "../escape", "/etc/passwd", "ok/../../x"}}); err != nil {
		t.Fatal(err)
	}
	s, ok, err := LoadState(home)
	if err != nil || !ok {
		t.Fatalf("LoadState: ok=%v err=%v", ok, err)
	}
	if len(s.Files) != 1 || s.Files[0] != ".gemini/config/a.md" {
		t.Fatalf("Files = %v, want only the local entry", s.Files)
	}
}

// A symlink inside a skill directory is refused by name, never followed:
// os.ReadFile would deploy the target's bytes under the link's mode, and a
// link's 0777 becomes a world-writable file in ~/.gemini/config (security review
// 2026-09-14, high). The same refusal guards adopt's copy.
func TestSkillFilesAndAdoptCopiesRefuseSymlinks(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, "skills", "s")
	writeFile(t, filepath.Join(repo, "context", "AGENTS.md"), "# ctx\n")
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: s\ndescription: \"x\"\n---\nbody\n")
	outside := filepath.Join(t.TempDir(), "secret")
	writeFile(t, outside, "not for ~/.gemini/config\n")
	if err := os.Symlink(outside, filepath.Join(dir, "link.md")); err != nil {
		t.Skip("symlinks unavailable here")
	}
	if _, err := Plan(DirSource(repo), nil); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Plan err = %v, want a symlink refusal", err)
	}
	if err := copyTree(dir, t.TempDir()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("copyTree err = %v, want a symlink refusal", err)
	}
}

// context/, agents/, rules/, commands/ and workflows/ are read with
// os.ReadFile, which follows a link and would deploy the TARGET'S bytes into
// every consumer home — and for context/ into the AGENTS.md both CLIs send to
// the model. The skill walk and adopt's copy already refuse links; these
// readers must too (BUG-036).
func TestContextAgentsAndDocsRefuseSymlinks(t *testing.T) {
	valid := map[string]string{
		"context":   "# extra\n\nbytes that must not be deployed\n",
		"agents":    "---\nname: shadow\ndescription: \"an agent\"\n---\nbody\n",
		"rules":     "---\ndescription: \"a rule\"\n---\nbody\n",
		"commands":  "---\ndescription: \"a command\"\n---\nbody\n",
		"workflows": "---\ndescription: \"a workflow\"\n---\nbody\n",
	}
	for _, tc := range []struct{ dir, file string }{
		{"context", "extra.md"},
		{"context", "AGENTS.md"},
		{"agents", "shadow.md"},
		{"rules", "rule.md"},
		{"commands", "cmd.md"},
		{"workflows", "flow.md"},
	} {
		t.Run(tc.dir+"/"+tc.file, func(t *testing.T) {
			repo := t.TempDir()
			writeFile(t, filepath.Join(repo, "context", "AGENTS.md"), "# ctx\n")
			writeFile(t, filepath.Join(repo, "skills", "s", "SKILL.md"), "---\nname: s\ndescription: \"x\"\n---\nbody\n")
			outside := filepath.Join(t.TempDir(), "target.md")
			writeFile(t, outside, valid[tc.dir])
			link := filepath.Join(repo, tc.dir, tc.file)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			os.Remove(link)
			if err := os.Symlink(outside, link); err != nil {
				t.Skip("symlinks unavailable here")
			}
			_, err := Plan(DirSource(repo), nil)
			if err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Errorf("Plan err = %v, want a symlink refusal naming %s", err, link)
			}
		})
	}
}

// A symlinked skills/<name>/SKILL.md is refused where it is read: plan.go's
// walk only visits skills LoadSkills returned, so swallowing the read error
// would drop the skill silently instead of refusing the link (BUG-036).
func TestSymlinkedSkillMdIsRefused(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "context", "AGENTS.md"), "# ctx\n")
	dir := filepath.Join(repo, "skills", "s")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "SKILL.md")
	writeFile(t, outside, "---\nname: s\ndescription: \"x\"\n---\nbody\n")
	if err := os.Symlink(outside, filepath.Join(dir, "SKILL.md")); err != nil {
		t.Skip("symlinks unavailable here")
	}
	if _, err := LoadSkills(DirSource(repo)); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("LoadSkills err = %v, want a symlink refusal", err)
	}
	if _, err := Plan(DirSource(repo), nil); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("Plan err = %v, want a symlink refusal", err)
	}
}

// A directory under skills/ with no SKILL.md is not a skill and is skipped,
// which the refusal above must not turn into an error.
func TestSkillDirWithoutSkillMdIsSkipped(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "context", "AGENTS.md"), "# ctx\n")
	writeFile(t, filepath.Join(repo, "skills", "real", "SKILL.md"), "---\nname: real\ndescription: \"x\"\n---\nbody\n")
	if err := os.MkdirAll(filepath.Join(repo, "skills", "notaskill"), 0o755); err != nil {
		t.Fatal(err)
	}
	skills, err := LoadSkills(DirSource(repo))
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "real" {
		t.Errorf("skills = %+v, want only real", skills)
	}
}

// A managed directory that is itself a symlink is refused too: os.DirFS, like
// os.ReadDir and filepath.Glob before it, follows a directory link, so guarding the leaf files alone let
// the same payload through one path segment up (BUG-036, review round 2).
func TestManagedDirsRefuseSymlinks(t *testing.T) {
	skillMD := "---\nname: s\ndescription: \"x\"\n---\nbody\n"
	content := map[string]string{
		"context":   "# ctx\n",
		"agents":    "---\nname: shadow\ndescription: \"an agent\"\n---\nbody\n",
		"rules":     "---\ndescription: \"a rule\"\n---\nbody\n",
		"commands":  "---\ndescription: \"a command\"\n---\nbody\n",
		"workflows": "---\ndescription: \"a workflow\"\n---\nbody\n",
	}
	for _, dir := range []string{"context", "agents", "rules", "commands", "workflows", "skills"} {
		t.Run(dir, func(t *testing.T) {
			repo := t.TempDir()
			outside := t.TempDir()
			switch dir {
			case "context":
				writeFile(t, filepath.Join(outside, "AGENTS.md"), content["context"])
				writeFile(t, filepath.Join(repo, "skills", "s", "SKILL.md"), skillMD)
			case "skills":
				writeFile(t, filepath.Join(outside, "s", "SKILL.md"), skillMD)
				writeFile(t, filepath.Join(repo, "context", "AGENTS.md"), content["context"])
			default:
				writeFile(t, filepath.Join(outside, "x.md"), content[dir])
				writeFile(t, filepath.Join(repo, "context", "AGENTS.md"), content["context"])
				writeFile(t, filepath.Join(repo, "skills", "s", "SKILL.md"), skillMD)
			}
			if err := os.Symlink(outside, filepath.Join(repo, dir)); err != nil {
				t.Skip("symlinks unavailable here")
			}
			if _, err := Plan(DirSource(repo), nil); err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Errorf("Plan err = %v, want a symlink refusal naming %s", err, filepath.Join(repo, dir))
			}
		})
	}
}

// binAt writes an executable named sh-agent into dir and returns its path.
func binAt(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "sh-agent")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func joinPath(dirs ...string) string { return strings.Join(dirs, string(os.PathListSeparator)) }

// A binary correctly installed to ~/.local/bin is worthless when an earlier
// PATH entry holds another one; nothing said so, and a verified release binary
// was silently ignored on two machines (BUG-078). Real files, not a fake
// resolver: identity is os.SameFile, which a fake cannot stand in for.
func TestPathShadows(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	binDir := filepath.Join(home, ".local", "bin")
	bin := binAt(t, binDir)
	a, b, c := filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c")
	aBin, bBin := binAt(t, a), binAt(t, b)
	binAt(t, c)
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Mkdir(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(linkDir, "sh-agent")); err != nil {
		t.Fatal(err)
	}
	hardDir := filepath.Join(root, "hard")
	if err := os.Mkdir(hardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(bin, filepath.Join(hardDir, "sh-agent")); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(root, "bin-alias") // ~/bin -> ~/.local/bin, the round-2 repro
	if err := os.Symlink(binDir, dirLink); err != nil {
		t.Fatal(err)
	}
	plainDir := filepath.Join(root, "plain")
	if err := os.Mkdir(plainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plainDir, "sh-agent"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		path   string
		want   []string
		onPath bool
	}{
		{"installed dir first — nothing shadows it", joinPath(binDir, a, b), nil, true},
		{"an earlier entry wins", joinPath(a, binDir), []string{aBin}, true},
		{"every earlier entry is named, in PATH order; later ones are not", joinPath(a, b, binDir, c), []string{aBin, bBin}, true},
		{"a symlink to the installed binary is not a shadow", joinPath(linkDir, binDir), nil, true},
		{"a hard link to the installed binary is not a shadow", joinPath(hardDir, binDir), nil, true},
		{"a repeated PATH entry is reported once", joinPath(a, a, a, binDir), []string{aBin}, true},
		{"a directory that holds no sh-agent is skipped", joinPath(empty, a, binDir), []string{aBin}, true},
		{"a file with no execute bit is not a binary", joinPath(plainDir, a, binDir), []string{aBin}, true},
		{"the installed dir absent from PATH: every candidate wins, onPath false", joinPath(a, b), []string{aBin, bBin}, false},
		{"a trailing slash on the installed dir still matches", joinPath(binDir+"/", a), nil, true},
		// Round 1's repro: a lexical "on PATH" and the file identity disagreed here.
		{"a double trailing slash on the installed dir still matches", joinPath(a, binDir+"//", b), []string{aBin}, true},
		{"a dot segment in the installed dir still matches", joinPath(binDir+"/.", a), nil, true},
		// Round 2's repro: PATH reaches the bin dir only through a symlinked directory.
		{"a symlinked directory that resolves to the bin dir: reached, and an earlier shadow is still named", joinPath(a, dirLink, b), []string{aBin}, true},
		{"a symlinked directory first: nothing shadows", joinPath(dirLink, a), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, onPath := PathShadows(tc.path, bin)
			if onPath != tc.onPath {
				t.Fatalf("onPath = %v, want %v", onPath, tc.onPath)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("shadows = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("shadows = %v, want %v", got, tc.want)
				}
			}
		})
	}

	t.Run("an installed binary that does not exist: nothing reaches it, every candidate is a shadow", func(t *testing.T) {
		missingDir := filepath.Join(root, "nowhere", ".local", "bin")
		got, onPath := PathShadows(joinPath(a, missingDir), filepath.Join(missingDir, "sh-agent"))
		if onPath || len(got) != 1 || got[0] != aBin {
			t.Fatalf("shadows = %v, onPath = %v; want [%s], false", got, onPath, aBin)
		}
	})

	t.Run("an empty PATH entry is the working directory and prints as ./sh-agent", func(t *testing.T) {
		t.Chdir(a)
		got, onPath := PathShadows(joinPath("", binDir), bin)
		if !onPath || len(got) != 1 || got[0] != "./sh-agent" {
			t.Fatalf("shadows = %v, onPath = %v; want [./sh-agent], true", got, onPath)
		}
	})
}

// executableAt is what PathShadows calls a binary; it is only as honest as this.
func TestExecutableAt(t *testing.T) {
	dir := t.TempDir()
	exe := binAt(t, dir)
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "gone"), dangling); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	exeInfo, ok := executableAt(exe)
	if !ok {
		t.Fatal("an executable file was not recognised")
	}
	if info, ok := executableAt(link); !ok || !os.SameFile(info, exeInfo) {
		t.Errorf("a symlink must resolve to the same file as its target: ok=%v", ok)
	}
	for _, tc := range []struct {
		name string
		path string
	}{
		{"a file with no execute bit", plain},
		{"a dangling symlink", dangling},
		{"a directory", sub},
		{"a missing path", filepath.Join(dir, "nope")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := executableAt(tc.path); ok {
				t.Errorf("executableAt(%s) = ok; want not an executable", tc.path)
			}
		})
	}
}

// `update` replaces ~/.local/bin/sh-agent and nothing else; a process running
// from another path a shell can reach by name has updated a file it is not
// (BUG-079).
func TestRunningElsewhere(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "home", ".local", "bin")
	installed := binAt(t, binDir)
	sysDir := filepath.Join(root, "usr-local-bin")
	sysBin := binAt(t, sysDir) // a different file, the devbox shape
	linkDir := filepath.Join(root, "link")
	if err := os.Mkdir(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(installed, filepath.Join(linkDir, "sh-agent")); err != nil {
		t.Fatal(err)
	}
	hidden := binAt(t, filepath.Join(root, "not-on-path"))
	// PATH reaches the running system copy only through a link under another
	// name — /opt/devbox/bin/sh-agent -> /usr/local/bin/sh-agent on devbox —
	// so the spelling a shell uses is not the spelling the process runs from.
	aliasDir := filepath.Join(root, "alias")
	if err := os.Mkdir(aliasDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sysBin, filepath.Join(aliasDir, "sh-agent")); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
		exe  string
		want string
		ok   bool
	}{
		{"running the installed file itself", joinPath(binDir, sysDir), installed, "", false},
		{"running a link to the installed file", joinPath(linkDir), filepath.Join(linkDir, "sh-agent"), "", false},
		{"running a system copy a shell reaches by name", joinPath(binDir, sysDir), sysBin, sysBin, true},
		{"reported at the PATH entry, whichever order", joinPath(sysDir, binDir), sysBin, sysBin, true},
		{"running a file no PATH entry reaches: nobody's sh-agent", joinPath(binDir, sysDir), hidden, "", false},
		{"reached only through a link: reported at the PATH entry, not the exe path", joinPath(binDir, aliasDir), sysBin, filepath.Join(aliasDir, "sh-agent"), true},
		{"installed file missing: nothing to be behind, silent", joinPath(sysDir), sysBin, "", false},
		{"exe missing", joinPath(binDir), filepath.Join(root, "gone"), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binPath := installed
			if tc.name == "installed file missing: nothing to be behind, silent" {
				binPath = filepath.Join(root, "nowhere", "sh-agent")
			}
			got, ok := RunningElsewhere(tc.path, binPath, tc.exe)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("RunningElsewhere = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
