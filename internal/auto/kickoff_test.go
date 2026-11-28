package auto

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/search"
)

func TestDefaultCapsuleDepsAndFormatKickoffCapsule(t *testing.T) {
	deps := DefaultCapsuleDeps()
	if deps.SliceFn == nil || deps.PreseedFn == nil {
		t.Fatalf("DefaultCapsuleDeps returned nil callback(s): %+v", deps)
	}
	home := t.TempDir()
	root := initIndexedTargetRepo(t, t.TempDir(), "capsule-proj", home, "PreseedTarget")

	sliceOut, err := deps.SliceFn(root, "PreseedTarget", 800)
	if err != nil || !strings.Contains(sliceOut, "PreseedTarget") {
		t.Fatalf("DefaultCapsuleDeps().SliceFn = %q, err = %v", sliceOut, err)
	}

	baseKickoff := isolate.Kickoff{
		Name:  "capsule-format",
		Text:  "Files: sample.go, README.md\n\nUpdate PreseedTarget.\n",
		Files: []string{"sample.go", "README.md"},
	}
	var empty isolate.CapsuleDeps
	if got := isolate.FormatKickoffCapsule(baseKickoff, root, empty.SliceFn, empty.PreseedFn); got != baseKickoff.Text {
		t.Errorf("empty CapsuleDeps attached capsule unexpectedly:\n%s", got)
	}
	withCapsule := isolate.FormatKickoffCapsule(baseKickoff, root, deps.SliceFn, deps.PreseedFn)
	if !strings.Contains(withCapsule, "## Structural Context Capsule\n") || !strings.Contains(withCapsule, "PreseedTarget") {
		t.Fatalf("FormatKickoffCapsule with DefaultCapsuleDeps missing capsule:\n%s", withCapsule)
	}
	res, err := search.CheckAndRecordRead(root, "work-kickoff", "sample.go", 3, 5, 1)
	if err != nil {
		t.Fatalf("CheckAndRecordRead: %v", err)
	}
	if res.Allowed || !strings.Contains(res.Reason, "[UNCHANGED]") {
		t.Errorf("expected FormatKickoffCapsule to pre-seed read_ledger, got %+v", res)
	}
	if err := deps.PreseedFn(root, []isolate.CapsulePivot{{Path: filepath.ToSlash("sample.go"), StartLine: 7, EndLine: 9}}); err != nil {
		t.Fatalf("DefaultCapsuleDeps().PreseedFn: %v", err)
	}
	res2, err := search.CheckAndRecordRead(root, "work-kickoff", "sample.go", 7, 9, 1)
	if err != nil {
		t.Fatalf("CheckAndRecordRead(helper): %v", err)
	}
	if res2.Allowed || !strings.Contains(res2.Reason, "[UNCHANGED]") {
		t.Errorf("expected explicit PreseedFn to block redundant read, got %+v", res2)
	}
}

