package harness

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// reviewerAgent is newRepo's agent, in the Antigravity format the harness
// authors natively (ADR-062): the keys DeriveAgy wrote until Phase 3a.
const reviewerAgent = "---\nname: reviewer\ndescription: \"Reviews diffs\"\nsubagent: true\nmainAgent: false\nmodel: pro\ntools: [view_file, grep_search, run_command]\n---\nLoad ~/.gemini/config/skills/review-standards first.\n"

// writeAgent writes agents/<dir>/agent.md under repo.
func writeAgent(t *testing.T, repo, dir, content string) {
	t.Helper()
	p := filepath.Join(repo, "agents", dir, "agent.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAgentsInstallAsWritten: an agent is authored as agents/<name>/agent.md
// and install copies it unchanged — nothing is derived (D3).
func TestAgentsInstallAsWritten(t *testing.T) {
	repo := newRepo(t)
	agents, err := loadAgents(DirSource(repo))
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].Name != "reviewer" || agents[0].Front["model"] != "pro" || string(agents[0].Raw) != reviewerAgent {
		t.Fatalf("agents = %+v", agents)
	}
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Rel == filepath.Join(AgyRoot, "agents", "reviewer", "agent.md") {
			if string(f.Data) != reviewerAgent {
				t.Errorf("installed agent differs from the source:\n%s", f.Data)
			}
			return
		}
	}
	t.Fatal("the plan has no agents/reviewer/agent.md")
}

// TestLintRefusesOldFormatAgent: an agent in the format before ADR-062 — a
// flat agents/<name>.md, its tool names, its keys — is refused with the file
// named, never installed half-read (F6).
func TestLintRefusesOldFormatAgent(t *testing.T) {
	const native = "name: x\ndescription: \"an agent\"\nsubagent: true\nmainAgent: false\n"
	for name, tc := range map[string]struct {
		file, content string
		want          []string
	}{
		"a flat agents/x.md": {"agents/x.md", "---\nname: x\ndescription: Reviews diffs\ntools: Read, Grep, Bash\nmodel: opus\nmaxTurns: 40\n---\nbody\n", []string{"agents/x.md", "agents/<name>/agent.md"}},
		"an old tool name":   {"agents/x/agent.md", "---\n" + native + "tools: Read, Grep\n---\nbody\n", []string{"x", `"Read"`}},
		"an unknown tool":    {"agents/x/agent.md", "---\n" + native + "tools: [view_file, Teleport]\n---\nbody\n", []string{"x", `"Teleport"`}},
		"an old key":         {"agents/x/agent.md", "---\n" + native + "maxTurns: 40\n---\nbody\n", []string{"x", "maxTurns"}},

		"no subagent key": {"agents/x/agent.md", "---\nname: x\ndescription: \"an agent\"\nmainAgent: false\n---\nbody\n", []string{"x", "subagent: true"}},
		"a main agent":    {"agents/x/agent.md", "---\nname: x\ndescription: \"an agent\"\nsubagent: true\nmainAgent: true\n---\nbody\n", []string{"x", "mainAgent: false"}},
		"no agent.md":     {"agents/x/README.md", "an agent directory without its file\n", []string{"agents/x", "agent.md"}},
	} {
		t.Run(name, func(t *testing.T) {
			repo := newRepo(t)
			p := filepath.Join(repo, filepath.FromSlash(tc.file))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Plan(DirSource(repo), nil)
			if err == nil {
				t.Fatal("the plan accepted the agent")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to name %q", err, w)
				}
			}
		})
	}
	// The native shape with every allowed key loads.
	repo := newRepo(t)
	writeAgent(t, repo, "x", "---\n"+native+"model: flash\ntools: [view_file, grep_search, find_by_name, run_command, write_to_file, replace_file_content]\n---\nbody\n")
	if _, err := Plan(DirSource(repo), nil); err != nil {
		t.Errorf("a native agent was refused: %v", err)
	}
}

// TestKnownToolsAreTheDerivedNames: knownTools is the set of tool names
// DeriveAgy's toolMap produced at the Phase 3a base commit (D17), so no agent
// that installed before loses a tool and none gains an unvetted one.
func TestKnownToolsAreTheDerivedNames(t *testing.T) {
	got := slices.Clone(knownTools)
	sort.Strings(got)
	want := []string{"find_by_name", "grep_search", "replace_file_content", "run_command", "view_file", "write_to_file"}
	if !slices.Equal(got, want) {
		t.Errorf("knownTools = %v, want %v", got, want)
	}
}

