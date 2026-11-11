package work

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/sandbox"
)

// TestWorkWavesRecordsStageSeconds verifies Phase 7 (D17, D25):
//  1. Each worker records setup_s, agy_s and check_s in Result, persists them
//     to <home>/.config/fyr/work/<stamp>/status.json, and feeds
//     AggregateWorkEvidence.
//  2. Wave-once seedRecords exports the lead's brain.db once across a
//     multi-worker DB-only wave while seeding every worker's sandbox brain.db.
//  3. A transient non-cancelled export error on the first worker is not cached,
//     allowing sibling workers to retry and seed cleanly.
func TestWorkWavesRecordsStageSeconds(t *testing.T) {
	r := newRig(t)
	r.git("-C", r.lead, "rm", "-q", ".agents/trail.jsonl")
	r.git("-C", r.lead, "commit", "-qm", "db-only")

	exportLog := filepath.Join(r.ctl, "export-repo.log")
	installCountingSelf(t, r, exportLog, "")

	r.on("models-list", "gemini-4-carbon-high\tGemini 4 Carbon (High)\n")
	r.on("change", "")
	r.on("noexport", "")
	r.on("file-w-a", "src/a.txt")
	r.on("file-w-b", "pkg/b.go")
	r.on("slow-w-a", "1.1")
	r.on("slow-w-b", "1.1")

	ka := writeStageKickoff(t, r, "w-a", "src/a.txt", "gemini-4-carbon-high",
		"test -f src/a.txt && grep -q work src/a.txt && sleep 1.1")
	kb := writeStageKickoff(t, r, "w-b", "pkg/b.go", "gemini-4-carbon-high",
		"test -f pkg/b.go && grep -q work pkg/b.go && sleep 1.1")

	opts := r.opts()
	opts.MaxWorkers = 2
	results, err := Run(context.Background(), opts, ka, kb)
	if err != nil {
		t.Fatalf("Run refused: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	for _, res := range results {
		wantReasons(t, res)
		if res.Verdict != "PASS" {
			t.Fatalf("%s verdict = %s (%v)\n%s", res.Name, res.Verdict, res.Reasons, r.out.String())
		}
		if res.SetupSec < 1 || res.AgySec < 1 || res.CheckSec < 1 {
			t.Errorf("%s stage seconds = setup=%d agy=%d check=%d, want each >= 1",
				res.Name, res.SetupSec, res.AgySec, res.CheckSec)
		}
	}

	// Verify status.json persisted setup_s, agy_s and check_s for both workers.
	stamp := fmt.Sprintf("2026-10-29-180000-%d", os.Getpid())
	statusPath := filepath.Join(sandbox.Root(r.home, "work"), stamp, "status.json")
	rawStatus, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read status.json: %v", err)
	}
	var wave WaveStatus
	if err := json.Unmarshal(rawStatus, &wave); err != nil {
		t.Fatalf("unmarshal status.json: %v\n%s", err, rawStatus)
	}
	if len(wave.Workers) != 2 {
		t.Fatalf("status.json workers = %d, want 2", len(wave.Workers))
	}
	for _, ws := range wave.Workers {
		if ws.SetupSec < 1 || ws.AgySec < 1 || ws.CheckSec < 1 {
			t.Errorf("status.json %s: setup_s=%d agy_s=%d check_s=%d, want each >= 1\n%s",
				ws.Name, ws.SetupSec, ws.AgySec, ws.CheckSec, rawStatus)
		}
	}

	ev := AggregateWorkEvidence(r.home, nil)
	me := ev["gemini-4-carbon-high"]
	if me.WorkRuns != 2 || me.WorkPass != 2 || me.WorkMeanSeconds < 3 {
		t.Errorf("AggregateWorkEvidence = %+v, want 2/2 runs with mean >= 3s", me)
	}

	// Wave-once seedRecords: exactly 1 `fyr record export --repo ...` ran across 2 workers.
	expBytes, err := os.ReadFile(exportLog)
	if err != nil {
		t.Fatalf("read export log: %v", err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(expBytes)), "\n")); got != 1 {
		t.Errorf("wave-once seedRecords ran %d repo exports across 2 workers, want 1:\n%s", got, expBytes)
	}

	// Subtest: a transient non-cancelled export failure on worker 1 is NOT cached,
	// so worker 2 retries the export, seeds its brain.db, and enforces base-record scope.
	t.Run("transient_export_error_retries", func(t *testing.T) {
		r2 := newRig(t)
		r2.git("-C", r2.lead, "rm", "-q", ".agents/trail.jsonl")
		r2.git("-C", r2.lead, "commit", "-qm", "db-only")
		// Ensure RealHome has no brain.db so worker 1 has a single candidate store that fails once.
		_ = os.RemoveAll(filepath.Join(r2.real, harness.StateDir))

		expLog2 := filepath.Join(r2.ctl, "export-retry.log")
		failOnce := filepath.Join(r2.ctl, "fail-first-export")
		put(t, failOnce, "1")
		installCountingSelf(t, r2, expLog2, failOnce)

		r2.on("noexport", "")
		r2.on("file-w-1", "src/a.txt")
		r2.on("file-w-2", "pkg/b.go")
		// Have worker 2 attempt an ungranted edit to the seeded base record r2.guide:
		// if worker 2 retried and seeded baseRecords, checkRecords catches it as ReasonOutside!
		r2.on("edit-record", r2.guide)

		k1 := filepath.Join(r2.kick, "w-1.md")
		put(t, k1, "Project: "+r2.lead+".\nFiles: src/a.txt\nStanding approvals: commit.\n")
		k2 := filepath.Join(r2.kick, "w-2.md")
		put(t, k2, "Project: "+r2.lead+".\nFiles: pkg/b.go\nStanding approvals: commit.\n")

		o2 := r2.opts()
		o2.MaxWorkers = 1 // serialize w-1 then w-2 deterministically
		res2, err := Run(context.Background(), o2, k1, k2)
		if err != nil {
			t.Fatalf("Run refused: %v", err)
		}
		expBytes2, _ := os.ReadFile(expLog2)
		if got := len(strings.Split(strings.TrimSpace(string(expBytes2)), "\n")); got != 2 {
			t.Fatalf("after transient export error, repo exports = %d, want 2 (retry on w-2):\n%s", got, expBytes2)
		}
		wantReasons(t, res2[1], ReasonOutside)
	})

	// Cancelled context caches immediately without retrying on subsequent calls.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	wsCancel := &waveSeed{}
	if _, _, ok := wsCancel.load(cctx, "/nonexistent/fyr", t.TempDir(), r.lead, r.home); ok || !wsCancel.cached {
		t.Errorf("cancelled waveSeed: ok=%v cached=%v, want ok=false cached=true", ok, wsCancel.cached)
	}
}

