package harness

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// A Source with no Dir is the shape of the files embedded in a release
// binary (ADR-033): no disk path, no links, every file mode 0444. These
// tests read the harness from one, so the embedded install is held to what
// a checkout install already does.

func mapSource(files map[string]string) Source {
	m := fstest.MapFS{}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body), Mode: 0o444}
	}
	return Source{FS: m}
}

func fsHarness() map[string]string {
	return map[string]string{
		"context/AGENTS.md":                "# AGENTS.md\n\nBe honest.\n",
		"context/dev-setup.md":             "## Environment\n",
		"skills/alpha/SKILL.md":            "---\nname: alpha\ndescription: \"Alpha does things\"\n---\nbody\n",
		"skills/alpha/references/notes.md": "notes\n",
		"skills/alpha/assets/.gitkeep":     "",
		"skills/VENDORED.md":               "not a skill\n",
		"skills/notaskill/README.md":       "no SKILL.md here\n",
		"agents/reviewer/agent.md":         "---\nname: reviewer\ndescription: \"Reviews diffs\"\nsubagent: true\nmainAgent: false\ntools: [view_file, grep_search]\n---\nbody\n",
		"commands/deploy.md":               "---\ndescription: Deploy the thing\n---\nSteps\n",
		"workflows/ship.md":                "# Ship it\n",
		"rules/go-style.md":                "# Go style\n",
	}
}

func TestPlanFromFSSource(t *testing.T) {
	files, err := Plan(mapSource(fsHarness()), nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var got []string
	byRel := map[string]File{}
	for _, f := range files {
		got = append(got, f.Rel)
		byRel[f.Rel] = f
	}
	want := []string{
		".gemini/config/agents/reviewer/agent.md",
		".gemini/config/rules/AGENTS.md",
		".gemini/config/rules/go-style.md",
		".gemini/config/skills/alpha/SKILL.md",
		".gemini/config/skills/alpha/assets/.gitkeep",
		".gemini/config/skills/alpha/references/notes.md",
		".gemini/config/workflows/deploy.md",
		".gemini/config/workflows/ship.md",
	}
	for i := range want {
		want[i] = filepath.FromSlash(want[i])
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("planned files:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	notes := byRel[filepath.FromSlash(".gemini/config/skills/alpha/references/notes.md")]
	if string(notes.Data) != "notes\n" {
		t.Errorf("notes.md data = %q", notes.Data)
	}
	// embed reports every file 0444; the deployed copy is owner-writable, the
	// same 0644 a checkout's 0644 file deploys as.
	if notes.Mode != 0o644 {
		t.Errorf("notes.md mode = %v, want 0644", notes.Mode)
	}
	ctx := string(byRel[filepath.FromSlash(".gemini/config/rules/AGENTS.md")].Data)
	for _, part := range []string{Marker, "Be honest.", "## Environment", "Skills (1): `alpha`", "Subagents (1): `reviewer`\n", "`/deploy`", "`go-style`"} {
		if !strings.Contains(ctx, part) {
			t.Errorf("rendered context lacks %q", part)
		}
	}
}

// Every refusal a checkout install makes, an install from an FS source makes
// too, naming the file by its path inside the source.
func TestFSSourceRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change map[string]string // "" deletes the file
		want   []string
	}{
		{"no context", map[string]string{"context/AGENTS.md": ""}, []string{"reading context"}},
		{"skill without description", map[string]string{"skills/alpha/SKILL.md": "---\nname: alpha\n---\nbody\n"}, []string{"alpha", "no description"}},
		{"secret-shaped file", map[string]string{"skills/alpha/.env": "TOKEN=x\n"}, []string{"skills/alpha/.env", "secret-shaped"}},
		{"duplicate skill name", map[string]string{"skills/alpha-copy/SKILL.md": "---\nname: alpha\ndescription: \"again\"\n---\nbody\n"}, []string{"alpha-copy", "two skills"}},
		{"bad agent name", map[string]string{"agents/reviewer/agent.md": "---\nname: ../x\ndescription: \"x\"\nsubagent: true\nmainAgent: false\n---\nbody\n"}, []string{"reviewer/agent.md", "single local path segment"}},
		{"unquoted hash", map[string]string{"commands/deploy.md": "---\ndescription: deploy #now\n---\nbody\n"}, []string{"quote the value"}},
		{"unknown tool", map[string]string{"agents/reviewer/agent.md": "---\nname: reviewer\ndescription: \"x\"\nsubagent: true\nmainAgent: false\ntools: Teleport\n---\nbody\n"}, []string{"Teleport"}},
		{"flat agent file", map[string]string{"agents/old.md": "---\nname: old\ndescription: \"x\"\ntools: Read\n---\nbody\n"}, []string{"agents/old.md", "agents/<name>/agent.md"}},
		{"command in both dirs", map[string]string{"workflows/deploy.md": "# again\n"}, []string{"commands/deploy.md", "workflows/deploy.md"}},
		{"rule named AGENTS", map[string]string{"rules/AGENTS.md": "# no\n"}, []string{"AGENTS.md"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := fsHarness()
			for k, v := range tc.change {
				if v == "" {
					delete(files, k)
				} else {
					files[k] = v
				}
			}
			_, err := Plan(mapSource(files), nil)
			if err == nil {
				t.Fatalf("Plan succeeded, want an error naming %q", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to contain %q", err, w)
				}
			}
		})
	}
}

// A directory named SKILL.md is not a regular file, from an FS source as from
// a checkout.
func TestFSSourceSkillMdMustBeRegular(t *testing.T) {
	m := fstest.MapFS{}
	for name, body := range fsHarness() {
		m[name] = &fstest.MapFile{Data: []byte(body), Mode: 0o444}
	}
	delete(m, "skills/alpha/SKILL.md")
	m["skills/alpha/SKILL.md/inner"] = &fstest.MapFile{Data: []byte("x"), Mode: 0o444}
	_, err := LoadSkills(Source{FS: m})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("LoadSkills err = %v, want a not-a-regular-file refusal", err)
	}
}

