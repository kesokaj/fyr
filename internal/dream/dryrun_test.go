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

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

// fit rendered the whole prompt again for every input it dropped, 34 renders
// of ~260 KB a project a run on one box (audit 2026-10-25, SD-S1). It now
// keeps the size by arithmetic: one render a project, for the prompt that
// is sent, and the same inputs dropped, with the same Through, as the same
// fit measuring by a render after every drop. The wording of the left-out
// lines is pinned by TestFitWording, never copied here (SD60a).
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
		c.Inputs = append(c.Inputs,
			Input{Kind: kindTrail, Label: "changes/big.md", Text: strings.Repeat("c", 3000), Time: now.Add(-time.Hour)},
			Input{Kind: kindFeedback, Label: "FEEDBACK-1", Text: strings.Repeat("f", 120), Time: now.Add(2 * time.Minute)},
			Input{Kind: kindSkill, Label: "skill golang", Text: "golang: Go"},
			Input{Kind: kindMetrics, Label: "metrics", Text: strings.Repeat("9", 100)})
		return c
	}
	reference := func(c Collected, limit int) (Collected, error) {
		f := newFitter(&c, limit)
		f.over = func() bool { return len(prompt(c)) > limit }
		err := f.run()
		return c, err
	}
	saved := render
	t.Cleanup(func() { render = saved })
	full := len(prompt(base()))
	for _, limit := range []int{full, full - 1, full - 500, full - 4000, full - 12000, 4096, 2048, 10} {
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
			t.Fatalf("limit %d: err %v, the rendering fit's %v", limit, err, wantErr)
		}
		if err == nil && renders != 1 {
			t.Errorf("limit %d: %d renders, want 1", limit, renders)
		}
		if labels(c) != labels(want) || strings.Join(c.Dropped, "|") != strings.Join(want.Dropped, "|") || !c.Through.Equal(want.Through) {
			t.Errorf("limit %d: kept or dropped other inputs than the rendering fit:\n%s\nwant\n%s", limit, labels(c), labels(want))
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
	calm.addTrail(t, brain.Options{Home: f.home, Dir: calm.root, Now: func() time.Time { return old }}, "change", "an old change entry", "done")
	markRegistered(t, f.home, calm.root)
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
	saved := forDream
	t.Cleanup(func() { forDream = saved })
	forDream = func(string, string, time.Time, time.Time) (brain.DreamInput, error) {
		return brain.DreamInput{Memory: []brain.Record{{ID: "MEMORY-huge", Title: "huge", Body: big}}}, nil
	}
	var out bytes.Buffer
	o := Options{Home: f.home, All: true, DryRun: true, Out: &out, Now: func() time.Time { return now }, Active: idle, Scopes: noScopes,
		Projects: func(string) ([]search.Project, error) { return []search.Project{project(f)}, nil }}
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "would fail (memory over cap)") {
		t.Errorf("over-cap memory not reported:\n%s", out.String())
	}
}

// A dry run printed one `left out:` line per dropped input, 34 on one box; it
// prints the count and the first few (audit 2026-10-25, SD-S2).
func TestDryRunSummarisesDropped(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 12; i++ {
		when := fresh.Add(time.Duration(i) * time.Second)
		f.addTrail(t, brain.Options{Home: f.home, Dir: f.root, Now: func() time.Time { return when }}, "bug", fmt.Sprintf("big %d", i), strings.Repeat("a long bug record line. ", 1700))
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

// A dry run refuses what the real run refuses before its first project, no
// agy on PATH (SD27 of the 2026-10-27 audit), and writes nothing while it
// does. No login and a relative HOME fail only a project the private branch
// would take (plan D12): the dry run says so on that project's branch line.
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
		code  int
	}{
		{"no agy", func(t *testing.T) { agyLogin(t); t.Setenv("PATH", t.TempDir()) }, "no agy on PATH", 2},
		{"no login", func(t *testing.T) { fakeCLI(t, "agy", "", 0); t.Setenv("HOME", t.TempDir()) }, "would fail: no Antigravity login", 0},
		{"relative HOME", func(t *testing.T) { fakeCLI(t, "agy", "", 0); t.Setenv("HOME", "relative/home") }, "would fail: HOME is \"relative/home\", not an absolute path", 0},
	}
	if err := os.MkdirAll(harness.DreamWork(f.home), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.setup(t)
			var out bytes.Buffer
			before := tree(t, f.home)
			err := Run(o(&out))
			var ee *ExitError
			switch {
			case c.code == 0 && err != nil:
				t.Errorf("err %v, want exit 0", err)
			case c.code == 0 && (!strings.Contains(out.String(), "[dry-run] "+f.root+": agy home private (no Antigravity login in ") || !strings.Contains(out.String(), c.want)):
				t.Errorf("no private branch line that %s:\n%s", c.want, out.String())
			case c.code != 0 && (!errors.As(err, &ee) || ee.Code != c.code || !strings.Contains(ee.Msg, c.want)):
				t.Errorf("err %v, want exit %d naming %q", err, c.code, c.want)
			}
			if after := tree(t, f.home); after != before {
				t.Errorf("a refused dry run wrote:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}
