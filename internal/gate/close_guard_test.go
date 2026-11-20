package gate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
)

func closeDateNow(year, month, day int) func() time.Time {
	return func() time.Time {
		return time.Date(year, time.Month(month), day, 12, 0, 0, 0, time.UTC)
	}
}

func assertCloseOutcome(t *testing.T, err error, wantRefuse bool, wantSubstrs ...string) {
	t.Helper()
	if wantRefuse {
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("expected ErrRefused, got %v", err)
		}
		for _, sub := range wantSubstrs {
			if !strings.Contains(err.Error(), sub) {
				t.Fatalf("error %q missing %q", err.Error(), sub)
			}
		}
		return
	}
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestCloseKickoffGuardTagsAndDates(t *testing.T) {
	ctx := context.Background()
	repo := initTestGoRepo(t)
	runGit(t, repo, "tag", "v9.2.0")
	nov20 := closeDateNow(2026, 11, 20)
	nov23 := closeDateNow(2026, 11, 23)
	if _, err := Verify(ctx, VerifyOptions{Dir: repo, Home: t.TempDir(), Now: nov20}); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	cases := []struct {
		name       string
		now        func() time.Time
		body       string
		wantRefuse bool
		wantSub    []string
	}{
		{
			name: "no kickoff section passes",
			now:  nov20,
			body: "Summary line only.\n\n## Next Steps\n\n1. Run /fyr-preflight v9.2.0 later.\n",
		},
		{
			name: "never inline clause hides existing tag and future date",
			now:  nov20,
			body: "## Kickoff\n\n```text\nProject: /repo. Do Phase 1.\nStanding approvals (closed set): tests. Never: /fyr-preflight v9.2.0; earliest 2026-11-23.\nDone when: go test passes.\n```\n",
		},
		{
			name: "never multiline bullet list hides existing tag and future date",
			now:  nov20,
			body: "## Kickoff\n\nNever:\n- /fyr-preflight v9.2.0\n- earliest 2026-11-23\nDone when: go test passes.\n",
		},
		{
			name:       "never on same line before Done when still checks Done when",
			now:        nov20,
			body:       "## Kickoff\n\nStanding approvals: tests. Never: /fyr-preflight v9.3.0. Done when: /fyr-preflight v9.2.0\n",
			wantRefuse: true,
			wantSub:    []string{"v9.2.0", "git tag"},
		},
		{
			name:       "actionable line after standalone Never is not swallowed",
			now:        nov20,
			body:       "## Kickoff\n\nNever: /fyr-preflight v9.3.0\n/fyr-preflight v9.2.0\n",
			wantRefuse: true,
			wantSub:    []string{"v9.2.0"},
		},
		{
			name: "quoted prose tag passes",
			now:  nov20,
			body: "## Kickoff\n\nDone when: note \"newest tag v9.2.0,\" and \"release tag v9.2.0\" without tagging.\n",
		},
		{
			name:       "actionable preflight existing tag refuses",
			now:        nov20,
			body:       "## Kickoff\n\nDone when: run `/fyr-preflight v9.2.0`.\n",
			wantRefuse: true,
			wantSub:    []string{"v9.2.0", "git tag"},
		},
		{
			name:       "actionable git tag -a existing refuses",
			now:        nov20,
			body:       "## Kickoff\n\nDone when: git tag -a v9.2.0\n",
			wantRefuse: true,
			wantSub:    []string{"v9.2.0"},
		},
		{
			name:       "actionable cut existing tag refuses",
			now:        nov20,
			body:       "## Kickoff\n\nDone when: cut v9.2.0\n",
			wantRefuse: true,
			wantSub:    []string{"v9.2.0"},
		},
		{
			name:       "actionable release tag existing refuses",
			now:        nov20,
			body:       "## Kickoff\n\nDone when: release tag v9.2.0\n",
			wantRefuse: true,
			wantSub:    []string{"v9.2.0"},
		},
		{
			name: "actionable preflight untagged passes",
			now:  nov20,
			body: "## Kickoff\n\nDone when: run `/fyr-preflight v9.3.0`.\n",
		},
		{
			name:       "non-never future earliest date refuses",
			now:        nov20,
			body:       "## Kickoff\n\nDone when: execute phase (earliest 2026-11-23).\n",
			wantRefuse: true,
			wantSub:    []string{"2026-11-23", "2026-11-20"},
		},
		{
			name:       "past earliest followed by future earliest refuses",
			now:        nov20,
			body:       "## Kickoff\n\nDone when: step A (earliest 2026-11-10) and step B (earliest 2026-11-23).\n",
			wantRefuse: true,
			wantSub:    []string{"2026-11-23", "2026-11-20"},
		},
		{
			name: "non-never earliest date equal to today passes",
			now:  nov23,
			body: "## Kickoff\n\nDone when: execute phase (earliest 2026-11-23).\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			_, err := Close(ctx, CloseOptions{
				Dir:   repo,
				Home:  home,
				Title: "guard-test",
				Body:  tc.body,
				Now:   tc.now,
			})
			assertCloseOutcome(t, err, tc.wantRefuse, tc.wantSub...)
		})
	}
}

