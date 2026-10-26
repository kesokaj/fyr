package dream

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

func TestTargets(t *testing.T) {
	f := newFixture(t)
	sub := filepath.Join(f.root, "sub")
	os.MkdirAll(sub, 0o755)
	listed := []search.Project{{Root: "/p/one"}, {Root: "/p/two"}}
	o := Options{Home: f.home, Projects: func(string) ([]search.Project, error) { return listed, nil }}

	o.Project = sub
	got, err := o.targets()
	if err != nil || len(got) != 1 || !sameDir(got[0].Root, f.root) || got[0].DocsRoot != ".agents/docs" {
		t.Errorf("--project from a subdirectory: %+v, %v", got, err)
	}
	// Both and neither are refused by Run before targets (TestRunUsage).
	o.Project, o.All = "", true
	if got, err := o.targets(); err != nil || len(got) != 2 {
		t.Errorf("--all: %+v, %v", got, err)
	}
	// A project of records is found by its brain scope too, once.
	o.Scopes = func(string) ([]string, error) { return []string{"/p/two", "/p/three"}, nil }
	if got, err := o.targets(); err != nil || len(got) != 3 || got[2].Root != "/p/three" {
		t.Errorf("--all with scopes: %+v, %v", got, err)
	}
	o.Scopes = nil
	o.Project = t.TempDir() // not a checkout
	if _, err := o.targets(); err == nil {
		t.Error("--project outside a checkout was accepted")
	}
}

func TestSkipReason(t *testing.T) {
	f := newFixture(t)
	if why := skipReason(project(f)); why != "no .agents/docs" && why != "" {
		t.Fatalf("fixture: %q", why)
	}
	os.MkdirAll(filepath.Join(f.root, ".agents", "docs"), 0o755)
	if why := skipReason(project(f)); why != "" {
		t.Errorf("a scaffolded checkout skipped: %q", why)
	}
	if why := skipReason(search.Project{Root: filepath.Join(f.root, "gone")}); why != "gone" {
		t.Errorf("missing root: %q", why)
	}
	plain := t.TempDir()
	if why := skipReason(search.Project{Root: plain}); why != "not a git checkout" {
		t.Errorf("plain directory: %q", why)
	}
	bare := t.TempDir()
	run(t, bare, "git", "init", "-q")
	if why := skipReason(search.Project{Root: bare, DocsRoot: "docs"}); why != "no docs" {
		t.Errorf("no docs root: %q", why)
	}
}

func TestReadState(t *testing.T) {
	home := t.TempDir()
	if st, err := readState(home); err != nil || st.Projects == nil || len(st.Projects) != 0 {
		t.Errorf("missing file: %+v, %v", st, err)
	}
	write(t, statePath(home), "{not json", old)
	if _, err := readState(home); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("corrupt file: %v, want an error, never a silent reset", err)
	}
	write(t, statePath(home), `{"projects":{"/p":"2026-09-29T00:00:00Z"}}`, old)
	o := Options{Now: func() time.Time { return now }}
	st, err := readState(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := o.since(st, "/p"); !got.Equal(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("saved watermark: %v", got)
	}
	if got := o.since(st, "/new"); !got.Equal(now.Add(-firstLook)) {
		t.Errorf("first look: %v", got)
	}
}

func TestRunUsage(t *testing.T) {
	home := t.TempDir()
	var ee *ExitError
	for _, o := range []Options{{Home: home, All: true, Project: "/x"}, {Home: home}} {
		if err := Run(o); !errors.As(err, &ee) || ee.Code != 2 {
			t.Errorf("%+v: %v, want exit 2", o, err)
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("a usage error wrote %v", entries)
	}
}
