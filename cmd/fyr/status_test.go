package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/auto"
	"github.com/kesokaj/fyr/internal/harness"
)

func writeStatusTestJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertAutoPortsWired(t *testing.T, p auto.Ports) {
	t.Helper()
	checks := []struct {
		name string
		ok   bool
	}{
		{"CheckRepo", p.CheckRepo != nil},
		{"InstalledCommit", p.InstalledCommit != nil},
		{"ParseUsage", p.ParseUsage != nil},
		{"PickModel", p.PickModel != nil},
		{"ReadTrackedSuite", p.ReadTrackedSuite != nil},
		{"ReadSuiteFile", p.ReadSuiteFile != nil},
		{"ParseSuite", p.ParseSuite != nil},
		{"CapsuleDeps", p.CapsuleDeps != nil},
		{"ReadAgyConversations", p.ReadAgyConversations != nil},
		{"NewFence", p.NewFence != nil},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("auto.Ports.%s is nil", c.name)
		}
	}
}

func TestStatusWiresAutoPorts(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	stateFile := filepath.Join(home, harness.StateDir, "state.json")
	writeStatusTestJSON(t, stateFile, harness.State{
		Repo:        "embed",
		Commit:      "11112222",
		InstalledAt: "2026-11-28T00:00:00Z",
	})

	var (
		captured auto.Options
		called   bool
		out      bytes.Buffer
	)
	repoRoot := filepath.Join("..", "..")
	_ = statusWithAutoLine(harness.Source{FS: os.DirFS(repoRoot), Dir: repoRoot}, home, &out, func(o auto.Options) string {
		called = true
		captured = o
		return auto.StatusLine(o)
	})
	if !called {
		t.Fatal("statusWithAutoLine did not invoke autoLine callback")
	}
	if captured.Home != home {
		t.Fatalf("captured.Home = %q, want %q", captured.Home, home)
	}
	assertAutoPortsWired(t, captured.Ports)

	if got := captured.Ports.InstalledCommit(home); got != "11112222" {
		t.Fatalf("InstalledCommit(installed) = %q, want %q", got, "11112222")
	}
	if got := captured.Ports.InstalledCommit(t.TempDir()); got != "" {
		t.Fatalf("InstalledCommit(empty) = %q, want empty", got)
	}

	assertStatusInstallPendingBanner(t, home, stateFile, captured)
}

func assertStatusInstallPendingBanner(t *testing.T, home, stateFile string, opts auto.Options) {
	t.Helper()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	cfgPath := auto.ConfigPath(home)
	writeStatusTestJSON(t, cfgPath, auto.Config{Enabled: true, ConversationIdle: "1h"})
	autoDir := filepath.Dir(cfgPath)
	if filepath.Base(autoDir) != "auto" {
		autoDir = filepath.Join(autoDir, "auto")
	}
	writeStatusTestJSON(t, filepath.Join(autoDir, "runs.json"), auto.State{
		Date:              "2026-11-28",
		LastRun:           now,
		InstallPending:    true,
		InstallPendingSHA: "deadbeef",
	})

	var pendingBuf bytes.Buffer
	opts.Out = &pendingBuf
	opts.Now = func() time.Time { return now }
	if err := auto.Status(opts); err != nil {
		t.Fatalf("auto.Status(stale): %v", err)
	}
	wantBanner := "install    pending (skills/ updated at deadbeef; run 'fyr install' to deploy)"
	if !strings.Contains(pendingBuf.String(), wantBanner) {
		t.Fatalf("expected pending banner %q, got:\n%s", wantBanner, pendingBuf.String())
	}

	writeStatusTestJSON(t, stateFile, harness.State{
		Repo:        "embed",
		Commit:      "deadbeef",
		InstalledAt: "2026-11-28T00:00:00Z",
	})
	var currentBuf bytes.Buffer
	opts.Out = &currentBuf
	if err := auto.Status(opts); err != nil {
		t.Fatalf("auto.Status(current): %v", err)
	}
	if strings.Contains(currentBuf.String(), "install    pending") {
		t.Fatalf("expected pending banner cleared after matching commit, got:\n%s", currentBuf.String())
	}
}
