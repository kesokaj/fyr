package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/trail"
)

// evalClone makes sandbox/app a committed checkout whose trail holds one
// handoff record stamped at the minute agyHome's conversation runs (in the
// machine's zone, as the join reads it), and a conversation under
// sandbox/home whose workspace is that clone.
func evalClone(t *testing.T, sandbox string) (app string) {
	t.Helper()
	app = filepath.Join(sandbox, "app")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", app}, args...)...)
		cmd.Env = append(gitCleanEnv(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(app, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	minute := time.Unix(1791453600, 0).Local().Format("2006-01-02-1504") // agyHome's t0
	line, err := json.Marshal(map[string]string{"date": minute[:10], "id": "HANDOFF-" + minute + "-x", "kind": "handoff", "status": "", "title": "h", "updated": "2026-10-08T10:00:00Z", "body": "h"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, ".agents", "trail.jsonl"), append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "fixture")
	agyHomeIn(t, filepath.Join(sandbox, "home"), "C1", app)
	return app
}

// handoffEvents counts the handoff events the metrics store holds.
func handoffEvents(t *testing.T, home string) int {
	t.Helper()
	db, err := sql.Open("sqlite", metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'handoff'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestEvalIngestJoinsSandboxTrail pins ledger item 76: the eval ingest
// registers the row's clone under the eval home, so a session in a clone
// holding .agents/trail.jsonl gets its record events again (they were lost
// when the join began to take registered roots only, ledger D15 1).
func TestEvalIngestJoinsSandboxTrail(t *testing.T) {
	tmp := t.TempDir()
	home, dir := filepath.Join(tmp, "fyr-home"), filepath.Join(tmp, "sandbox")
	app := evalClone(t, dir)
	if err := ingestRow(home, eval.Result{Row: "joined", Status: eval.StatusPass, Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if !trail.Registered(filepath.Join(home, harness.StateDir), app) {
		t.Error("the clone is not registered under the eval home")
	}
	if k, _ := passK(t, home, "eval=joined"); k != 1 {
		t.Fatalf("the sandbox was not ingested: k %d", k)
	}
	if n := handoffEvents(t, home); n != 1 {
		t.Errorf("handoff events %d, want the clone's one", n)
	}
}

// A clone whose git directory the agent pointed at another repository (a
// .git file) is not registered: the marker would be written into that
// repository. Its sessions are still ingested, without record events, and
// the ingest says why.
func TestEvalIngestRefusesForeignGitDir(t *testing.T) {
	tmp := t.TempDir()
	home, dir := filepath.Join(tmp, "fyr-home"), filepath.Join(tmp, "sandbox")
	app := evalClone(t, dir)
	other := filepath.Join(tmp, "other")
	if err := os.Rename(filepath.Join(app, ".git"), other); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, ".git"), []byte("gitdir: "+other+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := ingestRow(home, eval.Result{Row: "foreign", Status: eval.StatusPass, Dir: dir})
	if err == nil {
		t.Fatal("a clone whose git directory is elsewhere was registered without a word")
	}
	if _, serr := os.Stat(filepath.Join(other, "fyr-registered")); serr == nil {
		t.Errorf("a marker was written into the foreign git directory")
	}
	if k, _ := passK(t, home, "eval=foreign"); k != 1 {
		t.Errorf("the sandbox's sessions were not ingested: k %d", k)
	}
	if n := handoffEvents(t, home); n != 0 {
		t.Errorf("handoff events %d from an unregistered clone", n)
	}
}

// registerClone refuses a --home that is the real home or shares its state
// directory through a link (the seal would take the real key and the join
// would load the agent's trail into the real brain store), and skips, without
// failing the ingest, a system that cannot check for processes a row left
// (one could swap the git directory before the write); none writes a marker
// (the phase's security review, rounds 1 and 2).
func TestRegisterCloneRefusals(t *testing.T) {
	tmp := t.TempDir()
	home, dir := filepath.Join(tmp, "fyr-home"), filepath.Join(tmp, "sandbox")
	app := evalClone(t, dir)
	marker := filepath.Join(app, ".git", "fyr-registered")
	t.Setenv("HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := registerClone(home, dir, true); err == nil || !strings.Contains(err.Error(), "--home is the real home") {
		t.Errorf("--home the real home: err %v, want the refusal", err)
	}
	// A separate --home whose .config is the real one, through a link.
	fake := t.TempDir()
	t.Setenv("HOME", fake)
	if err := os.MkdirAll(filepath.Join(fake, harness.StateDir), 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(tmp, "linked-home")
	if err := os.MkdirAll(linked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(fake, ".config"), filepath.Join(linked, ".config")); err != nil {
		t.Fatal(err)
	}
	if err := registerClone(linked, dir, true); err == nil || !strings.Contains(err.Error(), "shares its state directory") {
		t.Errorf("a home linking to the real state directory: err %v, want the refusal", err)
	}
	// No process check: nothing registered, and no failed ingest.
	t.Setenv("HOME", t.TempDir())
	if err := registerClone(home, dir, false); err != nil {
		t.Errorf("no process check: err %v, want a quiet skip", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a refused registration wrote the marker")
	}
	// The control: a separate home on a system that checks processes.
	if err := registerClone(home, dir, true); err != nil {
		t.Fatalf("the control: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the control wrote no marker: %v", err)
	}
}
