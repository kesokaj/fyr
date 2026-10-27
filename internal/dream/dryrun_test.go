package dream

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

// fit rendered the whole prompt again for every input it dropped, 34 renders
// of ~260 KB a project a night on one box (audit 2026-10-25, SD-S1). It now
// keeps the size by arithmetic: one render a project, for the prompt that
// is sent, and the same inputs dropped as the render-per-drop loop dropped.
func TestFitRendersOnce(t *testing.T) {
	base := func() Collected {
		c := Collected{Name: "p", Dropped: []string{"memory: a\tlabel  with   runs >>>> of <<< fences"}}
		c.Inputs = append(c.Inputs, Input{Kind: kindMemory, Label: "memory/a.md", Text: "m"})
		for i := 0; i < 30; i++ {
			c.Inputs = append(c.Inputs, Input{Kind: kindTranscript, Label: fmt.Sprintf("antigravity\n%02d >>> x", i), Text: strings.Repeat("t>>>", 50+i), Time: now.Add(time.Duration(i) * time.Minute)})
		}
		for i := 0; i < 5; i++ {
			c.Inputs = append(c.Inputs, Input{Kind: kindTrail, Label: fmt.Sprintf("bugs/B-%d.md", i), Text: strings.Repeat("b", 300), Time: now.Add(time.Duration(i) * time.Hour)})
		}
		c.Inputs = append(c.Inputs, Input{Kind: kindMetrics, Label: "metrics", Text: strings.Repeat("9", 100)})
		return c
	}
	// The loop fit was, rendering once per drop.
	reference := func(c Collected, limit int) (Collected, error) {
		for _, kind := range []string{kindMetrics, kindTranscript, kindTrail, kindFeedback, kindSkill} {
			for len(prompt(c)) > limit {
				i := oldest(c.Inputs, kind)
				if i < 0 {
					break
				}
				c.Dropped = append(c.Dropped, fmt.Sprintf("%s %s (dropped to fit the %d KB cap)", c.Inputs[i].Kind, c.Inputs[i].Label, limit/1024))
				c.Inputs = append(c.Inputs[:i:i], c.Inputs[i+1:]...)
			}
		}
		if len(prompt(c)) > limit {
			return c, errors.New("memory over cap")
		}
		return c, nil
	}
	saved := render
	t.Cleanup(func() { render = saved })
	full := len(prompt(base()))
	for _, limit := range []int{full, full - 1, full - 500, full - 4000, full - 12000, 2048, 10} {
		want, wantErr := reference(base(), limit)
		renders := 0
		render = func(c Collected) string { renders++; return prompt(c) }
		c := base()
		err := c.fit(limit)
		if err == nil {
			c.Prompt = render(c)
		}
		render = saved
		if (err == nil) != (wantErr == nil) {
			t.Fatalf("limit %d: err %v, the loop's %v", limit, err, wantErr)
		}
		if err == nil && renders != 1 {
			t.Errorf("limit %d: %d renders, want 1", limit, renders)
		}
		if labels(c) != labels(want) || strings.Join(c.Dropped, "|") != strings.Join(want.Dropped, "|") {
			t.Errorf("limit %d: kept or dropped other inputs than the loop:\n%s\nwant\n%s", limit, labels(c), labels(want))
		}
		if err == nil && len(c.Prompt) > limit {
			t.Errorf("limit %d: prompt %d bytes", limit, len(c.Prompt))
		}
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	f := newFixture(t)
	var out bytes.Buffer
	o := Options{Home: f.home, All: true, DryRun: true, Out: &out, Now: func() time.Time { return now }, Active: idle, Scopes: noScopes,
		Projects: func(string) ([]search.Project, error) { return []search.Project{project(f)}, nil }}
	before := tree(t, f.home)
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if after := tree(t, f.home); after != before {
		t.Errorf("dry run wrote:\nbefore %s\nafter  %s", before, after)
	}
	if !strings.Contains(out.String(), f.root) || !strings.Contains(out.String(), "agy --input-format stream-json") {
		t.Errorf("dry run output lacks the project or the command:\n%s", out.String())
	}
}

// TestDryRunLines drives every line the dry run prints: a skipped root, an
// unchanged project, a project over the cap, and --project's prompt.
func TestDryRunLines(t *testing.T) {
	f := newFixture(t)
	calm := newFixture(t)
	later := now.Add(time.Hour)
	write(t, statePath(f.home), `{"projects":{"`+calm.root+`":"`+later.Format(time.RFC3339)+`"}}`, old)
	var out bytes.Buffer
	o := Options{Home: f.home, All: true, DryRun: true, Out: &out, Now: func() time.Time { return now }, Active: idle, Scopes: noScopes,
		Projects: func(string) ([]search.Project, error) {
			return []search.Project{project(f), project(calm), {Root: filepath.Join(f.root, "gone")}}, nil
		}}
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{f.root + ": would dream (idle 24h0m0s, changed since", "would run: agy --input-format stream-json", calm.root + ": idle, unchanged since", "gone: skip (gone)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run lacks %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	o.All, o.Project = false, f.root
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[dry-run] prompt:") || !strings.Contains(out.String(), "We deploy on Fridays.") {
		t.Errorf("--project dry run did not print the prompt:\n%s", out.String())
	}
}

func TestDryRunOverCapAndDropped(t *testing.T) {
	f := newFixture(t)
	big := strings.Repeat("a memory line that fills the store. ", 8000) // ~290 KB
	write(t, filepath.Join(f.root, ".agents", "memory", "huge.md"), big, fresh)
	var out bytes.Buffer
	o := Options{Home: f.home, All: true, DryRun: true, Out: &out, Now: func() time.Time { return now }, Active: idle, Scopes: noScopes,
		Projects: func(string) ([]search.Project, error) { return []search.Project{project(f)}, nil }}
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "would fail (memory over cap)") {
		t.Errorf("over-cap memory not reported:\n%s", out.String())
	}

	// A store that refuses to be read is left out and named, and the run
	// goes on with the rest.
	g := newFixture(t)
	store := filepath.Join(g.root, ".agents", "memory")
	os.RemoveAll(store)
	if err := os.Symlink(t.TempDir(), store); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	o.Projects = func(string) ([]search.Project, error) { return []search.Project{project(g)}, nil }
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "left out: memory:") || !strings.Contains(out.String(), "symlink") {
		t.Errorf("a refused store is not named:\n%s", out.String())
	}
}

