package auto

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/metrics"
)

func TestBuildLearningInputCapsOversizedProposalsAndPreservesTelemetry(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	props, err := r.sweepOpenProposals()
	if err != nil {
		t.Fatal(err)
	}
	r.writeProposalSection(&b, orderMatchingProposals(props, root, ""), b.Len()+len("## Queued proposals\n\n")+2)
	if strings.Contains(b.String(), "## Queued proposals") {
		t.Errorf("wrote orphaned Queued proposals heading: %q", b.String())
	}

	var tb strings.Builder
	tb.WriteString("# Head\n\n")
	limit := tb.Len() + telemetryReserve
	nums := strings.Repeat("n", 16<<10)
	diag := strings.Repeat("d", 1500)
	traj := strings.Repeat("t", 1500)
	appendTelemetrySectionsWithDigest(&tb, nums, diag, "", traj, home, home, limit)
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

func TestBuildLearningInputLeftOutHeaderMatchesDream(t *testing.T) {
	t.Parallel()

	t.Run("no Left out header when everything fits", func(t *testing.T) {
		home := t.TempDir()
		root := initGitRepo(t)
		pBrain := brain.Options{Home: home, Dir: root, Now: func() time.Time { return runAt }}
		if _, err := brain.Add(pBrain, brain.AddInput{
			Kind:       "memory",
			Status:     "closed",
			Provenance: "operator",
			Title:      "Small memory",
			Body:       strings.NewReader("fits easily"),
		}); err != nil {
			t.Fatal(err)
		}
		r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return runAt }}, cfg: Config{}}
		got := r.buildLearningInput(root, &Project{}, Task{Class: ClassRetroLite, Outcome: OutcomeDrafts, DraftsOnly: true}, runAt)
		if strings.Contains(got, "Left out: ") {
			t.Fatalf("unexpected Left out line when nothing omitted:\n%s", got)
		}
	})

	t.Run("omitted items emit shared dream Left out line", func(t *testing.T) {
		home := t.TempDir()
		realHome := filepath.Join(t.TempDir(), "realhome-leftout-secret")
		root := initGitRepo(t)
		pBrain := brain.Options{Home: home, Dir: root, Now: func() time.Time { return runAt }}
		gBrain := brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt }}

		for i := 0; i < 6; i++ {
			title := fmt.Sprintf("Mem %02d", i)
			if i == 0 {
				title = "Mem 00 " + realHome + "/x"
			}
			if _, err := brain.Add(pBrain, brain.AddInput{
				Kind:       "memory",
				Status:     "closed",
				Provenance: "operator",
				Title:      title,
				Body:       strings.NewReader(strings.Repeat("m", 4000)),
			}); err != nil {
				t.Fatal(err)
			}
		}
		tokNeedle := "gh" + "p_" + strings.Repeat("K", 36)
		for i := 0; i < 12; i++ {
			title := fmt.Sprintf("Prop %02d", i)
			if i == 2 {
				title = "Prop 02 " + tokNeedle
			}
			if _, err := brain.Add(gBrain, brain.AddInput{
				Kind:    "proposal",
				Status:  "queued",
				Project: root,
				Title:   title,
				Body:    strings.NewReader(strings.Repeat("p", 2000)),
			}); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 40; i++ {
			convDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", fmt.Sprintf("conv-%02d", i), ".system_generated", "logs")
			if err := os.MkdirAll(convDir, 0o700); err != nil {
				t.Fatal(err)
			}
			line := fmt.Sprintf(`{"cwd":%q,"type":"user","content":%q}`+"\n"+`{"type":"assistant","content":%q}`+"\n",
				root, fmt.Sprintf("request %02d %s", i, strings.Repeat("r", 260)), fmt.Sprintf("decision %02d %s", i, strings.Repeat("d", 260)))
			if err := os.WriteFile(filepath.Join(convDir, "transcript.jsonl"), []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}
			ts := runAt.Add(-2 * time.Hour)
			_ = os.Chtimes(filepath.Join(convDir, "transcript.jsonl"), ts, ts)
		}

		r := &run{o: Options{Home: home, RealHome: realHome, Now: func() time.Time { return runAt }}, cfg: Config{ConversationIdle: "1h"}}
		task := Task{Class: ClassRetroLite, Outcome: OutcomeDrafts, DraftsOnly: true}
		got := r.buildLearningInput(root, &Project{}, task, runAt)

		if len(got) > learningInputCap || !utf8.ValidString(got) {
			t.Fatalf("invalid size or UTF-8: len=%d valid=%v", len(got), utf8.ValidString(got))
		}
		for _, leaked := range []string{"realhome-leftout-secret", tokNeedle} {
			if strings.Contains(got, leaked) {
				t.Errorf("learning input leaked %q", leaked)
			}
		}
		if forged := dream.FormatLeftOutHeader([]string{"proposal: P1\n## ForgedProp"}, func(s string) string {
			return oneLine(scrubEntry(s, realHome, home))
		}); strings.Contains(forged, "\n") {
			t.Errorf("Left out header kept newline: %q", forged)
		}

		var expectedDropped []string
		pMems, err := brain.List(pBrain, brain.ListFilter{Kind: "memory"})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range pMems {
			if m.Status == "closed" && !strings.Contains(got, "### "+m.ID+" ") {
				expectedDropped = append(expectedDropped, "memory: "+m.ID+" "+strings.TrimSpace(m.Title))
			}
		}
		props, err := r.sweepOpenProposals()
		if err != nil {
			t.Fatal(err)
		}
		for _, pr := range orderMatchingProposals(props, root, task.ID) {
			if !strings.Contains(got, "### "+pr.ID+": ") {
				expectedDropped = append(expectedDropped, "proposal: "+pr.ID+" "+strings.TrimSpace(pr.Title))
			}
		}
		for _, conv := range r.conversationsSinceCutoff(root, time.Time{}, runAt) {
			if !strings.Contains(got, "## Conversation "+conv.label+"\n") {
				expectedDropped = append(expectedDropped, "transcript: "+conv.label)
			}
		}
		if len(expectedDropped) == 0 {
			t.Fatalf("fixture did not omit any items")
		}

		clean := func(s string) string { return oneLine(scrubEntry(s, realHome, home)) }
		wantLine := dream.FormatLeftOutHeader(expectedDropped, clean)
		lines := strings.Split(got, "\n")
		if len(lines) < 3 || lines[1] != wantLine {
			t.Fatalf("header Left out line = %q, want %q", lines[1], wantLine)
		}
	})

	t.Run("omitted telemetry sections reported", func(t *testing.T) {
		var b strings.Builder
		b.WriteString(strings.Repeat("x", 128))
		dropped := appendTelemetrySectionsWithDigest(&b, "nums", "diag", "", "traj", "", "", b.Len())
		got := dream.FormatLeftOutHeader(dropped, nil)
		want := "Left out: 3 (telemetry 3): telemetry: metric numbers; telemetry: diagnostic signals; telemetry: trajectory digest"
		if got != want {
			t.Fatalf("telemetry Left out = %q, want %q", got, want)
		}
	})
}

