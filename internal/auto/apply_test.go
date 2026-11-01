package auto

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/work"
)

// installLoggingGit puts a wrapper git on PATH that appends every invocation's
// argv to logFile before execing the real git binary.
func installLoggingGit(t *testing.T) string {
	t.Helper()
	realGit, err := lookRealGit()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "git.log")
	script := "#!/usr/bin/env bash\n" +
		"printf '%s\\n' \"$*\" >> " + sandbox.ShellQuote(logFile) + "\n" +
		"exec " + sandbox.ShellQuote(realGit) + " \"$@\"\n"
	writeExe(t, filepath.Join(bin, "git"), script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logFile
}

func writeExe(t *testing.T, path, script string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func lookRealGit() (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		p := filepath.Join(dir, "git")
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", errors.New("git not found on PATH")
}

const baseSpecLine = `{"id":"SPEC-greet","kind":"spec","status":"current","date":"2026-10-01","title":"Greeting spec","body":"# SPEC-greet\n\nCode: ` + "`internal/greet/greet.go`" + `\n","updated":"2026-10-01T00:00:00Z"}` + "\n"
const baseUserPlanLine = `{"id":"PLAN-2026-10-01-user-box","kind":"plan","status":"open","date":"2026-10-01","title":"Plan with user box","body":"# Plan\n\n- [ ] Approve — who: user\n","updated":"2026-10-01T00:00:00Z"}` + "\n"

// applyFixture builds an idle, clean repository with its bare remote, local
// user.name/user.email, a stale spec SPEC-greet, and the given AGENTS.md text.
func applyFixture(t *testing.T, agentsMD string) string {
	t.Helper()
	root := idleProject(t)
	git(t, root, "config", "user.name", "Fixture Author")
	git(t, root, "config", "user.email", "author@fixture.local")
	if agentsMD != "" {
		writeTree(t, root, "AGENTS.md", agentsMD)
	}
	writeTree(t, root, "internal/greet/greet.go", "package greet\n\nfunc Greet() string { return \"hi\" }\n")
	writeTree(t, root, ".agents/trail.jsonl", baseSpecLine+baseUserPlanLine)
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "fixture: spec and agents")
	writeTree(t, root, "internal/greet/greet.go", "package greet\n\n// Added comment.\nfunc Greet() string { return \"hi\" }\n")
	git(t, root, "add", "internal/greet/greet.go")
	git(t, root, "commit", "-q", "-m", "fixture: code change after spec")
	git(t, root, "push", "-q")
	backdate(t, root)
	return root
}

const defaultAgentsMD = "# AGENTS.md\n\n## Setup commands\n\n```bash\ntrue   # auto: test\n```\n"

