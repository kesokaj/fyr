package auto

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/search"
)

type realTargetsEnv struct {
	*fakeEnv
	targetHome string
}

func (e *realTargetsEnv) Targets(string) ([]dream.Target, error) {
	return dream.Targets(e.targetHome)
}

func seedSweepConversations(t *testing.T, realHome string, entries []struct {
	id       string
	wsJSON   string
	modified time.Time
	text     string
}) {
	t.Helper()
	pb := func(n int, b []byte) []byte {
		return append(binary.AppendUvarint(binary.AppendUvarint(nil, uint64(n<<3|2)), uint64(len(b))), b...)
	}
	pv := func(n int, v uint64) []byte {
		return binary.AppendUvarint(binary.AppendUvarint(nil, uint64(n<<3)), v)
	}
	dir := filepath.Join(realHome, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(dir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	sumPath := filepath.Join(dir, "conversation_summaries.db")
	sumDB, err := sql.Open("sqlite", sumPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sumDB.Close()
	if _, err := sumDB.Exec(`CREATE TABLE IF NOT EXISTS conversation_summaries (conversation_id text, last_modified_time datetime NOT NULL, workspace_uris text NOT NULL, agent_name text NOT NULL DEFAULT '', parent_conversation_id text NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		mod := entry.modified.UTC().Format("2006-01-02 15:04:05.000000000+00:00")
		if _, err := sumDB.Exec(`INSERT INTO conversation_summaries VALUES (?, ?, ?, '', '')`, entry.id, mod, entry.wsJSON); err != nil {
			t.Fatal(err)
		}
		cPath := filepath.Join(dir, "conversations", entry.id+".db")
		cDB, err := sql.Open("sqlite", cPath)
		if err != nil {
			t.Fatal(err)
		}
		_, err = cDB.Exec("CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob); CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
		if err != nil {
			cDB.Close()
			t.Fatal(err)
		}
		t0 := uint64(entry.modified.Unix())
		_, err = cDB.Exec(`INSERT INTO steps(idx, step_type, status, metadata, step_payload) VALUES (0, 14, 3, ?, ?)`,
			pb(1, pv(1, t0)), pb(1, []byte(entry.text)))
		cDB.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestAutoOnBinarySwitchAndAllProjectsSweep(t *testing.T) {
	t.Run("legacy-migration-and-on-without-level-or-checkout", func(t *testing.T) {
		home := homeWithBinary(t)
		fakeManagers(t, "0")
		writeConfig(t, home, `{"enabled": true, "level": "observe", "project_idle": "6h", "user_idle": "15m"}`)
		c, found, err := Load(home)
		if err != nil || !found || !c.Enabled || c.ConversationIdle != defaultConversationIdle {
			t.Fatalf("Load legacy = %+v, found=%v, err=%v", c, found, err)
		}
		var out bytes.Buffer
		o := Options{Home: home, Out: &out, GOOS: "linux", Path: "/p:/q", Repo: t.TempDir(), Now: func() time.Time { return runAt }}
		if err := On(o, Request{ConversationIdle: "2h"}); err != nil {
			t.Fatalf("On plain repo: %v\n%s", err, out.String())
		}
		rawBytes, err := os.ReadFile(ConfigPath(home))
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := json.Unmarshal(rawBytes, &raw); err != nil {
			t.Fatal(err)
		}
		if len(raw) != 2 || raw["enabled"] != true || raw["conversation_idle"] != "2h" {
			t.Fatalf("saved auto.json = %s, want only enabled + conversation_idle", rawBytes)
		}
		o.Repo = ""
		if err := On(o, Request{}); err != nil {
			t.Fatalf("On empty repo: %v", err)
		}
	})

	t.Run("all-projects-sweep-and-harness-dispatch", func(t *testing.T) {
		home := t.TempDir()
		realHome := t.TempDir()
		harnessRoot, _, propID := initHarnessEvalGateFixture(t, home, realHome, "")
		parentSHA := git(t, harnessRoot, "rev-parse", "HEAD")

		unregGit := idleProject(t)
		nonGit := t.TempDir()
		dirtyGit := idleProject(t)
		writeFakeMetricsDB(t, home, dirtyGit, []string{"search,verify", "search,verify", "search,verify"}, []int{0, 0, 0})
		writeTree(t, dirtyGit, "main.go", "package changed\n")

		seedSweepConversations(t, realHome, []struct {
			id       string
			wsJSON   string
			modified time.Time
			text     string
		}{
			{"conv-global", `[]`, runAt.Add(-10 * time.Minute), "active global workspace conversation note"},
			{"conv-nongit", `["file://` + nonGit + `"]`, runAt.Add(-10 * time.Minute), "active nongit workspace conversation note"},
			{"conv-dirty", `["file://` + dirtyGit + `"]`, runAt.Add(-10 * time.Minute), "active dirty checkout conversation note"},
			{"conv-unreg-active", `["file://` + unregGit + `"]`, runAt.Add(-10 * time.Minute), "active unreg workspace conversation note"},
			{"conv-harness-active", `["file://` + harnessRoot + `"]`, runAt.Add(-10 * time.Minute), "active harness workspace conversation note"},
		})

		var out bytes.Buffer
		o := gateOpts(home, realHome, harnessRoot, &out)
		o.Env = &realTargetsEnv{
			fakeEnv: &fakeEnv{
				usage: usageText("90%", "90%"),
			},
			targetHome: realHome,
		}
		var kickoffs []string
		candidateWork := makeCandidateBranchWork(t, harnessRoot, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
		o.Work = func(ctx context.Context, wo isolate.WorkerOptions, paths ...string) ([]isolate.WorkerResult, error) {
			data, err := os.ReadFile(paths[0])
			if err != nil {
				return nil, err
			}
			kickoffs = append(kickoffs, string(data))
			return candidateWork(ctx, wo, paths...)
		}
		o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
			got := 3
			if req.Row == "alpha" && req.Commit == parentSHA {
				got = 2
			}
			return EvalOutcome{Row: req.Row, Passed: got, Graded: req.Runs, RecordID: "TEST-gate"}, nil
		}

		if err := Run(o, false); err != nil {
			t.Fatalf("Run: %v\n%s", err, out.String())
		}
		gotOut := out.String()
		if !strings.Contains(gotOut, "5 of 5 projects checked") {
			t.Fatalf("expected 5 targets checked, got:\n%s", gotOut)
		}
		for _, root := range []string{search.GlobalWorkspace, nonGit, dirtyGit, unregGit, harnessRoot} {
			if !strings.Contains(gotOut, root+": health:") {
				t.Errorf("missing health line for %s in:\n%s", root, gotOut)
			}
		}
		if !strings.Contains(gotOut, dirtyGit+": health: no findings; budget:") || !strings.Contains(gotOut, "worker skipped: the tree is dirty") {
			t.Errorf("expected worker skipped for dirtyGit in:\n%s", gotOut)
		}
		for _, noTask := range []string{search.GlobalWorkspace, nonGit} {
			if !strings.Contains(gotOut, noTask+": health: no findings; budget:") || !strings.Contains(gotOut, "no model task due") {
				t.Errorf("expected no model task due for %s in:\n%s", noTask, gotOut)
			}
		}
		for _, retired := range []string{"G4 operator", "G5 agy", "G7 idle", "G8 dirty"} {
			if strings.Contains(gotOut, retired) {
				t.Errorf("retired gate %q appeared in:\n%s", retired, gotOut)
			}
		}
		if len(kickoffs) != 1 || !strings.Contains(kickoffs[0], "Class: harness-edit") {
			t.Fatalf("expected 1 harness-edit kickoff, got %d:\n%v", len(kickoffs), kickoffs)
		}
		prop := getGlobalProposal(t, home, propID)
		if prop.Status == "queued" {
			t.Errorf("expected harness proposal %s to advance past queued, body:\n%s", propID, prop.Body)
		}
	})

	t.Run("active-vs-stale-conversation-window", func(t *testing.T) {
		home := t.TempDir()
		realHome := t.TempDir()
		proj := idleProject(t)
		seedSweepConversations(t, realHome, []struct {
			id       string
			wsJSON   string
			modified time.Time
			text     string
		}{
			{"conv-active", `["file://` + proj + `"]`, runAt.Add(-10 * time.Minute), "active session should wait longer than one hour"},
			{"conv-stale", `["file://` + proj + `"]`, runAt.Add(-2 * time.Hour), "stale session should dream now after one hour"},
		})
		if err := save(home, Config{Enabled: true, ConversationIdle: "1h"}); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		o := runOpts(home, &out)
		o.RealHome = realHome
		o.Env = &fakeEnv{targets: targetsOf(proj), usage: usageText("90%", "90%")}
		o.IsolateAvailable = func() error { return nil }
		var kickoffText string
		o.Work = func(_ context.Context, _ isolate.WorkerOptions, paths ...string) ([]isolate.WorkerResult, error) {
			data, err := os.ReadFile(paths[0])
			if err != nil {
				return nil, err
			}
			kickoffText = string(data)
			name := strings.TrimSuffix(filepath.Base(paths[0]), ".md")
			return []isolate.WorkerResult{{Name: name, Verdict: "PASS"}}, nil
		}
		if err := Run(o, false); err != nil {
			t.Fatalf("Run: %v\n%s", err, out.String())
		}
		if !strings.Contains(kickoffText, "Class: retro-lite") || !strings.Contains(kickoffText, "1 redacted conversation(s)") {
			t.Fatalf("expected 1 stale retro-lite conversation in kickoff:\nout:\n%s\nkickoff:\n%s", out.String(), kickoffText)
		}
		if !strings.Contains(kickoffText, "stale session should dream now") || strings.Contains(kickoffText, "active session should wait") {
			t.Fatalf("conversation window leak in kickoff:\n%s", kickoffText)
		}
	})

	t.Run("worker-checkout-stop-enforces-gateclean", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			prep func(t *testing.T, root string)
			want string
		}{
			{
				name: "detached HEAD",
				prep: func(t *testing.T, root string) { git(t, root, "checkout", "-q", "--detach", "HEAD") },
				want: "worker skipped: HEAD is not a branch",
			},
			{
				name: "missing upstream",
				prep: func(t *testing.T, root string) { git(t, root, "checkout", "-q", "-b", "topic") },
				want: "worker skipped: the branch has no upstream",
			},
			{
				name: "ahead of upstream",
				prep: func(t *testing.T, root string) {
					writeTree(t, root, "extra.txt", "ahead\n")
					git(t, root, "add", "extra.txt")
					git(t, root, "commit", "-q", "-m", "unpushed")
				},
				want: "worker skipped: the branch is 1 ahead of its upstream",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				home := t.TempDir()
				realHome := t.TempDir()
				proj := idleProject(t)
				writeFakeMetricsDB(t, home, proj, []string{"search,verify", "search,verify", "search,verify"}, []int{0, 0, 0})
				seedSweepConversations(t, realHome, []struct {
					id       string
					wsJSON   string
					modified time.Time
					text     string
				}{
					{"conv-active", `["file://` + proj + `"]`, runAt.Add(-10 * time.Minute), "active session keeps target discovered"},
				})
				tc.prep(t, proj)
				if err := save(home, Config{Enabled: true, ConversationIdle: "1h"}); err != nil {
					t.Fatal(err)
				}
				workCalled := false
				var out bytes.Buffer
				o := runOpts(home, &out)
				o.RealHome = realHome
				o.Env = &fakeEnv{targets: targetsOf(proj), usage: usageText("90%", "90%")}
				o.Work = func(context.Context, isolate.WorkerOptions, ...string) ([]isolate.WorkerResult, error) {
					workCalled = true
					return nil, nil
				}
				if err := Run(o, false); err != nil {
					t.Fatalf("Run: %v\n%s", err, out.String())
				}
				if workCalled {
					t.Fatal("worker ran despite gateClean failure")
				}
				if !strings.Contains(out.String(), tc.want) {
					t.Errorf("missing %q in:\n%s", tc.want, out.String())
				}
				if st, _, _ := loadState(home); st.ModelRuns != 0 {
					t.Errorf("ModelRuns = %d, want 0 when workerCheckoutStop fires", st.ModelRuns)
				}
			})
		}
	})

	t.Run("drafts-only-runs-on-dirty-detached-nongit-and-global", func(t *testing.T) {
		dirtyRoot := idleProject(t)
		writeTree(t, dirtyRoot, "dirty.txt", "wip\n")
		detachedRoot := idleProject(t)
		git(t, detachedRoot, "checkout", "-q", "--detach", "HEAD")
		nonGitRoot := t.TempDir()

		for _, tc := range []struct {
			name   string
			root   string
			wsJSON string
		}{
			{"dirty", dirtyRoot, `["file://` + dirtyRoot + `"]`},
			{"detached", detachedRoot, `["file://` + detachedRoot + `"]`},
			{"non-git", nonGitRoot, `["file://` + nonGitRoot + `"]`},
			{"global", search.GlobalWorkspace, `[]`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				home := t.TempDir()
				realHome := t.TempDir()
				seedSweepConversations(t, realHome, []struct {
					id       string
					wsJSON   string
					modified time.Time
					text     string
				}{
					{"conv-stale-" + tc.name, tc.wsJSON, runAt.Add(-2 * time.Hour), "stale session for drafts-only dreaming should run now"},
				})
				if err := save(home, Config{Enabled: true, ConversationIdle: "1h"}); err != nil {
					t.Fatal(err)
				}
				var gotOpts isolate.WorkerOptions
				workCalled := false
				var out bytes.Buffer
				o := runOpts(home, &out)
				o.RealHome = realHome
				o.Env = &fakeEnv{targets: targetsOf(tc.root), usage: usageText("90%", "90%")}
				o.IsolateAvailable = func() error { return nil }
				o.Work = func(_ context.Context, wo isolate.WorkerOptions, paths ...string) ([]isolate.WorkerResult, error) {
					workCalled = true
					gotOpts = wo
					name := strings.TrimSuffix(filepath.Base(paths[0]), ".md")
					return []isolate.WorkerResult{{Name: name, Verdict: "PASS"}}, nil
				}
				if err := Run(o, false); err != nil {
					t.Fatalf("Run(%s): %v\n%s", tc.name, err, out.String())
				}
				if !workCalled || !gotOpts.DraftsOnly {
					t.Fatalf("DraftsOnly worker did not run on %s: called=%v opts=%+v\nout:\n%s", tc.name, workCalled, gotOpts, out.String())
				}
			})
		}
	})
}
