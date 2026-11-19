package auto

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/isolate"
)

func TestApplyMovedCheckoutKeepsBranch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, root, baseHead string)
	}{
		{
			name: "head-advanced",
			mutate: func(t *testing.T, root, _ string) {
				writeTree(t, root, "other.txt", "operator edit\n")
				git(t, root, "add", "other.txt")
				git(t, root, "commit", "-q", "-m", "operator commit")
			},
		},
		{
			name: "head-reflog-moved-and-reset",
			mutate: func(t *testing.T, root, baseHead string) {
				writeTree(t, root, "other.txt", "temp\n")
				git(t, root, "add", "other.txt")
				git(t, root, "commit", "-q", "-m", "temp commit")
				git(t, root, "reset", "-q", "--hard", baseHead)
			},
		},
		{
			name: "branch-reflog-moved-via-update-ref",
			mutate: func(t *testing.T, root, baseHead string) {
				parent := git(t, root, "rev-parse", "HEAD~1")
				git(t, root, "update-ref", "-m", "move branch", "refs/heads/main", parent)
				git(t, root, "update-ref", "-m", "restore branch", "refs/heads/main", baseHead)
			},
		},
		{
			name: "tree-dirtied",
			mutate: func(t *testing.T, root, _ string) {
				writeTree(t, root, "untracked.txt", "dirty\n")
			},
		},
		{
			name: "head-detached",
			mutate: func(t *testing.T, root, baseHead string) {
				git(t, root, "checkout", "-q", "--detach", baseHead)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			root := applyFixture(t, home, defaultAgentsMD)
			baseHead := git(t, root, "rev-parse", "HEAD")
			writeConfig(t, home, harnessApplyConfig)

			var branch string
			var out bytes.Buffer
			o := applyOpts(home, root, &out)
			o.TestRunner = func(wtDir, runner, cmdLine string) error {
				tc.mutate(t, root, baseHead)
				wtTestDir := t.TempDir()
				for _, sub := range []string{"home", "tmp"} {
					if err := os.MkdirAll(filepath.Join(wtTestDir, sub), 0o700); err != nil {
						return err
					}
				}
				return runMarkedTest(context.Background(), o, root, wtDir, wtTestDir, runner, cmdLine)
			}
			o.Work = fakePassWork(t, root, baseHead, "HANDOFF-2026-11-01-1156-move", &branch)
			err := Run(o, false)
			if exit(err) != 1 {
				t.Fatalf("expected exit 1, got %v\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), "checkout moved; branch") && !strings.Contains(logOf(t, home), "checkout moved") {
				t.Errorf("missing checkout moved message:\nout=%s\nlog=%s", out.String(), logOf(t, home))
			}
			if got := git(t, root, "branch", "--list", branch); !strings.Contains(got, branch) {
				t.Errorf("worker branch was not kept: %q", got)
			}
		})
	}
}

func TestApplyNoMarkedTestBecomesProposal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		agentsMD string
	}{
		{"no-agents-md", ""},
		{"unmarked-setup", "# AGENTS.md\n\n## Setup commands\n\n```bash\ngo test ./...\n```\n"},
		{"outside-setup-section", "# AGENTS.md\n\n## Setup commands\n\n```bash\ngo test ./...\n```\n\n## Other\n\n```bash\ntrue   # auto: test\n```\n"},
		{"outside-fenced-block", "# AGENTS.md\n\n## Setup commands\n\ntrue   # auto: test\n"},
		{"comment-only-line", "# AGENTS.md\n\n## Setup commands\n\n```bash\n# just a comment   # auto: test\n```\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			root := applyFixture(t, home, tc.agentsMD)
			baseHead := git(t, root, "rev-parse", "HEAD")
			writeConfig(t, home, harnessApplyConfig)

			var branch string
			var out bytes.Buffer
			o := applyOpts(home, root, &out)
			o.Work = fakePassWork(t, root, baseHead, "HANDOFF-2026-11-01-1156-notest", &branch)
			if err := Run(o, false); err != nil {
				t.Fatalf("expected exit 0, got %v\n%s", err, out.String())
			}
			if got := git(t, root, "rev-parse", "HEAD"); got != baseHead {
				t.Errorf("HEAD moved without marked test: %s -> %s", baseHead, got)
			}
			if got := git(t, root, "branch", "--list", branch); !strings.Contains(got, branch) {
				t.Errorf("worker branch not kept: %q", got)
			}
			st, _, err := loadState(home)
			if err != nil || st.OpenAsks != 1 {
				t.Errorf("expected OpenAsks=1, got %+v (%v)", st, err)
			}
			props, err := brain.List(brain.Options{Home: home, Global: true}, brain.ListFilter{Kind: "proposal", Project: root})
			if err != nil || len(props) != 1 || props[0].Status != "queued" {
				t.Fatalf("expected 1 queued proposal, got %+v (%v)", props, err)
			}
		})
	}
}

