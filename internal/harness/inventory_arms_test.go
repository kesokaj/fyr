package harness

import (
	"path/filepath"
	"strings"
	"testing"
)

// The arms of loadSkills and loadAgents that their splits move into helpers,
// pinned before the splits (PLAN-2026-10-27-funclen Phase 5, D13).

// A SKILL.md with no frontmatter is refused for that, not for the missing
// description its empty frontmatter would also lack; a skill whose
// frontmatter names none takes its directory's name.
func TestLoadSkillsFrontmatterArms(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "skills", "gamma", "SKILL.md"), "no fence here\n")
	if _, err := loadSkills(DirSource(repo)); err == nil || !strings.Contains(err.Error(), "skill gamma: no YAML frontmatter block") {
		t.Errorf("err = %v, want the missing frontmatter named", err)
	}

	repo = newRepo(t)
	writeFile(t, filepath.Join(repo, "skills", "gamma", "SKILL.md"), "---\ndescription: unnamed\n---\nbody\n")
	skills, err := loadSkills(DirSource(repo))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range skills {
		names = append(names, s.Name+"="+s.Dir)
	}
	if got := strings.Join(names, " "); got != "alpha=skills/alpha beta=skills/beta gamma=skills/gamma" {
		t.Errorf("skills = %s", got)
	}
}

// The agent arms: a .DS_Store beside the agent directories is skipped, an
// agent.md with no frontmatter is refused for that, one without a name takes
// its directory's, and one without a description is refused.
func TestLoadAgentsFrontmatterArms(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "agents", ".DS_Store"), "finder\n")
	unnamed := strings.Replace(reviewerAgent, "name: reviewer\n", "", 1)
	writeAgent(t, repo, "critic", unnamed)
	agents, err := loadAgents(DirSource(repo))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range agents {
		names = append(names, a.Name)
	}
	if got := strings.Join(names, " "); got != "critic reviewer" {
		t.Errorf("agents = %s", got)
	}

	for _, c := range []struct{ name, content, want string }{
		{"no frontmatter", "no fence here\n", "agent critic/agent.md: no YAML frontmatter block"},
		{"no description", strings.Replace(reviewerAgent, "description: \"Reviews diffs\"\n", "", 1), "agent reviewer: no description"},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo := newRepo(t)
			writeAgent(t, repo, "critic", c.content)
			if _, err := loadAgents(DirSource(repo)); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}