func TestRunWorkerThreadsCapsuleDeps(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	emptyRoot := initIndexedTargetRepo(t, base, "empty-deps", home, "EmptyDepsTarget")
	wiredRoot := initIndexedTargetRepo(t, base, "wired-deps", home, "WiredDepsTarget")
	const sentinelModel = "bad model"
	wantSentinel := "--model " + sentinelModel + ": not a model ID"

	writeKick := func(root, name string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), name+".md")
		body := "Files: sample.go, README.md\nProject: " + root + "\n\nCheck target.\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Empty CapsuleDeps{} must be a safe no-op: no capsule slice or pre-seed.
	_, err := isolate.RunWorker(context.Background(), isolate.WorkerOptions{
		Root:     emptyRoot,
		Home:     t.TempDir(),
		RealHome: t.TempDir(),
		Model:    sentinelModel,
		Capsule:  isolate.CapsuleDeps{},
	}, writeKick(emptyRoot, "empty-kick"))
	if err == nil || !strings.Contains(err.Error(), wantSentinel) {
		t.Fatalf("RunWorker(empty CapsuleDeps) err = %v, want %q", err, wantSentinel)
	}
	emptyRes, err := search.CheckAndRecordRead(emptyRoot, "work-kickoff", "sample.go", 3, 5, 1)
	if err != nil {
		t.Fatalf("CheckAndRecordRead(emptyRoot): %v", err)
	}
	if !emptyRes.Allowed {
		t.Errorf("empty CapsuleDeps unexpectedly pre-seeded read_ledger: %+v", emptyRes)
	}

	// Explicit DefaultCapsuleDeps() must thread through preflight into parseKickoff.
	_, err = isolate.RunWorker(context.Background(), isolate.WorkerOptions{
		Root:     wiredRoot,
		Home:     t.TempDir(),
		RealHome: t.TempDir(),
		Model:    sentinelModel,
		Capsule:  DefaultCapsuleDeps(),
	}, writeKick(wiredRoot, "wired-kick"))
	if err == nil || !strings.Contains(err.Error(), wantSentinel) {
		t.Fatalf("RunWorker(DefaultCapsuleDeps) err = %v, want %q", err, wantSentinel)
	}
	wiredRes, err := search.CheckAndRecordRead(wiredRoot, "work-kickoff", "sample.go", 3, 5, 1)
	if err != nil {
		t.Fatalf("CheckAndRecordRead(wiredRoot): %v", err)
	}
	if wiredRes.Allowed || !strings.Contains(wiredRes.Reason, "[UNCHANGED]") {
		t.Errorf("expected RunWorker with DefaultCapsuleDeps to pre-seed read_ledger, got %+v", wiredRes)
	}

	// workOptions must wire non-nil Capsule deps into isolate.WorkerOptions.
	r := &run{o: Options{Home: home}}
	wo := r.workOptions(wiredRoot, Task{Class: ClassDistill})
	if wo.Capsule.SliceFn == nil || wo.Capsule.PreseedFn == nil {
		t.Errorf("workOptions left Capsule deps nil: %+v", wo.Capsule)
	}
}