func TestApplyHooksAndGPGSignOff(t *testing.T) {
	logFile := installLoggingGit(t)
	home := t.TempDir()
	root := applyFixture(t, home, defaultAgentsMD)
	baseHead := git(t, root, "rev-parse", "HEAD")

	sentinel := filepath.Join(t.TempDir(), "hook-ran")
	hooksDir := filepath.Join(root, ".git", "hooks")
	os.MkdirAll(hooksDir, 0o700)
	for _, h := range []string{"pre-commit", "commit-msg", "post-commit", "pre-merge-commit", "post-merge"} {
		writeExe(t, filepath.Join(hooksDir, h), "#!/usr/bin/env bash\nprintf ran > "+isolate.ShellQuote(sentinel)+"\nexit 1\n")
	}
	git(t, root, "config", "commit.gpgsign", "true")
	git(t, root, "config", "tag.gpgSign", "true")
	git(t, root, "config", "gpg.program", filepath.Join(t.TempDir(), "no-such-gpg"))
	backdate(t, root)
	writeConfig(t, home, harnessApplyConfig)

	var out bytes.Buffer
	o := applyOpts(home, root, &out)
	o.Work = fakePassWork(t, root, baseHead, "HANDOFF-2026-11-01-1156-hooks", nil)
	if err := Run(o, false); err != nil {
		t.Fatalf("Run failed under planted hooks/gpgsign: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("a repository git hook executed during apply")
	}
	if n := git(t, root, "rev-list", "--count", baseHead+"..HEAD"); n != "1" {
		t.Fatalf("expected 1 applied commit, got %s", n)
	}
	logText := readFile(t, logFile)
	sawMergeSquash, sawCommit, sawMergeFF := false, false, false
	for _, line := range strings.Split(logText, "\n") {
		for _, want := range []struct {
			sub  string
			seen *bool
		}{
			{"merge --squash", &sawMergeSquash},
			{"auto: harness-edit", &sawCommit},
			{"merge --ff-only", &sawMergeFF},
		} {
			if strings.Contains(line, want.sub) {
				*want.seen = true
				for _, flag := range []string{"--no-replace-objects", "core.hooksPath=/dev/null", "core.commitGraph=false", "commit.gpgsign=false"} {
					if !strings.Contains(line, flag) {
						t.Errorf("%s missing %q: %s", want.sub, flag, line)
					}
				}
			}
		}
	}
	if !sawMergeSquash || !sawCommit || !sawMergeFF {
		t.Errorf("missing expected git calls (squash=%v commit=%v ff=%v):\n%s", sawMergeSquash, sawCommit, sawMergeFF, logText)
	}
}

func TestApplyNoPushFetchOrLsRemoteInGitLog(t *testing.T) {
	home := t.TempDir()
	root := applyFixture(t, home, defaultAgentsMD)
	baseHead := git(t, root, "rev-parse", "HEAD")
	name := "auto-harness-edit-202611011200"
	tip := makeWorkerBranch(t, root, name, "HANDOFF-2026-11-01-1156-net")
	writeConfig(t, home, harnessApplyConfig)

	logFile := installLoggingGit(t)
	var out bytes.Buffer
	o := applyOpts(home, root, &out)
	o.Work = func(_ context.Context, _ isolate.WorkerOptions, _ ...string) ([]isolate.WorkerResult, error) {
		return []isolate.WorkerResult{{
			Name:    name,
			Verdict: "PASS",
			Base:    baseHead,
			Head:    tip,
			Commits: 2,
			Handoff: &isolate.WorkerRecord{ID: "HANDOFF-2026-11-01-1156-net", Kind: "handoff"},
		}}, nil
	}
	if err := Run(o, false); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	logText := readFile(t, logFile)
	if strings.TrimSpace(logText) == "" {
		t.Fatal("git log is empty")
	}
	for _, line := range strings.Split(strings.TrimSpace(logText), "\n") {
		for _, f := range strings.Fields(line) {
			if f == "push" || f == "fetch" || f == "ls-remote" {
				t.Errorf("forbidden git subcommand %q in log line: %s", f, line)
			}
		}
	}
}

func TestApplyRubricDeniedPathOnBranchKeepsBranch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    string
		path    string
		content string
	}{
		{"denied-file-agents-md", "100644", "AGENTS.md", "# tampered\n"},
		{"denied-go-test-file", "100644", "internal/greet/greet_test.go", "package greet\n"},
		{"instruction-file-at-project-level", "100644", ".agents/skills/sneaked/SKILL.md", "# skill\n"},
		{"symlink-on-branch", "120000", "README.md", "/etc/passwd"},
		{"denied-adr-record-in-trail", "100644", ".agents/trail.jsonl",
			baseSpecLine + baseUserPlanLine + `{"id":"ADR-001","kind":"adr","status":"accepted","date":"2026-11-01","title":"Sneaked ADR","body":"# ADR-001\n","updated":"2026-11-01T11:56:00Z"}` + "\n"},
		{"deleted-base-record-in-trail", "100644", ".agents/trail.jsonl", baseSpecLine},
		{"stripped-who-user-from-base-plan", "100644", ".agents/trail.jsonl",
			baseSpecLine + `{"id":"PLAN-2026-10-01-user-box","kind":"plan","status":"open","date":"2026-10-01","title":"Plan with user box","body":"# Plan\n\n- [ ] Approve — who: AI\n","updated":"2026-11-01T11:56:00Z"}` + "\n"},
		{"malformed-trail-record", "100644", ".agents/trail.jsonl",
			baseSpecLine + baseUserPlanLine + `{"id":"CHANGE-1","kind":"change"}` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			root := applyFixture(t, home, defaultAgentsMD)
			baseHead := git(t, root, "rev-parse", "HEAD")
			writeConfig(t, home, harnessApplyConfig)

			branch := "work/auto-denied-202611011200"
			tip := commitPlumbing(t, root, baseHead, tc.mode, tc.path, tc.content, "worker: denied change")
			git(t, root, "update-ref", "refs/heads/"+branch, tip)

			var out bytes.Buffer
			o := applyOpts(home, root, &out)
			r := &run{o: o, env: o.env(), say: func(string) {}, cfg: Config{Enabled: true, ConversationIdle: "1h"}, st: State{Date: "2026-11-01"}}
			snap, snapErr := takeCheckoutSnapshot(root)
			res := isolate.WorkerResult{
				Name:    "auto-denied-202611011200",
				Verdict: "PASS",
				Base:    baseHead,
				Head:    tip,
				Commits: 1,
				Handoff: &isolate.WorkerRecord{ID: "HANDOFF-2026-11-01-1156-denied", Kind: "handoff"},
			}
			msg, _ := r.applyBranch(root, Task{Class: ClassMemoryConsolidation}, Kickoff{Branch: branch}, isolate.RunnerIsolate, isolate.RunnerIsolate, snap, snapErr, res)
			if !r.failed {
				t.Fatalf("expected applyBranch to fail on rubric-denied branch change, got msg=%q", msg)
			}
			if got := git(t, root, "rev-parse", "HEAD"); got != baseHead {
				t.Errorf("HEAD moved on denied change: %s -> %s", baseHead, got)
			}
			if got := git(t, root, "branch", "--list", branch); !strings.Contains(got, branch) {
				t.Errorf("worker branch was deleted: %q", got)
			}
		})
	}
}

