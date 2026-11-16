package main

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
	fyrisolate "github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
)

type fleetEnv struct {
	quietEnv
	home string
}

func (fleetEnv) PTYs(int) ([]time.Time, error) {
	return []time.Time{time.Now().Add(-2 * time.Hour)}, nil
}
func (fleetEnv) Ioreg(...string) (string, error)      { return `"HIDIdleTime" = 7200000000000`, nil }
func (fleetEnv) AgySession(string, int) (bool, error) { return false, nil }
func (fleetEnv) Usage(_, _ string) (string, error) {
	return "Gemini Models\tWeekly Limit Remaining\t80%\t2026-11-16T00:00:00Z\nGemini Models\tFive Hour Limit Remaining\t75%\t2026-11-09T23:00:00Z\n", nil
}
func (e fleetEnv) Targets(string) ([]dream.Target, error) { return dream.Targets(e.home) }

type fleetRig struct{ proj, home, real, src, tmp string }

func fyrMust(t *testing.T, in string, args ...string) string {
	t.Helper()
	out, errOut, code := fyrCLI(t, in, args...)
	if code != 0 {
		t.Fatalf("fyr %v exited %d:\nout=%s\nerr=%s", args, code, out, errOut)
	}
	return out
}

func requireContains(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Fatalf("missing %q in:\n%s", w, text)
		}
	}
}

func fleetHookMsg(t *testing.T, r *fleetRig, prompt string) string {
	t.Helper()
	out := runHook[search.PreInvocationOutput](t, r.home, "pre-invocation", search.HookInput{ConversationID: "conv-fleet-lead", WorkspacePaths: []string{r.proj}, LastUserInput: prompt})
	if len(out.InjectSteps) == 0 {
		t.Fatalf("empty pre-invocation InjectSteps for %q", prompt)
	}
	return out.InjectSteps[0].SystemMessage.SystemMessage
}