func writeStageKickoff(t *testing.T, r *rig, name, files, model, baseCheck string) string {
	t.Helper()
	p := filepath.Join(r.kick, name+".md")
	body := fmt.Sprintf("Project: %s. Do phase.\nFiles: %s\nModel: %s\nBase-Check: %s\nStanding approvals: commit.\n",
		r.lead, files, model, baseCheck)
	put(t, p, body)
	return p
}

// installCountingSelf installs a fakeSelf whose installed sandbox fyr wrapper
// sleeps 1.1s during install (making setup_s >= 1), logs every `record export --repo`
// invocation to exportLog, and optionally fails once when failOnceFile exists.
func installCountingSelf(t *testing.T, r *rig, exportLog, failOnceFile string) {
	t.Helper()
	realBin := realFyr(t)
	wrappedFyr := fmt.Sprintf(`#!/bin/bash
if [ "$1" = "record" ] && [ "$2" = "export" ] && [ "$3" = "--repo" ]; then
  echo call >> %s
  if [ -n %s ] && [ -e %s ]; then
    rm -f %s
    echo "transient sqlite busy" >&2
    exit 1
  fi
fi
exec %s "$@"
`, sandbox.ShellQuote(exportLog), sandbox.ShellQuote(failOnceFile), sandbox.ShellQuote(failOnceFile),
		sandbox.ShellQuote(failOnceFile), sandbox.ShellQuote(realBin))
	wrapPath := filepath.Join(r.bin, "wrapped-fyr")
	put(t, wrapPath, wrappedFyr)
	if err := os.Chmod(wrapPath, 0o755); err != nil {
		t.Fatal(err)
	}

	selfScript := fmt.Sprintf(`#!/bin/bash
case "$1" in
install)
  sleep 1.1
  mkdir -p "$3/.local/bin"
  cp %s "$3/.local/bin/fyr"
  chmod 0755 "$3/.local/bin/fyr"
  ;;
esac
`, sandbox.ShellQuote(wrapPath))
	put(t, filepath.Join(r.bin, "self"), selfScript)
	if err := os.Chmod(filepath.Join(r.bin, "self"), 0o755); err != nil {
		t.Fatal(err)
	}
}