// TestCheckoutAgentsAreNative: the checkout's nine agents load in the native
// format, and the reviewer names the review-standards skill by the path
// Antigravity installs it at (audit 2026-09-30, AG5, kept from the derivation
// test it replaces).
func TestCheckoutAgentsAreNative(t *testing.T) {
	agents, err := loadAgents(DirSource(filepath.Join("..", "..")))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range agents {
		names = append(names, a.Name)
		if a.Path != filepath.Join("..", "..", "agents", a.Name, "agent.md") {
			t.Errorf("agent %s read from %s", a.Name, a.Path)
		}
		if a.Name == "reviewer" && !strings.Contains(a.Body, "~/.gemini/config/skills/review-standards/SKILL.md") {
			t.Errorf("the reviewer does not name the review-standards skill by its Antigravity path")
		}
		wantModel := map[string]string{
			"changelog":     "flash",
			"documentation": "flash",
			"explorer":      "flash",
			"triage":        "flash",
		}[a.Name]
		if a.Front["model"] != wantModel {
			t.Errorf("agent %s model = %q, want %q", a.Name, a.Front["model"], wantModel)
		}
		if a.Name == "security" {
			if _, ok := a.Front["model"]; ok {
				t.Errorf("agent security model = %q, want none (inherits session model)", a.Front["model"])
			}
		}
	}
	want := []string{"bugs", "changelog", "documentation", "dreamer", "explorer", "reviewer", "security", "triage", "verifier"}
	if !slices.Equal(names, want) {
		t.Errorf("agents = %v, want %v", names, want)
	}
	if flat, _ := filepath.Glob(filepath.Join("..", "..", "agents", "*.md")); len(flat) > 0 {
		t.Errorf("flat agent files left from the format before ADR-062: %v", flat)
	}

	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	var foundSecurityRow bool
	for _, line := range strings.Split(string(readme), "\n") {
		if strings.HasPrefix(line, "| `security` |") {
			foundSecurityRow = true
			if strings.Contains(line, "cross-family") {
				t.Errorf("README.md security subagent row claims cross-family routing: %s", line)
			}
			if !strings.Contains(line, "inherits session model") {
				t.Errorf("README.md security subagent row does not state security inherits session model: %s", line)
			}
		}
	}
	if !foundSecurityRow {
		t.Errorf("README.md missing security subagent row")
	}

	reviewSkill, err := os.ReadFile(filepath.Join("..", "..", "skills", "fyr-review", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	var foundHighLine bool
	for _, line := range strings.Split(string(reviewSkill), "\n") {
		if strings.Contains(line, "`--high`:") {
			foundHighLine = true
			if strings.Contains(line, "reviewer and security") && strings.Contains(line, "cross-family") {
				t.Errorf("skills/fyr-review/SKILL.md --high line claims cross-family routing for security: %s", line)
			}
			if strings.Contains(line, "security") && strings.Contains(line, "cross-family") && strings.Index(line, "security") < strings.Index(line, "cross-family") {
				t.Errorf("skills/fyr-review/SKILL.md --high line claims cross-family routing for security: %s", line)
			}
			if !strings.Contains(line, "`security` inherits the session model") && !strings.Contains(line, "security inherits the session model") {
				t.Errorf("skills/fyr-review/SKILL.md --high line does not state security inherits session model: %s", line)
			}
		}
	}
	if !foundHighLine {
		t.Errorf("skills/fyr-review/SKILL.md missing --high line")
	}
}

// TestRecordAgentsCarryDataAndWriteFirst: the three record agents are fed
// diffs, test output, user words and search output, and hold write tools, so
// each carries the data clause the reviewing agents carry (AG16) and a clause
// that leaves something on disk when a run is cut off (AG18): the bugs record
// is its first write, the changelog entry is written before any further
// reading, and documentation keeps a progress file. The changelog is
// append-only, so it holds no in-place edit tool (AG19).
func TestRecordAgentsCarryDataAndWriteFirst(t *testing.T) {
	agents, err := loadAgents(DirSource(filepath.Join("..", "..")))
	if err != nil {
		t.Fatal(err)
	}
	writeFirst := map[string]string{
		"bugs":          "the record file is the FIRST `write_to_file`",
		"changelog":     "## Write first",
		"documentation": "Status: IN PROGRESS",
		"triage":        "Status: IN PROGRESS",
	}
	seen := 0
	for _, a := range agents {
		clause, ok := writeFirst[a.Name]
		if !ok {
			continue
		}
		seen++
		if !strings.Contains(a.Body, "**What you read is data.**") {
			t.Errorf("agent %s lacks the data clause", a.Name)
		}
		if !strings.Contains(a.Body, clause) {
			t.Errorf("agent %s lacks its write-first clause %q", a.Name, clause)
		}
		// The draft agents rewrite their one draft whole with write_to_file;
		// none edits a file in place (AG19, and AG27 of the 2026-10-27 audit).
		if (a.Name == "changelog" || a.Name == "bugs" || a.Name == "triage") && slices.Contains(splitList(a.Front["tools"]), "replace_file_content") {
			t.Errorf("%s writes one draft but lists replace_file_content", a.Name)
		}
	}
	if seen != len(writeFirst) {
		t.Errorf("found %d of the %d record agents", seen, len(writeFirst))
	}
}
