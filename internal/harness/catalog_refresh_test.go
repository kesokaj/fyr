package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRefreshCatalogScript runs the refresh against two local fixture
// repositories: the happy path, idempotence, and one fixture per refusal
// class, each of which must exit 1, name the entry, and leave the destination
// byte-identical. It never reaches the network.
func TestRefreshCatalogScript(t *testing.T) {
	r := newRefreshRig(t)
	r.happyPath()
	r.relativeDestWork()
	r.missingEntry()
	r.bystander()
	r.bareChecksums()
	r.destGuard()
	r.guardPlaces()
	r.guardFooting()
	r.workGuard()
	r.workAfterFailure()
	r.overrideShapes()
	r.cloneByRole()
	r.gitChannels()
	r.fetchByRole()
	r.homeAndPin()
	r.manifestExclusion()
	r.pinRefusals()
	r.entryRefusals()
	r.escapedNames()
	r.excludeOneName()
	r.moreRefusals()
}

// refreshRig is TestRefreshCatalogScript's fixture: the checkout and the
// script, the two fixture sources and their pins, the destination and work
// directory every section shares, the good catalog's snapshot and the
// fixture HOME. Its sections run in order and depend on it (D6).
type refreshRig struct {
	t                                              *testing.T
	repo, script, tmp, agents, gskills, dest, work string
	pinA, pinG, good, fakeHome                     string
}

// newRefreshRig makes the fixture, after the guards in their order: the
// script, -short, the midnight guard (BUG-159), the tools (D18).
func newRefreshRig(t *testing.T) *refreshRig {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repo, "skills", "google-catalog", "scripts", "refresh-catalog.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("no refresh-catalog.sh: %v", err)
	}
	if testing.Short() {
		t.Skip("runs git and a nested go test")
	}
	// The manifest carries the day's date, and the test compares the catalog
	// across runs of the script: a test that crosses midnight would compare
	// two dates and fail for that alone (seen once, 2026-10-02 00:00). Wait
	// the last two minutes of a day out.
	if now := time.Now(); now.Hour() == 23 && now.Minute() >= 58 {
		midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 2, 0, now.Location())
		t.Logf("waiting %s for midnight to pass", time.Until(midnight).Round(time.Second))
		time.Sleep(time.Until(midnight))
	}
	for _, tool := range []string{"bash", "git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	tmp := t.TempDir()
	r := &refreshRig{t: t, repo: repo, script: script, tmp: tmp}
	agents, gskills := filepath.Join(tmp, "agents-cli"), filepath.Join(tmp, "google-skills")
	for _, d := range []string{agents, gskills} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		r.git(d, "init", "-q")
	}
	r.put(agents, map[string]string{
		"LICENSE":               "Apache-2.0 (fixture)\n",
		"skills/alpha/SKILL.md": fixtureSkill("alpha", "description: plain one-line description of alpha\n"),
		"extensions/langchain/template/.agents/skills/beta/SKILL.md": fixtureSkill("beta", "description: \"a quoted description: with a colon\"\n"),
	})
	pinA := r.commit(agents)
	r.put(gskills, map[string]string{
		"LICENSE":                              "Apache-2.0 (fixture)\n",
		"skills/cloud/gamma/SKILL.md":          fixtureSkill("gamma", "description: >-\n  a block scalar description\n  folded over two lines\n"),
		"skills/cloud/gamma/scripts/run.sh":    "#!/bin/sh\necho hi\n",
		"skills/cloud/delta/SKILL.md":          fixtureSkill("delta", "metadata:\n  version: \"1.0\"\ndescription: delta with a metadata map\n"),
		"skills/developers/epsilon/SKILL.md":   fixtureSkill("epsilon", "description: epsilon\n"),
		"skills/ads/not-asked-for/SKILL.md":    fixtureSkill("not-asked-for", "description: outside the four source sets\n"),
		"skills/cloud/gamma/references/ref.md": "reference text\n",
	})
	if err := os.Chmod(filepath.Join(gskills, "skills/cloud/gamma/scripts/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	pinG := r.commit(gskills)

	dest, work := filepath.Join(tmp, "dest"), filepath.Join(tmp, "work")
	r.agents, r.gskills, r.pinA, r.pinG, r.dest, r.work = agents, gskills, pinA, pinG, dest, work
	return r
}

// fixtureSkill is a SKILL.md for name with the given description lines.
func fixtureSkill(name, description string) string {
	return "---\nname: " + name + "\n" + description + "---\n\n# " + name + "\n"
}