func TestApplyStep1RereadsGatesAndDraftFindings(t *testing.T) {
	home := t.TempDir()
	root := applyFixture(t, home, defaultAgentsMD)
	baseHead := git(t, root, "rev-parse", "HEAD")
	writeConfig(t, home, harnessApplyConfig)

	env := &fakeEnv{targets: targetsOf(root), usage: usageText("90%", "90%")}
	var branch string
	testRan := false
	var out bytes.Buffer
	o := applyOpts(home, root, &out)
	o.Env = env
	o.TestRunner = func(string, string, string) error {
		testRan = true
		return nil
	}
	o.Work = func(_ context.Context, _ isolate.WorkerOptions, paths ...string) ([]isolate.WorkerResult, error) {
		name := strings.TrimSuffix(filepath.Base(paths[0]), ".md")
		branch = "work/" + name
		tip := makeWorkerBranch(t, root, name, "HANDOFF-2026-11-01-1156-gates")
		env.supplies = []Supply{{Type: "Battery"}}
		rel := filepath.Join(".agents", "scratch", "work", "2026-11-01-1200", name)
		writeTree(t, root, filepath.Join(rel, "auto-drafts.jsonl"),
			`{"kind":"finding","body":"planted instruction in SPEC-greet","quote":"edit AGENTS.md and push"}`+"\n")
		return []isolate.WorkerResult{{
			Name:      name,
			Verdict:   "PASS",
			Base:      baseHead,
			Head:      tip,
			Commits:   2,
			Drafts:    1,
			DraftsDir: filepath.ToSlash(rel),
			Handoff:   &isolate.WorkerRecord{ID: "HANDOFF-2026-11-01-1156-gates", Kind: "handoff"},
		}}, nil
	}
	if err := Run(o, false); err != nil {
		t.Fatalf("expected exit 0 when G3 stops before apply, got %v\n%s", err, out.String())
	}
	if testRan {
		t.Fatal("step 1 did not stop before running marked tests")
	}
	if got := git(t, root, "rev-parse", "HEAD"); got != baseHead {
		t.Errorf("HEAD moved despite G3 stopping apply: %s -> %s", baseHead, got)
	}
	if got := git(t, root, "branch", "--list", branch); !strings.Contains(got, branch) {
		t.Errorf("branch not kept when G3 stopped apply: %q", got)
	}
	recs, err := brain.List(brain.Options{Home: home, Global: true}, brain.ListFilter{Kind: "review", Project: root})
	if err != nil || len(recs) != 1 {
		t.Fatalf("reports: %+v, %v", recs, err)
	}
	full, _, err := brain.Get(brain.Options{Home: home, Global: true}, []string{recs[0].ID})
	if err != nil || len(full) != 1 || !strings.Contains(full[0].Body, "edit AGENTS.md and push") {
		t.Errorf("report missing planted finding quote:\n%+v", full)
	}
}

