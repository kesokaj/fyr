package isolate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/trail"
)

func TestRunGroupSetsidAndKillsSubshell(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("sh", "-c", "sleep 30 & wait")
	start := time.Now()
	err := runGroup(cmd, 150*time.Millisecond)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("runGroup err = %v, want timed out", err)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatalf("runGroup SysProcAttr = %+v, want Setsid: true", cmd.SysProcAttr)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("runGroup took %s; child sleep held pipe", elapsed)
	}
}

func TestExportBoxRecordsUsesVerifiedSelf(t *testing.T) {
	t.Parallel()
	sbDir := filepath.Join(t.TempDir(), "sb")
	b := New(Config{Dir: sbDir, Prefix: "test: rec", Out: &strings.Builder{}})
	if !b.Make() {
		t.Fatalf("b.Make: %v", b.Reasons())
	}
	if err := os.MkdirAll(b.App(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(b.Home(), ".local", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(sbDir, "pwned")
	poisoned := "#!/bin/sh\ntouch " + ShellQuote(marker) + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(b.Home(), ".local", "bin", "fyr"), []byte(poisoned), 0o755); err != nil {
		t.Fatal(err)
	}

	leadBin := filepath.Join(t.TempDir(), "lead-fyr")
	recLine := `{"id":"CHANGE-1","kind":"change","status":"closed","date":"2026-11-19","title":"t","updated":"2026-11-19T08:00:00Z","body":"b"}`
	leadScript := "#!/bin/sh\nmkdir -p .agents\ncat <<'JSONL' > .agents/trail.jsonl\n" + recLine + "\nJSONL\n"
	if err := os.WriteFile(leadBin, []byte(leadScript), 0o755); err != nil {
		t.Fatal(err)
	}

	w := &worker{o: WorkerOptions{Self: leadBin}, b: b}
	recs, err := w.exportBoxRecords(context.Background())
	if err != nil || recs["CHANGE-1"].Title != "t" {
		t.Fatalf("exportBoxRecords = (%+v, %v)", recs, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("sandbox .local/bin/fyr was executed by lead!")
	}
}

func TestParseTrailAndWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 19, 8, 30, 0, 0, time.UTC)
	line := `{"id":"HANDOFF-1","kind":"handoff","status":"closed","date":"2026-11-19","title":"t","updated":"2026-11-19T08:30:00Z","body":"b"}`
	recs, err := parseTrail([]byte(line + "\n" + line + "\n"))
	if err != nil || len(recs) != 1 {
		t.Fatalf("parseTrail = (%+v, %v)", recs, err)
	}
	conflict := `{"id":"HANDOFF-1","kind":"handoff","status":"closed","date":"2026-11-19","title":"diff","updated":"2026-11-19T08:30:00Z","body":"b"}`
	if _, err := parseTrail([]byte(line + "\n" + conflict + "\n")); err == nil {
		t.Fatal("parseTrail accepted conflicting duplicate ID")
	}
	win := window{from: now, to: now.Add(time.Minute), loc: time.UTC}
	if why := win.outside(recs["HANDOFF-1"]); why != "" {
		t.Fatalf("window.outside = %q, want empty", why)
	}
	bad := recs["HANDOFF-1"]
	bad.Updated = "2026-11-19T08:30:00+01:00"
	if why := win.outside(bad); why == "" {
		t.Fatal("window.outside accepted non-UTC updated")
	}
}

func TestKickoffDisjointAndUTF8Truncation(t *testing.T) {
	t.Parallel()
	kDir := t.TempDir()
	k1Path := filepath.Join(kDir, "task-a.md")
	k2Path := filepath.Join(kDir, "task-b.md")
	k3Path := filepath.Join(kDir, "task-c.md")
	kBadPath := filepath.Join(kDir, "task-bad.md")
	_ = os.WriteFile(k1Path, []byte("Files: a/\n\nbody\n"), 0o600)
	_ = os.WriteFile(k2Path, []byte("Files: a/x.go\n\nbody\n"), 0o600)
	_ = os.WriteFile(k3Path, []byte("Files: b/y.go\n\nbody\n"), 0o600)
	_ = os.WriteFile(kBadPath, []byte("Files: a/../x.go\n\nbody\n"), 0o600)

	const sentinelModel = "bad model"
	baseOpts := func() WorkerOptions {
		return WorkerOptions{
			Home:       t.TempDir(),
			RealHome:   t.TempDir(),
			Model:      sentinelModel,
			DraftsOnly: true,
		}
	}
	opts := baseOpts()
	if _, err := preflightWorker(context.Background(), &opts, []string{k1Path, k2Path}); err == nil || !strings.Contains(err.Error(), "overlap on") {
		t.Fatalf("preflightWorker(overlapping) err = %v, want overlap refusal", err)
	}
	opts = baseOpts()
	if _, err := preflightWorker(context.Background(), &opts, []string{kBadPath}); err == nil || !strings.Contains(err.Error(), "contains a '..' segment") {
		t.Fatalf("preflightWorker(parent dir) err = %v, want '..' refusal", err)
	}
	opts = baseOpts()
	if _, err := preflightWorker(context.Background(), &opts, []string{k1Path, k3Path}); err == nil || !strings.Contains(err.Error(), "--model "+sentinelModel+": not a model ID") {
		t.Fatalf("preflightWorker(disjoint) err = %v, want sentinel model refusal past parse/disjoint", err)
	}

	// Multi-byte UTF-8 boundary truncation test (IN101).
	long := strings.Repeat("─", 2500) // 7500 bytes, 3 bytes per rune
	got := trail.TruncateUTF8(long, 6000)
	if len(got) > 6000 || !utf8.ValidString(got) {
		t.Fatalf("trail.TruncateUTF8 invalid: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
	gotOdd := trail.TruncateUTF8(long, 10)
	if len(gotOdd) != 9 || !utf8.ValidString(gotOdd) {
		t.Fatalf("trail.TruncateUTF8(10) = len=%d valid=%v, want 9 and valid", len(gotOdd), utf8.ValidString(gotOdd))
	}
}

func TestWorkerDraftsZoneModelsRemoveAndBash(t *testing.T) {
	// worker_zone
	t.Setenv("TZ", "")
	if z, err := leadZone(); err != nil || z.name != "UTC" {
		t.Fatalf("leadZone empty TZ = (%+v, %v)", z, err)
	}
	t.Setenv("TZ", "../evil")
	if _, err := leadZone(); err == nil {
		t.Fatal("leadZone accepted ../evil")
	}

	// worker_model_profiles (IN97, IN-S43, IN-S47)
	listed := map[string]bool{"gemini-4-carbon-high": true, "gemini-pro-orchestrator": true}
	ids := []string{"gemini-4-carbon-high", "gemini-pro-orchestrator"}
	docOpts := WorkerOptions{Home: t.TempDir()}
	docKs := []Kickoff{{Path: "doc.md", Files: []string{"README.md"}, Text: "update doc", Model: "auto", JudgeModel: "auto"}}
	if err := resolveAutoModels(&docOpts, docKs, listed, ids); err != nil || docKs[0].Model != "gemini-4-carbon-high" || docKs[0].JudgeModel != "" {
		t.Fatalf("resolveAutoModels(doc) = (err=%v, k.Model=%q, k.JudgeModel=%q)", err, docKs[0].Model, docKs[0].JudgeModel)
	}
	opts := WorkerOptions{Home: t.TempDir(), Model: "auto"}
	ks := []Kickoff{{Path: "k1.md", Files: []string{"a.go"}, Text: "work"}}
	if err := resolveAutoModels(&opts, ks, listed, ids); err != nil || opts.Model != "gemini-4-carbon-high" || ks[0].Model != "gemini-4-carbon-high" {
		t.Fatalf("resolveAutoModels = (err=%v, o.Model=%q, k.Model=%q)", err, opts.Model, ks[0].Model)
	}
	optsFail := WorkerOptions{Home: t.TempDir(), Model: "auto"}
	if err := resolveAutoModels(&optsFail, []Kickoff{{Path: "k1.md", Text: "work"}}, map[string]bool{"gemini-pro-orchestrator": true}, []string{"gemini-pro-orchestrator"}); err == nil {
		t.Fatal("resolveAutoModels succeeded when no eligible model listed")
	}

	// IN-S45: shared trail.CappedWriter
	cb := trail.NewCappedBuffer(5, nil)
	_, _ = cb.Write([]byte("abc"))
	if cb.Truncated() || cb.String() != "abc" {
		t.Fatalf("CappedWriter under limit: truncated=%v buf=%q", cb.Truncated(), cb.String())
	}
	_, _ = cb.Write([]byte("defg"))
	if !cb.Truncated() || cb.String() != "abcde" {
		t.Fatalf("CappedWriter over limit: truncated=%v buf=%q", cb.Truncated(), cb.String())
	}

	// IN100: PrintTimeout capped grace for 5m timeout
	w5 := newWorker(WorkerOptions{Timeout: 5 * time.Minute}, prepared{k: Kickoff{Name: "a"}}, "s", t.TempDir(), nil)
	if w5.b.c.PrintTimeout != "4m" {
		t.Fatalf("PrintTimeout for 5m = %q, want 4m", w5.b.c.PrintTimeout)
	}

	// IN99: remove() removes !w.ran sandbox, keeps w.ran && !w.collected
	r := newRig(t)
	bClean := r.prepared(r.config("clean-unran"))
	wClean := &worker{o: WorkerOptions{}, b: bClean, ran: false, collected: false}
	wClean.remove()
	if _, err := os.Stat(bClean.Dir()); !os.IsNotExist(err) {
		t.Fatalf("remove() leaked !w.ran sandbox %s", bClean.Dir())
	}
	bKeep := r.prepared(r.config("keep-ran"))
	wKeep := &worker{o: WorkerOptions{}, b: bKeep, ran: true, collected: false}
	wKeep.remove()
	if _, err := os.Stat(bKeep.Dir()); err != nil {
		t.Fatalf("remove() deleted uncollected ran sandbox %s", bKeep.Dir())
	}

	// session.go:Bash
	if !bKeep.Bash(context.Background(), "echo ok", FyrEnv(), "bash.log") {
		t.Fatal("Bash failed on echo ok")
	}
	if bKeep.Bash(context.Background(), "exit 2", FyrEnv(), "bash2.log") {
		t.Fatal("Bash passed on exit 2")
	}

	// drafts + reflog on temp repo
	wDraft := &worker{o: WorkerOptions{Root: r.src, Auto: true}, p: prepared{k: Kickoff{Name: "d"}}, b: bKeep, stamp: "st1"}
	dSrc := filepath.Join(bKeep.App(), draftsRel)
	_ = os.MkdirAll(dSrc, 0o700)
	_ = os.WriteFile(filepath.Join(dSrc, "note-1.md"), []byte("# ok\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dSrc, "BAD NAME.md"), []byte("skip"), 0o600)
	if !wDraft.drafts() || wDraft.res.Drafts != 1 {
		t.Fatalf("drafts() = (count=%d)", wDraft.res.Drafts)
	}
	wDraft.closeLeadDir()
	if start, err := reflogStart(r.src); err != nil || reflogMoved(r.src, start) != "" {
		t.Fatalf("reflog = (%+v, %v)", start, err)
	}

	// IN102: leftoverProcs ignores pid <= 1
	oldProc := procDir
	procDir = t.TempDir()
	defer func() { procDir = oldProc }()
	_ = os.MkdirAll(filepath.Join(procDir, "0"), 0o700)
	_ = os.MkdirAll(filepath.Join(procDir, "1"), 0o700)
	if found, err := leftoverProcs(nil, "test", bKeep.Home(), bKeep.Home()); err != nil || len(found) != 0 {
		t.Fatalf("leftoverProcs = (%v, %v), want empty", found, err)
	}
}
