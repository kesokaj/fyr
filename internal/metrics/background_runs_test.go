package metrics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBackgroundRunsHaltReasonPersistenceAndScrub(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	secretTok := strings.Join([]string{"gh", "p_", "BGRUNSECRET1234567890ABCDEF"}, "")

	rows := []BackgroundRunRow{
		{
			TS: now.Add(-2 * time.Hour).UnixMilli(), Kind: "auto", Outcome: "halted",
			HaltReason: "BUDGET_FLOOR", DurationMS: 950, ProjectsChecked: 2,
		},
		{
			TS: now.Add(-1 * time.Hour).UnixMilli(), Kind: "dream", Outcome: "pass",
			DurationMS: 410, ProjectsChecked: 1, RatingsWritten: 3,
		},
		{
			TS: now.Add(-30 * time.Minute).UnixMilli(), Kind: "auto", Outcome: "fail",
			HaltReason: "token=" + secretTok, DurationMS: 220,
		},
		{
			TS: now.Add(-15 * time.Minute).UnixMilli(), Kind: "auto", Outcome: "halted",
			HaltReason: "dirty_tree in /Users/alice/work/app\x1b[2J", DurationMS: 120,
		},
	}
	for _, r := range rows {
		if err := RecordBackgroundRun(home, r); err != nil {
			t.Fatalf("RecordBackgroundRun(%+v): %v", r, err)
		}
	}

	s := openT(t, Path(home))
	got, err := s.QueryBackgroundRuns(now.Add(-3*time.Hour), now)
	if err != nil {
		t.Fatalf("QueryBackgroundRuns: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4", len(got))
	}
	// Newest first: index 0 is -15m, 1 is -30m, 2 is -1h, 3 is -2h.
	if got[3].HaltReason != "BUDGET_FLOOR" {
		t.Errorf("got[3].HaltReason = %q, want BUDGET_FLOOR", got[3].HaltReason)
	}
	if got[2].HaltReason != "" {
		t.Errorf("got[2].HaltReason = %q, want empty", got[2].HaltReason)
	}
	if got[1].HaltReason != "" || strings.Contains(got[1].HaltReason, secretTok) {
		t.Errorf("got[1].HaltReason = %q, want empty after secret redaction", got[1].HaltReason)
	}
	if strings.Contains(got[0].HaltReason, "/Users/alice") || strings.Contains(got[0].HaltReason, "\x1b") || !strings.Contains(got[0].HaltReason, "~/work/app") {
		t.Errorf("got[0].HaltReason = %q, want scrubbed path without escape bytes", got[0].HaltReason)
	}

	// Empty HaltReason must be omitted from JSON; non-empty must use snake_case key.
	passJSON, _ := json.Marshal(got[2])
	if strings.Contains(string(passJSON), `"halt_reason"`) {
		t.Errorf("empty HaltReason not omitted: %s", passJSON)
	}
	haltJSON, _ := json.Marshal(got[3])
	if !strings.Contains(string(haltJSON), `"halt_reason":"BUDGET_FLOOR"`) {
		t.Errorf("non-empty HaltReason missing from JSON: %s", haltJSON)
	}
}

func TestIsValidShapeOutcome(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"pass", true},
		{"halted_ok-1", true},
		{"a0_-z9", true},
		{strings.Repeat("a", 32), true},
		{"", false},
		{"Pass", false},
		{"bad/char", false},
		{"bad space", false},
		{strings.Repeat("a", 33), false},
	}
	for _, tc := range cases {
		if got := isValidShapeOutcome(tc.in); got != tc.want {
			t.Errorf("isValidShapeOutcome(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
