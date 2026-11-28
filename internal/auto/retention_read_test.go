package auto

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

func TestAutoEnabledMissingValidAndCorrupt(t *testing.T) {
	t.Parallel()

	t.Run("missing does not create", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		on, err := Enabled(home)
		if err != nil || on {
			t.Fatalf("Enabled(missing) = (%v, %v), want (false, nil)", on, err)
		}
		if _, err := os.Stat(ConfigPath(home)); !os.IsNotExist(err) {
			t.Fatalf("Enabled created auto.json: %v", err)
		}
		if _, err := os.Stat(filepath.Join(home, harness.StateDir)); !os.IsNotExist(err) {
			t.Fatalf("Enabled created state dir: %v", err)
		}
	})

	t.Run("enabled true and false", func(t *testing.T) {
		t.Parallel()
		homeOn := t.TempDir()
		writeConfig(t, homeOn, `{"enabled": true, "conversation_idle": "1h"}`)
		if on, err := Enabled(homeOn); err != nil || !on {
			t.Fatalf("Enabled(on) = (%v, %v), want (true, nil)", on, err)
		}

		homeOff := t.TempDir()
		writeConfig(t, homeOff, `{"enabled": false, "conversation_idle": "1h"}`)
		if on, err := Enabled(homeOff); err != nil || on {
			t.Fatalf("Enabled(off) = (%v, %v), want (false, nil)", on, err)
		}
	})

	t.Run("corrupt fails closed", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		writeConfig(t, home, `{"enabled": "yes"}`)
		on, err := Enabled(home)
		if err == nil || on {
			t.Fatalf("Enabled(corrupt) = (%v, %v), want (false, non-nil err)", on, err)
		}
	})
}

func TestProjectRetroWatermarkMissingValidAndCorrupt(t *testing.T) {
	t.Parallel()

	t.Run("missing does not create", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		retro, pending, mem, err := ProjectRetroWatermark(home, "/work/repo")
		if err != nil || !retro.IsZero() || pending || !mem.IsZero() {
			t.Fatalf("missing = (%v, %v, %v, %v), want zero/false/zero/nil", retro, pending, mem, err)
		}
		if _, err := os.Stat(dir(home)); !os.IsNotExist(err) {
			t.Fatalf("ProjectRetroWatermark created auto dir: %v", err)
		}
	})

	t.Run("corrupt fails closed", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		if err := os.MkdirAll(dir(home), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir(home), "runs.json"), []byte(`{bad json`), 0o600); err != nil {
			t.Fatal(err)
		}
		retro, pending, mem, err := ProjectRetroWatermark(home, "/work/repo")
		if err == nil || !retro.IsZero() || pending || !mem.IsZero() {
			t.Fatalf("corrupt = (%v, %v, %v, %v), want error", retro, pending, mem, err)
		}
	})

	t.Run("valid projects and normalization", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		if err := os.MkdirAll(dir(home), 0o700); err != nil {
			t.Fatal(err)
		}
		retroTime := time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)
		memTime := time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC)
		globalRetro := time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC)
		st := State{
			Date: "2026-11-28",
			Projects: map[string]Project{
				"/work/repo": {
					LastRetroLite:           retroTime,
					PendingMemoryCandidates: 3,
					LastMemoryCandidates:    memTime,
				},
				search.GlobalWorkspace: {
					LastRetroLite:           globalRetro,
					PendingMemoryCandidates: 0,
				},
			},
		}
		if err := saveState(home, st); err != nil {
			t.Fatal(err)
		}

		retro, pending, mem, err := ProjectRetroWatermark(home, "/work/repo/.")
		if err != nil || !retro.Equal(retroTime) || !pending || !mem.Equal(memTime) {
			t.Fatalf("repo = (%v, %v, %v, %v), want (%v, true, %v, nil)", retro, pending, mem, err, retroTime, memTime)
		}

		gRetro, gPending, gMem, err := ProjectRetroWatermark(home, search.GlobalWorkspace)
		if err != nil || !gRetro.Equal(globalRetro) || gPending || !gMem.IsZero() {
			t.Fatalf("global = (%v, %v, %v, %v), want (%v, false, zero, nil)", gRetro, gPending, gMem, err, globalRetro)
		}

		uRetro, uPending, uMem, err := ProjectRetroWatermark(home, "/work/other")
		if err != nil || !uRetro.IsZero() || uPending || !uMem.IsZero() {
			t.Fatalf("unknown = (%v, %v, %v, %v), want zero/false/zero/nil", uRetro, uPending, uMem, err)
		}
	})
}

func TestIsEphemeralOrRefused(t *testing.T) {
	t.Parallel()
	home := "/home/alice"
	refused := []string{
		"/",
		"/tmp",
		"/private/tmp",
		home,
		"/home/alice/sandbox/fyr-eval/case1",
		"/home/alice/sandbox/fyr-auto/run1",
		"/home/alice/worktrees/wt-1",
		"/home/alice/project/.agents/scratch/tmp",
		"/home/alice/.config/fyr/eval/work",
	}
	for _, p := range refused {
		if !IsEphemeralOrRefused(p, home) {
			t.Errorf("IsEphemeralOrRefused(%q, %q) = false, want true", p, home)
		}
	}
	allowed := []string{
		"/home/alice/src/myrepo",
		search.GlobalWorkspace,
		"",
	}
	for _, p := range allowed {
		if IsEphemeralOrRefused(p, home) {
			t.Errorf("IsEphemeralOrRefused(%q, %q) = true, want false", p, home)
		}
	}
}
