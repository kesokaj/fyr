package auto

import (
	"bytes"
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
