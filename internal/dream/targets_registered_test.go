package dream

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

// --all discovers and dreams over unregistered checkouts and clones alike,
// deduplicating each checkout once even when listed in both Projects and Scopes
// (ADR-524 retiring ADR-068's registration gate for dream).
func TestAllIncludesUnregistered(t *testing.T) {
	f := newFixture(t)
	other := newFixture(t) // registered only under its own home, not f.home
	clone := filepath.Join(t.TempDir(), "clone")
	run(t, filepath.Dir(clone), "git", "clone", "-q", f.root, clone)
	g := brain.Options{Home: f.home, Dir: other.root, Now: func() time.Time { return now }}
	if _, err := brain.Add(g, brain.AddInput{Kind: "bug", Title: "the other checkout's bug", Body: strings.NewReader("a bug")}); err != nil {
		t.Fatal(err)
	}
	rec := fakeAgy(t, `{"proposals":[],"skills":[]}`, 0)
	var out bytes.Buffer
	o := runOpts(f, &out)
	o.Projects = func(string) ([]search.Project, error) {
		return []search.Project{project(other), {Root: clone, Name: "clone"}}, nil
	}
	o.Scopes = func(string) ([]string, error) { return []string{other.root}, nil }

	o.DryRun = true
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "not registered") {
		t.Errorf("dry run still gated on registration:\n%s", out.String())
	}
	if !strings.Contains(out.String(), other.root+": would dream") {
		t.Errorf("dry run skipped unregistered checkout:\n%s", out.String())
	}

	o.DryRun = false
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	stdin, err := os.ReadFile(filepath.Join(rec, "stdin"))
	if err != nil || !strings.Contains(string(stdin), "the other checkout's bug") {
		t.Errorf("the unregistered checkout was not sent: %v\n%.300s", err, stdin)
	}
	log, _ := os.ReadFile(filepath.Join(f.home, harness.StateDir, "dream.log"))
	if n := strings.Count(string(log), other.root+" ok "); n != 1 {
		t.Errorf("%s: %d ok lines, want 1:\n%s", other.root, n, log)
	}
}