// DirSource reads the tree through os.DirFS and keeps the path for the link
// checks, so the embedded and on-disk readers are one code path.
func TestDirSourceReadsTheSameAsFS(t *testing.T) {
	repo := t.TempDir()
	for name, body := range fsHarness() {
		writeFile(t, filepath.Join(repo, filepath.FromSlash(name)), body)
	}
	fromDir, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatalf("Plan(DirSource): %v", err)
	}
	fromFS, err := Plan(mapSource(fsHarness()), nil)
	if err != nil {
		t.Fatalf("Plan(map): %v", err)
	}
	if len(fromDir) != len(fromFS) {
		t.Fatalf("dir source plans %d files, fs source %d", len(fromDir), len(fromFS))
	}
	for i := range fromDir {
		a, b := fromDir[i], fromFS[i]
		if a.Rel != b.Rel || string(a.Data) != string(b.Data) || a.Mode != b.Mode {
			t.Errorf("file %d: dir %s %v, fs %s %v", i, a.Rel, a.Mode, b.Rel, b.Mode)
		}
	}
	var _ fs.FS = DirSource(repo).FS
}

// skills/<name> may be a link — `npx skills add -g` leaves one — and a
// DirSource follows it to the skill's files, as the path-based reader did by
// resolving it; a link that points nowhere is refused, never skipped.
func TestDirSourceFollowsLinkedSkillDir(t *testing.T) {
	repo := t.TempDir()
	for name, body := range fsHarness() {
		writeFile(t, filepath.Join(repo, filepath.FromSlash(name)), body)
	}
	outside := filepath.Join(t.TempDir(), "gamma")
	writeFile(t, filepath.Join(outside, "SKILL.md"), "---\nname: gamma\ndescription: \"linked\"\n---\nbody\n")
	writeFile(t, filepath.Join(outside, "references", "g.md"), "g\n")
	if err := os.Symlink(outside, filepath.Join(repo, "skills", "gamma")); err != nil {
		t.Skip("symlinks unavailable here")
	}
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	found := false
	for _, f := range files {
		if f.Rel == filepath.Join(AgyRoot, "skills", "gamma", "references", "g.md") && string(f.Data) == "g\n" {
			found = true
		}
	}
	if !found {
		t.Errorf("the linked skill's references/g.md is not planned")
	}

	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), filepath.Join(repo, "skills", "dangling")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSkills(DirSource(repo)); err == nil || !strings.Contains(err.Error(), "dangling") {
		t.Errorf("LoadSkills err = %v, want a refusal naming the dangling link", err)
	}
}

// An install from the embedded files records the release, not a path, with
// no commit — no git command runs for it (D17).
func TestEmbeddedInstallState(t *testing.T) {
	src := mapSource(fsHarness())
	src.Version = "v1.2.3"
	home := t.TempDir()
	files, err := Plan(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(src, home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	st, found, err := LoadState(home)
	if err != nil || !found {
		t.Fatalf("LoadState: %v %v", found, err)
	}
	if st.Repo != "embedded:v1.2.3" || st.Commit != "" {
		t.Errorf("state repo %q commit %q, want embedded:v1.2.3 and no commit", st.Repo, st.Commit)
	}
}
