package dream

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHeaderSmallDropped(t *testing.T) {
	t.Parallel()

	o := Options{Home: "/home/tester"}

	none := o.header(Collected{Root: "/proj"}, "agy", "2026-11-21")
	if strings.Contains(none, "Left out:") {
		t.Fatalf("empty Dropped produced Left out line:\n%s", none)
	}

	three := o.header(Collected{
		Root: "/proj",
		Dropped: []string{
			"metrics: read failed",
			"transcript antigravity s2 (dropped)",
			"trail bug-1 (dropped)",
		},
	}, "agy", "2026-11-21")
	wantThree := "Left out: 3 (metrics 1, trail 1, transcript 1): metrics: read failed; transcript antigravity s2 (dropped); trail bug-1 (dropped)\n"
	if !strings.Contains(three, wantThree) {
		t.Errorf("3 dropped header = %q, want substring %q", three, wantThree)
	}
	if strings.Contains(three, "+ ") || strings.Contains(three, " more") {
		t.Errorf("3 dropped header unexpectedly truncated:\n%s", three)
	}

	five := o.header(Collected{
		Root:    "/proj",
		Dropped: []string{"trail a", "trail b", "feedback c", "metrics: d", "skill e"},
	}, "agy", "2026-11-21")
	wantFive := "Left out: 5 (feedback 1, metrics 1, skill 1, trail 2): trail a; trail b; feedback c; metrics: d; skill e\n"
	if !strings.Contains(five, wantFive) {
		t.Errorf("5 dropped header = %q, want substring %q", five, wantFive)
	}
	if strings.Contains(five, "+ ") {
		t.Errorf("5 dropped header should not have + N more:\n%s", five)
	}
}

