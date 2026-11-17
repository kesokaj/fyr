package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/auto"
	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
)

// quietEnv is a machine with nobody at it; only its process check is real, so
// a test can put a process behind it.
type quietEnv struct{}

func (quietEnv) Supplies() ([]auto.Supply, error)          { return nil, nil }
func (quietEnv) Pmset(...string) (string, error)           { return "'AC Power'", nil }
func (quietEnv) Sessions(int) ([]map[string]string, error) { return nil, nil }
func (quietEnv) PTYs(int) ([]time.Time, error)             { return nil, nil }
func (quietEnv) Ioreg(...string) (string, error)           { return "", nil }
func (quietEnv) AgySession(goos string, uid int) (bool, error) {
	return dream.AgySession(goos, uid)
}
func (quietEnv) Usage(_, _ string) (string, error)      { return "", nil }
func (quietEnv) Targets(string) ([]dream.Target, error) { return nil, nil }
func (quietEnv) LastConversation(string, string, time.Time) (time.Time, error) {
	return time.Time{}, nil
}

// TestAutoDryRunNamesG5 is E2E (a) with a process list that holds a stand-in:
// a copy of sleep called agy, which the real /proc check reads as a session.
func TestAutoDryRunNamesG5(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip(err)
	}
	data, err := os.ReadFile(sleep)
	if err != nil {
		t.Skip(err)
	}
	agy := filepath.Join(t.TempDir(), "agy")
	if err := os.WriteFile(agy, data, 0o755); err != nil {
		t.Fatal(err)
	}
	home, _ := autoHome(t)
	var out bytes.Buffer
	if err := run([]string{"auto", "on", "--home", home}, &out); err != nil {
		t.Fatalf("%v, %s", err, out.String())
	}
	autoEnv = quietEnv{}
	t.Cleanup(func() { autoEnv = nil })

	dry := func() string {
		out.Reset()
		if err := run([]string{"auto", "run", "--dry-run", "--home", home}, &out); err != nil {
			t.Fatalf("%v, %s", err, out.String())
		}
		return out.String()
	}
	proc := exec.Command(agy, "60")
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			proc.Process.Kill()
			proc.Wait()
		}
	}
	t.Cleanup(stop)
	for i := 0; i < 200; i++ {
		if up, _ := dream.AgySession(runtime.GOOS, os.Getuid()); up {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if up, err := dream.AgySession(runtime.GOOS, os.Getuid()); err != nil || !up {
		t.Fatalf("agy stand-in not visible: up=%v err=%v", up, err)
	}
	got := dry()
	for _, want := range []string{"G1 switch: on", "G2 lock: not taken in a dry run", "budget: not read in a dry run"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "G5 agy session") {
		t.Errorf("G5 should be retired in Phase 3A:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir, "auto")); err == nil {
		t.Error("a dry run made auto/")
	}
	stop()
}

func TestChecksInstallFindings(t *testing.T) {
	home, _ := autoHome(t)
	var out bytes.Buffer
	// A checkout with nothing installed: the timer is not there and every
	// file differs, and each comes back as a finding.
	root := repoRoot(t)
	got := autoInstallFindings(root, home)
	var rules []string
	for _, f := range got {
		rules = append(rules, f.Rule)
	}
	joined := strings.Join(rules, " ")
	if !strings.Contains(joined, "timer") || !strings.Contains(joined, "drift") {
		t.Errorf("%v", got)
	}
	if err := run([]string{"auto", "on", "--home", home}, &out); err != nil {
		t.Fatalf("%v, %s", err, out.String())
	}
	for _, f := range autoInstallFindings(root, home) {
		if f.Rule == "timer" {
			t.Errorf("the timer is installed: %v", f)
		}
	}
}

func TestChecksMetricsFindingsEmptyStore(t *testing.T) {
	home := t.TempDir()
	if got, err := autoMetricsFindings(home); len(got) != 0 || err != nil {
		t.Errorf("%v, %v", got, err)
	}
}

func TestAutoBareDryRun(t *testing.T) {
	home, _ := autoHome(t)
	var out bytes.Buffer
	if err := run([]string{"auto", "--dry-run", "--home", home}, &out); err != nil {
		t.Fatalf("auto --dry-run (off): %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "G1 switch: off") {
		t.Errorf("auto --dry-run (off) output:\n%s", out.String())
	}

	if err := run([]string{"auto", "on", "--home", home}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	autoEnv = quietEnv{}
	t.Cleanup(func() { autoEnv = nil })

	out.Reset()
	if err := run([]string{"auto", "--dry-run", "--home", home}, &out); err != nil {
		t.Fatalf("auto --dry-run (on): %v\n%s", err, out.String())
	}
	for _, want := range []string{"G1 switch: on", "metrics: not ingested in a dry run", "dream: not run in a dry run"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("auto --dry-run (on) missing %q:\n%s", want, out.String())
		}
	}
	for _, rel := range []string{"auto", "metrics.db", "dream.log", "dream-state.json"} {
		if _, err := os.Stat(filepath.Join(home, harness.StateDir, rel)); !os.IsNotExist(err) {
			t.Errorf("auto --dry-run created %s", rel)
		}
	}
	if err := run([]string{"auto", "--home", home}, &bytes.Buffer{}); exitOf(err) != 2 {
		t.Errorf("bare auto exit = %d (%v), want 2", exitOf(err), err)
	}
	if err := run([]string{"auto", "--dry-run", "--conversation-idle", "2h", "--home", home}, &bytes.Buffer{}); exitOf(err) != 2 {
		t.Errorf("auto --dry-run --conversation-idle exit = %d (%v), want 2", exitOf(err), err)
	}
}

func seedDreamAutoConversation(t *testing.T, home, proj string, modified time.Time) {
	t.Helper()
	pb := func(n int, b []byte) []byte {
		return append(binary.AppendUvarint(binary.AppendUvarint(nil, uint64(n<<3|2)), uint64(len(b))), b...)
	}
	pv := func(n int, v uint64) []byte { return binary.AppendUvarint(binary.AppendUvarint(nil, uint64(n<<3)), v) }
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(dir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	sumDB := filepath.Join(dir, "conversation_summaries.db")
	execSQL(t, sumDB, "CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')")
	execSQL(t, sumDB, `INSERT INTO conversation_summaries VALUES (?, ?, ?, '', '')`, "conv-s1", modified.UTC().Format("2006-01-02 15:04:05.000000000+00:00"), `["file://`+proj+`"]`)
	cDB := filepath.Join(dir, "conversations", "conv-s1.db")
	execSQL(t, cDB, "CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob); CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
	t0 := uint64(modified.Unix())
	toolMeta := append(pb(1, pv(1, t0)), pb(4, append(pb(2, []byte("run_command")), pb(3, []byte(`{"CommandLine":"git status"}`))...))...)
	execSQL(t, cDB, `INSERT INTO steps(idx, step_type, status, metadata, step_payload) VALUES (0, 14, 3, ?, ?), (1, 132, 3, ?, NULL)`,
		pb(1, pv(1, t0)), pb(1, []byte("check repo status")), toolMeta)
}

func setupAutoDreamE2ERig(t *testing.T, emitTool bool) (home, proj, recDir string) {
	t.Helper()
	home, _ = autoHome(t)
	token := filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	if err := os.MkdirAll(filepath.Dir(token), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	proj = t.TempDir()
	old := time.Now().UTC().Add(-48 * time.Hour)
	runCmd(t, proj, "git", "init", "-q")
	writeTestFile(t, filepath.Join(proj, ".gitignore"), ".agents/scratch/*\n")
	writeTestFile(t, filepath.Join(proj, "main.go"), "package main\n")
	runCmd(t, proj, "git", "add", ".gitignore", "main.go")
	cmd := exec.Command("git", "-C", proj, "-c", "user.email=t@example.invalid", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")
	cmd.Env = append(gitCleanEnv(), "GIT_AUTHOR_DATE="+old.Format(time.RFC3339), "GIT_COMMITTER_DATE="+old.Format(time.RFC3339))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	if _, err := brain.Add(brain.Options{Home: home, Dir: proj, Now: func() time.Time { return old }}, brain.AddInput{
		Kind: "bug", Title: "Old check bug", Body: strings.NewReader("Symptom: missed check.\nRule: run check.\n"),
	}); err != nil {
		t.Fatal(err)
	}
	fyrMust(t, "", "index", "--register", "-C", proj, "--home", home)
	seedDreamAutoConversation(t, home, proj, old)

	recDir = t.TempDir()
	bin := t.TempDir()
	for _, m := range []string{"systemctl", "launchctl"} {
		writeTestFile(t, filepath.Join(bin, m), "#!/bin/sh\nexit 0\n")
		os.Chmod(filepath.Join(bin, m), 0o755)
	}
	toolLine := ""
	if emitTool {
		toolLine = `printf '%s\n' '{"event":"step_update","step_update":{"step_type":"tool","tool_name":"run_command"}}'` + "\n"
	}
	agyScript := "#!/bin/sh\n" +
		`case "$*" in *--mode*plan*)` + "\n" +
		`  printf '%s\n' "$@" > "` + filepath.Join(recDir, "args") + `"` + "\n" +
		`  cat > "` + filepath.Join(recDir, "stdin") + `"` + "\n" +
		`  printf '%s\n' '{"event":"init"}'` + "\n" +
		toolLine +
		`  printf '%s\n' '{"event":"result","result":{"status":"SUCCESS","response":"{\"proposals\":[{\"title\":\"Consolidate check\",\"kind\":\"rule\",\"target\":\"project\",\"file\":\"AGENTS.md\",\"text\":\"Always run check.\",\"evidence\":\"seen twice\"}],\"skills\":[]}"}}'` + "\n" +
		`  exit 0 ;;` + "\n" +
		`*) exit 1 ;;` + "\n" +
		`esac` + "\n"
	writeTestFile(t, filepath.Join(bin, "agy"), agyScript)
	os.Chmod(filepath.Join(bin, "agy"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	fyrMust(t, "", "auto", "on", "--home", home)
	return home, proj, recDir
}

func TestAutoRunEndToEndIngestFirstAndPlanModeDream(t *testing.T) {
	autoEnv = quietEnv{}
	t.Cleanup(func() { autoEnv = nil })

	t.Run("ingests metrics before plan-mode no-tool dream", func(t *testing.T) {
		home, _, recDir := setupAutoDreamE2ERig(t, false)
		out := fyrMust(t, "", "auto", "run", "--home", home)
		if !strings.Contains(out, "metrics: 1 sessions") {
			t.Errorf("auto run output missing ingest line:\n%s", out)
		}
		argsBytes, err := os.ReadFile(filepath.Join(recDir, "args"))
		if err != nil {
			t.Fatalf("agy was not called in plan mode: %v", err)
		}
		args := string(argsBytes)
		if !strings.Contains(args, "--mode\nplan\n") || !strings.Contains(args, "--input-format\nstream-json\n") {
			t.Errorf("agy args missing plan mode or stream-json:\n%s", args)
		}
		if strings.Contains(args, "--dangerously-skip-permissions") {
			t.Errorf("agy args bypassed permissions:\n%s", args)
		}
		stdinBytes, err := os.ReadFile(filepath.Join(recDir, "stdin"))
		if err != nil {
			t.Fatal(err)
		}
		var msg struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(stdinBytes), &msg); err != nil {
			t.Fatalf("unmarshal dream stdin: %v\n%s", err, stdinBytes)
		}
		if !strings.Contains(msg.Message.Content, "<<<data metrics:") || !strings.Contains(msg.Message.Content, `"B.tool_calls.agy": 1`) {
			t.Errorf("dream prompt lacks metrics ingested before dream:\n%s", msg.Message.Content)
		}
		recs, err := brain.List(brain.Options{Home: home, Global: true}, brain.ListFilter{Kind: "proposal", Open: true})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, r := range recs {
			if r.Title == "Dream, project — Consolidate check" && r.Status == "queued" {
				found = true
			}
		}
		if !found {
			t.Errorf("missing queued dream proposal in brain.db: %+v", recs)
		}
	})

	t.Run("tool call during dream fails run and stores no proposal", func(t *testing.T) {
		home, _, _ := setupAutoDreamE2ERig(t, true)
		out, _, code := fyrCLI(t, "", "auto", "run", "--home", home)
		if code != 1 {
			t.Fatalf("auto run with tool call exit = %d, want 1\nout=%s", code, out)
		}
		recs, err := brain.List(brain.Options{Home: home, Global: true}, brain.ListFilter{Kind: "proposal"})
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) != 0 {
			t.Errorf("tool-using dream stored %d proposals, want 0: %+v", len(recs), recs)
		}
	})
}
