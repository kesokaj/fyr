package auto

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

func TestIsEphemeralPath(t *testing.T) {
	ephemeral := []string{
		"/home/u/.gemini/antigravity-cli/worktrees/dbf618ed/subagent-1",
		"/home/u/workspace/fyr/.git/worktrees/wt1",
		"/tmp/real-home-smoke-20261121",
		"/home/u/real-home-smoke-abc/repo",
		"/tmp/fyr-smoke-123",
		"/home/u/.config/fyr/dev/fyr-smoke-42/app",
		"/home/u/.config/fyr/dev/smoke-1",
		"/home/u/.config/fyr/dev/run/smoke-abc/repo",
		"/home/u/sandbox/smoke-2",
		"/home/u/sandbox/nested/smoke-test/repo",
		"/home/u/sandbox/fyr-eval/trial-1",
		"/home/u/sandbox/fyr-eval",
		"/home/u/sandbox/fyr-auto/wt-1",
		"/home/u/sandbox/fyr-auto",
		"/home/u/workspace/fyr/.agents/scratch/land-wt-123",
		"/home/u/workspace/fyr/.agents/scratch/e2e-home",
		"/home/u/.config/fyr/auto/wt-foo",
		"/home/u/.config/fyr/sandbox/run-1",
		"/home/u/.config/fyr/eval/row-1",
	}
	for _, p := range ephemeral {
		if !IsEphemeralPath(p) {
			t.Errorf("IsEphemeralPath(%q) = false, want true", p)
		}
	}

	normal := []string{
		"",
		".",
		"/",
		search.GlobalWorkspace,
		"/home/u/workspace/fyr",
		"/home/u/code/my-app",
		"/home/u/code/smoke-detector",
		"/home/u/code/worktrees",
		"/home/u/code/my-worktrees/repo",
		"/home/u/sandbox/my-real-project",
		"/home/u/workspace/fyr/.agents",
		"/lost",
		"/gone-00",
		"/tmp/TestAutoRun12345/001",
	}
	for _, p := range normal {
		if IsEphemeralPath(p) {
			t.Errorf("IsEphemeralPath(%q) = true, want false", p)
		}
	}
}

func TestPruneGoneProjectsDropsEphemeralImmediatelyAndKeepsNormalTTL(t *testing.T) {
	now := runAt
	ephemeralGone := []string{
		"/home/u/.gemini/antigravity-cli/worktrees/conv1/subagent-a",
		"/tmp/real-home-smoke-1",
		"/tmp/fyr-smoke-2",
		"/home/u/.config/fyr/dev/smoke-3",
		"/home/u/sandbox/smoke-4",
		"/home/u/sandbox/fyr-eval/trial-9",
		"/home/u/sandbox/fyr-auto/wt-9",
		"/home/u/repo/.agents/scratch/land-wt-9",
	}
	projects := map[string]Project{
		"/active":        {Verdict: "health: no findings", LastRun: now},
		"/normal-fresh":  {Verdict: "skipped: gone", LastRun: now, GoneSince: now.Add(-6 * 24 * time.Hour)},
		"/normal-expire": {Verdict: "skipped: gone", LastRun: now, GoneSince: now.Add(-(7*24*time.Hour + time.Minute))},
	}
	for _, e := range ephemeralGone {
		projects[e] = Project{Verdict: "skipped: gone", LastRun: now, GoneSince: now}
	}

	pruneGoneProjects(projects, now)

	for _, e := range ephemeralGone {
		if _, ok := projects[e]; ok {
			t.Errorf("gone ephemeral path %s was not immediately pruned (0d TTL)", e)
		}
	}
	if _, ok := projects["/normal-fresh"]; !ok {
		t.Errorf("normal gone repo <7d old was prematurely pruned")
	}
	if _, ok := projects["/normal-expire"]; ok {
		t.Errorf("normal gone repo >7d old was not pruned")
	}
	if _, ok := projects["/active"]; !ok {
		t.Errorf("active repo was unexpectedly pruned")
	}
}

