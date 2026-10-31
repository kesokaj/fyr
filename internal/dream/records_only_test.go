package dream

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// A dream reads records only (item 33, D4): a project's old files and the
// old feedback log beside its records reach no prompt and change nothing.
func TestDreamReadsOnlyRecords(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.root, ".agents", "memory", "old-store.md"), "an old memory store file\n", fresh)
	write(t, filepath.Join(f.root, ".agents", "docs", "changes", "2026-10-01-0000-x.md"), "an old trail file\n", fresh)
	line := `{"ts":"` + fresh.Format(time.RFC3339) + `","project":"` + f.root + `","original":"an old log line"}` + "\n"
	write(t, filepath.Join(f.home, harness.StateDir, "feedback.jsonl"), line, fresh)
	c, err := collect(f.home, project(f), since, time.Now(), maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	for _, not := range []string{"an old memory store file", "an old trail file", "an old log line"} {
		if strings.Contains(c.Prompt, not) {
			t.Errorf("prompt carries %q", not)
		}
	}
	if !strings.Contains(c.Prompt, "fresh bug row") || !strings.Contains(c.Prompt, "fresh feedback for this project") {
		t.Errorf("the records are gone:\n%s", labels(c))
	}
}

// A project with no trail is skipped and told how to migrate, a file-era
// tree or not: only `fyr record import` reads that tree now.
func TestSkipReasonNamesTheImport(t *testing.T) {
	const want = "no .agents/trail.jsonl: fyr record import"
	bare := t.TempDir()
	run(t, bare, "git", "init", "-q")
	if why := skipReason(projectAt(bare)); why != want {
		t.Errorf("no trail: %q, want %q", why, want)
	}
	write(t, filepath.Join(bare, ".agents", "docs", "bugs", "BUG-001.md"), "a bug\n", fresh)
	if why := skipReason(projectAt(bare)); why != want {
		t.Errorf("a file-era tree: %q, want %q", why, want)
	}
}

// A file-era project is skipped by a run: no CLI call.
func TestRunSkipsAFileEraProject(t *testing.T) {
	f := newFixture(t)
	rec := fakeCLI(t, "agy", canned, 0)
	bare := t.TempDir()
	run(t, bare, "git", "init", "-q")
	write(t, filepath.Join(bare, ".agents", "docs", "bugs", "BUG-001.md"), "a bug\n", fresh)
	var out bytes.Buffer
	o := runOpts(f, &out)
	o.All, o.Project = false, bare
	if err := Run(o); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if log := read(t, filepath.Join(f.home, harness.StateDir, "dream.log")); !strings.Contains(log, " skip "+noTrail) {
		t.Errorf("no skip line:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(rec, "args")); !os.IsNotExist(err) {
		t.Error("a project with no trail reached the CLI")
	}
}
