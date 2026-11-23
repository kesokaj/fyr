package metrics

import (
	"database/sql"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestRootCanonicalizerNeverCollapsesHome(t *testing.T) {
	userHome, _ := os.UserHomeDir()
	tmpRoot := filepath.Clean(os.TempDir())
	sandboxDir := filepath.Join("/home/alice", harness.StateDir, "sandbox")
	all := []sess{
		{id: "r0", root: "/"},
		{id: "r1", root: "/home/alice"},
		{id: "r2", root: "/Users/bob"},
		{id: "r3", root: tmpRoot},
		{id: "r4", root: "/tmp/fyr-agy-probes"},
		{id: "r4b", root: "/tmp/fyr-agy-probes-999"},
		{id: "r5", root: "/tmp/probes"},
		{id: "r6", root: sandboxDir},
		{id: "r7", root: "/tmp/smoke-42"},
		{id: "r8", root: "/home/alice/code/fyr"},
		{id: "r9", root: "/home/alice/code/other"},
		{id: "r10", root: "/Users/bob/app"},
		{id: "r11", root: "/tmp/real-proj"},
	}
	if userHome != "" {
		all = append(all, sess{id: "rh", root: userHome})
	}
	vets := map[string]*projVetAcc{
		"/":                   {turns: 1},
		"/home/alice":         {turns: 1},
		tmpRoot:               {turns: 1},
		"/tmp/smoke-42":       {turns: 1},
		sandboxDir:            {turns: 1},
		"/tmp/probes":         {turns: 1},
		"/tmp/fyr-agy-probes": {turns: 1},
		"/tmp/real-proj":      {turns: 1},
		"/Users/bob":          {turns: 1},
		"/home/alice/code":    nil,
	}
	delete(vets, "/home/alice/code")

	canon := buildRootCanonicalizer(all, vets)
	cases := []struct {
		in, want string
	}{
		{"/home/alice/code/fyr", "/home/alice/code/fyr"},
		{"/home/alice/code/fyr/internal/metrics", "/home/alice/code/fyr"},
		{"/home/alice/code/other", "/home/alice/code/other"},
		{"/Users/bob/app", "/Users/bob/app"},
		{"/Users/bob/app/sub", "/Users/bob/app"},
		{"/tmp/real-proj", "/tmp/real-proj"},
		{"/tmp/real-proj/pkg", "/tmp/real-proj"},
		{"/tmp/fyr-agy-probes/sub", "/tmp/fyr-agy-probes/sub"},
		{"/tmp/probes/sub", "/tmp/probes/sub"},
		{filepath.Join(sandboxDir, "w1"), filepath.Join(sandboxDir, "w1")},
		{"/tmp/smoke-42/sub", "/tmp/smoke-42/sub"},
	}
	if userHome != "" {
		repoUnderHome := filepath.Join(filepath.Clean(userHome), "repo-under-home")
		cases = append(cases, struct{ in, want string }{repoUnderHome, repoUnderHome})
	}
	for _, tc := range cases {
		if got := canon(tc.in); got != tc.want {
			t.Errorf("canon(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// End-to-end worktree attribution + /usage probe filtering.
	home := t.TempDir()
	mainRepo := filepath.Join(t.TempDir(), "fyr-repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit(mainRepo, "init", "-q", "-b", "main")
	runGit(mainRepo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(mainRepo, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(mainRepo, "add", "README.md")
	runGit(mainRepo, "commit", "-q", "-m", "init")
	wtDir := filepath.Join(t.TempDir(), "wt-active")
	runGit(mainRepo, "worktree", "add", "-q", wtDir)

	deletedWT := filepath.Join(home, ".gemini", "antigravity-cli", "worktrees", "S_MAIN", "workflow-self-abcd1234")
	probeDir := filepath.Join(os.TempDir(), "fyr-agy-probes")
	probeDir2 := filepath.Join(os.TempDir(), "fyr-agy-probes-999")
	agyDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(agyDir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	sqliteT(t, filepath.Join(agyDir, "conversation_summaries.db"),
		"CREATE TABLE conversation_summaries (`conversation_id` text, `title` text NOT NULL DEFAULT '', `preview` text NOT NULL DEFAULT '', `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')",
		`INSERT INTO conversation_summaries VALUES ('S_MAIN', '', 'work', '2026-10-08 10:00:00+00:00', '["file://`+mainRepo+`"]', '', '')`,
		`INSERT INTO conversation_summaries VALUES ('S_WT_DISK', '', 'wt1', '2026-10-08 10:01:00+00:00', '["file://`+wtDir+`"]', '', '')`,
		`INSERT INTO conversation_summaries VALUES ('S_WT_DEL', '', 'wt2', '2026-10-08 10:02:00+00:00', '["file://`+deletedWT+`"]', '', '')`,
		`INSERT INTO conversation_summaries VALUES ('S_USAGE_ROOT', '', '/usage', '2026-10-08 10:03:00+00:00', '["file:///"]', '', '')`,
		`INSERT INTO conversation_summaries VALUES ('S_USAGE_PROBE', '', '/usage', '2026-10-08 10:04:00+00:00', '["file://`+probeDir+`"]', '', '')`,
		`INSERT INTO conversation_summaries VALUES ('S_USAGE_PROBE2', '', '/usage', '2026-10-08 10:04:30+00:00', '["file://`+probeDir2+`"]', '', '')`,
		`INSERT INTO conversation_summaries VALUES ('S_HOME', '', 'home', '2026-10-08 10:05:00+00:00', '["file:///home/alice"]', '', '')`,
		`INSERT INTO conversation_summaries VALUES ('S_OTHER', '', 'other', '2026-10-08 10:06:00+00:00', '["file:///home/alice/other-proj"]', '', '')`,
	)
	const t0 = 1791453600
	for i, id := range []string{"S_MAIN", "S_WT_DISK", "S_WT_DEL", "S_USAGE_ROOT", "S_USAGE_PROBE", "S_USAGE_PROBE2", "S_HOME", "S_OTHER"} {
		cp := filepath.Join(agyDir, "conversations", id+".db")
		sqliteT(t, cp,
			"CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)",
			"CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
		db, _ := sql.Open("sqlite", cp)
		db.Exec(`INSERT INTO steps VALUES (0, 14, 3, ?, NULL, NULL)`, agyStepMeta(uint64(t0+i*10), "", nil))
		db.Exec(`INSERT INTO gen_metadata VALUES (0, ?, 0)`, agyGen(100, 20, 0, 1000))
		db.Close()
	}

	s := openT(t, Path(t.TempDir()))
	ingestT(t, s, Options{Home: home})
	sum, err := s.Show(ShowOptions{
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Now:   time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	byProj := map[string]ProjectRow{}
	for _, p := range sum.Projects {
		byProj[p.Project] = p
	}
	if _, ok := byProj["/"]; ok {
		t.Errorf("/usage root session leaked into Projects: %+v", sum.Projects)
	}
	if _, ok := byProj[probeDir]; ok {
		t.Errorf("/usage probe session leaked into Projects: %+v", sum.Projects)
	}
	if _, ok := byProj[probeDir2]; ok {
		t.Errorf("/usage per-call probe session leaked into Projects: %+v", sum.Projects)
	}
	if _, ok := byProj[wtDir]; ok {
		t.Errorf("active worktree leaked into Projects separately from mainRepo: %+v", sum.Projects)
	}
	if _, ok := byProj[deletedWT]; ok {
		t.Errorf("deleted worktree leaked into Projects separately from mainRepo: %+v", sum.Projects)
	}
	if got := byProj[filepath.Clean(mainRepo)]; got.Sessions != 3 {
		t.Errorf("mainRepo ProjectRow = %+v, want Sessions=3 (main + active wt + deleted wt)", got)
	}
	if _, ok := byProj["/home/alice/other-proj"]; !ok {
		t.Errorf("child project under /home/alice was collapsed or missing: %+v", sum.Projects)
	}
}

func TestDisjointRangedReadsNotRedundant(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	agyDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(agyDir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	sqliteT(t, filepath.Join(agyDir, "conversation_summaries.db"),
		"CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')",
		`INSERT INTO conversation_summaries VALUES ('C_DISJ', '2026-10-08 10:05:00+00:00', '["file://`+root+`"]', '', '')`,
		`INSERT INTO conversation_summaries VALUES ('C_OVLP', '2026-10-08 10:06:00+00:00', '["file://`+root+`"]', '', '')`,
	)
	const t0 = 1791367200
	writeSteps := func(id string, baseT uint64, steps [][]any) {
		cp := filepath.Join(agyDir, "conversations", id+".db")
		sqliteT(t, cp,
			"CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)",
			"CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
		db, _ := sql.Open("sqlite", cp)
		defer db.Close()
		for i, st := range steps {
			if _, err := db.Exec(`INSERT INTO steps VALUES (?, ?, ?, ?, NULL, NULL)`, i, st[0], st[1], st[2]); err != nil {
				t.Fatal(err)
			}
		}
	}
	foo := filepath.Join(root, "main.go")
	writeSteps("C_DISJ", t0, [][]any{
		{14, 3, agyStepMeta(t0, "", nil)},
		{132, 3, agyStepMeta(t0+1, "read_file", map[string]any{"Path": foo, "StartLine": 1, "EndLine": 50})},
		{132, 3, agyStepMeta(t0+2, "read_file", map[string]any{"Path": foo, "StartLine": 51, "EndLine": 100})},
		{132, 3, agyStepMeta(t0+3, "read_file", map[string]any{"Path": foo, "Offset": 101, "Limit": 50})},
	})
	aFile := filepath.Join(root, "a.go")
	bFile := filepath.Join(root, "b.go")
	cFile := filepath.Join(root, "c.go")
	writeSteps("C_OVLP", t0+100, [][]any{
		{14, 3, agyStepMeta(t0+100, "", nil)},
		// 49% overlap (<50%) -> NOT redundant
		{132, 3, agyStepMeta(t0+101, "read_file", map[string]any{"Path": aFile, "StartLine": 1, "EndLine": 100})},
		{132, 3, agyStepMeta(t0+102, "read_file", map[string]any{"Path": aFile, "StartLine": 52, "EndLine": 151})},
		// 50% overlap (>=50%) -> redundant (count=1)
		{132, 3, agyStepMeta(t0+103, "read_file", map[string]any{"Path": bFile, "StartLine": 1, "EndLine": 100})},
		{132, 3, agyStepMeta(t0+104, "read_file", map[string]any{"Path": bFile, "StartLine": 51, "EndLine": 150})},
		// Edit clears intervals; re-read 1..100 is NOT redundant
		{132, 3, agyStepMeta(t0+105, "edit_file", map[string]any{"Path": bFile})},
		{132, 3, agyStepMeta(t0+106, "read_file", map[string]any{"Path": bFile, "StartLine": 1, "EndLine": 100})},
		// Full read followed by ranged read -> redundant (count=2)
		{132, 3, agyStepMeta(t0+107, "read_file", map[string]any{"Path": cFile})},
		{132, 3, agyStepMeta(t0+108, "read_file", map[string]any{"Path": cFile, "StartLine": 10, "EndLine": 30})},
	})

	s := openT(t, Path(t.TempDir()))
	ingestT(t, s, Options{Home: home})

	var gotHashes []string
	rows, err := s.db.Query(`SELECT cmd_hash FROM steps WHERE session_id = 'C_DISJ' AND tool = 'Read' ORDER BY ext_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var h string
		rows.Scan(&h)
		gotHashes = append(gotHashes, h)
	}
	rows.Close()
	wantSuffixes := []string{":1-50", ":51-100", ":101-150"}
	if len(gotHashes) != len(wantSuffixes) {
		t.Fatalf("C_DISJ hashes = %v, want %d rows", gotHashes, len(wantSuffixes))
	}
	for i, suf := range wantSuffixes {
		if !strings.HasPrefix(gotHashes[i], "r:") || !strings.HasSuffix(gotHashes[i], suf) {
			t.Errorf("hash[%d] = %q, want r:<hash>%s", i, gotHashes[i], suf)
		}
	}

	sum, err := s.Show(ShowOptions{
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Now:   time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.C.RedundantReads["agy"] != 2 {
		t.Errorf("RedundantReads[agy] = %d, want 2", sum.C.RedundantReads["agy"])
	}

	disjTraj, err := s.Trajectories(TrajectoryOptions{SessionID: "C_DISJ"})
	if err != nil || len(disjTraj) != 1 {
		t.Fatalf("C_DISJ traj = %+v, err=%v", disjTraj, err)
	}
	if slices.Contains(disjTraj[0].Anomalies, "REDUNDANT_READ") {
		t.Errorf("C_DISJ had unexpected REDUNDANT_READ anomaly: %v", disjTraj[0].Anomalies)
	}
	ovlpTraj, err := s.Trajectories(TrajectoryOptions{SessionID: "C_OVLP"})
	if err != nil || len(ovlpTraj) != 1 {
		t.Fatalf("C_OVLP traj = %+v, err=%v", ovlpTraj, err)
	}
	if !slices.Contains(ovlpTraj[0].Anomalies, "REDUNDANT_READ") {
		t.Errorf("C_OVLP missing REDUNDANT_READ anomaly: %v", ovlpTraj[0].Anomalies)
	}
}

func TestHookDenialsExcludedFromToolErrorRate(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	agyDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(agyDir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	sqliteT(t, filepath.Join(agyDir, "conversation_summaries.db"),
		"CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')",
		`INSERT INTO conversation_summaries VALUES ('C_DENY', '2026-10-08 10:05:00+00:00', '["file://`+root+`"]', '', '')`,
	)

	const t0 = 1791367200
	markers := []string{
		"[CLAMPED]",
		"[UNCHANGED]",
		"[COORDINATOR_GUARD]",
		"[POLL_LOOP]",
		"[TOKEN_LOOP]",
		"[ERROR_LOOP]",
		"[SUBAGENT_BUDGET]",
		"[MODEL_OVERRIDE]",
		"[AUTO_SUBAGENT_PRO]",
		"[INTERACTIVE_STDIN]",
		"denied by pre-tool hook",
	}

	cp := filepath.Join(agyDir, "conversations", "C_DENY.db")
	sqliteT(t, cp,
		"CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)",
		"CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
	db, _ := sql.Open("sqlite", cp)
	insert := func(idx, typ, status int, meta, errd []byte) {
		if _, err := db.Exec(`INSERT INTO steps VALUES (?, ?, ?, ?, ?, NULL)`, idx, typ, status, meta, errd); err != nil {
			t.Fatal(err)
		}
	}
	insert(0, 14, 3, agyStepMeta(t0, "", nil), nil)
	// Succeeded Bash command whose CommandLine mentions [CLAMPED] must NOT be marked denied.
	insert(1, 132, 3, agyStepMeta(t0+1, "run_command", map[string]string{"CommandLine": "grep '[CLAMPED]' foo.go"}), nil)
	// Succeeded read.
	insert(2, 132, 3, agyStepMeta(t0+2, "read_file", map[string]string{"Path": filepath.Join(root, "a.go")}), nil)
	// Real tool failure (status 7, non-hook error).
	insert(3, 132, 7, agyStepMeta(t0+3, "run_command", map[string]string{"CommandLine": "go test ./..."}), pbB(2, []byte("exit status 1")))
	// Hook denials stored via metadata field 34 and/or error_details.
	for i, m := range markers {
		idx := 4 + i
		meta := agyStepMeta(uint64(t0+idx), "read_file", map[string]string{"Path": filepath.Join(root, "big.go")})
		var errd []byte
		switch i {
		case 0:
			// Exact Antigravity protobuf layout: field 34->4="deny", 34->5="[CLAMPED]..."
			meta = cat(meta, pbB(34, cat(pbB(4, []byte("deny")), pbB(5, []byte(m+"\n{\"status\":\"CLAMPED\"}")))))
			errd = pbB(2, []byte("tool call denied by pre-tool hook: "+m))
		case len(markers) - 1:
			errd = pbB(2, []byte("tool call "+m+": blocked"))
		default:
			meta = cat(meta, pbB(34, pbB(5, []byte(m+" blocked"))))
		}
		insert(idx, 132, 7, meta, errd)
	}
	db.Close()

	s := openT(t, Path(t.TempDir()))
	ingestT(t, s, Options{Home: home})

	var errCnt, denyCnt int
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(is_error), 0), COALESCE(SUM(denied), 0) FROM steps WHERE session_id = 'C_DENY' AND kind = 'tool'`).Scan(&errCnt, &denyCnt); err != nil {
		t.Fatal(err)
	}
	if errCnt != 1 || denyCnt != len(markers) {
		t.Fatalf("steps counts: is_error=%d denied=%d, want 1 and %d", errCnt, denyCnt, len(markers))
	}

	sum, err := s.Show(ShowOptions{
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Now:   time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantRate := 1.0 / float64(3+len(markers))
	if r := sum.D.ToolErrorRate["agy"]; r == nil || math.Abs(*r-wantRate) > 1e-9 {
		t.Errorf("ToolErrorRate[agy] = %v, want %.6f (hook denials must not inflate error rate)", r, wantRate)
	}

	// Legacy row with both is_error=1 and denied=1 must also be excluded by show_add.go.
	if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, is_error, denied) VALUES ('C_DENY', 'C_DENY:99', ?, 'tool', 'Read', 1, 1)`, int64(t0+99)*1000); err != nil {
		t.Fatal(err)
	}
	sum2, err := s.Show(ShowOptions{
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Now:   time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantRate2 := 1.0 / float64(4+len(markers))
	if r := sum2.D.ToolErrorRate["agy"]; r == nil || math.Abs(*r-wantRate2) > 1e-9 {
		t.Errorf("legacy is_error=1,denied=1 row inflated ToolErrorRate: got %v, want %.6f", r, wantRate2)
	}
	if got := hookDenialKind("[ERROR_LOOP] blocked"); got != "guard" {
		t.Errorf("hookDenialKind([ERROR_LOOP]) = %q, want guard", got)
	}
}

func TestRetryLoopsResetAcrossEdit(t *testing.T) {
	s := openT(t, Path(t.TempDir()))
	t0 := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC).UnixMilli()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES
		('TDD', 'agy', '/work/proj', ?, ?),
		('LOOP', 'agy', '/work/proj', ?, ?)`,
		t0, t0+60000, t0+120000, t0+180000)
	// TDD cycle: Bash -> Edit -> Bash -> Read -> Bash must NOT count as a retry loop.
	exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, cmd_hash) VALUES
		('TDD', 'TDD:1', ?, 'tool', 'Bash', 'cmd-test'),
		('TDD', 'TDD:2', ?, 'tool', 'Edit', 'f:file1'),
		('TDD', 'TDD:3', ?, 'tool', 'Bash', 'cmd-test'),
		('TDD', 'TDD:4', ?, 'tool', 'Read', 'f:file2'),
		('TDD', 'TDD:5', ?, 'tool', 'Bash', 'cmd-test')`,
		t0+1000, t0+2000, t0+3000, t0+4000, t0+5000)

	sumTDD, err := s.Show(ShowOptions{
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Now:   time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sumTDD.D.RetryLoops["agy"] != 0 {
		t.Errorf("TDD cycle RetryLoops = %d, want 0", sumTDD.D.RetryLoops["agy"])
	}
	tddTraj, err := s.Trajectories(TrajectoryOptions{SessionID: "TDD"})
	if err != nil || len(tddTraj) != 1 {
		t.Fatalf("TDD traj = %+v, err=%v", tddTraj, err)
	}
	if slices.Contains(tddTraj[0].Anomalies, "RETRY_LOOP") {
		t.Errorf("TDD traj falsely flagged RETRY_LOOP: %v", tddTraj[0].Anomalies)
	}

	// Consecutive identical Bash steps MUST count as 1 retry loop.
	exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, cmd_hash) VALUES
		('LOOP', 'LOOP:1', ?, 'tool', 'Bash', 'cmd-loop'),
		('LOOP', 'LOOP:2', ?, 'tool', 'Bash', 'cmd-loop')`,
		t0+121000, t0+122000)
	sumLoop, err := s.Show(ShowOptions{
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Now:   time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sumLoop.D.RetryLoops["agy"] != 1 {
		t.Errorf("RetryLoops after consecutive Bash = %d, want 1", sumLoop.D.RetryLoops["agy"])
	}
	loopTraj, err := s.Trajectories(TrajectoryOptions{SessionID: "LOOP"})
	if err != nil || len(loopTraj) != 1 || !slices.Contains(loopTraj[0].Anomalies, "RETRY_LOOP") {
		t.Fatalf("LOOP traj = %+v, err=%v; want RETRY_LOOP", loopTraj, err)
	}
}

func TestJudgeVerdictsMigration(t *testing.T) {
	home := t.TempDir()
	dbPath := Path(home)

	// Start from schema 8 and upgrade to schema 9.
	saved := migrations
	if len(saved) < 9 {
		t.Fatalf("len(migrations) = %d, want >= 9", len(saved))
	}
	migrations = saved[:8]
	s8, err := Open(dbPath)
	if err != nil {
		migrations = saved
		t.Fatalf("open schema 8: %v", err)
	}
	if _, err := s8.db.Exec(`INSERT INTO sessions(id, vendor) VALUES ('keep-s8', 'agy')`); err != nil {
		s8.Close()
		migrations = saved
		t.Fatalf("insert schema-8 row: %v", err)
	}
	s8.Close()
	migrations = saved

	s9 := openT(t, dbPath)
	if got := s9.meta(t, "schema"); got != "14" {
		t.Fatalf("schema = %q, want 14", got)
	}
	var idxCnt int
	if err := s9.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='judge_verdicts_ts'`).Scan(&idxCnt); err != nil || idxCnt != 1 {
		t.Fatalf("judge_verdicts_ts index missing: cnt=%d err=%v", idxCnt, err)
	}
	var kept int
	if err := s9.db.QueryRow(`SELECT count(*) FROM sessions WHERE id = 'keep-s8'`).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("lost existing session across migration 9: kept=%d err=%v", kept, err)
	}
	s9.Close()

	t1 := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC).UnixMilli()
	t2 := time.Date(2026, 10, 8, 11, 0, 0, 0, time.UTC).UnixMilli()
	if err := RecordJudgeVerdict(home, JudgeVerdictRow{
		TS:             t1,
		ProjectRoot:    "/work/alpha",
		Target:         "phase-1b",
		Model:          "claude-opus-5-5",
		FallbackReason: "",
		CriterionHash:  "hash-aaa",
		Verdict:        "PASS",
	}); err != nil {
		t.Fatalf("RecordJudgeVerdict 1: %v", err)
	}
	if err := RecordJudgeVerdict(home, JudgeVerdictRow{
		TS:             t2,
		ProjectRoot:    "/work/beta",
		Target:         "phase-1c",
		Model:          "gemini-4-carbon",
		FallbackReason: "anthropic quota low",
		CriterionHash:  "hash-bbb",
		Verdict:        "FAIL",
	}); err != nil {
		t.Fatalf("RecordJudgeVerdict 2: %v", err)
	}

	allRows := judgeVerdictsT(t, dbPath, "", time.Time{}, time.Time{})
	if len(allRows) != 2 {
		t.Fatalf("got %d judge_verdicts, want 2", len(allRows))
	}
	if allRows[0].Model != "gemini-4-carbon" || allRows[0].FallbackReason != "anthropic quota low" || allRows[0].Verdict != "FAIL" {
		t.Errorf("newest verdict = %+v", allRows[0])
	}
	alphaRows := judgeVerdictsT(t, dbPath, "/work/alpha", time.UnixMilli(t1), time.UnixMilli(t2))
	if len(alphaRows) != 1 || alphaRows[0].CriterionHash != "hash-aaa" || alphaRows[0].Verdict != "PASS" {
		t.Errorf("filtered verdicts = %+v", alphaRows)
	}

	// Verify Export includes judge_verdicts.
	expPath := filepath.Join(t.TempDir(), "export.jsonl")
	sExp := openT(t, dbPath)
	if err := sExp.Export(expPath); err != nil {
		t.Fatalf("Export: %v", err)
	}
	rawExp, err := os.ReadFile(expPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawExp), `"table":"judge_verdicts"`) {
		t.Errorf("export missing judge_verdicts table:\n%s", string(rawExp))
	}
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(string(rawExp)), "\n") {
		var rec struct {
			Table string          `json:"table"`
			Row   json.RawMessage `json:"row"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Table == "judge_verdicts" {
			found = true
			break
		}
	}
	if !found {
		t.Error("judge_verdicts row not decoded from export")
	}
}

func judgeVerdictsT(t *testing.T, dbPath, project string, since, now time.Time) []JudgeVerdictRow {
	t.Helper()
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open %s: %v", dbPath, err)
	}
	defer st.Close()
	rows, err := st.JudgeVerdicts(project, since, now)
	if err != nil {
		t.Fatalf("JudgeVerdicts(%q): %v", project, err)
	}
	return rows
}