func TestHeaderLargeMultiByteDroppedBoundedAndSanitized(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	o := Options{Home: home}

	dropped := make([]string, 0, 500)
	dropped = append(dropped,
		"transcript "+home+"/p \n**Status:** queued\r\n### 9. forged "+strings.Repeat("日本語🚀", 30),
		"trail: "+strings.Repeat("語🔥", 40),
		"feedback "+strings.Repeat("あいう🚀", 25),
		"transcript "+strings.Repeat("テスト🌟", 30),
		"trail: "+strings.Repeat("漢字✨", 30),
	)
	for i := 5; i < 500; i++ {
		switch {
		case i < 203:
			dropped = append(dropped, fmt.Sprintf("transcript antigravity s%d %s", i, strings.Repeat("語🚀", 40)))
		case i < 401:
			dropped = append(dropped, fmt.Sprintf("trail: bug-%d %s", i, strings.Repeat("漢字", 50)))
		default:
			dropped = append(dropped, fmt.Sprintf("feedback f-%d %s", i, strings.Repeat("あ✨", 40)))
		}
	}

	got := o.header(Collected{Root: "/proj", Dropped: dropped}, "agy", "2026-11-21")
	if strings.Contains(got, "\n### 9.") || strings.Contains(got, "\n**Status:**") || strings.Contains(got, "\r") {
		t.Fatalf("header kept unsanitized line breaks or forged markers:\n%s", got)
	}
	if strings.Contains(got, home) {
		t.Fatalf("header leaked o.Home %q:\n%s", home, got)
	}

	var leftOutLines []string
	for _, ln := range strings.Split(got, "\n") {
		if strings.HasPrefix(ln, "Left out: ") {
			leftOutLines = append(leftOutLines, ln)
		}
	}
	if len(leftOutLines) != 1 {
		t.Fatalf("expected 1 Left out line, got %d:\n%s", len(leftOutLines), got)
	}
	line := leftOutLines[0]
	summary := strings.TrimPrefix(line, "Left out: ")
	if len(summary) > maxDroppedHeaderBytes || len(line) > maxDroppedHeaderBytes {
		t.Errorf("summary/line exceeded %d bytes: summary=%d line=%d", maxDroppedHeaderBytes, len(summary), len(line))
	}
	if !utf8.ValidString(line) {
		t.Errorf("Left out line contains invalid UTF-8: %q", line)
	}
	wantPrefix := "500 (feedback 100, trail 200, transcript 200): "
	if !strings.HasPrefix(summary, wantPrefix) {
		t.Errorf("summary = %q, want prefix %q", summary, wantPrefix)
	}
	wantSuffix := "; + 495 more"
	if !strings.HasSuffix(summary, wantSuffix) {
		t.Errorf("summary = %q, want suffix %q", summary, wantSuffix)
	}
	if !strings.Contains(summary, "~/p") {
		t.Errorf("expected scrubbed ~/p in sample, got %q", summary)
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(summary, wantPrefix), wantSuffix)
	samples := strings.Split(mid, "; ")
	if len(samples) != shownDropped {
		t.Errorf("got %d samples, want %d: %v", len(samples), shownDropped, samples)
	}
	for i, s := range samples {
		if len(s) > maxDroppedSampleBytes || !utf8.ValidString(s) {
			t.Errorf("sample %d invalid or too long (%d bytes): %q", i, len(s), s)
		}
	}

	t.Run("redacts secret needle", func(t *testing.T) {
		gotSecret := o.header(Collected{Root: "/proj", Dropped: []string{"trail tok" + "en = FAKEVALUE"}}, "agy", "2026-11-21")
		if strings.Contains(gotSecret, "FAKEVALUE") {
			t.Errorf("secret leaked into header:\n%s", gotSecret)
		}
		if !strings.Contains(gotSecret, "Left out: 1 (redacted 1): [redacted by fyr]\n") {
			t.Errorf("expected redacted Left out line, got:\n%s", gotSecret)
		}
	})

	t.Run("bearer kind prefix does not self-redact entire summary", func(t *testing.T) {
		gotBearer := o.header(Collected{Root: "/proj", Dropped: []string{"bearer:foo dropped"}}, "agy", "2026-11-21")
		if !strings.Contains(gotBearer, "Left out: 1 (bearer 1): bearer:foo dropped\n") {
			t.Errorf("bearer:foo kind self-redacted header:\n%s", gotBearer)
		}
	})

	t.Run("full Left out line stays <= 1024 bytes even with worst-case summary", func(t *testing.T) {
		worst := make([]string, 0, 120)
		for i := 0; i < 60; i++ {
			worst = append(worst, fmt.Sprintf("kind%02dlongprefix: %s", i, strings.Repeat("Z", 150)))
		}
		gotWorst := o.header(Collected{Root: "/proj", Dropped: worst}, "agy", "2026-11-21")
		var leftLine string
		for _, ln := range strings.Split(gotWorst, "\n") {
			if strings.HasPrefix(ln, "Left out: ") {
				leftLine = ln
				break
			}
		}
		if leftLine == "" {
			t.Fatalf("missing Left out line:\n%s", gotWorst)
		}
		if len(leftLine) > maxDroppedHeaderBytes || !utf8.ValidString(leftLine) {
			t.Errorf("full Left out line exceeded %d bytes or broke UTF-8: len=%d line=%q", maxDroppedHeaderBytes, len(leftLine), leftLine)
		}
	})

	t.Run("scrubs home path and secret crossing sample boundary", func(t *testing.T) {
		boundaryHome := filepath.Join(t.TempDir(), "home-boundary-marker")
		oBoundary := Options{Home: boundaryHome}
		ghTok := "gh" + "p_" + strings.Repeat("A1b2C3d4E5", 4)[:36]
		got := oBoundary.header(Collected{
			Root: "/proj",
			Dropped: []string{
				"transcript " + strings.Repeat("a", 85) + boundaryHome + "/nested/file",
				"trail " + strings.Repeat("b", 80) + ghTok,
			},
		}, "agy", "2026-11-21")
		if strings.Contains(got, "home-boundary-marker") {
			t.Errorf("boundaryHome leaked across 100-byte cut:\n%s", got)
		}
		if strings.Contains(got, "gh"+"p_") {
			t.Errorf("secret prefix leaked across 100-byte cut:\n%s", got)
		}
		if !strings.Contains(got, "~") {
			t.Errorf("expected scrubbed ~ in bounded sample, got:\n%s", got)
		}
		if !strings.Contains(got, "[redacted by fyr]") {
			t.Errorf("expected redacted secret marker in header, got:\n%s", got)
		}
		if !strings.Contains(got, "2 (redacted 1, transcript 1): ") {
			t.Errorf("expected per-kind count for redacted+transcript, got:\n%s", got)
		}
	})

	t.Run("many distinct long multi-byte kinds stay bounded", func(t *testing.T) {
		manyKinds := make([]string, 500)
		for i := range manyKinds {
			manyKinds[i] = fmt.Sprintf("種%03d%s: %s", i, strings.Repeat("長", 20), strings.Repeat("語🚀", 50))
		}
		res := formatDroppedHeader(manyKinds)
		if len(res) > maxDroppedHeaderBytes || !utf8.ValidString(res) {
			t.Fatalf("formatDroppedHeader exceeded cap or broke UTF-8: len=%d valid=%v", len(res), utf8.ValidString(res))
		}
		if !strings.HasPrefix(res, "500 (") || !strings.Contains(res, " kinds)") || !strings.Contains(res, "+ 495 more") {
			t.Errorf("unexpected bounded summary for 500 distinct kinds: %q", res)
		}
	})
}