func newFleetRig(t *testing.T) *fleetRig {
	t.Helper()
	if checkoutRootErr != nil {
		t.Fatal(checkoutRootErr)
	}
	tmp := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = resolved
	}
	if out, err := exec.Command("git", "-C", tmp, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
		t.Skipf("tempdir inside git checkout (%s)", strings.TrimSpace(string(out)))
	}
	fyrBin, binDir, self := buildMockFyr(t), filepath.Join(tmp, "bin"), filepath.Join(tmp, "self")
	r := &fleetRig{proj: filepath.Join(tmp, "mock-fleet-ops"), home: filepath.Join(tmp, "fyr-home"), real: filepath.Join(tmp, "real"), src: checkoutRoot, tmp: tmp}
	for _, d := range []string{r.proj, r.home, r.real, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	materializeMockIn(t, r.proj, fyrBin, "mock-fleet-ops")
	writeTestFile(t, filepath.Join(r.real, fyrisolate.TokenRel), "token")
	selfBody := "#!/bin/bash\nset -euo pipefail\ncase \"${1:-}\" in\ninstall) mkdir -p \"$3/.local/bin\" && cp " + fyrisolate.ShellQuote(fyrBin) + " \"$3/.local/bin/fyr\" && chmod +x \"$3/.local/bin/fyr\" ;;\n*) exec " + fyrisolate.ShellQuote(fyrBin) + " \"$@\" ;;\nesac\n"
	for p, body := range map[string]string{filepath.Join(binDir, "agy"): "#!/bin/sh\nexit 0\n", filepath.Join(binDir, "systemctl"): "#!/bin/sh\nexit 0\n", filepath.Join(binDir, "launchctl"): "#!/bin/sh\nexit 0\n", self: selfBody, filepath.Join(r.home, harness.BinPath): selfBody} {
		writeTestFile(t, p, body)
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldAuto := autoEnv
	autoEnv = fleetEnv{home: r.home}
	t.Cleanup(func() { autoEnv = oldAuto })
	for _, kv := range [][2]string{{"PATH", binDir + string(os.PathListSeparator) + os.Getenv("PATH")}, {"HOME", r.real}, {"TZ", "UTC"}, {"FYR_REPO", ""}} {
		t.Setenv(kv[0], kv[1])
	}
	return r
}

func TestMockScenario3FleetOpsWorkersTrajectoryAndLearningLoop(t *testing.T) {
	r := newFleetRig(t)
	planID := verifyFleetTurns1And2(t, r)
	verifyFleetTurn3GateAndLand(t, r)
	verifyFleetTurn3bTrajectory(t, r)
	verifyFleetTurn4LearningDreamAutoAndCheck(t, r, planID)
}

func verifyFleetTurns1And2(t *testing.T, r *fleetRig) string {
	t.Helper()
	requireContains(t, fleetHookMsg(t, r, "Scaffold this project with /fyr-init-project and define requirements in PRD.md."), "[FYR_TURN_CONTRACT]")
	writeMockFile(t, filepath.Join(r.proj, "AGENTS.md"), "# AGENTS.md — fleet-ops\n\n## Setup commands\n\n```bash\ngofmt -l cmd internal && go vet ./... && go test ./... # auto: test\n```\n")
	writeMockFile(t, filepath.Join(r.proj, "PRD.md"), "# PRD — fleet-ops\n\n## Must\n\n- **R1** — Collector histogram buckets probe latency via an injected clock seam. <!-- provenance: operator, 2026-11-09; enforced by: TestHistogram, internal/collector/probe.go::Histogram -->\n- **R2** — Alert router classifies warning and critical severity deterministically. <!-- provenance: operator, 2026-11-09; enforced by: internal/router/notify.go -->\n- **R3** — Summary CLI renders health probes using stdlib only. <!-- provenance: operator, 2026-11-09; enforced by: TestRender, cmd/fleetctl/main.go -->\n\n## Non-goals\n\n- External agent daemons.\n")
	writeMockFile(t, filepath.Join(r.proj, "SDD.md"), "# SDD — fleet-ops\n\n## Components\n\n| ID | Name | Path |\n|---|---|---|\n| M1 | Collector | `internal/collector` |\n| M2 | Router | `internal/router` |\n| M3 | CLI & Report | `cmd/fleetctl`, `internal/report` |\n\n## Trace\n\n| Requirement | Components |\n|---|---|\n| R1 | M1 |\n| R2 | M2 |\n| R3 | M1, M2, M3 |\n")
	writeMockFile(t, filepath.Join(r.proj, "TDD.md"), "# TDD — fleet-ops\n\n## Trace\n\n| Component | Section | Verified by |\n|---|---|---|\n| M1 | T1 | `TestCollect`, `TestHistogram` |\n| M2 | T2 | `TestRouteAlert`, `TestRouteSeverity` |\n| M3 | T3 | `TestRender` |\n")
	writeMockFile(t, filepath.Join(r.proj, "TODO.md"), "# TODO\n\n## Now\n\n- [ ] 1.1 Run collector and router workers.\n\n## Next\n\n## Backlog\n")
	fyrMust(t, "Title: Testing fleet-ops\n\nRun `gofmt -l cmd internal && go vet ./... && go test ./...`.\n", "record", "add", "guide", "--id", "GUIDE-testing", "-C", r.proj, "--home", r.home)
	fyrMust(t, "Title: Local fleetctl\n\nRun `go run ./cmd/fleetctl`.\n", "record", "add", "deploy", "--id", "DEPLOY-local", "-C", r.proj, "--home", r.home)
	fyrMust(t, "Title: Injected clock seam and stdlib probes\nStatus: accepted\n\nProbes use an injected clock function and never sleep in tests.\nRejected: polling loops, prometheus daemon.\n", "record", "add", "adr", "--id", "ADR-001", "-C", r.proj, "--home", r.home)
	planID := strings.TrimSpace(fyrMust(t, "Title: Parallel collector and router phases\n\n- [ ] 1.1 Collector histogram (`collector`)\n- [ ] 1.2 Router severity (`router`)\n", "record", "add", "plan", "-C", r.proj, "--home", r.home))
	fyrMust(t, "Title: Cold-reader check of plan\n\nPASS.\n", "record", "add", "review", "-C", r.proj, "--home", r.home)
	runCmd(t, r.proj, "git", "add", "-A")
	runCmd(t, r.proj, "git", "commit", "-qm", "docs: scaffold fleet-ops PRD, SDD, TDD, and TODO")
	chkOut, _, c := fyrCLI(t, "", "check", "-C", r.proj, "--home", r.home)
	if c != 1 {
		t.Fatalf("expected pre-worker check exit 1, got %d:\n%s", c, chkOut)
	}
	requireContains(t, chkOut, "TestHistogram")
	requireContains(t, fleetHookMsg(t, r, "Add collector histogram and router severity using polling loops."), `"intent": "FEATURE"`, `"gate": "VET_FIRST"`, "conflicts with ADR-001")
	t2 := runHook[search.PreInvocationOutput](t, r.home, "pre-invocation", search.HookInput{ConversationID: "conv-fleet-lead", WorkspacePaths: []string{r.proj}, LastUserInput: "go all approved — execute the collector and router phases in parallel with /fyr-swarm."})
	if len(t2.InjectSteps) != 0 {
		t.Fatalf("Turn 2 APPROVAL PASS_THROUGH expected zero-token empty injectSteps, got: %+v", t2.InjectSteps)
	}
	return planID
}

func fleetWorkerCommit(t *testing.T, r *fleetRig, name string) string {
	t.Helper()
	wt := filepath.Join(r.tmp, "wt-"+name)
	runCmd(t, r.proj, "git", "worktree", "add", "-q", "-b", "work/"+name, wt)
	switch name {
	case "rogue":
		writeMockFile(t, filepath.Join(wt, "README.md"), readFile(t, filepath.Join(wt, "README.md"))+"\nout-of-scope edit\n")
		runCmd(t, wt, "git", "add", "README.md")
	case "collector":
		writeMockFile(t, filepath.Join(wt, "internal/collector/probe.go"), readFile(t, filepath.Join(wt, "internal/collector/probe.go"))+"\nfunc Histogram(samples []Sample, clock func() int64) (fast, slow int) {\n\t_ = clock()\n\tfor _, s := range samples {\n\t\tif s.LatencyMS < 50 {\n\t\t\tfast++\n\t\t} else {\n\t\t\tslow++\n\t\t}\n\t}\n\treturn fast, slow\n}\n")
		writeMockFile(t, filepath.Join(wt, "internal/collector/probe_test.go"), readFile(t, filepath.Join(wt, "internal/collector/probe_test.go"))+"\nfunc TestHistogram(t *testing.T) {\n\tif f, s := Histogram([]Sample{{Host: \"a\", LatencyMS: 10}, {Host: \"b\", LatencyMS: 80}}, func() int64 { return 42 }); f != 1 || s != 1 {\n\t\tt.Fatalf(\"got %d,%d\", f, s)\n\t}\n}\n")
		runCmd(t, wt, "git", "add", "internal/collector/probe.go", "internal/collector/probe_test.go")
	case "router":
		writeMockFile(t, filepath.Join(wt, "internal/router/notify.go"), readFile(t, filepath.Join(wt, "internal/router/notify.go"))+"\nfunc RouteSeverity(ms, warn, crit int) string {\n\tif ms >= crit {\n\t\treturn \"critical\"\n\t}\n\tif ms >= warn {\n\t\treturn \"warning\"\n\t}\n\treturn \"ok\"\n}\n")
		writeMockFile(t, filepath.Join(wt, "internal/router/notify_test.go"), readFile(t, filepath.Join(wt, "internal/router/notify_test.go"))+"\nfunc TestRouteSeverity(t *testing.T) {\n\tif got := RouteSeverity(150, 80, 120); got != \"critical\" {\n\t\tt.Fatalf(\"got %q\", got)\n\t}\n}\n")
		runCmd(t, wt, "git", "add", "internal/router/notify.go", "internal/router/notify_test.go")
	}
	runCmd(t, wt, "git", "commit", "-qm", name+": implement phase changes")
	return wt
}

func verifyFleetTurn3GateAndLand(t *testing.T, r *fleetRig) {
	t.Helper()
	rogueWT := fleetWorkerCommit(t, r, "rogue")
	if out, _, code := fyrCLI(t, "", "gate", "--lease", "internal/report/summary.go", "--home", r.home, "-C", r.proj, rogueWT); code != 1 {
		t.Fatalf("out-of-lease gate expected exit 1, got %d:\n%s", code, out)
	}
	runCmd(t, r.proj, "git", "worktree", "remove", rogueWT)
	runCmd(t, r.proj, "git", "branch", "-D", "work/rogue")

	for _, w := range []struct{ name, lease string }{
		{"collector", "internal/collector/*"},
		{"router", "internal/router/*"},
	} {
		wt := fleetWorkerCommit(t, r, w.name)
		requireContains(t, fyrMust(t, "", "gate", "--lease", w.lease, "--home", r.home, "-C", r.proj, wt), "gate: PASS")
		runCmd(t, r.proj, "git", "worktree", "remove", wt)
		if w.name == "router" {
			writeMockFile(t, filepath.Join(r.proj, "PRD.md"), strings.ReplaceAll(readFile(t, filepath.Join(r.proj, "PRD.md")), "enforced by: internal/router/notify.go", "enforced by: TestRouteSeverity, internal/router/notify.go::RouteSeverity"))
			runCmd(t, r.proj, "git", "add", "PRD.md")
			runCmd(t, r.proj, "git", "commit", "-qm", "docs: wire R2 enforced-by before router land")
			preChk, _, pc := fyrCLI(t, "", "check", "-C", r.proj, "--home", r.home)
			if pc != 1 {
				t.Fatalf("expected pre-router check exit 1, got %d:\n%s", pc, preChk)
			}
			requireContains(t, preChk, "TestRouteSeverity")
		}
		fyrMust(t, "", "land", "--lease", w.lease, "--message", "merge "+w.name+" worker phase", "--home", r.home, "-C", r.proj, "work/"+w.name)
		fyrMust(t, "Title: Worker "+w.name+" changes\n\nImplemented "+w.name+" scope.\n", "record", "add", "change", "-C", r.proj, "--home", r.home)
		fyrMust(t, "Title: Handoff "+w.name+"\n\n## Next Steps\n\n1. Continue fleet-ops verification.\n", "record", "add", "handoff", "-C", r.proj, "--home", r.home)
	}
	runCmd(t, r.proj, "go", "test", "./...")
	requireContains(t, fyrMust(t, "", "git-check", "-C", r.proj, "--home", r.home), "untracked-files=all\n(none)", "merge collector worker phase", "merge router worker phase")
}

func verifyFleetTurn3bTrajectory(t *testing.T, r *fleetRig) {
	t.Helper()
	seedFleetConversations(t, r.home, r.proj)
	requireContains(t, fyrMust(t, "", "metrics", "ingest", "--home", r.home), "3 sessions")
	var list []metrics.SessionTrajectory
	if err := json.Unmarshal([]byte(fyrMust(t, "", "metrics", "trajectory", "--json", "-C", r.proj, "--home", r.home)), &list); err != nil || len(list) != 1 {
		t.Fatalf("unmarshal trajectory (%v): %+v", err, list)
	}
	lead, agents := list[0], []string(nil)
	for _, ch := range lead.Subagents {
		if ch.ParentID != "conv-fleet-lead" || len(ch.Anomalies) != 0 {
			t.Fatalf("unexpected child trajectory: %+v", ch)
		}
		agents = append(agents, ch.AgentType)
	}
	codes := slices.Clone(lead.Anomalies)
	slices.Sort(codes)
	slices.Sort(agents)
	if !slices.Equal(codes, []string{"KNEE_EXCEEDED", "REDUNDANT_READ", "RETRY_LOOP"}) || lead.PeakContextTok != 195000 || !slices.Equal(lead.TurnTokens, []int64{60000, 130000, 195000}) || !slices.Equal(agents, []string{"reviewer", "verifier"}) {
		t.Fatalf("trajectory mismatch: codes=%v peak=%d turns=%v agents=%v", codes, lead.PeakContextTok, lead.TurnTokens, agents)
	}
}

func execSQL(t *testing.T, dbPath, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open %s: %v", dbPath, err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %s: %v", query, err)
	}
}