func initIndexedTargetRepo(t *testing.T, base, name, home, targetSym string) string {
	t.Helper()
	remote := filepath.Join(base, name+"-remote.git")
	root := filepath.Join(base, name)
	git(t, base, "init", "-q", "--bare", "-b", "main", remote)
	git(t, base, "clone", "-q", remote, root)
	git(t, root, "checkout", "-q", "-b", "main")
	writeTree(t, root, ".agents/trail.jsonl", "")
	writeTree(t, root, ".gitignore", ".agents/scratch/*\n")
	src := strings.Join([]string{
		"package sample",
		"",
		"func " + targetSym + "(input string) string {",
		"\treturn helperSymbol(input)",
		"}",
		"",
		"func helperSymbol(s string) string {",
		"\treturn s",
		"}",
		"",
	}, "\n")
	writeTree(t, root, "sample.go", src)
	writeTree(t, root, "README.md", "see `"+targetSym+"` and `helperSymbol`\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "init "+name)
	git(t, root, "push", "-q", "-u", "origin", "main")
	backdate(t, root)
	if _, err := brain.Add(brain.Options{Home: home, Dir: root, Now: func() time.Time { return runAt.Add(-8 * time.Hour) }}, brain.AddInput{
		Kind:  "guide",
		ID:    "GUIDE-onboarding",
		Title: "Onboarding",
		Body:  strings.NewReader("# Guide\n"),
	}); err != nil {
		t.Fatalf("brain.Add(%s): %v", name, err)
	}
	var out bytes.Buffer
	if err := search.Index(search.Options{Dir: root, Home: home}, &out); err != nil {
		t.Fatalf("search.Index(%s): %v", name, err)
	}
	return root
}

func TestExecWorkerPropagatesRootToWriteKickoff(t *testing.T) {
	home := t.TempDir()
	realHome := t.TempDir()
	base := t.TempDir()
	alphaRoot := initIndexedTargetRepo(t, base, "alpha-proj", home, "AlphaTargetSymbol")
	zuluRoot := initIndexedTargetRepo(t, base, "zulu-proj", home, "ZuluTargetSymbol")
	if alphaRoot >= zuluRoot {
		t.Fatalf("expected alphaRoot %q < zuluRoot %q", alphaRoot, zuluRoot)
	}
	if err := ensureDir(home); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, home, validConfig)
	st := State{
		Date: runAt.Format("2006-01-02"),
		Projects: map[string]Project{
			alphaRoot: {},
			zuluRoot:  {},
		},
	}
	if err := saveState(home, st); err != nil {
		t.Fatal(err)
	}

	var kickoffName, kickoffBody string
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.RealHome = realHome
	o.IsolateAvailable = func() error { return nil }
	o.Work = func(_ context.Context, wo isolate.WorkerOptions, paths ...string) ([]isolate.WorkerResult, error) {
		if wo.Root != zuluRoot {
			t.Errorf("worker Root = %q, want %q", wo.Root, zuluRoot)
		}
		if wo.Capsule.SliceFn == nil || wo.Capsule.PreseedFn == nil {
			t.Errorf("worker Capsule deps not wired: %+v", wo.Capsule)
		}
		kickoffName = strings.TrimSuffix(filepath.Base(paths[0]), ".md")
		data, err := os.ReadFile(paths[0])
		if err != nil {
			t.Fatal(err)
		}
		kickoffBody = string(data)
		git(t, wo.Root, "branch", "work/"+kickoffName)
		return []isolate.WorkerResult{{Name: kickoffName, Verdict: "PASS"}}, nil
	}
	r := &run{
		ctx: context.Background(),
		o:   o,
		cfg: Config{Enabled: true, ConversationIdle: "1h"},
		st:  st,
	}
	task, err := PrepareTask(ClassDistill, []string{".agents/skills/auto-distilled-helpersymbol-run/"}, "", 3)
	if err != nil {
		t.Fatalf("PrepareTask: %v", err)
	}
	if task.Root != "" {
		t.Fatalf("expected PrepareTask to leave Root empty, got %q", task.Root)
	}
	var p Project
	verdict, _ := r.execWorker(zuluRoot, &p, task)
	if !strings.HasPrefix(verdict, "PASS") {
		t.Fatalf("execWorker verdict = %q, want PASS", verdict)
	}
	if !strings.Contains(kickoffBody, "ZuluTargetSymbol") || strings.Contains(kickoffBody, "AlphaTargetSymbol") {
		t.Fatalf("kickoff capsule targeted wrong root (want ZuluTargetSymbol only):\n%s", kickoffBody)
	}
	zuluRes, err := search.CheckAndRecordRead(zuluRoot, kickoffName, "sample.go", 7, 9, 1)
	if err != nil {
		t.Fatalf("CheckAndRecordRead(zulu): %v", err)
	}
	if zuluRes.Allowed || !strings.Contains(zuluRes.Reason, "[UNCHANGED]") {
		t.Errorf("expected zuluRoot read_ledger pre-seeded, got %+v", zuluRes)
	}
	alphaRes, err := search.CheckAndRecordRead(alphaRoot, kickoffName, "sample.go", 7, 9, 1)
	if err != nil {
		t.Fatalf("CheckAndRecordRead(alpha): %v", err)
	}
	if !alphaRes.Allowed {
		t.Errorf("expected alphaRoot read_ledger untouched, got %+v", alphaRes)
	}
}