func TestCloseKickoffGuardOpenPlanEarliest(t *testing.T) {
	ctx := context.Background()
	repo := initTestGoRepo(t)
	home := t.TempDir()
	nov20 := closeDateNow(2026, 11, 20)
	nov23 := closeDateNow(2026, 11, 23)
	if _, err := Verify(ctx, VerifyOptions{Dir: repo, Home: home, Now: nov20}); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	openBody := "# Plan — release schedule\n\n## Phases\n\n" +
		"### Phase 1 — Setup\n- [ ] Prepare fixtures\n\n" +
		"### Phase 7 — Release (after: Phase 1)\n- [ ] Cut v9.3.0 release — verify: go test (earliest 2026-11-23)\n"
	planID, err := brain.Add(
		brain.Options{Home: home, Dir: repo, Now: nov20},
		brain.AddInput{Kind: "plan", Title: "Release schedule", Body: strings.NewReader(openBody)},
	)
	if err != nil {
		t.Fatalf("seed plan: %v", err)
	}

	t.Run("refuses when kickoff schedules future phase by phase label", func(t *testing.T) {
		body := "## Kickoff\n\nProject: /repo. Run fyr resume (plan " + planID + "); do Phase 7 only.\n"
		_, err := Close(ctx, CloseOptions{Dir: repo, Home: home, Title: "p7", Body: body, Now: nov20})
		assertCloseOutcome(t, err, true, planID, "Phase 7", "2026-11-23", "2026-11-20")
	})

	t.Run("refuses when kickoff schedules future phase by box text", func(t *testing.T) {
		body := "## Kickoff\n\nProject: /repo. Run fyr resume (plan " + planID + "); do: Cut v9.3.0 release.\n"
		_, err := Close(ctx, CloseOptions{Dir: repo, Home: home, Title: "box", Body: body, Now: nov20})
		assertCloseOutcome(t, err, true, planID, "Phase 7", "2026-11-23", "2026-11-20")
	})

	t.Run("passes when kickoff schedules earlier phase without future date", func(t *testing.T) {
		body := "## Kickoff\n\nProject: /repo. Run fyr resume (plan " + planID + "); do Phase 1 — Setup only.\nNever: Phase 7.\n"
		_, err := Close(ctx, CloseOptions{Dir: repo, Home: home, Title: "p1", Body: body, Now: nov20})
		assertCloseOutcome(t, err, false)
	})

	t.Run("passes when today reaches earliest date", func(t *testing.T) {
		body := "## Kickoff\n\nProject: /repo. Run fyr resume (plan " + planID + "); do Phase 7 only.\n"
		_, err := Close(ctx, CloseOptions{Dir: repo, Home: home, Title: "p7-ready", Body: body, Now: nov23})
		assertCloseOutcome(t, err, false)
	})

	t.Run("passes when future box is already ticked", func(t *testing.T) {
		ticked := strings.Replace(openBody, "- [ ] Cut v9.3.0 release", "- [x] Cut v9.3.0 release", 1)
		if err := brain.Edit(brain.Options{Home: home, Dir: repo, Now: nov20}, brain.EditInput{ID: planID, Body: strings.NewReader(ticked)}); err != nil {
			t.Fatalf("edit plan: %v", err)
		}
		body := "## Kickoff\n\nProject: /repo. Run fyr resume (plan " + planID + "); do Phase 7 only.\n"
		_, err := Close(ctx, CloseOptions{Dir: repo, Home: home, Title: "p7-ticked", Body: body, Now: nov20})
		assertCloseOutcome(t, err, false)
	})
}