// commitPlumbing creates a commit on parent in root using a temporary index file
// and git plumbing (read-tree, hash-object, update-index, write-tree, commit-tree)
// so HEAD, refs/heads/main, their reflogs and repository hooks are untouched.
func commitPlumbing(t *testing.T, root, parent, mode, path, content, msg string) string {
	t.Helper()
	idx := filepath.Join(t.TempDir(), "index")
	runIdx := func(in string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+idx,
			"GIT_AUTHOR_NAME=w", "GIT_AUTHOR_EMAIL=w@localhost",
			"GIT_COMMITTER_NAME=w", "GIT_COMMITTER_EMAIL=w@localhost")
		if in != "" {
			cmd.Stdin = strings.NewReader(in)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runIdx("", "read-tree", parent)
	blob := runIdx(content, "hash-object", "-w", "--stdin")
	runIdx("", "update-index", "--add", "--cacheinfo", mode+","+blob+","+path)
	tree := runIdx("", "write-tree")
	return runIdx("", "commit-tree", tree, "-p", parent, "-m", msg)
}

// makeWorkerBranch creates refs/heads/work/<name> in root with two commits that
// restamp SPEC-greet and add a worker handoff record, without moving HEAD.
func makeWorkerBranch(t *testing.T, root, name, handoffID string) string {
	t.Helper()
	base := git(t, root, "rev-parse", "HEAD")
	trailText := `{"id":"SPEC-greet","kind":"spec","status":"current","date":"2026-10-01","title":"Greeting spec","body":"# SPEC-greet\n\nCode: ` + "`internal/greet/greet.go`" + `\n\nRestamped.\n","updated":"2026-11-01T11:55:00Z"}` + "\n" + baseUserPlanLine
	c1 := commitPlumbing(t, root, base, "100644", ".agents/trail.jsonl", trailText, "worker: restamp SPEC-greet")
	handoffLine := `{"id":"` + handoffID + `","kind":"handoff","status":"","date":"2026-11-01","title":"Handoff — restamp SPEC-greet","body":"# Handoff\n\nRestamped SPEC-greet.\n","updated":"2026-11-01T11:56:00Z"}` + "\n"
	c2 := commitPlumbing(t, root, c1, "100644", ".agents/trail.jsonl", trailText+handoffLine, "worker: handoff")
	git(t, root, "update-ref", "refs/heads/work/"+name, c2)
	return c2
}

func fakePassWork(t *testing.T, root, baseHead, handoffID string, branchOut *string) func(context.Context, work.Options, ...string) ([]work.Result, error) {
	return func(_ context.Context, _ work.Options, paths ...string) ([]work.Result, error) {
		name := strings.TrimSuffix(filepath.Base(paths[0]), ".md")
		if branchOut != nil {
			*branchOut = "work/" + name
		}
		tip := makeWorkerBranch(t, root, name, handoffID)
		return []work.Result{{
			Name:    name,
			Verdict: "PASS",
			Base:    baseHead,
			Head:    tip,
			Commits: 2,
			Handoff: &work.Record{ID: handoffID, Kind: "handoff", Title: "Handoff — restamp SPEC-greet"},
		}}, nil
	}
}

func TestApplyHappyPath(t *testing.T) {
	logFile := installLoggingGit(t)
	home := t.TempDir()
	markerFile := filepath.Join(t.TempDir(), "test-ran")
	agents := "# AGENTS.md\n\n## Setup commands\n\n```bash\ntest -z \"${GIT_DIR:-}\" && printf ok > " + sandbox.ShellQuote(markerFile) + "   # auto: test\n```\n"
	root := applyFixture(t, agents)
	baseHead := git(t, root, "rev-parse", "HEAD")
	writeConfig(t, home, validConfig)

	const handoffID = "HANDOFF-2026-11-01-1156-handoff-restamp-spec-greet"
	var branch string
	var out bytes.Buffer
	o := driftOpts(home, root, &out)
	passWork := fakePassWork(t, root, baseHead, handoffID, &branch)
	o.Work = func(ctx context.Context, wopts work.Options, paths ...string) ([]work.Result, error) {
		res, err := passWork(ctx, wopts, paths...)
		t.Setenv("GIT_DIR", "/nonexistent/git-dir")
		return res, err
	}
	err := Run(o, false)
	os.Unsetenv("GIT_DIR")
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if got := readFile(t, markerFile); got != "ok" {
		t.Fatalf("marked test did not run: %q", got)
	}
	if n := git(t, root, "rev-list", "--count", baseHead+"..HEAD"); n != "1" {
		t.Fatalf("expected 1 commit past base, got %s", n)
	}
	newHead := git(t, root, "rev-parse", "HEAD")
	shortHead := newHead[:7]
	if got := git(t, root, "branch", "--list", branch); got != "" {
		t.Errorf("worker branch still exists: %q", got)
	}
	entries, _ := os.ReadDir(dir(home))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "wt-") {
			t.Errorf("worktree left behind: %s", e.Name())
		}
	}
	subj := git(t, root, "log", "-1", "--format=%s")
	if !strings.HasPrefix(subj, "auto: spec-drift — ") {
		t.Errorf("subject = %q", subj)
	}
	body := git(t, root, "log", "-1", "--format=%B")
	recs, err := brain.List(brain.Options{Home: home, Global: true}, brain.ListFilter{Kind: "review", Project: root})
	if err != nil || len(recs) != 1 {
		t.Fatalf("reports: %+v, %v", recs, err)
	}
	reportID := recs[0].ID
	for _, want := range []string{reportID, handoffID, "revert: git revert HEAD"} {
		if !strings.Contains(body, want) {
			t.Errorf("commit body missing %q:\n%s", want, body)
		}
	}
	if trailers := git(t, root, "log", "-1", "--format=%(trailers)"); strings.TrimSpace(trailers) != "" {
		t.Errorf("commit has trailers: %q", trailers)
	}
	if got := git(t, root, "log", "-1", "--format=%an <%ae> / %cn <%ce>"); got != "Fixture Author <author@fixture.local> / Fixture Author <author@fixture.local>" {
		t.Errorf("identity = %q", got)
	}
	full, _, err := brain.Get(brain.Options{Home: home, Global: true}, []string{reportID})
	if err != nil || len(full) != 1 {
		t.Fatalf("get report: %v", err)
	}
	if !strings.Contains(full[0].Body, shortHead) || !strings.Contains(full[0].Body, "revert: git revert "+shortHead) {
		t.Errorf("report body missing commit or revert:\n%s", full[0].Body)
	}
	git(t, root, "revert", "--no-edit", "HEAD")
	if diff := git(t, root, "diff", baseHead, "HEAD"); diff != "" {
		t.Errorf("git revert did not restore base:\n%s", diff)
	}
	_ = logFile
}

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
			root := applyFixture(t, defaultAgentsMD)
			baseHead := git(t, root, "rev-parse", "HEAD")
			writeConfig(t, home, validConfig)

			var branch string
			var out bytes.Buffer
			o := driftOpts(home, root, &out)
			o.TestRunner = func(wtDir, _ string, cmdLine string) error {
				tc.mutate(t, root, baseHead)
				return runShellTest(wtDir, cmdLine, o.limit(workerTimeout))
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

func TestApplyRedMarkedTestKeepsBranch(t *testing.T) {
	t.Run("marked-test-fails", func(t *testing.T) {
		home := t.TempDir()
		root := applyFixture(t, "# AGENTS.md\n\n## Setup commands\n\n```bash\nfalse   # auto: test\n```\n")
		baseHead := git(t, root, "rev-parse", "HEAD")
		writeConfig(t, home, validConfig)

		var branch string
		var out bytes.Buffer
		o := driftOpts(home, root, &out)
		o.Work = fakePassWork(t, root, baseHead, "HANDOFF-2026-11-01-1156-red", &branch)
		err := Run(o, false)
		if exit(err) != 1 {
			t.Fatalf("expected exit 1 on failing test, got %v\n%s", err, out.String())
		}
		if got := git(t, root, "rev-parse", "HEAD"); got != baseHead {
			t.Errorf("HEAD moved from %s to %s", baseHead, got)
		}
		if got := git(t, root, "branch", "--list", branch); !strings.Contains(got, branch) {
			t.Errorf("worker branch was deleted on red test: %q", got)
		}
	})

	t.Run("fyr-check-fails-when-prd-present", func(t *testing.T) {
		home := t.TempDir()
		root := applyFixture(t, defaultAgentsMD)
		writeTree(t, root, "PRD.md", "# PRD\n\n## Must\n\n- **R1** — Req.\n  <!-- provenance: op, 2026-11-01; enforced by: TestNonExistentCheckPin -->\n")
		git(t, root, "add", "PRD.md")
		git(t, root, "commit", "-q", "-m", "add prd with missing test pin")
		git(t, root, "push", "-q")
		backdate(t, root)
		baseHead := git(t, root, "rev-parse", "HEAD")
		writeConfig(t, home, validConfig)

		var branch string
		var out bytes.Buffer
		o := driftOpts(home, root, &out)
		o.Work = fakePassWork(t, root, baseHead, "HANDOFF-2026-11-01-1156-prd", &branch)
		err := Run(o, false)
		if exit(err) != 1 {
			t.Fatalf("expected exit 1 when fyr check fails, got %v\n%s", err, out.String())
		}
		if got := git(t, root, "rev-parse", "HEAD"); got != baseHead {
			t.Errorf("HEAD moved from %s to %s", baseHead, got)
		}
		if got := git(t, root, "branch", "--list", branch); !strings.Contains(got, branch) {
			t.Errorf("worker branch was deleted on red fyr check: %q", got)
		}
	})
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
			root := applyFixture(t, tc.agentsMD)
			baseHead := git(t, root, "rev-parse", "HEAD")
			writeConfig(t, home, validConfig)

			var branch string
			var out bytes.Buffer
			o := driftOpts(home, root, &out)
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
	root := applyFixture(t, defaultAgentsMD)
	baseHead := git(t, root, "rev-parse", "HEAD")

	sentinel := filepath.Join(t.TempDir(), "hook-ran")
	hooksDir := filepath.Join(root, ".git", "hooks")
	os.MkdirAll(hooksDir, 0o700)
	for _, h := range []string{"pre-commit", "commit-msg", "post-commit", "pre-merge-commit", "post-merge"} {
		writeExe(t, filepath.Join(hooksDir, h), "#!/usr/bin/env bash\nprintf ran > "+sandbox.ShellQuote(sentinel)+"\nexit 1\n")
	}
	git(t, root, "config", "commit.gpgsign", "true")
	git(t, root, "config", "tag.gpgSign", "true")
	git(t, root, "config", "gpg.program", filepath.Join(t.TempDir(), "no-such-gpg"))
	backdate(t, root)
	writeConfig(t, home, validConfig)

	var out bytes.Buffer
	o := driftOpts(home, root, &out)
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
			{"auto: spec-drift", &sawCommit},
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
	root := applyFixture(t, defaultAgentsMD)
	baseHead := git(t, root, "rev-parse", "HEAD")
	name := "auto-spec-drift-202611011200"
	tip := makeWorkerBranch(t, root, name, "HANDOFF-2026-11-01-1156-net")
	writeConfig(t, home, validConfig)

	logFile := installLoggingGit(t)
	var out bytes.Buffer
	o := driftOpts(home, root, &out)
	o.Work = func(_ context.Context, _ work.Options, _ ...string) ([]work.Result, error) {
		return []work.Result{{
			Name:    name,
			Verdict: "PASS",
			Base:    baseHead,
			Head:    tip,
			Commits: 2,
			Handoff: &work.Record{ID: "HANDOFF-2026-11-01-1156-net", Kind: "handoff"},
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
			root := applyFixture(t, defaultAgentsMD)
			baseHead := git(t, root, "rev-parse", "HEAD")
			writeConfig(t, home, validConfig)

			var branch string
			var out bytes.Buffer
			o := driftOpts(home, root, &out)
			o.Work = func(_ context.Context, _ work.Options, paths ...string) ([]work.Result, error) {
				name := strings.TrimSuffix(filepath.Base(paths[0]), ".md")
				branch = "work/" + name
				tip := commitPlumbing(t, root, baseHead, tc.mode, tc.path, tc.content, "worker: denied change")
				git(t, root, "update-ref", "refs/heads/"+branch, tip)
				return []work.Result{{
					Name:    name,
					Verdict: "PASS",
					Base:    baseHead,
					Head:    tip,
					Commits: 1,
					Handoff: &work.Record{ID: "HANDOFF-2026-11-01-1156-denied", Kind: "handoff"},
				}}, nil
			}
			err := Run(o, false)
			if exit(err) != 1 {
				t.Fatalf("expected exit 1 on rubric-denied branch change, got %v\n%s", err, out.String())
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
	root := applyFixture(t, defaultAgentsMD)
	baseHead := git(t, root, "rev-parse", "HEAD")
	writeConfig(t, home, validConfig)

	env := &fakeEnv{targets: targetsOf(root), usage: usageText("90%", "90%")}
	var branch string
	testRan := false
	var out bytes.Buffer
	o := driftOpts(home, root, &out)
	o.Env = env
	o.TestRunner = func(string, string, string) error {
		testRan = true
		return nil
	}
	o.Work = func(_ context.Context, _ work.Options, paths ...string) ([]work.Result, error) {
		name := strings.TrimSuffix(filepath.Base(paths[0]), ".md")
		branch = "work/" + name
		tip := makeWorkerBranch(t, root, name, "HANDOFF-2026-11-01-1156-gates")
		env.agy = func() (bool, error) { return true, nil }
		rel := filepath.Join(".agents", "scratch", "work", "2026-11-01-1200", name)
		writeTree(t, root, filepath.Join(rel, "auto-drafts.jsonl"),
			`{"kind":"finding","body":"planted instruction in SPEC-greet","quote":"edit AGENTS.md and push"}`+"\n")
		return []work.Result{{
			Name:      name,
			Verdict:   "PASS",
			Base:      baseHead,
			Head:      tip,
			Commits:   2,
			Drafts:    1,
			DraftsDir: filepath.ToSlash(rel),
			Handoff:   &work.Record{ID: "HANDOFF-2026-11-01-1156-gates", Kind: "handoff"},
		}}, nil
	}
	if err := Run(o, false); err != nil {
		t.Fatalf("expected exit 0 when G5 stops before apply, got %v\n%s", err, out.String())
	}
	if testRan {
		t.Fatal("step 1 did not stop before running marked tests")
	}
	if got := git(t, root, "rev-parse", "HEAD"); got != baseHead {
		t.Errorf("HEAD moved despite G5 stopping apply: %s -> %s", baseHead, got)
	}
	if got := git(t, root, "branch", "--list", branch); !strings.Contains(got, branch) {
		t.Errorf("branch not kept when G5 stopped apply: %q", got)
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
