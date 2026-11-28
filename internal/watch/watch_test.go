package watch

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
)

type convSpec struct {
	id, ws, agent, parent string
	mod                   time.Time
	transcript            string
	stepOutputs           map[string]string
}

func TestWatchCheck(t *testing.T) {
	t.Parallel()
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

func TestParseDarwinPsForUID(t *testing.T) {
	t.Parallel()
	psOut := strings.Join([]string{
		"  UID   PID  PPID STAT      ELAPSED COMMAND",
		"    0     1     0 Ss       10:00:00 /sbin/launchd",
		"  502   200     1 S           01:30 /usr/bin/other-user --flag",
		"  501   301   100 S+          02:05 /usr/local/bin/agy   --mode   plan",
		"  501   302   301 Ss     1-02:03:04 ssh -p 2222 host.example",
		"  501     0     1 R           00:01 invalid-pid-zero",
		"bad line",
	}, "\n")
	procs := parseDarwinPsForUID(psOut, 501)
	if len(procs) != 2 {
		t.Fatalf("got %d procs %+v, want 2", len(procs), procs)
	}
	if procs[0].PID != 301 || procs[0].PPID != 100 || procs[0].State != "S+" || procs[0].ElapsedSec != 125 || procs[0].Cmdline != "/usr/local/bin/agy   --mode   plan" {
		t.Errorf("proc[0] mismatch: %+v", procs[0])
	}
	wantElapsed := int64(86400 + 2*3600 + 3*60 + 4)
	if procs[1].PID != 302 || procs[1].PPID != 301 || procs[1].State != "Ss" || procs[1].ElapsedSec != wantElapsed || procs[1].Cmdline != "ssh -p 2222 host.example" {
		t.Errorf("proc[1] mismatch: %+v", procs[1])
	}
}

func TestWatchProcRedaction(t *testing.T) {
	t.Parallel()
	secretCmd := "curl --to" + "ken=" + "supersecret1234 https://api.example.com"
	if got := compactCmdTarget(secretCmd, 42); got != "[redacted by fyr]" {
		t.Fatalf("compactCmdTarget(%q) = %q, want [redacted by fyr]", secretCmd, got)
	}
	longSecretCmd := "cmd " + strings.Repeat("x", 69) + " --pw" + "d=" + "mysecret99"
	if got := compactCmdTarget(longSecretCmd, 43); got != "[redacted by fyr]" {
		t.Fatalf("compactCmdTarget(long secret) = %q, want [redacted by fyr]", got)
	}
	f := procFinding(kindInteractiveWait, "cmd", ProcInfo{PID: 1, ElapsedSec: 5}, "to"+"ken="+"secret1234", "act")
	if f.PromptHint != "[redacted by fyr]" {
		t.Fatalf("procFinding PromptHint = %q, want [redacted by fyr]", f.PromptHint)
	}
	credURL := strings.Join([]string{"https://", "user", ":", "supersecret1234", "@example.com/auth"}, "")
	fileHint := clipPrintableMode("pa"+"ssword: "+credURL, promptHintMaxB, false)
	if strings.Contains(fileHint, "supersecret1234") {
		t.Fatalf("clipPrintableMode leaked credential in PromptHint: %q", fileHint)
	}
}

func TestWatchProcFilteringAndHeuristics(t *testing.T) {
	t.Parallel()
	self := os.Getpid()
	procs := []ProcInfo{
		{PID: 10, PPID: 1, Cmdline: "systemd"},
		{PID: self, PPID: 10, Cmdline: "go test"},
		{PID: 1000, PPID: 1, Cmdline: "/usr/bin/agy --workspace /target/repo"},
		{PID: 1001, PPID: 1000, Cmdline: "cat"},
		{PID: 2000, PPID: 1, Cmdline: "/usr/bin/antigravity --workspace /other/repo"},
		{PID: 2001, PPID: 2000, Cmdline: "cat"},
		{PID: 3000, PPID: 1, Cmdline: "bash /target/repo/run.sh"},
	}
	anc := buildAncestorSet(procs, self)
	if !anc[self] || !anc[10] || anc[1000] {
		t.Fatalf("unexpected ancestor set: %+v", anc)
	}
	cycleProcs := []ProcInfo{{PID: 50, PPID: 51}, {PID: 51, PPID: 50}}
	if cAnc := buildAncestorSet(cycleProcs, 50); !cAnc[50] || !cAnc[51] {
		t.Fatalf("cycle ancestor set failed: %+v", cAnc)
	}
	if !hasAncestorIn(1001, map[int]int{1001: 1000}, map[int]bool{1000: true}) {
		t.Error("expected hasAncestorIn true for child of root")
	}
	if hasAncestorIn(50, map[int]int{50: 51, 51: 50}, map[int]bool{1000: true}) {
		t.Error("expected hasAncestorIn false on cycle without root")
	}
	if !isAgyProcess("/usr/local/bin/agy --mode plan") || !isAgyProcess("antigravity-server") || isAgyProcess("go test") || isAgyProcess("") {
		t.Error("isAgyProcess classification mismatch")
	}
	if !cmdMatchesTarget("bash /target/repo/sub", "/target/repo", "") || cmdMatchesTarget("bash /target/repo-other/sub", "/target/repo", "") {
		t.Error("cmdMatchesTarget dir boundary mismatch")
	}
	if !cmdMatchesTarget("--conv=conv-1", "", "conv-1") || cmdMatchesTarget("--conv=conv-10", "", "conv-1") {
		t.Error("cmdMatchesTarget convID boundary mismatch")
	}

	got := filterProcs(procs, false, "/target/repo", "")
	gotPIDs := map[int]bool{}
	for _, p := range got {
		gotPIDs[p.PID] = true
	}
	if !gotPIDs[1001] || !gotPIDs[3000] || gotPIDs[2000] || gotPIDs[2001] || gotPIDs[10] || gotPIDs[self] {
		t.Fatalf("filterProcs scoped mismatch: got %+v", got)
	}

	for _, benign := range []ProcInfo{
		{PID: 1, State: "S", Wchan: "futex_wait_queue", Cmdline: "gopls", ElapsedSec: 300},
		{PID: 2, State: "S", Wchan: "do_epoll_wait", Cmdline: "node", ElapsedSec: 300},
		{PID: 3, State: "S", Wchan: "do_wait", Cmdline: "agy --workspace /target/repo", ElapsedSec: 300},
		{PID: 4, State: "R", Wchan: "do_wait", Cmdline: "make", ElapsedSec: 300},
	} {
		if f, ok := classifyProc(benign, 90); ok {
			t.Errorf("unexpected finding for %+v: %+v", benign, f)
		}
	}
	if f, ok := classifyProc(ProcInfo{PID: 5, State: "T+", Cmdline: "vim", ElapsedSec: 100}, 90); !ok || f.Kind != kindInteractiveWait {
		t.Errorf("T+ stopped proc = %+v, %v", f, ok)
	}
	if f, ok := classifyProc(ProcInfo{PID: 6, State: "T", Cmdline: "sleep 99", ElapsedSec: 100}, 90); !ok || f.Kind != kindStuckNoProgress {
		t.Errorf("T stopped proc = %+v, %v", f, ok)
	}

	for _, tc := range []struct {
		state string
		want  string
		ok    bool
	}{
		{"S+", kindInteractiveWait, true},
		{"S", kindStuckNoProgress, true},
		{"I", kindStuckNoProgress, true},
		{"U", kindStuckNoProgress, true},
		{"T", kindStuckNoProgress, true},
		{"R", "", false},
	} {
		f, ok := classifyDarwinState(ProcInfo{PID: 7, State: tc.state, Cmdline: "sleep 100", ElapsedSec: 120}, "sleep 100", tc.state)
		if ok != tc.ok || (ok && f.Kind != tc.want) {
			t.Errorf("classifyDarwinState(%q) = %+v, %v; want %q, %v", tc.state, f, ok, tc.want, tc.ok)
		}
	}
}

func TestWatchFilesClipAndArtifacts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 18, 21, 0, 0, 0, time.UTC)
	home, dir := t.TempDir(), t.TempDir()
	seedWatchDBs(t, home, dir, []convSpec{
		{
			id:         "conv-secret-tr",
			ws:         dir,
			mod:        now.Add(-2 * time.Minute),
			transcript: "{\"content\":\"Proceed? [y/n] \\u0041KIAFAKEVALUE\"}\n",
		},
	}, nil)
	if f, ok := inspectConversationFiles(home, "conv-secret-tr", now); !ok || strings.Contains(f.PromptHint, "FAKEVALUE") {
		t.Fatalf("transcript escaped secret leaked or missed prompt: %+v, ok=%v", f, ok)
	}
	clipped := clipPrintableMode("a"+strings.Repeat("é", 40)+" [y/n]", 20, false)
	if !utf8.ValidString(clipped) || !strings.HasSuffix(clipped, "...") || len(clipped) > 23 {
		t.Fatalf("clipPrintableMode invalid UTF-8 clip: %q", clipped)
	}
	if got := clipPrintableMode("hello", 0, false); got != "..." {
		t.Fatalf("clipPrintableMode(maxB=0) = %q, want %q", got, "...")
	}

	if checked, artFindings, err := inspectArtifactsChecked(dir, 90, now); err != nil || checked != 0 || len(artFindings) != 0 {
		t.Fatalf("inspectArtifactsChecked(missing agents dir) = checked:%d findings:%+v err:%v, want checked=0 findings=nil err=nil", checked, artFindings, err)
	}
	writeArt(t, dir, "old-abandoned.md", "# Old\nStatus: IN PROGRESS\n", now.Add(-25*time.Hour))
	writeArt(t, dir, "fenced-example.md", "# Guide\n```md\nStatus: IN PROGRESS\n```\n~~~~\n## Status: IN PROGRESS\n~~~~\n````md\n```\nStatus: IN PROGRESS\n```\n````\n", now.Add(-5*time.Minute))
	writeArt(t, dir, "active-stuck.md", "# Audit\n## Status: IN PROGRESS\n", now.Add(-5*time.Minute))
	checked, artFindings, err := inspectArtifactsChecked(dir, 90, now)
	if err != nil || checked != 2 || len(artFindings) != 1 || !strings.Contains(artFindings[0].Target, "active-stuck.md") {
		t.Fatalf("inspectArtifactsChecked = checked:%d findings:%+v err:%v, want checked=2, only active-stuck.md, err=nil", checked, artFindings, err)
	}
}

