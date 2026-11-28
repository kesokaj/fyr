package auto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
)

func TestProposalHeaderTarget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		line string
		want string
	}{
		{"target harness at start", "Target: harness", "harness"},
		{"target project at start", "Target: project", "project"},
		{"scope harness at start", "scope: harness", "harness"},
		{"scope project at start", "Scope: project", "project"},
		{"project overrides harness on same line", "Target: harness Scope: project", "project"},
		{"repeated target project overrides harness", "target: harness target: project", "project"},
		{"mid-line target harness", "Meta: x target: harness", "harness"},
		{"mid-line without space ignored", "xtarget: harness", ""},
		{"empty value", "Target:   ", ""},
		{"unknown value", "Target: global", ""},
		{"unrelated header", "Class: harness-edit", ""},
	}
	for _, tc := range cases {
		if got := proposalHeaderTarget(tc.line); got != tc.want {
			t.Errorf("%s: proposalHeaderTarget(%q) = %q, want %q", tc.name, tc.line, got, tc.want)
		}
	}
}

func TestHasProposalEvidence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"evidence header", "Target: harness\nEvidence: repeated failure in 3 runs\n", true},
		{"empty evidence header", "Target: harness\nEvidence:   \n", false},
		{"valid signal_id", "signal_id: sig:global:search_grep_low\n", true},
		{"invalid signal_id", "signal_id: not-a-signal\n", false},
		{"sessions ge 2", "sessions: 2,\n", true},
		{"sessions eq 1", "sessions: 1\n", false},
		{"record reference BUG", "See BUG-123 for details.\n", true},
		{"PROPOSAL self reference does not count", "See PROPOSAL-2026-11-28-001 for details.\n", false},
		{"indented evidence ignored", "  Evidence: quoted\n\tBUG-123\n  sessions: 5\n", false},
		{"CRLF normalized", "Target: harness\r\nSessions: 3;\r\n", true},
		{"no evidence", "Target: harness\nJust a suggestion.\n", false},
	}
	for _, tc := range cases {
		if got := hasProposalEvidence(tc.body); got != tc.want {
			t.Errorf("%s: hasProposalEvidence = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestScoreProposalImpact(t *testing.T) {
	t.Parallel()
	rec := brain.Record{
		ID:      "PROPOSAL-2026-11-28-001",
		Updated: "2026-11-28T10:00:00Z",
		Body: strings.Join([]string{
			"Sessions: 2,",
			"Sessions: 5;",
			"  Sessions: 99",
			"signal_id: sig:global:a",
			"signal: sig:global:b",
			"Refs BUG-101, ADR-042, BUG-101, PROPOSAL-2026-11-28-001",
			"  BUG-999",
		}, "\r\n"),
	}
	got := scoreProposalImpact(rec, 3)
	if got.sessions != 5 {
		t.Errorf("sessions = %d, want 5", got.sessions)
	}
	// 2 unique non-PROPOSAL record refs (BUG-101, ADR-042) + 2 signal lines = 4.
	if got.signals != 4 {
		t.Errorf("signals = %d, want 4", got.signals)
	}
	if got.targets != 3 {
		t.Errorf("targets = %d, want 3", got.targets)
	}
	wantWhen := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	if !got.when.Equal(wantWhen) {
		t.Errorf("when = %v, want %v", got.when, wantWhen)
	}
}

func TestInstallPendingBannerCases(t *testing.T) {
	t.Parallel()
	t.Run("not pending", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		if got := installPendingBanner(Options{Home: home}, State{}); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})
	t.Run("pending without sha", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		got := installPendingBanner(Options{Home: home}, State{InstallPending: true})
		if !strings.Contains(got, "skills/ updated;") || !strings.Contains(got, "fyr install") {
			t.Fatalf("got %q, want generic pending banner", got)
		}
	})
	t.Run("pending with sha cleared when installed matches prefix", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		stPath := filepath.Join(home, harness.StateDir, "state.json")
		if err := os.MkdirAll(filepath.Dir(stPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := harness.SaveState(home, harness.State{Commit: "abcdef1234567890"}); err != nil {
			t.Fatal(err)
		}
		if got := installPendingBanner(Options{Home: home}, State{InstallPending: true, InstallPendingSHA: "abcdef1"}); got != "" {
			t.Fatalf("matching installed prefix returned %q, want empty", got)
		}
		got := installPendingBanner(Options{Home: home}, State{InstallPending: true, InstallPendingSHA: "9999999"})
		if !strings.Contains(got, "updated at 9999999") {
			t.Fatalf("mismatched sha returned %q, want updated at 9999999", got)
		}
	})
}
