package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type autonomyDocs struct {
	context       string
	feature       string
	sessionSizing string
	rows          map[string]Row
}

func checkDocPhrases(name, text string, must, forbid []string) []string {
	var errs []string
	for _, want := range must {
		if !strings.Contains(text, want) {
			errs = append(errs, name+" missing required phrase "+want)
		}
	}
	for _, bad := range forbid {
		if strings.Contains(text, bad) {
			errs = append(errs, name+" still contains build-time wait-point "+bad)
		}
	}
	return errs
}

func autonomyProblems(d autonomyDocs) []string {
	var errs []string
	errs = append(errs, checkDocPhrases("context/AGENTS.md", d.context,
		[]string{"decides in the pre-build interview; never asked during build"},
		[]string{"wants to be asked often"},
	)...)
	errs = append(errs, checkDocPhrases("skills/fyr-feature/SKILL.md", d.feature,
		[]string{
			"pre-build interview",
			"grilling",
			"never stops to ask",
			"by: implementer",
			"closing handoff",
		},
		[]string{
			"Three points always wait for the user",
			"Confirm or correct.",
			"At a cap, ask",
			"the question is asked then, as this step's gate",
		},
	)...)
	errs = append(errs, checkDocPhrases("rules/session-sizing.md", d.sessionSizing,
		[]string{"fyr metrics trajectory", "KNEE_EXCEEDED", "180,000", "zero build-time questions"},
		[]string{"the model cannot see its own fill"},
	)...)
	if r, ok := d.rows["autonomy"]; !ok || r.Check == "" || r.Prompt2 != "" {
		errs = append(errs, "GUIDE-eval.md missing single-turn capability row autonomy")
	}
	return errs
}

func loadAutonomyDocs(t *testing.T, root string) autonomyDocs {
	t.Helper()
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}
	list, found, err := ReadTrackedSuite(root, false)
	if err != nil || !found {
		t.Fatalf("ReadTrackedSuite: found=%v err=%v", found, err)
	}
	rows := make(map[string]Row, len(list))
	for _, r := range list {
		rows[r.ID] = r
	}
	return autonomyDocs{
		context:       read("context/AGENTS.md"),
		feature:       read("skills/fyr-feature/SKILL.md"),
		sessionSizing: read("rules/session-sizing.md"),
		rows:          rows,
	}
}

func assertAutonomyMustFailMutations(t *testing.T, good autonomyDocs) {
	t.Helper()
	cloneRows := func(skip string) map[string]Row {
		m := make(map[string]Row, len(good.rows))
		for k, v := range good.rows {
			if k != skip {
				m[k] = v
			}
		}
		return m
	}
	mutations := []struct {
		name string
		mut  func(d *autonomyDocs)
	}{
		{"context asks often", func(d *autonomyDocs) { d.context += "\nwants to be asked often" }},
		{"feature three gates", func(d *autonomyDocs) { d.feature += "\nThree points always wait for the user" }},
		{"feature cap ask", func(d *autonomyDocs) { d.feature += "\nAt a cap, ask" }},
		{"session sizing missing knee", func(d *autonomyDocs) { d.sessionSizing = strings.ReplaceAll(d.sessionSizing, "180,000", "") }},
		{"missing autonomy row", func(d *autonomyDocs) { d.rows = cloneRows("autonomy") }},
	}
	for _, tc := range mutations {
		cp := good
		tc.mut(&cp)
		if len(autonomyProblems(cp)) == 0 {
			t.Errorf("must-fail mutation %q unexpectedly passed autonomyProblems", tc.name)
		}
	}
}

// TestAutonomyNoBuildTimeQuestions pins ADR-517 across context, workflow skills,
// session-sizing, and GUIDE-eval: questions happen only in the pre-build
// interview, and no build-time wait-point remains except a safety FAIL.
func TestAutonomyNoBuildTimeQuestions(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	docs := loadAutonomyDocs(t, root)
	if errs := autonomyProblems(docs); len(errs) > 0 {
		for _, e := range errs {
			t.Error(e)
		}
		return
	}
	assertAutonomyMustFailMutations(t, docs)
}