func TestBuildLearningInputDailyDigestRedactionCapAndFailOpen(t *testing.T) {
	t.Parallel()

	t.Run("includes-redacted-digest-and-respects-cap", func(t *testing.T) {
		home := t.TempDir()
		realHome := filepath.Join(t.TempDir(), "realhome-digest-path")
		root := initGitRepo(t)
		seedAutoKneeMetrics(t, home, root, runAt)

		redactedVal := "gh" + "p_" + strings.Repeat("E", 36)
		digest := metrics.DailyDigest{
			Day:     "2026-11-01",
			Verdict: metrics.VerdictRegress,
			Signals: []metrics.DigestAction{{
				SignalID:          "sig:global:search_grep_low",
				Severity:          "high",
				Rule:              "search_grep_low",
				ActionScope:       "auto_skill",
				RecommendedAction: "Fix item " + redactedVal,
				Summary:           "Digest summary in " + realHome + "/skills and " + home + "/work",
			}},
		}
		if err := metrics.WriteLatestDailyDigest(home, digest); err != nil {
			t.Fatal(err)
		}
		// Also add oversized proposals so total output must still stay <= learningInputCap.
		g := brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt }}
		for i := 0; i < 6; i++ {
			if _, err := brain.Add(g, brain.AddInput{
				Kind: "proposal", Status: "queued", Project: root,
				Title: fmt.Sprintf("Prop %d", i), Body: strings.NewReader(strings.Repeat("z", 3000)),
			}); err != nil {
				t.Fatal(err)
			}
		}

		r := &run{o: Options{Home: home, RealHome: realHome, Now: func() time.Time { return runAt }}, cfg: Config{}}
		got := r.buildLearningInput(root, &Project{}, Task{Class: ClassRetroLite, Outcome: OutcomeDrafts, DraftsOnly: true}, runAt)
		if len(got) > learningInputCap {
			t.Fatalf("len(got) = %d, want <= %d", len(got), learningInputCap)
		}
		if !strings.Contains(got, "## Daily digest\n") || !strings.Contains(got, "sig:global:search_grep_low") {
			t.Fatalf("learning input missing Daily digest section:\n%s", got)
		}
		for _, leaked := range []string{"realhome-digest-path", home, redactedVal} {
			if strings.Contains(got, leaked) {
				t.Errorf("learning input leaked %q", leaked)
			}
		}
		if !strings.Contains(got, "[redacted by fyr]") || !strings.Contains(got, "~/skills") {
			t.Errorf("expected redacted marker and ~/skills in learning input:\n%s", got)
		}
	})

	t.Run("missing-or-corrupt-digest-omits-cleanly", func(t *testing.T) {
		for _, corrupt := range []bool{false, true} {
			home := t.TempDir()
			root := initGitRepo(t)
			if corrupt {
				p := metrics.LatestDailyDigestPath(home)
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("{corrupt json"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return runAt }}, cfg: Config{}}
			got := r.buildLearningInput(root, &Project{}, Task{Class: ClassRetroLite, Outcome: OutcomeDrafts, DraftsOnly: true}, runAt)
			if strings.Contains(got, "## Daily digest") {
				t.Errorf("corrupt=%v unexpectedly wrote Daily digest section:\n%s", corrupt, got)
			}
		}
	})
}
