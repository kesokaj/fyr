package gate

import (
	"context"
	"path/filepath"
	"testing"
)

func TestGateAndLandJudgeWorkerAttributionAndRepoID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	repo := initTestGoRepo(t)
	runGit(t, repo, "remote", "add", "origin", "git@github.com:ExampleOrg/AttrSvc.git")

	wt := filepath.Join(t.TempDir(), "wt-attr")
	runGit(t, repo, "worktree", "add", "-b", "branch-attr", wt, "main")
	writeFile(t, filepath.Join(wt, "attr.go"), "package main\n\nfunc Attr() int { return 42 }\n")
	runGit(t, wt, "add", "attr.go")
	runGit(t, wt, "commit", "-m", "add Attr")
	runGit(t, repo, "worktree", "remove", wt)

	home := t.TempDir()
	var gotReq JudgeRequest
	var recorded []JudgeVerdictRecord
	lRep, err := Land(ctx, LandOptions{
		Dir:             repo,
		Branch:          "branch-attr",
		Home:            home,
		Lease:           []string{"attr.go"},
		Judge:           "Attr returns 42",
		JudgeModel:      "claude-opus-5-5-high",
		WorkerModel:     "gemini-4-neon-high",
		WorkerSessionID: "sess-worker-1",
		JudgeRunner: func(_ context.Context, req JudgeRequest) (JudgeResult, error) {
			gotReq = req
			return JudgeResult{Model: req.ExplicitModel, Verdict: "PASS", Reason: "ok"}, nil
		},
		RecordVerdict: func(r JudgeVerdictRecord) error {
			recorded = append(recorded, r)
			return nil
		},
	})
	if err != nil || !lRep.OK {
		t.Fatalf("Land failed: err=%v rep=%+v", err, lRep)
	}
	if gotReq.WorkerModel != "gemini-4-neon-high" || gotReq.WorkerSessionID != "sess-worker-1" {
		t.Errorf("JudgeRequest worker fields = (%q, %q)", gotReq.WorkerModel, gotReq.WorkerSessionID)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded len = %d, want 1", len(recorded))
	}
	rec := recorded[0]
	if rec.RepoID != "git:github.com/exampleorg/attrsvc" {
		t.Errorf("JudgeVerdictRecord.RepoID = %q, want git:github.com/exampleorg/attrsvc", rec.RepoID)
	}
	if rec.WorkerModel != "gemini-4-neon-high" || rec.WorkerSessionID != "sess-worker-1" || rec.Model != "claude-opus-5-5-high" {
		t.Errorf("JudgeVerdictRecord attribution = %+v", rec)
	}
}