func TestWatchSessionMetaWindowAndBudgets(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 18, 21, 0, 0, 0, time.UTC)
	home, dir := t.TempDir(), t.TempDir()
	convs := []convSpec{
		{id: "lead", ws: dir, mod: now.Add(-5 * time.Minute)},
		{id: "worker-builder", ws: dir, agent: "builder-1c", parent: "lead", mod: now.Add(-5 * time.Minute)},
		{id: "worker-coder", ws: dir, agent: "coder-1a", parent: "lead", mod: now.Add(-5 * time.Minute)},
		{id: "worker-custom", ws: dir, agent: "worker-x", parent: "lead", mod: now.Add(-5 * time.Minute)},
		{id: "anon-self", ws: dir, agent: "", parent: "lead", mod: now.Add(-5 * time.Minute)},
	}
	turns := map[string]int{
		"worker-builder":       45,
		"worker-coder":         96,
		"worker-custom":        45,
		"anon-self":            45,
		"orphan-unbacked":      38,
		"orphan-old-sub":       38,
		"sub-rev-recent":       33,
		"orphan-builder-fresh": 45,
	}
	auditRoles := []string{"reviewer", "security", "verifier", "explorer", "dreamer", "documentation", "research", "subagent", "sub-helper"}
	for _, r := range auditRoles {
		id := "role-" + r
		convs = append(convs, convSpec{id: id, ws: dir, agent: r, parent: "lead", mod: now.Add(-5 * time.Minute)})
		turns[id] = 32
	}
	seedWatchDBs(t, home, dir, convs, turns)

	agyBrain := filepath.Join(home, ".gemini", "antigravity-cli", "brain")
	mustWriteWatchFile(t, filepath.Join(agyBrain, "orphan-old-sub", ".system_generated", "logs", "transcript.jsonl"), "{}\n", now.Add(-48*time.Hour))
	mustWriteWatchFile(t, filepath.Join(agyBrain, "sub-rev-recent", ".system_generated", "logs", "transcript.jsonl"), "{}\n", now.Add(-5*time.Minute))
	mustWriteWatchFile(t, filepath.Join(agyBrain, "orphan-builder-fresh", ".system_generated", "logs", "transcript.jsonl"), "{}\n", now.Add(-5*time.Minute))

	rep, err := Check(context.Background(), Options{
		Home:       home,
		Dir:        dir,
		Now:        func() time.Time { return now },
		ProcLister: func() ([]ProcInfo, error) { return nil, nil },
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// Under Phase 7 / F-036 aligned role caps (search.SubagentBudgetCap),
	// unknown custom roles ("worker-x") default to cap 40 (not 120).
	for _, want := range []string{"worker-coder", "worker-custom", "sub-rev-recent"} {
		if !hasWatchFinding(rep, kindBudgetNearLimit, want) {
			t.Errorf("missing BUDGET_NEAR_LIMIT for %s: %+v", want, rep.Findings)
		}
	}
	for _, r := range auditRoles {
		if !hasWatchFinding(rep, kindBudgetNearLimit, "role-"+r) {
			t.Errorf("missing BUDGET_NEAR_LIMIT for audit role %s: %+v", r, rep.Findings)
		}
	}
	for _, noWant := range []string{"worker-builder", "anon-self", "orphan-unbacked", "orphan-old-sub", "orphan-builder-fresh"} {
		if hasWatchFinding(rep, kindBudgetNearLimit, noWant) {
			t.Errorf("unexpected BUDGET_NEAR_LIMIT for %s: %+v", noWant, rep.Findings)
		}
	}

	// Without conversation_summaries.db, a >24h search.db falls outside discoveryWindow.
	homeNoSum, dirNoSum := t.TempDir(), t.TempDir()
	seedWatchDBs(t, homeNoSum, dirNoSum, nil, map[string]int{"sub-stale": 38})
	_ = os.Remove(filepath.Join(homeNoSum, ".gemini", "antigravity-cli", "conversation_summaries.db"))
	oldTime := now.Add(-30 * time.Hour)
	if err := os.Chtimes(filepath.Join(dirNoSum, ".agents", "scratch", "search.db"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	repNoSum, err := Check(context.Background(), Options{
		Home:       homeNoSum,
		Dir:        dirNoSum,
		Now:        func() time.Time { return now },
		ProcLister: func() ([]ProcInfo, error) { return nil, nil },
	})
	if err != nil || !repNoSum.OK {
		t.Fatalf("stale search.db without summaries should be skipped, got rep=%+v err=%v", repNoSum, err)
	}
}

func TestWatchWorkspaceMatchesSymlinkedDir(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	realRepo := filepath.Join(base, "repo")
	realSub := filepath.Join(realRepo, "sub", "nested")
	sibling := filepath.Join(base, "repo-other")
	linkRepo := filepath.Join(base, "link-repo")
	for _, d := range []string{realSub, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(realRepo, linkRepo); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		uris string
		dir  string
		want bool
	}{
		{`["file://` + realRepo + `"]`, linkRepo, true},
		{`["file://` + realSub + `"]`, linkRepo, true},
		{`["file://` + filepath.Join(linkRepo, "sub") + `"]`, realRepo, true},
		{`["file://` + sibling + `"]`, linkRepo, false},
		{`["file://` + sibling + `"]`, realRepo, false},
	}
	for _, tc := range cases {
		if got := workspaceMatchesDir(tc.uris, tc.dir); got != tc.want {
			t.Errorf("workspaceMatchesDir(%s, %q) = %v, want %v", tc.uris, tc.dir, got, tc.want)
		}
	}
	if !cmdMatchesTarget("bash "+filepath.Join(realRepo, "sub"), linkRepo, "") {
		t.Errorf("cmdMatchesTarget did not resolve symlinked dir %q", linkRepo)
	}
}

func TestV910WatchReadOnlyLivenessAndOpenSSH(t *testing.T) {
	t.Parallel()
	t.Run("readonly_40mb_search_db", testV910ReadOnly40MB)
	t.Run("readonly_fallback_and_refusal", testV910ReadOnlyFallbackAndRefusal)
	t.Run("liveness_filters", testV910LivenessFilters)
	t.Run("aligned_role_caps", testV910AlignedRoleCaps)
	t.Run("openssh_prompts", testV910OpenSSHPrompts)
}

func TestWatchTestFileHasNoSecretPatterns(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("watch_test.go")
	if err != nil {
		t.Fatalf("read watch_test.go: %v", err)
	}
	text := string(data)
	if redact.Redact(text) == text {
		return
	}
	lineHits := 0
	for i, line := range strings.Split(text, "\n") {
		if scrubbed := redact.Redact(line); scrubbed != line {
			lineHits++
			t.Errorf("watch_test.go:%d matched secret pattern (%s)", i+1, scrubbed)
		}
	}
	if lineHits == 0 {
		t.Errorf("watch_test.go matched a multi-line secret pattern")
	}
}
