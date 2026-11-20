package dream

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
)

func TestDreamDiagnosticsAndCoupling(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	toolConversation(t, f)
	seedDreamDiagnosticsAndCoupling(t, f)

	c, err := collect(f.home, project(f), since, now, maxPrompt)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	sigID := extractDreamSignalID(t, c.Prompt, f.home)
	assertDreamSignalProposalAndBgRun(t, f, sigID)
}

func seedDreamDiagnosticsAndCoupling(t *testing.T, f fixture) {
	t.Helper()
	for rel, body := range map[string]string{
		".gitignore": ".agents/scratch/*\n",
		"go.mod":     "module example.com/dreamproj\n\ngo 1.24\n",
		"pkg1/a.go":  "package pkg1\n\nfunc One() int { return 1 }\n",
		"pkg2/b.go":  "package pkg2\n\nfunc Two() int { return 2 }\n",
	} {
		full := filepath.Join(f.root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := search.Index(search.Options{Home: f.home, Dir: f.root}, io.Discard); err != nil {
		t.Fatalf("search.Index: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(f.root, ".agents", "scratch", "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO co_activity(file_a, file_b, kind, count, updated_at) VALUES ('pkg1/a.go', 'pkg2/b.go', 'co_modified', 5, 100)`); err != nil {
		t.Fatal(err)
	}
	seedDreamKneeSessions(t, f.home, f.root)
}

func seedDreamKneeSessions(t *testing.T, home, root string) {
	t.Helper()
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ts := now.Add(-2 * time.Hour).UnixMilli()
	for i := 0; i < 5; i++ {
		sid := fmt.Sprintf("dk-%d", i)
		if _, err := db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES (?, 'agy', ?, ?, ?)`, sid, root, ts+int64(i)*1000, ts+int64(i)*1000+500); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, context) VALUES (?, '1', ?, 'model', 200000)`, sid, ts+int64(i)*1000+10); err != nil {
			t.Fatal(err)
		}
	}
}

func extractDreamSignalID(t *testing.T, promptText, home string) string {
	t.Helper()
	if !strings.Contains(promptText, `"diagnostics"`) || !strings.Contains(promptText, "<<<data trail: hidden-coupling") {
		t.Fatalf("prompt missing diagnostics or hidden-coupling:\n%s", promptText)
	}
	if !strings.Contains(promptText, "pkg1/a.go") || !strings.Contains(promptText, "pkg2/b.go") {
		t.Fatalf("prompt missing repo-relative coupling pair:\n%s", promptText)
	}
	if strings.Contains(promptText, home) {
		t.Fatalf("prompt leaked home path %q", home)
	}
	idx := strings.Index(promptText, "sig:proj-")
	if idx < 0 {
		t.Fatalf("prompt missing sig:proj- signal_id:\n%s", promptText)
	}
	end := strings.IndexAny(promptText[idx:], `" `+"\n")
	return promptText[idx : idx+end]
}

func assertDreamSignalProposalAndBgRun(t *testing.T, f fixture, sigID string) {
	t.Helper()
	reply := fmt.Sprintf(`{"proposals":[{"title":"Split coupled workflow","kind":"rule","target":"project","file":"AGENTS.md","text":"Keep session context under knee.","evidence":"metrics, hidden-coupling","signal_id":%q},{"title":"Duplicate signal rule","kind":"rule","target":"project","file":"AGENTS.md","text":"Second duplicate.","evidence":"metrics","signal_id":%q}],"skills":[],"ratings":[]}`, sigID, sigID)
	fakeAgy(t, reply, 0)

	var bgRows []metrics.BackgroundRunRow
	var dryOut bytes.Buffer
	dryOpts := runOpts(f, &dryOut)
	dryOpts.DryRun = true
	dryOpts.RecordBackgroundRun = func(r metrics.BackgroundRunRow) error {
		bgRows = append(bgRows, r)
		return nil
	}
	if err := Run(dryOpts); err != nil || len(bgRows) != 0 {
		t.Fatalf("dry run err=%v bgRows=%+v", err, bgRows)
	}

	var out bytes.Buffer
	opts := runOpts(f, &out)
	opts.SkipIngest = true
	opts.RecordBackgroundRun = func(r metrics.BackgroundRunRow) error {
		bgRows = append(bgRows, r)
		return nil
	}
	if err := Run(opts); err != nil {
		t.Fatalf("real run: %v\n%s", err, out.String())
	}
	if len(bgRows) != 1 || bgRows[0].Kind != "dream" || bgRows[0].Outcome != "pass" || bgRows[0].ProposalsWritten != 1 {
		t.Fatalf("bgRows = %+v, want 1 dream pass with 1 deduped proposal", bgRows)
	}
	recs, err := brain.List(brain.Options{Home: f.home, Global: true}, brain.ListFilter{Kind: "proposal"})
	if err != nil || len(recs) != 1 || !strings.Contains(recs[0].Body, "Signal: "+sigID) {
		t.Fatalf("proposals = %+v, err=%v", recs, err)
	}
	rem, err := dedupeProposals(f.home, f.root, []brain.Proposal{{Title: "Dream, project — Another title", Body: "Target: project   Kind: rule   File: AGENTS.md\nSignal: " + sigID + "\n\n    body\n\nEvidence: metrics\n"}})
	if err != nil || len(rem) != 0 {
		t.Fatalf("dedupeProposals against queued signal_id = %+v, err=%v; want empty", rem, err)
	}
}

func TestAddHiddenCouplingHintsRejectsSymlinkScratch(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "search.db"), []byte("not-a-db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".agents", "scratch")); err != nil {
		t.Fatal(err)
	}
	called := false
	addHiddenCouplingHints("", root, func(string, string, string, time.Time) {
		called = true
	})
	if called {
		t.Fatal("addHiddenCouplingHints read through symlinked .agents/scratch")
	}
}
