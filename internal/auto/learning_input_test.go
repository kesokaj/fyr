package auto

import (
	"fmt"
	"path/filepath"
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

func TestBuildLearningInputScrubsBeforeTruncationAndAvoidsStarvation(t *testing.T) {
	home := t.TempDir()
	realHome := filepath.Join(t.TempDir(), "realhome-boundary-secret-path")
	root := initGitRepo(t)
	seedAutoKneeMetrics(t, home, root, runAt)

	pBrain := brain.Options{Home: home, Dir: root, Now: func() time.Time { return runAt }}
	gBrain := brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt }}

	skKey := "sk" + "-" + strings.Repeat("A1b2C3d4", 6)
	urlCred := "https://" + "alice:" + "s3cr3tPass" + "@example.com/repo"
	tokNeedle := "tok" + "en = " + strings.Repeat("Z9y8", 4)
	pemHead := "-----BEG" + "IN " + "RSA PRIV" + "ATE KEY-----"

	// Oversized closed memory crossing 4 KiB boundary with home path + URL credential near the cut.
	bigMemBody := strings.Repeat("m", 4040) + "\npath=" + realHome + "/mem/secret\ncred=" + urlCred + "\ntail=ok"
	for i := 0; i < 6; i++ {
		body := strings.Repeat("filler-mem\n", 500)
		if i == 0 {
			body = bigMemBody
		}
		if _, err := brain.Add(pBrain, brain.AddInput{
			Kind:       "memory",
			Status:     "closed",
			Provenance: "operator",
			Title:      fmt.Sprintf("Memory %d", i),
			Body:       strings.NewReader(body),
		}); err != nil {
			t.Fatalf("brain.Add memory(%d): %v", i, err)
		}
	}

	// Add 15 unrelated proposals before the active taskID proposal, plus boundary-crossing secrets and unterminated PEM.
	for i := 0; i < 15; i++ {
		var body string
		switch i {
		case 0:
			body = strings.Repeat("x", 2000) + urlCred + "\n" + skKey
		case 1:
			body = strings.Repeat("y", 2005) + realHome + "/deep/file\n" + tokNeedle
		case 2:
			body = "before\n" + pemHead + "\n" + strings.Repeat("k", 2500)
		default:
			body = "small proposal"
		}
		if _, err := brain.Add(gBrain, brain.AddInput{
			Kind:    "proposal",
			Status:  "queued",
			Project: root,
			Title:   fmt.Sprintf("Queue %02d", i),
			Body:    strings.NewReader(body),
		}); err != nil {
			t.Fatalf("brain.Add proposal(%d): %v", i, err)
		}
	}
	targetID, err := brain.Add(gBrain, brain.AddInput{
		Kind:    "proposal",
		Status:  "queued",
		Project: root,
		Title:   "Target",
		Body:    strings.NewReader("target-proposal-body-visible"),
	})
	if err != nil {
		t.Fatalf("brain.Add target proposal: %v", err)
	}
	if gotTitle := oneLine("Target\r\n## ForgedTitleLine"); gotTitle != "Target  ## ForgedTitleLine" || strings.ContainsAny(gotTitle, "\r\n") {
		t.Fatalf("oneLine did not fold title breaks: %q", gotTitle)
	}

	r := &run{o: Options{Home: home, RealHome: realHome, Now: func() time.Time { return runAt }}, cfg: Config{}}
	got := r.buildLearningInput(root, &Project{}, Task{ID: targetID, Class: ClassRetroLite, Outcome: OutcomeDrafts, DraftsOnly: true}, runAt)

	for _, leaked := range []string{"realhome-boundary-secret-path", "alice:s3cr3t", "s3cr3tPass", "A1b2C3d4", "Z9y8", "\n## ForgedHeading", "\n## ForgedTitleLine"} {
		if strings.Contains(got, leaked) {
			t.Errorf("learning input leaked or forged %q", leaked)
		}
	}
	if !strings.Contains(got, targetID) || !strings.Contains(got, "target-proposal-body-visible") {
		t.Errorf("active task proposal %s missing (starved by queued proposals)", targetID)
	}
	targetIdx := strings.Index(got, targetID)
	queueIdx := strings.Index(got, "Queue 00")
	if targetIdx < 0 || (queueIdx >= 0 && targetIdx > queueIdx) {
		t.Errorf("active taskID %s (%d) did not precede Queue 00 (%d)", targetID, targetIdx, queueIdx)
	}
	for _, want := range []string{"\n\n## Metric numbers\n", "\n\n## Diagnostic signals\n", "\n\n## Trajectory digest\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("learning input missing telemetry section %q (PEM or starvation broke telemetry)", want)
		}
	}
}

func TestAppendProposalSectionNoOrphanHeadingAndTelemetryShare(t *testing.T) {
	home := t.TempDir()
	root := initGitRepo(t)
	gBrain := brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt }}
	if _, err := brain.Add(gBrain, brain.AddInput{
		Kind:    "proposal",
		Status:  "queued",
		Project: root,
		Title:   "P1",
		Body:    strings.NewReader("body"),
	}); err != nil {
		t.Fatal(err)
	}
	r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return runAt }}}
	var b strings.Builder
	b.WriteString("prefix\n\n")
	// Remaining after header ("## Queued proposals\n\n" = 21 bytes) is only 2 bytes -> boundProposalEntry returns "".
	r.appendProposalSection(&b, root, "", b.Len()+len("## Queued proposals\n\n")+2)
	if strings.Contains(b.String(), "## Queued proposals") {
		t.Errorf("wrote orphaned Queued proposals heading: %q", b.String())
	}

	var tb strings.Builder
	tb.WriteString("# Head\n\n")
	limit := tb.Len() + telemetryReserve
	nums := strings.Repeat("n", 16<<10)
	diag := strings.Repeat("d", 1500)
	traj := strings.Repeat("t", 1500)
	appendTelemetrySections(&tb, nums, diag, traj, home, home, limit)
	out := tb.String()
	if len(out) > limit {
		t.Fatalf("telemetry exceeded limit %d: got %d", limit, len(out))
	}
	for _, want := range []string{"## Metric numbers\n", "## Diagnostic signals\n", "## Trajectory digest\n", strings.Repeat("d", 100), strings.Repeat("t", 100)} {
		if !strings.Contains(out, want) {
			t.Errorf("telemetry share failed, missing %q", want)
		}
	}
	// Ensure conversation separation after mid-line truncated telemetry.
	tb.WriteString(sectionBreakPrefix(tb.String()))
	tb.WriteString("## Conversation conv-1\nhello\n\n")
	if !strings.Contains(tb.String(), "\n\n## Conversation conv-1\n") {
		t.Errorf("missing blank-line separator before Conversation heading")
	}
}
