package worker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

type fakeBox struct {
	mu      sync.Mutex
	dir     string
	app     string
	home    string
	reasons []string
	notes   bytes.Buffer
	intact  bool
	gitOK   bool
	removed bool
	cfg     BoxConfig
}

func newFakeBox(t *testing.T) *fakeBox {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sb")
	app := filepath.Join(dir, "app")
	home := filepath.Join(dir, "home")
	for _, d := range []string{app, home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return &fakeBox{dir: dir, app: app, home: home, intact: true, gitOK: true}
}

func (f *fakeBox) Dir() string                                                 { return f.dir }
func (f *fakeBox) App() string                                                 { return f.app }
func (f *fakeBox) Home() string                                                { return f.home }
func (f *fakeBox) Make() bool                                                  { return true }
func (f *fakeBox) ScratchApp() bool                                            { return true }
func (f *fakeBox) Clone(context.Context) bool                                  { return true }
func (f *fakeBox) Fingerprint()                                                {}
func (f *fakeBox) Install(context.Context) bool                                { return true }
func (f *fakeBox) Settings() bool                                              { return true }
func (f *fakeBox) Baseline()                                                   {}
func (f *fakeBox) Session(context.Context, string, string, string, string) int { return 0 }
func (f *fakeBox) Started() bool                                               { return true }
func (f *fakeBox) After() bool                                                 { return true }
func (f *fakeBox) ExitChecks()                                                 {}
func (f *fakeBox) TimedOut() bool                                              { return false }
func (f *fakeBox) Intact() bool                                                { return f.intact }
func (f *fakeBox) GitIntact() bool                                             { return f.gitOK }

func (f *fakeBox) Reasons() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reasons)
}

func (f *fakeBox) Fail(reason string) {
	f.mu.Lock()
	f.reasons = append(f.reasons, reason)
	f.mu.Unlock()
}

func (f *fakeBox) Note(format string, a ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fmt.Fprintf(&f.notes, format+"\n", a...)
}

func (f *fakeBox) Has(reason string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.reasons, reason)
}

func (f *fakeBox) Remove() bool {
	f.removed = true
	return os.RemoveAll(f.dir) == nil
}

func testDeps() Deps {
	return Deps{
		Root: func(home, verb string) string { return filepath.Join(home, harness.StateDir, verb) },
		SandboxRoot: func(home, root string) string {
			return filepath.Join(home, harness.StateDir, "sandbox", filepath.Base(root))
		},
		CheckRoot:    func(string, string, string) error { return nil },
		CheckWorkDir: func(string) error { return nil },
		OutsideCheckout: func(dir string) error {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				return fmt.Errorf("%s is inside a git checkout", dir)
			}
			return nil
		},
		ChooseRunner:   func(string, func() error) (string, string, error) { return "isolate", "isolate", nil },
		CheckAvailable: func() error { return nil },
		CheckWorkLogin: func(string, string, string) (string, error) { return "", nil },
		OpenRegular:    func(p string, flag int) (*os.File, error) { return os.OpenFile(p, flag, 0o600) },
		FyrEnv:         os.Environ,
		NewBox: func(cfg BoxConfig) BoxHandle {
			return &fakeBox{dir: cfg.Dir, app: filepath.Join(cfg.Dir, "app"), home: filepath.Join(cfg.Dir, "home"), intact: true, gitOK: true, cfg: cfg}
		},
	}
}

