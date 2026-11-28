package main

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
	fyrisolate "github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/metrics"
)

// TestEvalIngestSkipsReplaced: a sandbox whose home the agent replaced with
// a link (to the real home, here a home holding one conversation) is never
// ingested; the same arrangement without that reason is, which shows the
// link would have been read.
func TestEvalIngestSkipsReplaced(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	home, real, dir := filepath.Join(tmp, "fyr-home"), filepath.Join(tmp, "real"), filepath.Join(tmp, "sandbox")
	agyHome(t, real, "C1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "home")); err != nil {
		t.Fatal(err)
	}
	replaced := eval.Result{Row: "x", Status: eval.StatusFail, Reasons: []string{eval.ReasonReplaced}, Dir: dir}
	if err := ingestRow(home, replaced); err != nil {
		t.Fatal(err)
	}
	if k, _ := passK(t, home, "eval=x"); k != 0 {
		t.Errorf("a replaced sandbox was ingested: k %d", k)
	}
	// Without the reason, a linked home is still not followed (IN35 of the
	// 2026-10-27 audit): only a real directory is a sandbox home.
	if err := ingestRow(home, eval.Result{Row: "link", Status: eval.StatusFail, Reasons: []string{eval.ReasonCheck}, Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if k, _ := passK(t, home, "eval=link"); k != 0 {
		t.Errorf("a linked sandbox home was ingested without the replaced reason: k %d", k)
	}
	// A link one component down, home/.gemini to the real home's, is not
	// followed either (security review of the fix, finding 2).
	deep := filepath.Join(tmp, "deep")
	if err := os.MkdirAll(filepath.Join(deep, "home"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(real, ".gemini"), filepath.Join(deep, "home", ".gemini")); err != nil {
		t.Fatal(err)
	}
	if err := ingestRow(home, eval.Result{Row: "deep", Status: eval.StatusFail, Reasons: []string{eval.ReasonCheck}, Dir: deep}); err != nil {
		t.Fatal(err)
	}
	if k, _ := passK(t, home, "eval=deep"); k != 0 {
		t.Errorf("a sandbox whose .gemini links to the real home was ingested: k %d", k)
	}
	// A row that left processes is not read: one that ignored SIGTERM could
	// swap a directory after the check (security review of the fix, round 2).
	procs := filepath.Join(tmp, "procs")
	agyHome(t, filepath.Join(procs, "home"), "C3")
	if err := ingestRow(home, eval.Result{Row: "procs", Status: eval.StatusFail, Reasons: []string{eval.ReasonProcs}, Dir: procs}); err != nil {
		t.Fatal(err)
	}
	if k, _ := passK(t, home, "eval=procs"); k != 0 {
		t.Errorf("a row that left processes was ingested: k %d", k)
	}
	// Every reason of the one list is skipped, by that list (PLAN.md item 51).
	for _, reason := range fyrisolate.IntegrityReasons {
		row := "integrity-" + strings.ReplaceAll(reason, " ", "-")
		d := filepath.Join(tmp, row)
		agyHome(t, filepath.Join(d, "home"), "C4")
		if err := ingestRow(home, eval.Result{Row: row, Status: eval.StatusFail, Reasons: []string{reason}, Dir: d}); err != nil {
			t.Fatal(err)
		}
		if k, _ := passK(t, home, "eval="+row); k != 0 {
			t.Errorf("a row failed %q was ingested: k %d", reason, k)
		}
	}
	// The control: the same conversation under a real home directory is read.
	ctl := filepath.Join(tmp, "control")
	agyHome(t, filepath.Join(ctl, "home"), "C2")
	if err := ingestRow(home, eval.Result{Row: "ctl", Status: eval.StatusFail, Reasons: []string{eval.ReasonCheck}, Dir: ctl}); err != nil {
		t.Fatal(err)
	}
	if k, _ := passK(t, home, "eval=ctl"); k != 1 {
		t.Errorf("the control was not ingested: k %d", k)
	}
}

// TestEvalIngestSkipsWriteFailed: a sandbox the machine could not write is
// not ingested, as it was not while that failure read sandbox replaced
// (PLAN.md item 48); the same real home with another reason is, which shows
// it would have been read.
func TestEvalIngestSkipsWriteFailed(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "fyr-home")
	for _, c := range []struct {
		row, status, reason string
	}{
		{"w", eval.StatusError, eval.ReasonWrite},
		{"c", eval.StatusFail, eval.ReasonCheck},
	} {
		dir := filepath.Join(tmp, c.row)
		agyHome(t, filepath.Join(dir, "home"), "C-"+c.row)
		if err := ingestRow(home, eval.Result{Row: c.row, Status: c.status, Reasons: []string{c.reason}, Dir: dir}); err != nil {
			t.Fatal(err)
		}
	}
	// Sessions, not pass^k: an ERROR trial's session is never graded, so
	// pass^k would not show it ingested.
	got := map[string]int{}
	for _, s := range exported(t, home, "sessions") {
		got[str(s["label"])]++
	}
	if got["eval=w"] != 0 || got["eval=c"] != 1 {
		t.Errorf("sessions by label %v, want none for eval=w and one for eval=c", got)
	}
}

func TestEvalIngestPopulatesSessionModel(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "fyr-home")
	dir := filepath.Join(tmp, "sb")
	agyHomeModel(t, filepath.Join(dir, "home"), "C-model", "/proj", "gemini-4-carbon-high")
	now := time.Now().UTC()
	runKey := "20261122/row-m/1"
	if err := ingestRow(home, eval.Result{Row: "row-m", Status: eval.StatusPass, Run: runKey, Dir: dir}); err != nil {
		t.Fatalf("ingestRow: %v", err)
	}
	if err := recordTrial(home, metrics.Trial{
		Run: runKey, Label: "eval=row-m", Harness: "abc1234", Status: "pass",
		StartedAt: now, Seconds: 2,
	}); err != nil {
		t.Fatalf("recordTrial: %v", err)
	}
	sessions := exported(t, home, "sessions")
	if len(sessions) != 1 || str(sessions[0]["model"]) != "gemini-4-carbon-high" {
		t.Fatalf("sessions.model = %v, want gemini-4-carbon-high", sessions)
	}
	if got := metrics.LatestTrialModel(home, "row-m", now.Add(-time.Minute).UnixMilli()); got != "gemini-4-carbon-high" {
		t.Errorf("LatestTrialModel = %q, want gemini-4-carbon-high", got)
	}
}

func TestV910EvalIngest(t *testing.T) {
	skipWithoutProcCheck(t)
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })

	tmp := t.TempDir()
	home := filepath.Join(tmp, "fyr-home")
	dir := filepath.Join(tmp, "sandbox")
	sb := filepath.Join(dir, "home")
	app := filepath.Join(dir, "app")
	if err := os.MkdirAll(filepath.Join(app, ".agents", "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	const t0 = 1791453600 // 2026-10-08 10:00:00Z
	when := time.Unix(t0+60, 0).UTC()
	gitAt := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", app}, args...)...)
		stamp := when.Format(time.RFC3339)
		cmd.Env = append(gitCleanEnv(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitAt("init", "-q")
	if err := os.WriteFile(filepath.Join(app, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt("add", "main.go")
	gitAt("commit", "-q", "-m", "work")

	// Sandbox brain.db holds an unexported handoff record written by the trial agent.
	if _, err := brain.Add(brain.Options{Home: sb, Dir: app, Now: func() time.Time { return when }}, brain.AddInput{
		Kind: "handoff", ID: "HANDOFF-2026-10-08-1001-done", Title: "Handoff — done", Body: strings.NewReader("done\n"),
	}); err != nil {
		t.Fatalf("brain.Add in sandbox: %v", err)
	}

	// Sandbox search.db holds a vet_history row with suggested_skills.
	sdb, err := sql.Open("sqlite", filepath.Join(app, ".agents", "scratch", "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, suggested_skills TEXT, pivot_files TEXT, subtask_count INT, directive TEXT, duration_ms INT, timed_out INT, injected_bytes INT, pivots_used_count INT, skills_loaded_count INT, created_at INT)`,
		`INSERT INTO vet_history VALUES ('C-MAIN', 1, 'TASK', 'PROCEED', 90, 'do', 'golang,fyr-bug', 'main.go', 1, 'PROCEED', 30, 0, 256, 0, 0, 1791453600)`,
	} {
		if _, err := sdb.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	sdb.Close()

	// Sandbox conversations: main C-MAIN (gemini-4-carbon-high, checkpoint, knee>300k, 1 anchored + 1 repo SKILL.md) + subagent C-SUB (gemini-3-flash).
	pbB := func(n int, b []byte) []byte {
		out := binary.AppendUvarint(nil, uint64(n<<3|2))
		return append(binary.AppendUvarint(out, uint64(len(b))), b...)
	}
	pbV := func(n int, v uint64) []byte { return binary.AppendUvarint(binary.AppendUvarint(nil, uint64(n<<3)), v) }
	stepMeta := func(sec uint64, tool string, args any) []byte {
		m := pbB(1, pbV(1, sec))
		if tool != "" {
			a, _ := json.Marshal(args)
			m = append(m, pbB(4, append(pbB(2, []byte(tool)), pbB(3, a)...))...)
		}
		return m
	}
	genRow := func(model string, ctxTok uint64) []byte {
		u := append(pbV(2, 1000), pbV(3, 100)...)
		c := pbB(9, pbB(10, append(pbV(1, ctxTok), pbV(4, 1000000)...)))
		return pbB(1, append(append(pbB(4, u), c...), pbB(19, []byte(model))...))
	}
	agyDir := filepath.Join(sb, ".gemini", "antigravity-cli")
	os.MkdirAll(filepath.Join(agyDir, "conversations"), 0o755)
	sumDB, _ := sql.Open("sqlite", filepath.Join(agyDir, "conversation_summaries.db"))
	sumDB.Exec("CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')")
	sumDB.Exec(`INSERT INTO conversation_summaries VALUES ('C-MAIN', '2026-10-08 10:05:00+00:00', '["file://` + app + `"]', '', '')`)
	sumDB.Exec(`INSERT INTO conversation_summaries VALUES ('C-SUB', '2026-10-08 10:04:00+00:00', '["file://` + app + `"]', 'explorer', 'C-MAIN')`)
	sumDB.Close()

	mainDB, _ := sql.Open("sqlite", filepath.Join(agyDir, "conversations", "C-MAIN.db"))
	mainDB.Exec("CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)")
	mainDB.Exec("CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
	mainDB.Exec(`INSERT INTO steps VALUES (0, 14, 3, ?, NULL, NULL)`, stepMeta(t0, "", nil))
	mainDB.Exec(`INSERT INTO steps VALUES (1, 132, 3, ?, NULL, NULL)`, stepMeta(t0+10, "read_file", map[string]string{"Path": filepath.Join(sb, ".gemini", "config", "skills", "golang", "SKILL.md")}))
	mainDB.Exec(`INSERT INTO steps VALUES (2, 132, 3, ?, NULL, NULL)`, stepMeta(t0+20, "read_file", map[string]string{"Path": filepath.Join(app, "skills", "fyr-bug", "SKILL.md")}))
	mainDB.Exec(`INSERT INTO steps VALUES (3, 23, 3, ?, NULL, NULL)`, stepMeta(t0+30, "", nil))
	mainDB.Exec(`INSERT INTO steps VALUES (4, 15, 3, ?, NULL, NULL)`, stepMeta(t0+40, "", nil))
	mainDB.Exec(`INSERT INTO gen_metadata VALUES (0, ?, 0)`, genRow("gemini-4-carbon-high", 310000))
	mainDB.Close()

	subDB, _ := sql.Open("sqlite", filepath.Join(agyDir, "conversations", "C-SUB.db"))
	subDB.Exec("CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)")
	subDB.Exec("CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
	subDB.Exec(`INSERT INTO steps VALUES (0, 15, 3, ?, NULL, NULL)`, stepMeta(t0+25, "", nil))
	subDB.Exec(`INSERT INTO gen_metadata VALUES (0, ?, 0)`, genRow("gemini-3-flash", 10000))
	subDB.Close()

	runKey := "20261123/v910-truth/1"
	res := eval.Result{Row: "v910-truth", Status: eval.StatusPass, Run: runKey, Dir: dir}
	if err := ingestRow(home, res); err != nil {
		t.Fatalf("ingestRow: %v", err)
	}

	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 1. Policy model must be main session only (not subagent gemini-3-flash).
	if got := trialPolicyModel(home, "", res); got != "gemini-4-carbon-high" {
		t.Errorf("trialPolicyModel = %q, want gemini-4-carbon-high", got)
	}
	// 2. Sandbox brain.db handoff + commit -> A.DoDClosed > 0.
	sum, err := s.Show(metrics.ShowOptions{Home: home, Label: "eval=v910-truth", Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if sum.A.DoDClosed["agy"].Closed != 1 {
		t.Errorf("A.DoDClosed = %+v, want Closed=1", sum.A.DoDClosed["agy"])
	}
	// 3. Sandbox search.db turn_vets ingested immediately with suggested_skills & anchored skills_loaded_count=1.
	var sug string
	var loaded int
	db, _ := sql.Open("sqlite", metrics.Path(home))
	defer db.Close()
	if err := db.QueryRow(`SELECT suggested_skills, skills_loaded_count FROM turn_vets WHERE conversation_id = 'C-MAIN'`).Scan(&sug, &loaded); err != nil || sug != "golang,fyr-bug" || loaded != 1 {
		t.Errorf("turn_vets = (suggested=%q, loaded=%d, err=%v), want ('golang,fyr-bug', 1, nil)", sug, loaded, err)
	}
	// 4. Compaction step & knee_exceeded event persisted.
	var comp, knee int
	db.QueryRow(`SELECT count(*) FROM steps WHERE session_id = 'C-MAIN' AND kind = 'compaction'`).Scan(&comp)
	db.QueryRow(`SELECT count(*) FROM events WHERE session_id = 'C-MAIN' AND kind = 'knee_exceeded'`).Scan(&knee)
	if comp != 1 || knee != 1 {
		t.Errorf("comp=%d knee=%d, want 1, 1", comp, knee)
	}
	// Must-fail security: symlinked sandbox .config/fyr must not read target brain.db.
	evilDir := filepath.Join(tmp, "evil-sb")
	evilSB := filepath.Join(evilDir, "home")
	evilApp := filepath.Join(evilDir, "app")
	os.MkdirAll(filepath.Dir(filepath.Join(evilSB, harness.StateDir)), 0o700)
	os.Symlink(filepath.Join(sb, harness.StateDir), filepath.Join(evilSB, harness.StateDir))
	os.MkdirAll(evilApp, 0o755)
	if out, err := exec.Command("git", "-C", evilApp, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init evil: %v\n%s", err, out)
	}
	agyHomeIn(t, evilSB, "C-EVIL", evilApp)
	if err := ingestRow(home, eval.Result{Row: "evil-link", Status: eval.StatusPass, Run: "20261123/evil/1", Dir: evilDir}); err != nil {
		t.Fatalf("ingestRow evil: %v", err)
	}
	var evilHandoffs int
	db.QueryRow(`SELECT count(*) FROM events WHERE session_id = 'C-EVIL' AND kind = 'handoff'`).Scan(&evilHandoffs)
	if evilHandoffs != 0 {
		t.Errorf("symlinked sandbox .config/fyr produced %d handoff events, want 0", evilHandoffs)
	}
	_ = harness.StateDir
}
