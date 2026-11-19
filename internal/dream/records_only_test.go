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

// A project with no trail records is not skipped (ADR-524 drops noTrail),
// though file-era trees still contribute nothing on their own.
func TestSkipReasonAllowsUnmigrated(t *testing.T) {
	bare := t.TempDir()
	run(t, bare, "git", "init", "-q")
	if why := absent(projectAt(bare)); why != "" {
		t.Errorf("no trail: %q, want empty", why)
	}
	write(t, filepath.Join(bare, ".agents", "docs", "bugs", "BUG-001.md"), "a bug\n", fresh)
	if why := absent(projectAt(bare)); why != "" {
		t.Errorf("a file-era tree: %q, want empty", why)
	}
}

// A file-era project with no records or conversations is unchanged (no CLI
// call), while one with a conversation sends only the conversation and never
// reads .agents/docs/ files.
func TestRunUnmigratedFileEraProject(t *testing.T) {
	f := newFixture(t)
	rec := fakeCLI(t, "agy", agyResult(`{"proposals":[],"skills":[]}`, false), 0)
	bare := t.TempDir()
	run(t, bare, "git", "init", "-q")
	write(t, filepath.Join(bare, ".agents", "docs", "bugs", "BUG-001.md"), "file-era bug body\n", fresh)
	var out bytes.Buffer
	o := runOpts(f, &out)
	o.All, o.Project = false, bare
	if err := Run(o); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if log := read(t, filepath.Join(f.home, harness.StateDir, "dream.log")); !strings.Contains(log, bare+" unchanged") {
		t.Errorf("expected unchanged log line:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(rec, "args")); !os.IsNotExist(err) {
		t.Error("an unchanged project without records or conversations reached the CLI")
	}

	agyLogin(t)
	agyConversations(t, f.home, map[string]agyConv{
		"bare-conv": {bare, fresh, []agyStep{{typ: 14, text: "hello from bare workspace today"}}},
	})
	if err := Run(o); err != nil {
		t.Fatalf("Run with conv: %v\n%s", err, out.String())
	}
	stdin := read(t, filepath.Join(rec, "stdin"))
	if !strings.Contains(stdin, "hello from bare workspace today") || strings.Contains(stdin, "file-era bug body") {
		t.Errorf("unexpected prompt for unmigrated workspace:\n%s", stdin)
	}
}
