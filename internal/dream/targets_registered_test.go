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

// --all sends nothing of a checkout the operator never registered, a clone
// of a registered one included, and says so once per root, in the log and
// in the dry run (ADR-068, audit-choices D15-D17); registering it under this
// home brings it in.
func TestAllSkipsUnregistered(t *testing.T) {
	f := newFixture(t)
	other := newFixture(t) // registered, but under its own home only
	clone := filepath.Join(t.TempDir(), "clone")
	run(t, filepath.Dir(clone), "git", "clone", "-q", f.root, clone)
	rec := fakeAgy(t, `{"proposals":[],"skills":[]}`, 0)
	var out bytes.Buffer
	o := runOpts(f, &out)
	o.Projects = func(string) ([]search.Project, error) {
		return []search.Project{project(other), {Root: clone, Name: "clone"}}, nil
	}
	o.Scopes = func(string) ([]string, error) { return []string{other.root}, nil } // the same root twice: one line

	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rec, "args")); err == nil {
		t.Error("agy was called for unregistered checkouts")
	}
	log, _ := os.ReadFile(filepath.Join(f.home, harness.StateDir, "dream.log"))
	for _, root := range []string{other.root, clone} {
		if n := strings.Count(string(log), root+" skip not registered (fyr index --register)"); n != 1 {
			t.Errorf("%s: %d skip lines, want 1:\n%s", root, n, log)
		}
	}
	if !strings.Contains(string(log), "- run 0 projects") {
		t.Errorf("an unregistered root was counted as dreamt over:\n%s", log)
	}

	o.DryRun = true
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if want := "[dry-run] " + clone + ": skip (" + notRegistered + ")"; !strings.Contains(out.String(), want) {
		t.Errorf("dry run lacks %q:\n%s", want, out.String())
	}

	o.DryRun = false
	markRegistered(t, f.home, other.root)
	g := brain.Options{Home: f.home, Dir: other.root, Now: func() time.Time { return now }}
	if _, err := brain.Add(g, brain.AddInput{Kind: "bug", Title: "the other checkout's bug", Body: strings.NewReader("a bug")}); err != nil {
		t.Fatal(err)
	}
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	stdin, err := os.ReadFile(filepath.Join(rec, "stdin"))
	if err != nil || !strings.Contains(string(stdin), "the other checkout's bug") {
		t.Errorf("the registered checkout was not sent: %v\n%.300s", err, stdin)
	}
}