func seedFleetConversations(t *testing.T, home, ws string) {
	t.Helper()
	pb := func(n int, b []byte) []byte {
		return append(binary.AppendUvarint(binary.AppendUvarint(nil, uint64(n<<3|2)), uint64(len(b))), b...)
	}
	pv := func(n int, v uint64) []byte { return binary.AppendUvarint(binary.AppendUvarint(nil, uint64(n<<3)), v) }
	toolMeta := func(ts uint64, name, args string) []byte {
		return append(pb(1, pv(1, ts)), pb(4, append(pb(2, []byte(name)), pb(3, []byte(args))...))...)
	}
	genMeta := func(ctxTok uint64) []byte {
		return pb(1, append(pb(4, append(pv(2, 1000), pv(3, 200)...)), append(pb(9, pb(10, pv(1, ctxTok))), pb(19, []byte("claude-opus-4-6"))...)...))
	}
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(dir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	t0, mod, sumPath := uint64(now.Add(-35*time.Minute).Unix()), now.Add(-30*time.Minute).Format(time.RFC3339), filepath.Join(dir, "conversation_summaries.db")
	execSQL(t, sumPath, "CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')")
	for _, row := range [][3]string{{"conv-fleet-lead", "", ""}, {"conv-fleet-verifier", "verifier", "conv-fleet-lead"}, {"conv-fleet-reviewer", "reviewer", "conv-fleet-lead"}} {
		execSQL(t, sumPath, `INSERT INTO conversation_summaries VALUES (?, ?, ?, ?, ?)`, row[0], mod, `["file://`+ws+`"]`, row[1], row[2])
		cPath := filepath.Join(dir, "conversations", row[0]+".db")
		execSQL(t, cPath, "CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob); CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
		if row[0] == "conv-fleet-lead" {
			prose := "Operator note: keep collector probes deterministic in tests\npasted token " + "gh" + "p_" + strings.Repeat("b", 36) + " here\npasted aws " + "AK" + "IAIOSFODNN7EXAMPLE here\nclean closing sentence with four spaces\n"
			execSQL(t, cPath, `INSERT INTO steps(idx, step_type, status, metadata, step_payload) VALUES (0, 14, 3, ?, ?), (1, 132, 3, ?, NULL), (2, 132, 3, ?, NULL), (3, 15, 3, ?, NULL), (4, 132, 7, ?, NULL), (5, 132, 3, ?, NULL), (6, 15, 3, ?, NULL), (7, 15, 3, ?, NULL)`,
				pb(1, pv(1, t0)), pb(1, []byte(prose)), toolMeta(t0+10, "read_file", `{"Path":"internal/collector/probe.go"}`), toolMeta(t0+20, "read_file", `{"Path":"internal/collector/probe.go","StartLine":1,"EndLine":10}`),
				pb(1, pv(1, t0+30)), toolMeta(t0+40, "run_command", `{"CommandLine":"go test ./internal/collector"}`), toolMeta(t0+50, "run_command", `{"CommandLine":"go test ./internal/collector"}`), pb(1, pv(1, t0+60)), pb(1, pv(1, t0+70)))
			execSQL(t, cPath, `INSERT INTO gen_metadata(idx, data) VALUES (3, ?), (6, ?), (7, ?)`, genMeta(60000), genMeta(130000), genMeta(195000))
		} else {
			execSQL(t, cPath, `INSERT INTO steps(idx, step_type, status, metadata) VALUES (0, 132, 3, ?), (1, 15, 3, ?)`, toolMeta(t0+45, "run_command", `{"CommandLine":"go vet ./..."}`), pb(1, pv(1, t0+48)))
			execSQL(t, cPath, `INSERT INTO gen_metadata(idx, data) VALUES (1, ?)`, genMeta(12000))
		}
	}
}

func verifyFleetTurn4LearningDreamAutoAndCheck(t *testing.T, r *fleetRig, planID string) {
	t.Helper()
	requireContains(t, fleetHookMsg(t, r, "Actually, never use time.Sleep in internal/collector/probe_test.go — always use a fake clock seam."), `"intent": "CORRECTION"`, `"gate": "ENRICH_AND_PROCEED"`, `"directive": "CORRECTION"`)
	fyrMust(t, "Never use time.Sleep in collector tests; inject a fake clock.\n", "memory", "add", "--type", "feedback", "--name", "fake-clock-seam", "--description", "Fake clock seam in collector tests", "-C", r.proj, "--home", r.home)
	fyrMust(t, "Title: Never use time.Sleep in collector tests\n\nAlways inject a fake clock seam.\n", "record", "add", "feedback", "--global", "--project", r.proj, "--home", r.home)
	dbPath := filepath.Join(r.home, harness.StateDir, "brain.db")
	if got := queryInt(t, dbPath, `SELECT COUNT(*) FROM records WHERE kind IN ('memory','feedback') AND provenance = 'operator'`); got != 2 {
		t.Fatalf("expected 2 operator-provenance learning records, got %d", got)
	}
	fyrMust(t, "Title: Graduate fake clock rule\n\nPropose `.agents/rules/testing-clocks.md`.\n", "record", "add", "retro", "-C", r.proj, "--home", r.home)
	fyrMust(t, "Title: Complete parallel collector/router wave and learning loop\n\nMerged collector and router workers and logged clock-seam retro.\n", "record", "add", "change", "-C", r.proj, "--home", r.home)
	fyrMust(t, "Title: Close fleet-ops milestone\n\n## Kickoff\n\n```text\nNone — milestone complete.\n```\n", "record", "add", "handoff", "-C", r.proj, "--home", r.home)
	fyrMust(t, "- [x] 1.1 Collector histogram (`collector`)\n- [x] 1.2 Router severity (`router`)\n", "record", "edit", planID, "--body", "-C", r.proj, "--home", r.home)
	writeMockFile(t, filepath.Join(r.proj, "TODO.md"), "# TODO\n\n## Now\n\nNone.\n\n## Next\n\n## Backlog\n")
	runCmd(t, r.proj, "git", "add", "TODO.md")
	runCmd(t, r.proj, "git", "commit", "-qm", "docs: close TODO Now after worker merge")
	fyrMust(t, "", "index", "--register", "-C", r.proj, "--home", r.home)
	dreamOut := fyrMust(t, "", "dream", "--dry-run", "-C", r.proj, "--home", r.home)
	requireContains(t, dreamOut, "[dry-run] prompt:", "[redacted by fyr]", "Operator note: keep collector probes deterministic")
	for _, s := range []string{"gh" + "p_", strings.Repeat("b", 20), "AK" + "IA", "IOSFODNN7EXAMPLE"} {
		if strings.Contains(dreamOut, s) {
			t.Fatalf("dream --dry-run leaked secret %q:\n%s", s, dreamOut)
		}
	}
	verifyFleetAutoAndCheck(t, r, dbPath)
}

func verifyFleetAutoAndCheck(t *testing.T, r *fleetRig, dbPath string) {
	t.Helper()
	fyrMust(t, "", "auto", "on", "--level", "observe", "--project-idle", "1h", "--user-idle", "1m", "--home", r.home)
	requireContains(t, fyrMust(t, "", "auto", "run", "--dry-run", "--home", r.home), "G7 idle:")
	remote, old, headLog := filepath.Join(r.tmp, "origin.git"), time.Now().UTC().Add(-3*time.Hour), filepath.Join(r.proj, ".git", "logs", "HEAD")
	t.Setenv("GIT_COMMITTER_DATE", old.Format(time.RFC3339))
	runCmd(t, r.proj, "git", "commit", "--amend", "--no-edit", "--date", old.Format(time.RFC3339))
	writeMockFile(t, headLog, strings.Join(strings.Fields(readFile(t, headLog))[:4], " ")+" "+strconv.FormatInt(old.Unix(), 10)+" +0000\tcloned\n")
	for _, rel := range strings.Split(runCmd(t, r.proj, "git", "ls-files"), "\n") {
		if err := os.Chtimes(filepath.Join(r.proj, rel), old, old); err != nil {
			t.Fatal(err)
		}
	}
	execSQL(t, dbPath, `UPDATE records SET updated = ?`, old.Format(time.RFC3339))
	requireContains(t, fyrMust(t, "", "auto", "run", "--dry-run", "--home", r.home), "G8 clean:")
	runCmd(t, r.tmp, "git", "init", "--bare", "-q", remote)
	runCmd(t, r.proj, "git", "remote", "add", "origin", remote)
	runCmd(t, r.proj, "git", "push", "-q", "-u", "origin", "main")
	requireContains(t, fyrMust(t, "", "auto", "run", "--dry-run", "--home", r.home), "would run health", "1 of 1 projects checked, level observe")
	for _, rel := range []string{"auto", "dream-state.json", "dream.log"} {
		if _, err := os.Stat(filepath.Join(r.home, harness.StateDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("dry-run created %s: %v", rel, err)
		}
	}
	autoOut := fyrMust(t, "", "auto", "run", "--home", r.home)
	requireContains(t, autoOut, "health: no findings", "1 of 1 projects checked, level observe")
	if strings.Contains(autoOut, "Gemini Models") {
		t.Fatalf("observe level should not probe or print /usage quota (BUG-530):\n%s", autoOut)
	}
	fyrMust(t, "", "auto", "off", "--home", r.home)
	fyrMust(t, "", "check", "-C", r.proj, "--home", r.home)
}