func TestDraftsOnlyKickoffSelfContainedSchemaAndExamplesValid(t *testing.T) {
	home := t.TempDir()
	root := initGitRepo(t)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	// Seed one existing derived project memory so memory-consolidation retire/merge examples can validate.
	bo := brain.Options{Home: home, Dir: root, Now: func() time.Time { return now }}
	if _, err := brain.MemoryDraftWithOptions(bo, "add", "existing-id", "existing memory", brain.MemoryDraftOptions{Type: "semantic"}); err != nil {
		t.Fatalf("seed MemoryDraftWithOptions: %v", err)
	}

	cases := []struct {
		class       string
		id          string
		zeroTool    bool
		wantSubstrs []string
	}{
		{
			class:    ClassRetroLite,
			zeroTool: true,
			wantSubstrs: []string{
				"signal_id",
				"report_date",
				"action_scope",
				"recommended_action",
				`never use "type" or "class"`,
			},
		},
		{
			class:    ClassMemoryCandidates,
			zeroTool: true,
			wantSubstrs: []string{
				`"kind":"memory"`,
				`"action":"add"`,
				`"memory_type"`,
				`never use "type" or "class"`,
			},
		},
		{
			class:    ClassMemoryConsolidation,
			zeroTool: true,
			wantSubstrs: []string{
				`"kind":"memory"`,
				`"action":"retire"`,
				`"action":"merge"`,
				`"kind":"proposal"`,
			},
		},
		{
			class:    ClassMissingSkill,
			id:       "PROPOSAL-2026-11-26-1000-missing",
			zeroTool: false,
			wantSubstrs: []string{
				`"kind":"proposal"`,
				`"kind":"finding"`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.class, func(t *testing.T) {
			task, err := PrepareTask(tc.class, nil, tc.id, 3)
			if err != nil {
				t.Fatalf("PrepareTask: %v", err)
			}
			k, err := BuildKickoff(now, task)
			if err != nil {
				t.Fatalf("BuildKickoff: %v", err)
			}
			if tc.zeroTool {
				if strings.Contains(k.Text, "First read ~/.gemini/config/skills/fyr-auto/SKILL.md") {
					t.Errorf("zero-tool %s Kickoff still tells worker to read SKILL.md:\n%s", tc.class, k.Text)
				}
				if strings.Contains(k.Text, "Stop and write the reason as a finding rather than touch a path outside `Files:`.") {
					t.Errorf("zero-tool %s Kickoff still has contradictory empty-Files stop rule:\n%s", tc.class, k.Text)
				}
			}
			for _, sub := range tc.wantSubstrs {
				if !strings.Contains(k.Text, sub) {
					t.Errorf("%s Kickoff missing %q:\n%s", tc.class, sub, k.Text)
				}
			}
			// Every JSON example line embedded in the Kickoff must pass parseAndValidateDraft.
			r := &run{o: Options{Home: home, Now: func() time.Time { return now }}}
			examples := 0
			for _, line := range strings.Split(k.Text, "\n") {
				trimmed := strings.TrimSpace(line)
				if !strings.HasPrefix(trimmed, `{"kind":`) {
					continue
				}
				examples++
				d, err := parseAndValidateDraft(trimmed)
				if err != nil {
					t.Errorf("%s Kickoff example failed parseAndValidateDraft: %v\nline: %s", tc.class, err, trimmed)
					continue
				}
				if _, err := r.applyDraftLine(root, tc.class, d); err != nil {
					t.Errorf("%s Kickoff example failed applyDraftLine: %v\nline: %s", tc.class, err, trimmed)
				}
			}
			if examples == 0 {
				t.Errorf("%s Kickoff contains zero inline JSONL schema examples", tc.class)
			}
		})
	}
}

func TestHarnessEditKickoffGuidesFastTargetedChecks(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		class string
		files []string
		id    string
	}{
		{ClassHarnessEdit, []string{"skills/fyr-feature/"}, "PROPOSAL-1"},
		{ClassDistill, []string{".agents/skills/auto-distilled-foo/"}, ""},
	} {
		task, err := PrepareTask(tc.class, tc.files, tc.id, 2)
		if err != nil {
			t.Fatalf("PrepareTask(%s): %v", tc.class, err)
		}
		k, err := BuildKickoff(now, task)
		if err != nil {
			t.Fatalf("BuildKickoff(%s): %v", tc.class, err)
		}
		for _, want := range []string{
			"First read ~/.gemini/config/skills/fyr-auto/SKILL.md",
			"targeted",
			"never run full-repo `fyr verify` or kill a running `go test`",
		} {
			if !strings.Contains(k.Text, want) {
				t.Errorf("%s Kickoff missing %q:\n%s", tc.class, want, k.Text)
			}
		}
	}
}