func writeTestFile(t *testing.T, p, s string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeTestFile(t, filepath.Join(repo, "README.md"), "demo\n", 0o644)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

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
	b := newFakeBox(t)
	if err := os.MkdirAll(filepath.Join(b.Home(), ".local", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(b.Dir(), "pwned")
	poisoned := "#!/bin/sh\ntouch '" + marker + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(b.Home(), ".local", "bin", "fyr"), []byte(poisoned), 0o755); err != nil {
		t.Fatal(err)
	}

	leadBin := filepath.Join(t.TempDir(), "lead-fyr")
	recLine := `{"id":"CHANGE-1","kind":"change","status":"closed","date":"2026-11-19","title":"t","updated":"2026-11-19T08:00:00Z","body":"b"}`
	leadScript := "#!/bin/sh\nmkdir -p .agents\ncat <<'JSONL' > .agents/trail.jsonl\n" + recLine + "\nJSONL\n"
	if err := os.WriteFile(leadBin, []byte(leadScript), 0o755); err != nil {
		t.Fatal(err)
	}

	w := &worker{deps: testDeps(), o: Options{Self: leadBin}, b: b}
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

	deps := testDeps()
	const sentinelModel = "bad model"
	baseOpts := func() Options {
		return Options{
			Home:       t.TempDir(),
			RealHome:   t.TempDir(),
			Model:      sentinelModel,
			DraftsOnly: true,
		}
	}
	opts := baseOpts()
	if _, err := preflightWorker(context.Background(), deps, &opts, []string{k1Path, k2Path}); err == nil || !strings.Contains(err.Error(), "overlap on") {
		t.Fatalf("preflightWorker(overlapping) err = %v, want overlap refusal", err)
	}
	opts = baseOpts()
	if _, err := preflightWorker(context.Background(), deps, &opts, []string{kBadPath}); err == nil || !strings.Contains(err.Error(), "contains a '..' segment") {
		t.Fatalf("preflightWorker(parent dir) err = %v, want '..' refusal", err)
	}
	opts = baseOpts()
	if _, err := preflightWorker(context.Background(), deps, &opts, []string{k1Path, k3Path}); err == nil || !strings.Contains(err.Error(), "--model "+sentinelModel+": not a model ID") {
		t.Fatalf("preflightWorker(disjoint) err = %v, want sentinel model refusal past parse/disjoint", err)
	}

	// checkWorkerRoots DraftsOnly must-pass (global / dir) and must-fail (non-dir file).
	fileNotDir := filepath.Join(t.TempDir(), "file.txt")
	_ = os.WriteFile(fileNotDir, []byte("x"), 0o600)
	if err := checkWorkerRoots(deps, Options{Root: globalWorkspace, Home: t.TempDir(), DraftsOnly: true}); err != nil {
		t.Fatalf("checkWorkerRoots(global) err = %v, want nil", err)
	}
	if err := checkWorkerRoots(deps, Options{Root: fileNotDir, Home: t.TempDir(), DraftsOnly: true}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("checkWorkerRoots(file) err = %v, want not a directory", err)
	}

	long := strings.Repeat("─", 2500)
	got := trail.TruncateUTF8(long, 6000)
	if len(got) > 6000 || !utf8.ValidString(got) {
		t.Fatalf("trail.TruncateUTF8 invalid: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
	gotOdd := trail.TruncateUTF8(long, 10)
	if len(gotOdd) != 9 || !utf8.ValidString(gotOdd) {
		t.Fatalf("trail.TruncateUTF8(10) = len=%d valid=%v, want 9 and valid", len(gotOdd), utf8.ValidString(gotOdd))
	}
}

func TestLeadZone(t *testing.T) {
	t.Setenv("TZ", "")
	if z, err := leadZone(); err != nil || z.name != "UTC" {
		t.Fatalf("leadZone empty TZ = (%+v, %v)", z, err)
	}
	t.Setenv("TZ", "../evil")
	if _, err := leadZone(); err == nil {
		t.Fatal("leadZone accepted ../evil")
	}
}

func TestResolveAutoModelsAndValidateListed(t *testing.T) {
	t.Parallel()
	listed := map[string]bool{"gemini-4-carbon-high": true, "gemini-pro-orchestrator": true}
	ids := []string{"gemini-4-carbon-high", "gemini-pro-orchestrator"}

	docOpts := Options{Home: t.TempDir()}
	docKs := []Kickoff{{Path: "doc.md", Files: []string{"README.md"}, Text: "update doc", Model: "auto", JudgeModel: "auto"}}
	if err := resolveAutoModels(&docOpts, docKs, listed, ids); err != nil || docKs[0].Model != "gemini-4-carbon-high" || docKs[0].JudgeModel != "" {
		t.Fatalf("resolveAutoModels(doc) = (err=%v, k.Model=%q, k.JudgeModel=%q)", err, docKs[0].Model, docKs[0].JudgeModel)
	}

	opts := Options{Home: t.TempDir(), Model: "auto"}
	ks := []Kickoff{{Path: "k1.md", Files: []string{"a.go"}, Text: "work"}}
	if err := resolveAutoModels(&opts, ks, listed, ids); err != nil || opts.Model != "gemini-4-carbon-high" || ks[0].Model != "gemini-4-carbon-high" {
		t.Fatalf("resolveAutoModels = (err=%v, o.Model=%q, k.Model=%q)", err, opts.Model, ks[0].Model)
	}

	optsFail := Options{Home: t.TempDir(), Model: "auto"}
	if err := resolveAutoModels(&optsFail, []Kickoff{{Path: "k1.md", Text: "work"}}, map[string]bool{"gemini-pro-orchestrator": true}, []string{"gemini-pro-orchestrator"}); err == nil {
		t.Fatal("resolveAutoModels succeeded when no eligible model listed")
	}

	// Refactored checkModels helpers: must-pass & must-fail.
	if hasNamedModel(&Options{}, []Kickoff{{Path: "a.md"}}) {
		t.Fatal("hasNamedModel = true for unnamed kickoff/options")
	}
	if !hasNamedModel(&Options{}, []Kickoff{{Path: "a.md", JudgeModel: "m1"}}) {
		t.Fatal("hasNamedModel = false when JudgeModel set")
	}
	if err := validateListedModels(&Options{Model: "gemini-4-carbon-high"}, []Kickoff{{Path: "a.md", Model: "gemini-4-carbon-high"}}, listed, ids); err != nil {
		t.Fatalf("validateListedModels valid err = %v", err)
	}
	if err := validateListedModels(&Options{Model: "unlisted-model"}, nil, listed, ids); err == nil || !strings.Contains(err.Error(), "does not list") {
		t.Fatalf("validateListedModels unlisted --model err = %v, want refusal", err)
	}
	if err := validateListedModels(&Options{}, []Kickoff{{Path: "a.md", JudgeModel: "unlisted-judge"}}, listed, ids); err == nil || !strings.Contains(err.Error(), "judge model") {
		t.Fatalf("validateListedModels unlisted judge model err = %v, want refusal", err)
	}
}

func TestWorkerPrintTimeoutRemoveDraftsAndReflog(t *testing.T) {
	t.Parallel()
	deps := testDeps()
	// CappedWriter
	cb := trail.NewCappedBuffer(5, nil)
	_, _ = cb.Write([]byte("abc"))
	if cb.Truncated() || cb.String() != "abc" {
		t.Fatalf("CappedWriter under limit: truncated=%v buf=%q", cb.Truncated(), cb.String())
	}
	_, _ = cb.Write([]byte("defg"))
	if !cb.Truncated() || cb.String() != "abcde" {
		t.Fatalf("CappedWriter over limit: truncated=%v buf=%q", cb.Truncated(), cb.String())
	}

	// PrintTimeout capped grace for 5m timeout
	w5 := newWorker(deps, Options{Timeout: 5 * time.Minute}, prepared{k: Kickoff{Name: "a"}}, "s", t.TempDir(), nil)
	if got := w5.b.(*fakeBox).cfg.PrintTimeout; got != "4m" {
		t.Fatalf("PrintTimeout for 5m = %q, want 4m", got)
	}

	// remove(): removes !w.ran sandbox, keeps w.ran && !w.collected
	bClean := newFakeBox(t)
	wClean := &worker{deps: deps, o: Options{}, b: bClean, ran: false, collected: false}
	wClean.remove()
	if _, err := os.Stat(bClean.Dir()); !os.IsNotExist(err) {
		t.Fatalf("remove() leaked !w.ran sandbox %s", bClean.Dir())
	}
	bKeep := newFakeBox(t)
	wKeep := &worker{deps: deps, o: Options{}, b: bKeep, ran: true, collected: false}
	wKeep.remove()
	if _, err := os.Stat(bKeep.Dir()); err != nil {
		t.Fatalf("remove() deleted uncollected ran sandbox %s", bKeep.Dir())
	}

	// drafts() copyDrafts must-pass (valid .md + auto-drafts.jsonl) and must-fail (bad name skipped)
	repo := initTestRepo(t)
	wDraft := &worker{deps: deps, o: Options{Root: repo, Auto: true}, p: prepared{k: Kickoff{Name: "d"}}, b: bKeep, stamp: "st1"}
	dSrc := filepath.Join(bKeep.App(), draftsRel)
	_ = os.MkdirAll(dSrc, 0o700)
	_ = os.WriteFile(filepath.Join(dSrc, "note-1.md"), []byte("# ok\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dSrc, autoDraftsName), []byte("{}\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dSrc, "BAD NAME.md"), []byte("skip"), 0o600)
	if !wDraft.drafts() || wDraft.res.Drafts != 2 {
		t.Fatalf("drafts() count = %d, want 2 (notes=%s)", wDraft.res.Drafts, bKeep.notes.String())
	}
	if !strings.Contains(bKeep.notes.String(), "BAD NAME.md") {
		t.Errorf("expected BAD NAME.md in skip note, got %q", bKeep.notes.String())
	}
	wDraft.closeLeadDir()

	// reflog on temp repo
	if start, err := reflogStart(repo); err != nil || reflogMoved(repo, start) != "" {
		t.Fatalf("reflog = (%+v, %v)", start, err)
	}
}

func TestKickoffCapsulePivotsAndFormat(t *testing.T) {
	t.Parallel()
	capsule := "[Hop 0] internal/a.go:10-25 (Foo)\nbody\n[Hop 1] internal/b.go:3-8 snippet [call site]\nsnip\n[Hop 1] internal/c.go:1-12 (Bar)\nbody2\n"
	pivots := ParseKickoffCapsulePivots(capsule)
	if len(pivots) != 2 || pivots[0].Path != "internal/a.go" || pivots[1].Path != "internal/c.go" {
		t.Fatalf("ParseKickoffCapsulePivots = %+v", pivots)
	}

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, ".agents", "scratch", "search.db"), "db", 0o600)
	var preseeded []CapsulePivot
	k := Kickoff{Name: "k", Files: []string{"internal/a.go"}, Text: "Files: internal/a.go\n\nPrompt\n"}
	got := FormatKickoffCapsule(k, root,
		func(string, string, int) (string, error) { return capsule, nil },
		func(_ string, ps []CapsulePivot) error { preseeded = ps; return nil },
	)
	if !strings.Contains(got, "## Structural Context Capsule\n") || len(preseeded) != 2 {
		t.Fatalf("FormatKickoffCapsule got=%q preseeded=%+v", got, preseeded)
	}
}