// A dry run printed one `left out:` line per dropped input, 34 on one box; it
// prints the count and the first few (audit 2026-10-25, SD-S2).
func TestDryRunSummarisesDropped(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 12; i++ {
		write(t, filepath.Join(f.root, ".agents", "docs", "bugs", fmt.Sprintf("BUG-%03d-big.md", i)), strings.Repeat("a long bug record line. ", 1700), fresh.Add(time.Duration(i)*time.Second))
	}
	c, err := collect(f.home, project(f), now.Add(-firstLook), time.Now(), maxPrompt) // the dry run's first look
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Dropped) <= shownDropped {
		t.Fatalf("the fixture drops %d inputs, not more than %d, so it proves nothing", len(c.Dropped), shownDropped)
	}
	var out bytes.Buffer
	o := Options{Home: f.home, Project: f.root, DryRun: true, Out: &out, Now: func() time.Time { return now },
		Projects: func(string) ([]search.Project, error) { return []search.Project{project(f)}, nil }}
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "[dry-run]   left out:") {
			lines = append(lines, l)
		}
	}
	want := fmt.Sprintf("[dry-run]   left out: %d inputs, the first %d below", len(c.Dropped), shownDropped)
	if len(lines) != shownDropped+2 || lines[0] != want || !strings.HasSuffix(lines[len(lines)-1], fmt.Sprintf("and %d more", len(c.Dropped)-shownDropped)) {
		t.Errorf("left-out lines:\n%s\nwant %q, %d records and the rest counted", strings.Join(lines, "\n"), want, shownDropped)
	}
	if !strings.Contains(out.String(), "[dry-run]   left out: "+oneLine(c.Dropped[0])+"\n") {
		t.Errorf("the first dropped input is not listed:\n%s", strings.Join(lines, "\n"))
	}
}

// A dry run refuses what the real run refuses before its first project: no
// agy on PATH, no login, a relative HOME (SD27 of the 2026-10-27 audit, plan
// D17), and writes nothing while it does.
func TestDryRunRefusesWhatTheRunRefuses(t *testing.T) {
	f := newFixture(t)
	o := func(out *bytes.Buffer) Options {
		return Options{Home: f.home, All: true, DryRun: true, Out: out, Now: func() time.Time { return now }, Active: idle, Scopes: noScopes,
			Projects: func(string) ([]search.Project, error) { return []search.Project{project(f)}, nil }}
	}
	cases := []struct {
		name  string
		setup func(t *testing.T)
		want  string
	}{
		{"no agy", func(t *testing.T) { agyLogin(t); t.Setenv("PATH", t.TempDir()) }, "no agy on PATH"},
		{"no login", func(t *testing.T) { fakeCLI(t, "agy", "", 0); t.Setenv("HOME", t.TempDir()) }, "no Antigravity login"},
		{"relative HOME", func(t *testing.T) { fakeCLI(t, "agy", "", 0); t.Setenv("HOME", "relative/home") }, "not an absolute path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.setup(t)
			var out bytes.Buffer
			before := tree(t, f.home)
			err := Run(o(&out))
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code != 2 || !strings.Contains(ee.Msg, c.want) {
				t.Errorf("err %v, want exit 2 naming %q", err, c.want)
			}
			if after := tree(t, f.home); after != before {
				t.Errorf("a refused dry run wrote:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}
