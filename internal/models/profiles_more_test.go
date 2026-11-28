package models

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestRunAgyChangelogAndEnrichCatalogChangelog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	script := filepath.Join(t.TempDir(), "agy")
	body := `#!/bin/sh
case "$1" in
  changelog)
    printf '\033[32m1.5.8:\033[0m\n- custom-x retiring on 2026-12-01\n'
    ;;
esac
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := RunAgyChangelog(context.Background(), ""); got != "" {
		t.Fatalf("empty agyPath changelog = %q, want empty", got)
	}
	got := RunAgyChangelog(context.Background(), script)
	if !strings.Contains(got, "1.5.8:") || strings.Contains(got, "\x1b[") {
		t.Fatalf("RunAgyChangelog = %q, want ANSI-stripped changelog", got)
	}
	cat := Catalog{Entries: []CatalogEntry{{ID: "custom-x-high", Family: "other", Tier: "pro", Status: "active"}}}
	enriched := EnrichCatalogChangelog(context.Background(), home, script, cat)
	if enriched.Entries[0].RetiringOn != "2026-12-01" || enriched.Entries[0].ChangelogNote == "" {
		t.Fatalf("EnrichCatalogChangelog did not enrich: %+v", enriched.Entries[0])
	}
	loaded, ok, err := LoadCatalog(home)
	if err != nil || !ok || loaded.Entries[0].RetiringOn != "2026-12-01" {
		t.Fatalf("LoadCatalog after EnrichCatalogChangelog = (%+v, %v, %v)", loaded, ok, err)
	}
	// 70 KiB changelog succeeds under 1 MiB cap; > 1 MiB returns empty cleanly.
	med := filepath.Join(t.TempDir(), "agy-med")
	medBody := "#!/bin/sh\nhead -c 70000 /dev/zero | tr '\\0' 'a'\n"
	if err := os.WriteFile(med, []byte(medBody), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := RunAgyChangelog(context.Background(), med); len(got) != 70000 {
		t.Fatalf("70 KiB changelog = %d bytes, want 70000", len(got))
	}
	big := filepath.Join(t.TempDir(), "agy-big")
	bigBody := "#!/bin/sh\nhead -c 1100000 /dev/zero | tr '\\0' 'a'\n"
	if err := os.WriteFile(big, []byte(bigBody), 0o755); err != nil {
		t.Fatal(err)
	}
	if over := RunAgyChangelog(context.Background(), big); over != "" {
		t.Fatalf("oversized changelog = %d bytes, want empty", len(over))
	}
}

func TestAutoPickHelpersAndListedProfiles(t *testing.T) {
	t.Parallel()
	if !IsInteractiveOnlyID("gemini-pro-orchestrator") || IsInteractiveOnlyID("gemini-4-carbon-high") {
		t.Fatal("IsInteractiveOnlyID mismatch")
	}
	ids := []string{"gemini-pro-orchestrator", "gemini-4-carbon-high", "gemini-4-hydrogen-medium"}
	entries := BuildListedCatalogEntries("", ids)
	if len(entries) != 3 || !entries[0].InteractiveOnly || entries[1].Subfamily != "carbon" {
		t.Fatalf("BuildListedCatalogEntries missing profile enrichment: %+v", entries)
	}
	listed := map[string]bool{
		"gemini-pro-orchestrator":  true,
		"gemini-4-carbon-high":     true,
		"gemini-4-hydrogen-medium": true,
	}
	picked, _, err := PickAutoModelForScope("", []string{"internal/search/x.go"}, "fix security", listed, ids)
	if err != nil || picked != "gemini-4-carbon-high" {
		t.Fatalf("PickAutoModelForScope = (%q, %v), want gemini-4-carbon-high", picked, err)
	}
	onlyOrch := map[string]bool{"gemini-pro-orchestrator": true}
	if _, _, err := PickAutoModelForScope("", []string{"a.go"}, "task", onlyOrch, []string{"gemini-pro-orchestrator"}); err == nil {
		t.Fatal("PickAutoModelForScope succeeded with only orchestrator listed")
	}
}

func TestProbeWorkDirSaveCatalogAndEvidenceSecurity(t *testing.T) {
	t.Parallel()
	if _, _, err := probeWorkDir("/"); err == nil {
		t.Fatal("probeWorkDir(/) succeeded")
	}
	explicitHome := t.TempDir()
	dir, cleanup, err := probeWorkDir(explicitHome)
	if err != nil {
		t.Fatalf("probeWorkDir(explicitHome): %v", err)
	}
	if !strings.HasPrefix(dir, filepath.Join(explicitHome, harness.StateDir, "sandbox")) {
		cleanup()
		t.Fatalf("probeWorkDir used wrong home: %s", dir)
	}
	cleanup()

	// Symlinked state dir refused by probeWorkDir, SaveCatalog, and AggregateWorkEvidence.
	symHome := t.TempDir()
	target := t.TempDir()
	stateParent := filepath.Dir(filepath.Join(symHome, harness.StateDir))
	if err := os.MkdirAll(stateParent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(symHome, harness.StateDir)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := probeWorkDir(symHome); err == nil {
		t.Fatal("probeWorkDir accepted symlinked state dir")
	}
	if err := SaveCatalog(symHome, Catalog{}); err == nil {
		t.Fatal("SaveCatalog accepted symlinked state dir")
	}

	// AggregateWorkEvidence skips symlinked work dir.
	evHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(evHome, harness.StateDir), 0o700); err != nil {
		t.Fatal(err)
	}
	evilWork := t.TempDir()
	evilRun := filepath.Join(evilWork, "r1")
	if err := os.MkdirAll(evilRun, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(evilRun, "status.json"), []byte(`{"workers":[{"model":"m1","state":"PASS"}]}`), 0o600)
	if err := os.Symlink(evilWork, filepath.Join(evHome, harness.StateDir, "work")); err != nil {
		t.Fatal(err)
	}
	if ev := AggregateWorkEvidence(evHome, nil); len(ev) != 0 {
		t.Fatalf("AggregateWorkEvidence read symlinked work dir: %+v", ev)
	}

	// RunUsage reports parent cancellation uniformly.
	pctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunUsage(pctx, "/bin/true"); err == nil || !strings.Contains(err.Error(), "ended before it answered") {
		t.Fatalf("RunUsage on canceled parent = %v, want 'ended before it answered'", err)
	}
}