func TestSweepSkipsEphemeralPathsInDryRunAndPrunesState(t *testing.T) {
	home := t.TempDir()
	realHome := t.TempDir()
	writeConfig(t, home, validConfig)

	realRepo := idleProject(t)
	goneWT := filepath.Join(realHome, ".gemini", "antigravity-cli", "worktrees", "conv-1", "subagent-dead")
	goneSmoke := filepath.Join(realHome, harness.StateDir, "dev", "smoke-dead")
	goneRealSmoke := "/nonexistent/real-home-smoke-dead"
	goneScratch := filepath.Join(realRepo, ".agents", "scratch", "land-wt-dead")

	seedSweepConversations(t, realHome, []struct {
		id       string
		wsJSON   string
		modified time.Time
		text     string
	}{
		{"conv-real", `["file://` + realRepo + `"]`, runAt.Add(-10 * time.Minute), "real repo"},
		{"conv-wt", `["file://` + goneWT + `"]`, runAt.Add(-10 * time.Minute), "dead worktree"},
		{"conv-smoke", `["file://` + goneSmoke + `"]`, runAt.Add(-10 * time.Minute), "dead smoke"},
		{"conv-real-smoke", `["file://` + goneRealSmoke + `"]`, runAt.Add(-10 * time.Minute), "dead real smoke"},
		{"conv-scratch", `["file://` + goneScratch + `"]`, runAt.Add(-10 * time.Minute), "dead scratch"},
	})

	// Pre-seed runs.json with legacy gone ephemeral entries as if from an older fyr version.
	if err := ensureDir(home); err != nil {
		t.Fatal(err)
	}
	gone := Project{Verdict: "skipped: gone", LastRun: runAt.Add(-time.Hour), GoneSince: runAt.Add(-time.Hour)}
	if err := saveState(home, State{
		Date:     runAt.Format("2006-01-02"),
		Projects: map[string]Project{goneWT: gone, goneRealSmoke: gone, "/normal-old": gone},
	}); err != nil {
		t.Fatal(err)
	}

	runRealTargets := func(t *testing.T, dry bool) string {
		t.Helper()
		var out bytes.Buffer
		o := runOpts(home, &out)
		o.RealHome = realHome
		o.Env = &realTargetsEnv{fakeEnv: &fakeEnv{usage: usageText("90%", "90%")}, targetHome: realHome}
		if err := Run(o, dry); err != nil {
			t.Fatalf("Run(dry=%v): %v\n%s", dry, err, out.String())
		}
		return out.String()
	}

	t.Run("dry-run skips ephemeral entries without skipped: gone noise and writes nothing", func(t *testing.T) {
		before := treeOf(t, home)
		got := runRealTargets(t, true)
		if strings.Contains(got, "skipped: gone") || !strings.Contains(got, "skipped 4 ephemeral workspace(s)") || !strings.Contains(got, "1 of 1 projects checked") {
			t.Errorf("unexpected dry-run output:\n%s", got)
		}
		if treeOf(t, home) != before {
			t.Errorf("dry-run mutated home directory")
		}
	})

	t.Run("real sweep prunes seeded ephemeral entries from runs.json immediately", func(t *testing.T) {
		got := runRealTargets(t, false)
		if strings.Contains(got, "skipped: gone") || !strings.Contains(got, "1 of 1 projects checked") {
			t.Errorf("unexpected real sweep output:\n%s", got)
		}
		st, _, err := loadState(home)
		if err != nil {
			t.Fatal(err)
		}
		for _, dead := range []string{goneWT, goneSmoke, goneRealSmoke, goneScratch} {
			if _, ok := st.Projects[dead]; ok {
				t.Errorf("ephemeral path %s survived in runs.json: %+v", dead, st.Projects[dead])
			}
		}
		if _, ok := st.Projects[realRepo]; !ok {
			t.Errorf("real repo missing from runs.json: %+v", st.Projects)
		}
	})

	t.Run("normal gone repo stays visible while ephemeral gone repo is skipped", func(t *testing.T) {
		home2 := t.TempDir()
		writeConfig(t, home2, validConfig)
		var out bytes.Buffer
		o := runOpts(home2, &out)
		o.Env = &fakeEnv{
			targets: []dream.Target{
				{Root: "/lost-normal", Skip: "gone"},
				{Root: "/home/u/.gemini/antigravity-cli/worktrees/c1/sub-1", Skip: "gone"},
				{Root: realRepo},
			},
			usage: usageText("90%", "90%"),
		}
		if err := Run(o, false); err != nil {
			t.Fatalf("Run: %v\n%s", err, out.String())
		}
		st, _, _ := loadState(home2)
		if _, ok := st.Projects["/lost-normal"]; !ok {
			t.Errorf("normal gone repo should stay for 7d TTL")
		}
		if _, ok := st.Projects["/home/u/.gemini/antigravity-cli/worktrees/c1/sub-1"]; ok {
			t.Errorf("ephemeral worktree should not enter runs.json")
		}
		if !strings.Contains(out.String(), "/lost-normal: skipped: gone") || !strings.Contains(out.String(), "1 of 2 projects checked") {
			t.Errorf("unexpected output:\n%s", out.String())
		}
	})
}