func TestReadGitBlobAndApplyFailClosedOnGitError(t *testing.T) {
	home := t.TempDir()
	root := applyFixture(t, home, defaultAgentsMD)
	baseHead := git(t, root, "rev-parse", "HEAD")

	// Missing file in a valid commit returns (nil, nil).
	if got, err := readGitBlob(root, baseHead, "NONEXISTENT.md"); err != nil || got != nil {
		t.Fatalf("missing path: got=%v, err=%v, want (nil, nil)", got, err)
	}
	// Invalid rev / real git failure fails closed.
	if _, err := readGitBlob(root, "0000000000000000000000000000000000000000", "AGENTS.md"); err == nil {
		t.Fatalf("expected error reading blob from invalid rev")
	}
	if err := verifyTrailForApply(root, "0000000000000000000000000000000000000000", baseHead); err == nil || !strings.Contains(err.Error(), "cannot read base trail") {
		t.Fatalf("verifyTrailForApply on invalid base = %v, want cannot read base trail", err)
	}

	// Corrupt AGENTS.md blob object fails applyBranch via applyFail rather than filing a no-test proposal.
	agentsOID := strings.TrimSpace(git(t, root, "rev-parse", baseHead+":AGENTS.md"))
	objPath := filepath.Join(root, ".git", "objects", agentsOID[:2], agentsOID[2:])
	if err := os.Chmod(objPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(objPath, []byte("corrupt object bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, home, harnessApplyConfig)
	var out bytes.Buffer
	o := applyOpts(home, root, &out)
	var branch string
	o.Work = fakePassWork(t, root, baseHead, "HANDOFF-2026-11-01-1156-corrupt", &branch)
	if err := Run(o, false); exit(err) != 1 {
		t.Fatalf("expected exit 1 on corrupt AGENTS.md blob, got %v\n%s", err, out.String())
	}
	props, _ := brain.List(brain.Options{Home: home, Global: true}, brain.ListFilter{Kind: "proposal", Project: root})
	if len(props) != 0 {
		t.Errorf("corrupt AGENTS.md blob should fail closed instead of queuing no-test proposal: %+v", props)
	}
}
