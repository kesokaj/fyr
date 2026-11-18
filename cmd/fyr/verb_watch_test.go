package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/watch"
	_ "modernc.org/sqlite"
)

func TestVerbWatch(t *testing.T) {
	prevLister := watchProcLister
	watchProcLister = func() ([]watch.ProcInfo, error) { return nil, nil }
	t.Cleanup(func() { watchProcLister = prevLister })

	repo := initCLITestRepo(t)
	home := t.TempDir()
	agyDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(agyDir, 0o755); err != nil {
		t.Fatalf("mkdir agyDir: %v", err)
	}

	if _, _, code := runCLIWithErr("watch", "--home", home, "--dir", repo); code != 2 {
		t.Fatalf("missing --check: code=%d, want 2", code)
	}
	if _, _, code := runCLIWithErr("watch", "--home", home, "--dir", repo, "--check", "--idle-sec", "-1"); code != 2 {
		t.Fatalf("negative --idle-sec: code=%d, want 2", code)
	}

	t.Run("real_home_refusal", func(t *testing.T) {
		guardHome(t, "")
		outStr, errStr, code := runCLIWithErr("watch", "--check", "--dir", repo)
		if code != 2 || !strings.Contains(outStr+errStr, "BUG-507") {
			t.Fatalf("real-home guard failed: code=%d out=%q err=%q", code, outStr, errStr)
		}
	})

	outStr, errStr, code := runCLIWithErr("watch", "--home", home, "--dir", repo, "--check")
	if code != 0 || !strings.Contains(outStr, "watch: ok (") {
		t.Fatalf("healthy watch failed: code=%d out=%q err=%q", code, outStr, errStr)
	}

	artPath := filepath.Join(repo, ".agents", "scratch", "agents", "reviewer.md")
	writeCLITestFile(t, artPath, "# Review\n## Status: IN PROGRESS\n")
	old := time.Now().Add(-10 * time.Second)
	if err := os.Chtimes(artPath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	outStr, errStr, code = runCLIWithErr("watch", "--home", home, "--dir", repo, "--check", "--idle-sec", "1", "--json")
	if code != 1 {
		t.Fatalf("stale artifact: code=%d out=%q err=%q, want 1", code, outStr, errStr)
	}
	var rep watch.Report
	if err := json.Unmarshal([]byte(outStr), &rep); err != nil {
		t.Fatalf("unmarshal watch report %q: %v", outStr, err)
	}
	if rep.OK || len(rep.Findings) == 0 || rep.Findings[0].Kind != "STUCK_NO_PROGRESS" {
		t.Fatalf("unexpected stale report: %+v", rep)
	}
	_ = os.Remove(artPath)

	db, err := sql.Open("sqlite", search.DSN(filepath.Join(agyDir, "conversation_summaries.db")))
	if err != nil {
		t.Fatalf("open summary db: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE conversation_summaries (conversation_id text, last_modified_time datetime NOT NULL, workspace_uris text NOT NULL, agent_name text NOT NULL DEFAULT '', parent_conversation_id text NOT NULL DEFAULT '')`)
	if err == nil {
		mod := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
		_, err = db.Exec(`INSERT INTO conversation_summaries VALUES ('parent-1', ?, ?, '', ''), ('child-1', ?, ?, 'reviewer', 'parent-1')`, mod, `["file://`+repo+`"]`, mod, `["file://`+repo+`"]`)
	}
	_ = db.Close()
	if err != nil {
		t.Fatalf("seed summaries: %v", err)
	}
	trPath := filepath.Join(agyDir, "brain", "child-1", ".system_generated", "logs", "transcript.jsonl")
	writeCLITestFile(t, trPath, "{\"content\":\"Continue?\"}\n")
	_ = os.Chtimes(trPath, time.Now().Add(-5*time.Minute), time.Now().Add(-5*time.Minute))

	outStr, errStr, code = runCLIWithErr("watch", "--home", home, "--dir", repo, "--check", "--conv", "parent-1")
	if code != 1 || !strings.Contains(outStr, "[INTERACTIVE_WAIT]") || !strings.Contains(outStr, "child-1") {
		t.Fatalf("conv child check: code=%d out=%q err=%q", code, outStr, errStr)
	}
}
