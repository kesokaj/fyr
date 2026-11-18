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
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/isolate"
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
		"printf '%s\\n' \"$*\" >> " + isolate.ShellQuote(logFile) + "\n" +
		"exec " + isolate.ShellQuote(realGit) + " \"$@\"\n"
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

const harnessApplyConfig = `{"enabled": true, "conversation_idle": "1h"}`

const applyGreetSuite = "# GUIDE-eval\n\n" +
	"| id | setup | prompt | prompt2 | check | agent | grants | version | kind | cluster | expect |\n" +
	"|---|---|---|---|---|---|---|---|---|---|---|\n" +
	"| `greet` |  | use skills/greet/SKILL.md |  | `true` |  | named | 1 | regression | greet |  |\n"

// applyFixture builds an idle, clean harness repository with its bare remote,
// local user.name/user.email, skills/greet/SKILL.md, GUIDE-eval, holdout.md, a
// queued harness proposal, and the given AGENTS.md text.
func applyFixture(t *testing.T, home, agentsMD string) string {
	t.Helper()
	root := idleProject(t)
	git(t, root, "config", "user.name", "Fixture Author")
	git(t, root, "config", "user.email", "author@fixture.local")
	if agentsMD != "" {
		writeTree(t, root, "AGENTS.md", agentsMD)
	}
	writeTree(t, root, "skills/greet/SKILL.md", "---\nname: greet\ndescription: greet\n---\n# greet v1\n")
	writeTree(t, root, "internal/greet/greet.go", "package greet\n\nfunc Greet() string { return \"hi\" }\n")
	writeTree(t, root, ".agents/trail.jsonl", baseSpecLine+baseUserPlanLine)
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "fixture: skill and agents")
	git(t, root, "push", "-q")
	backdate(t, root)

	nowFn := func() time.Time { return runAt.Add(-9 * time.Hour) }
	if _, err := brain.Add(brain.Options{Home: home, Dir: root, Now: nowFn}, brain.AddInput{
		ID:     "GUIDE-eval",
		Kind:   "guide",
		Status: "current",
		Title:  "GUIDE-eval",
		Body:   strings.NewReader(applyGreetSuite),
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "holdout.md"), []byte(testGateHoldoutSuite), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.Add(brain.Options{Home: home, Global: true, Now: nowFn}, brain.AddInput{
		Kind:   "proposal",
		Status: "queued",
		Title:  "Auto, harness — update skills/greet/",
		Body:   strings.NewReader("Target: harness\nEvidence: sess-1, sess-2\n\nUpdate `skills/greet/SKILL.md`.\n"),
	}); err != nil {
		t.Fatal(err)
	}
	return root
}

func applyOpts(home, root string, out *bytes.Buffer) Options {
	o := driftOpts(home, root, out)
	o.Repo = root
	o.RealHome = home
	o.HoldoutFile = filepath.Join(home, "holdout.md")
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		return EvalOutcome{Row: req.Row, Passed: 3, Graded: req.Runs, Errors: 0, RecordID: "TEST-gate"}, nil
	}
	return o
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
// update skills/greet/SKILL.md and add a worker handoff record, without moving HEAD.
func makeWorkerBranch(t *testing.T, root, name, handoffID string) string {
	t.Helper()
	base := git(t, root, "rev-parse", "HEAD")
	c1 := commitPlumbing(t, root, base, "100644", "skills/greet/SKILL.md", "---\nname: greet\ndescription: greet\n---\n# greet updated\n", "worker: update greet skill")
	trailText := baseSpecLine + baseUserPlanLine
	handoffLine := `{"id":"` + handoffID + `","kind":"handoff","status":"","date":"2026-11-01","title":"Handoff — update greet skill","body":"# Handoff\n\nUpdated skills/greet/SKILL.md.\n","updated":"2026-11-01T11:56:00Z"}` + "\n"
	c2 := commitPlumbing(t, root, c1, "100644", ".agents/trail.jsonl", trailText+handoffLine, "worker: handoff")
	git(t, root, "update-ref", "refs/heads/work/"+name, c2)
	return c2
}

func fakePassWork(t *testing.T, root, baseHead, handoffID string, branchOut *string) func(context.Context, isolate.WorkerOptions, ...string) ([]isolate.WorkerResult, error) {
	return func(_ context.Context, _ isolate.WorkerOptions, paths ...string) ([]isolate.WorkerResult, error) {
		name := strings.TrimSuffix(filepath.Base(paths[0]), ".md")
		if branchOut != nil {
			*branchOut = "work/" + name
		}
		tip := makeWorkerBranch(t, root, name, handoffID)
		return []isolate.WorkerResult{{
			Name:    name,
			Verdict: "PASS",
			Base:    baseHead,
			Head:    tip,
			Commits: 2,
			Handoff: &isolate.WorkerRecord{ID: handoffID, Kind: "handoff", Title: "Handoff — update greet skill"},
		}}, nil
	}
}

func TestApplyHappyPath(t *testing.T) {
	logFile := installLoggingGit(t)
	binDir, _, _ := strings.Cut(os.Getenv("PATH"), string(os.PathListSeparator))
	home := t.TempDir()
	markerFile := filepath.Join(t.TempDir(), "test-ran")
	agents := "# AGENTS.md\n\n## Setup commands\n\n```bash\ntest -z \"${GIT_DIR:-}\" && printf ok > " + isolate.ShellQuote(markerFile) + "   # auto: test\n```\n"
	root := applyFixture(t, home, agents)
	baseHead := git(t, root, "rev-parse", "HEAD")
	writeConfig(t, home, harnessApplyConfig)

	const handoffID = "HANDOFF-2026-11-01-1156-handoff-update-greet"
	var branch string
	var out bytes.Buffer
	o := applyOpts(home, root, &out)
	o.TestRO = append(o.TestRO, binDir, filepath.Dir(logFile))
	o.TestRW = append(o.TestRW, filepath.Dir(markerFile))
	passWork := fakePassWork(t, root, baseHead, handoffID, &branch)
	o.Work = func(ctx context.Context, wopts isolate.WorkerOptions, paths ...string) ([]isolate.WorkerResult, error) {
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
	if !strings.HasPrefix(subj, "auto: harness-edit — ") {
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

func TestApplyRedMarkedTestKeepsBranch(t *testing.T) {
	t.Run("marked-test-fails", func(t *testing.T) {
		home := t.TempDir()
		root := applyFixture(t, home, "# AGENTS.md\n\n## Setup commands\n\n```bash\nfalse   # auto: test\n```\n")
		baseHead := git(t, root, "rev-parse", "HEAD")
		writeConfig(t, home, harnessApplyConfig)

		var branch string
		var out bytes.Buffer
		o := applyOpts(home, root, &out)
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
		root := applyFixture(t, home, defaultAgentsMD)
		writeTree(t, root, "PRD.md", "# PRD\n\n## Must\n\n- **R1** — Req.\n  <!-- provenance: op, 2026-11-01; enforced by: TestNonExistentCheckPin -->\n")
		git(t, root, "add", "PRD.md")
		git(t, root, "commit", "-q", "-m", "add prd with missing test pin")
		git(t, root, "push", "-q")
		backdate(t, root)
		baseHead := git(t, root, "rev-parse", "HEAD")
		writeConfig(t, home, harnessApplyConfig)

		var branch string
		var out bytes.Buffer
		o := applyOpts(home, root, &out)
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
