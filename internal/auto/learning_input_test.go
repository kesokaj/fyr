package auto

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/brain"
)

func TestBuildLearningInputCapsOversizedProposalsAndPreservesTelemetry(t *testing.T) {
	home := t.TempDir()
	root := initGitRepo(t)
	seedAutoKneeMetrics(t, home, root, runAt)

	g := brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt }}
	bodies := []string{
		strings.Repeat("alpha-line\n", 4800),
		strings.Repeat("é", 25600),
		strings.Repeat("gamma", 10240),
	}
	for i := 0; i < 14; i++ {
		body := bodies[i%len(bodies)]
		if _, err := brain.Add(g, brain.AddInput{
			Kind:    "proposal",
			Status:  "queued",
			Project: root,
			Title:   fmt.Sprintf("Oversized proposal %02d", i+1),
			Body:    strings.NewReader(body),
		}); err != nil {
			t.Fatalf("brain.Add(%d): %v", i, err)
		}
	}

	r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return runAt }}, cfg: Config{}}
	task := Task{Class: ClassRetroLite, Outcome: OutcomeDrafts, DraftsOnly: true, Count: 1}
	got := r.buildLearningInput(root, &Project{}, task, runAt)

	if len(got) > learningInputCap {
		t.Fatalf("len(buildLearningInput) = %d, want <= %d", len(got), learningInputCap)
	}
	if !utf8.ValidString(got) || strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("learning input contains invalid UTF-8")
	}

	propStart := strings.Index(got, "## Queued proposals\n\n")
	metricStart := strings.Index(got, "\n\n## Metric numbers\n")
	if propStart < 0 || metricStart <= propStart {
		t.Fatalf("missing Queued proposals or Metric numbers heading:\n%s", got)
	}

	propBlock := got[propStart+len("## Queued proposals\n\n") : metricStart+2]
	if !strings.HasSuffix(propBlock, "\n\n") {
		t.Fatalf("proposal section does not end cleanly with \\n\\n")
	}
	rawEntries := strings.Split(strings.TrimSuffix(propBlock, "\n\n"), "\n\n### ")
	if len(rawEntries) < 8 {
		t.Fatalf("expected >= 8 capped proposals within 24 KiB budget, got %d", len(rawEntries))
	}
	for i, part := range rawEntries {
		entry := part
		if i > 0 {
			entry = "### " + part
		}
		entry += "\n\n"
		if len(entry) > maxProposalEntryBytes {
			t.Errorf("proposal entry %d len = %d, want <= %d", i, len(entry), maxProposalEntryBytes)
		}
		if !utf8.ValidString(entry) {
			t.Errorf("proposal entry %d invalid UTF-8", i)
		}
	}
	for _, marker := range []string{"alpha-line", "ééé", "gammagamma"} {
		if !strings.Contains(propBlock, marker) {
			t.Errorf("proposal section missing truncated body marker %q", marker)
		}
	}

	for _, want := range []string{
		"\n\n## Metric numbers\n",
		`"H.sessions_over_knee"`,
		"\n\n## Diagnostic signals\n",
		"context_knee_rate",
		"\n\n## Trajectory digest\n",
		`"kneeExceeded"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("learning input missing %q:\n%s", want, got)
		}
	}
}

func TestBoundProposalEntry(t *testing.T) {
	short := "### P1: Short\nhello\n\n"
	if got := boundProposalEntry(short, maxProposalEntryBytes); got != short {
		t.Errorf("short entry mutated: %q", got)
	}
	if got := boundProposalEntry(short, 2); got != "" {
		t.Errorf("tiny cap = %q, want empty", got)
	}

	utf8Entry := "### P2: UTF8\n" + strings.Repeat("é", 2000) + "\n\n"
	gotUTF8 := boundProposalEntry(utf8Entry, maxProposalEntryBytes)
	if len(gotUTF8) > maxProposalEntryBytes || !utf8.ValidString(gotUTF8) || !strings.HasSuffix(gotUTF8, "\n\n") {
		t.Errorf("UTF-8 bound failed: len=%d valid=%v suffix=%v", len(gotUTF8), utf8.ValidString(gotUTF8), strings.HasSuffix(gotUTF8, "\n\n"))
	}

	singleLine := "### P3: OneLine\n" + strings.Repeat("x", 5000) + "\n\n"
	gotSingle := boundProposalEntry(singleLine, maxProposalEntryBytes)
	if len(gotSingle) != maxProposalEntryBytes || !strings.HasSuffix(gotSingle, "\n\n") || !strings.Contains(gotSingle, strings.Repeat("x", 100)) {
		t.Errorf("single-line bound unexpected: len=%d", len(gotSingle))
	}

	multiLine := "### P4: Multi\n" + strings.Repeat("line-item\n", 400) + "\n\n"
	gotMulti := boundProposalEntry(multiLine, maxProposalEntryBytes)
	if len(gotMulti) > maxProposalEntryBytes || !strings.HasSuffix(gotMulti, "line-item\n\n") {
		t.Errorf("multi-line bound did not break on line boundary: len=%d tail=%q", len(gotMulti), gotMulti[max(0, len(gotMulti)-25):])
	}
}