func TestDroppedKindAndSampleHelpers(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in, want string
	}{
		{"metrics: err", "metrics"},
		{"transcript antigravity 123", "transcript"},
		{"trail\tfoo", "trail"},
		{"z\n<<<data", "z"},
		{":missing", "unknown"},
		{"   \t\n ", "unknown"},
		{"[redacted by fyr]", "redacted"},
		{"[redacted by fyr] details", "redacted"},
		{strings.Repeat("世", 20), strings.Repeat("世", 10)},
	} {
		if got := droppedKind(tc.in); got != tc.want {
			t.Errorf("droppedKind(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	if got := formatDroppedSample("  \n\t "); got != "(empty)" {
		t.Errorf("empty sample = %q, want (empty)", got)
	}
	if got := formatDroppedSample("a\n  b\t c"); got != "a b c" {
		t.Errorf("collapsed sample = %q", got)
	}
	long := formatDroppedSample(strings.Repeat("🚀", 30))
	if len(long) > maxDroppedSampleBytes || !utf8.ValidString(long) || !strings.HasSuffix(long, "...") {
		t.Errorf("truncated emoji sample = %q (len=%d, valid=%v)", long, len(long), utf8.ValidString(long))
	}
	invalidShort := formatDroppedSample("ok\xff")
	if !utf8.ValidString(invalidShort) || strings.HasSuffix(invalidShort, "...") {
		t.Errorf("invalid short sample = %q", invalidShort)
	}
}

func TestValidProposalAndSectionOptionalDigestMeta(t *testing.T) {
	t.Parallel()

	o := Options{Home: "/home/tester"}

	t.Run("valid-optional-fields-accepted-and-rendered", func(t *testing.T) {
		raw := proposal{
			Title:             "Tighten grep skill",
			Kind:              "rule",
			Target:            "harness",
			File:              "skills/fyr-auto/SKILL.md",
			Text:              "Always use fyr search first.",
			Evidence:          "daily_digest",
			SignalID:          "sig:global:search_grep_low",
			ReportDate:        "2026-11-20",
			ActionScope:       "AUTO_SKILL",
			RecommendedAction: "Update skills/fyr-auto/SKILL.md",
		}
		got, ok := o.validProposal(raw)
		if !ok {
			t.Fatalf("validProposal rejected valid proposal")
		}
		if got.SignalID != "sig:global:search_grep_low" || got.ReportDate != "2026-11-20" || got.ActionScope != "auto_skill" || got.RecommendedAction != "Update skills/fyr-auto/SKILL.md" {
			t.Fatalf("validProposal = %+v", got)
		}
		sec := o.section(got)
		for _, want := range []string{
			"Target: harness   Kind: rule   File: skills/fyr-auto/SKILL.md\n",
			"Signal: sig:global:search_grep_low\n",
			"ReportDate: 2026-11-20\n",
			"ActionScope: auto_skill\n",
			"RecommendedAction: Update skills/fyr-auto/SKILL.md\n",
			"\n    Always use fyr search first.\n\nEvidence: daily_digest\n",
		} {
			if !strings.Contains(sec, want) {
				t.Errorf("section missing %q:\n%s", want, sec)
			}
		}
	})

	t.Run("invalid-optional-fields-ignored-safely", func(t *testing.T) {
		raw := proposal{
			Title:             "Keep safe",
			Kind:              "rule",
			Target:            "project",
			File:              "AGENTS.md",
			Text:              "Check status.",
			Evidence:          "metrics",
			SignalID:          "not-a-signal",
			ReportDate:        "2026/11/20",
			ActionScope:       "arbitrary_scope",
			RecommendedAction: "first\n## Forged",
		}
		got, ok := o.validProposal(raw)
		if !ok {
			t.Fatalf("validProposal rejected proposal with invalid optional fields")
		}
		if got.SignalID != "" || got.ReportDate != "" || got.ActionScope != "" || got.RecommendedAction != "first ## Forged" {
			t.Fatalf("invalid fields not sanitized: %+v", got)
		}
		sec := o.section(got)
		for _, bad := range []string{"Signal:", "ReportDate:", "ActionScope:", "\n## Forged"} {
			if strings.Contains(sec, bad) {
				t.Errorf("section kept invalid %q:\n%s", bad, sec)
			}
		}
	})

	t.Run("omitted-optional-fields-preserve-legacy-output", func(t *testing.T) {
		raw := proposal{
			Title:    "Legacy shape",
			Kind:     "rule",
			Target:   "project",
			File:     "AGENTS.md",
			Text:     "Run go vet.",
			Evidence: "seen twice",
		}
		got, ok := o.validProposal(raw)
		if !ok {
			t.Fatal("validProposal failed")
		}
		want := "Target: project   Kind: rule   File: AGENTS.md\n\n    Run go vet.\n\nEvidence: seen twice\n"
		if sec := o.section(got); sec != want {
			t.Errorf("legacy section = %q, want %q", sec, want)
		}
	})
}
