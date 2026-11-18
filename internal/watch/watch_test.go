package watch

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

type convSpec struct {
	id, ws, agent, parent string
	mod                   time.Time
	transcript            string
	stepOutputs           map[string]string
}

func TestWatchCheck(t *testing.T) {
	now := time.Date(2026, 11, 18, 21, 0, 0, 0, time.UTC)

	t.Run("healthy", func(t *testing.T) {
		home, dir := t.TempDir(), t.TempDir()
		seedWatchDBs(t, home, dir, []convSpec{
			{id: "conv-root", ws: dir, mod: now.Add(-10 * time.Minute), transcript: "{\"step_index\":1,\"status\":\"DONE\",\"content\":\"all good\"}\n", stepOutputs: map[string]string{"1": "ok\n"}},
			{id: "conv-rev", ws: dir, agent: "reviewer", parent: "conv-root", mod: now.Add(-5 * time.Minute)},
			{id: "conv-self", ws: dir, agent: "self", parent: "conv-root", mod: now.Add(-5 * time.Minute)},
		}, map[string]int{
			"conv-root": 40,
			"conv-rev":  31,
			"conv-self": 95,
		})
		writeArt(t, dir, "reviewer-done.md", "# Review\n## Status: COMPLETE\n", now.Add(-10*time.Minute))
		writeArt(t, dir, "verifier-pass.md", "# Verification\n## Status: PASS   <!-- or FAIL, BLOCKED, IN PROGRESS -->\n", now.Add(-10*time.Minute))
		writeArt(t, dir, "fresh-wip.md", "# Review\nStatus: IN PROGRESS\n", now.Add(-15*time.Second))

		rep, err := Check(context.Background(), Options{
			Home: home,
			Dir:  dir,
			Now:  func() time.Time { return now },
			ProcLister: func() ([]ProcInfo, error) {
				return []ProcInfo{{PID: 101, PPID: 1, State: "R", Wchan: "0", Cmdline: "go test ./...", ElapsedSec: 20}}, nil
			},
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if !rep.OK || rep.Checked == 0 || len(rep.Findings) != 0 {
			t.Fatalf("got OK=%v Checked=%d Findings=%+v, want healthy", rep.OK, rep.Checked, rep.Findings)
		}
		assertNoWatchTempDirs(t, home)
	})

	t.Run("stdin_wait", func(t *testing.T) {
		home, dir := t.TempDir(), t.TempDir()
		seedWatchDBs(t, home, dir, []convSpec{
			{
				id:          "conv-tr",
				ws:          dir,
				mod:         now.Add(-2 * time.Minute),
				transcript:  "{\"step_index\":1,\"status\":\"DONE\",\"content\":\"normal start\"}\n{\"step_index\":2,\"status\":\"RUNNING\",\"content\":\"Proceed with migration? [y/n]\"}\n",
				stepOutputs: map[string]string{"1": "done\n"},
			},
			{
				id:          "conv-step",
				ws:          dir,
				mod:         now.Add(-2 * time.Minute),
				transcript:  "{\"step_index\":1,\"status\":\"RUNNING\",\"content\":\"running ssh\"}\n",
				stepOutputs: map[string]string{"1": "old output\n", "2": "Enter passphrase for key '/id_ed25519': "},
			},
		}, nil)

		rep, err := Check(context.Background(), Options{
			Home: home,
			Dir:  dir,
			Now:  func() time.Time { return now },
			ProcLister: func() ([]ProcInfo, error) {
				return []ProcInfo{
					{PID: 201, PPID: 100, State: "S", Wchan: "n_tty_read", Cmdline: "sudo apt update", ElapsedSec: 15},
					{PID: 202, PPID: 100, State: "S", Wchan: "pipe_read", Cmdline: "cat", ElapsedSec: 120},
					{PID: 203, PPID: 100, State: "S", Wchan: "pipe_read", Cmdline: "git diff", ElapsedSec: 5},
				}, nil
			},
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if rep.OK {
			t.Fatalf("expected OK=false, got %+v", rep)
		}
		if !hasWatchFinding(rep, "INTERACTIVE_WAIT", "conv-tr") {
			t.Errorf("missing INTERACTIVE_WAIT for conv-tr: %+v", rep.Findings)
		}
		if !hasWatchFinding(rep, "INTERACTIVE_WAIT", "conv-step") {
			t.Errorf("missing INTERACTIVE_WAIT for conv-step: %+v", rep.Findings)
		}
		if !hasWatchFindingPID(rep, "INTERACTIVE_WAIT", 201) || !hasWatchFindingPID(rep, "INTERACTIVE_WAIT", 202) {
			t.Errorf("missing INTERACTIVE_WAIT for tty/pipe pids 201/202: %+v", rep.Findings)
		}
		if hasWatchFindingPID(rep, "INTERACTIVE_WAIT", 203) {
			t.Errorf("short pipe_read pid 203 should not trigger: %+v", rep.Findings)
		}
	})

	t.Run("stale_in_progress_artifact", func(t *testing.T) {
		home, dir := t.TempDir(), t.TempDir()
		seedWatchDBs(t, home, dir, []convSpec{{id: "conv-1", ws: dir, mod: now.Add(-3 * time.Minute)}}, nil)
		writeArt(t, dir, "security-stale.md", "# Security\n## Status: IN PROGRESS\n", now.Add(-125*time.Second))

		rep, err := Check(context.Background(), Options{
			Home: home,
			Dir:  dir,
			Now:  func() time.Time { return now },
			ProcLister: func() ([]ProcInfo, error) {
				return []ProcInfo{{PID: 301, PPID: 100, State: "S", Wchan: "do_wait", Cmdline: "bash -c sleep 200", ElapsedSec: 120}}, nil
			},
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if rep.OK {
			t.Fatalf("expected OK=false, got %+v", rep)
		}
		var gotArt bool
		for _, f := range rep.Findings {
			if f.Kind == "STUCK_NO_PROGRESS" && strings.Contains(f.Target, "security-stale.md") && f.ArtifactStatus == "IN PROGRESS" && f.IdleSec >= 125 {
				gotArt = true
			}
		}
		if !gotArt {
			t.Errorf("missing STUCK_NO_PROGRESS artifact finding: %+v", rep.Findings)
		}
		if !hasWatchFindingPID(rep, "STUCK_NO_PROGRESS", 301) {
			t.Errorf("missing STUCK_NO_PROGRESS proc finding for pid 301: %+v", rep.Findings)
		}
	})

	t.Run("budget_near_limit", func(t *testing.T) {
		home, dir := t.TempDir(), t.TempDir()
		seedWatchDBs(t, home, dir, []convSpec{
			{id: "lead", ws: dir, mod: now.Add(-5 * time.Minute)},
			{id: "sub-rev", ws: dir, agent: "reviewer", parent: "lead", mod: now.Add(-5 * time.Minute)},
			{id: "sub-sec", ws: dir, agent: "security", parent: "lead", mod: now.Add(-5 * time.Minute)},
			{id: "sub-self-ok", ws: dir, agent: "self", parent: "lead", mod: now.Add(-5 * time.Minute)},
			{id: "sub-self-high", ws: dir, agent: "self", parent: "lead", mod: now.Add(-5 * time.Minute)},
		}, map[string]int{
			"lead":          45,
			"sub-rev":       32,
			"sub-sec":       31,
			"sub-self-ok":   50,
			"sub-self-high": 96,
		})

		rep, err := Check(context.Background(), Options{
			Home:       home,
			Dir:        dir,
			Now:        func() time.Time { return now },
			ProcLister: func() ([]ProcInfo, error) { return nil, nil },
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if rep.OK {
			t.Fatalf("expected OK=false, got %+v", rep)
		}
		if !hasWatchFinding(rep, "BUDGET_NEAR_LIMIT", "sub-rev") || !hasWatchFinding(rep, "BUDGET_NEAR_LIMIT", "sub-self-high") {
			t.Errorf("missing BUDGET_NEAR_LIMIT for sub-rev / sub-self-high: %+v", rep.Findings)
		}
		for _, okID := range []string{"lead", "sub-sec", "sub-self-ok"} {
			if hasWatchFinding(rep, "BUDGET_NEAR_LIMIT", okID) {
				t.Errorf("unexpected BUDGET_NEAR_LIMIT for %s: %+v", okID, rep.Findings)
			}
		}
	})

	t.Run("conv_id_with_children", func(t *testing.T) {
		home, dir := t.TempDir(), t.TempDir()
		seedWatchDBs(t, home, dir, []convSpec{
			{id: "parent-1", ws: dir, mod: now.Add(-5 * time.Minute), transcript: "{\"content\":\"ok\"}\n"},
			{id: "child-1", ws: dir, agent: "reviewer", parent: "parent-1", mod: now.Add(-5 * time.Minute), transcript: "{\"content\":\"Continue?\"}\n"},
			{id: "other-1", ws: dir, mod: now.Add(-5 * time.Minute), transcript: "{\"content\":\"Pa" + "ssword:\"}\n"},
		}, nil)

		rep, err := Check(context.Background(), Options{
			Home:       home,
			Dir:        dir,
			ConvID:     "parent-1",
			Now:        func() time.Time { return now },
			ProcLister: func() ([]ProcInfo, error) { return nil, nil },
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if !hasWatchFinding(rep, "INTERACTIVE_WAIT", "child-1") {
			t.Errorf("expected child-1 finding: %+v", rep.Findings)
		}
		if hasWatchFinding(rep, "INTERACTIVE_WAIT", "other-1") {
			t.Errorf("unrelated other-1 should not be inspected when ConvID=parent-1: %+v", rep.Findings)
		}
	})
}

func seedWatchDBs(t *testing.T, home, dir string, convs []convSpec, metaTurns map[string]int) {
	t.Helper()
	agyDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(agyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sumPath := filepath.Join(agyDir, "conversation_summaries.db")
	db, err := sql.Open("sqlite", search.DSN(sumPath))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range convs {
		mod := c.mod.UTC().Format("2006-01-02 15:04:05.000000000+00:00")
		if _, err := db.Exec(`INSERT INTO conversation_summaries VALUES (?, ?, ?, ?, ?)`, c.id, mod, `["file://`+c.ws+`"]`, c.agent, c.parent); err != nil {
			t.Fatal(err)
		}
		if c.transcript != "" {
			p := filepath.Join(agyDir, "brain", c.id, ".system_generated", "logs", "transcript.jsonl")
			mustWriteWatchFile(t, p, c.transcript, c.mod)
		}
		for step, body := range c.stepOutputs {
			p := filepath.Join(agyDir, "brain", c.id, ".system_generated", "steps", step, "output.txt")
			mustWriteWatchFile(t, p, body, c.mod)
		}
	}
	_ = db.Close()

	if len(metaTurns) > 0 {
		sdbPath := filepath.Join(dir, ".agents", "scratch", "search.db")
		if err := os.MkdirAll(filepath.Dir(sdbPath), 0o755); err != nil {
			t.Fatal(err)
		}
		sdb, err := sql.Open("sqlite", search.DSN(sdbPath))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sdb.Exec(`CREATE TABLE session_meta(conversation_id TEXT PRIMARY KEY, generation_id INT, last_transcript_bytes INT, turn_count INT, est_tokens INT)`); err != nil {
			t.Fatal(err)
		}
		for id, turns := range metaTurns {
			if _, err := sdb.Exec(`INSERT INTO session_meta VALUES (?, 0, 1000, ?, 250)`, id, turns); err != nil {
				t.Fatal(err)
			}
		}
		_ = sdb.Close()
	}
}

func writeArt(t *testing.T, dir, name, content string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(dir, ".agents", "scratch", "agents", name)
	mustWriteWatchFile(t, p, content, mtime)
}

func mustWriteWatchFile(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func assertNoWatchTempDirs(t *testing.T, home string) {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(home, harness.StateDir, "watch-agy-*"))
	if len(left) != 0 {
		t.Errorf("leftover watch temp dirs: %v", left)
	}
}

func hasWatchFinding(rep Report, kind, target string) bool {
	for _, f := range rep.Findings {
		if f.Kind == kind && f.Target == target {
			return true
		}
	}
	return false
}

func hasWatchFindingPID(rep Report, kind string, pid int) bool {
	for _, f := range rep.Findings {
		if f.Kind == kind && f.PID == pid {
			return true
		}
	}
	return false
}
