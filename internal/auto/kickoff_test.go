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

func TestDefaultPreseedFuncAndSliceFunc(t *testing.T) {
	if isolate.DefaultPreseedFunc == nil || isolate.DefaultSliceFunc == nil {
		t.Fatalf("kickoff init did not register DefaultPreseedFunc/DefaultSliceFunc")
	}
	home := t.TempDir()
	root := idleProject(t)
	writeTree(t, root, ".gitignore", ".agents/scratch/*\n")
	writeTree(t, root, "sample.go", "package sample\n\nfunc PreseedTarget() string {\n\treturn helperValue()\n}\n\nfunc helperValue() string {\n\treturn \"ok\"\n}\n")
	git(t, root, "add", ".gitignore", "sample.go")
	git(t, root, "commit", "-q", "-m", "add sample.go")
	git(t, root, "push", "-q")
	backdate(t, root)

	if _, err := brain.Add(brain.Options{Home: home, Dir: root, Now: func() time.Time { return runAt.Add(-8 * time.Hour) }}, brain.AddInput{
		Kind:  "guide",
		ID:    "GUIDE-onboarding",
		Title: "Onboarding",
		Body:  strings.NewReader("# Guide\n"),
	}); err != nil {
		t.Fatalf("brain.Add: %v", err)
	}
	var out bytes.Buffer
	if err := search.Index(search.Options{Dir: root, Home: home}, &out); err != nil {
		t.Fatalf("search.Index: %v", err)
	}
	if err := isolate.DefaultPreseedFunc(root, []isolate.CapsulePivot{{Path: filepath.ToSlash("sample.go"), StartLine: 3, EndLine: 5}}); err != nil {
		t.Fatalf("DefaultPreseedFunc: %v", err)
	}
	res, err := search.CheckAndRecordRead(root, "work-kickoff", "sample.go", 3, 5, 1)
	if err != nil {
		t.Fatalf("CheckAndRecordRead: %v", err)
	}
	if res.Allowed || !strings.Contains(res.Reason, "[UNCHANGED]") {
		t.Errorf("expected pre-seeded read_ledger to block redundant read, got %+v", res)
	}
	sliceOut, err := isolate.DefaultSliceFunc(root, "PreseedTarget", 800)
	if err != nil || !strings.Contains(sliceOut, "PreseedTarget") {
		t.Errorf("DefaultSliceFunc = %q, err = %v", sliceOut, err)
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
		Date: runAt.Format("2006-11-02"),
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
