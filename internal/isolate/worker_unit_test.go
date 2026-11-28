package isolate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBoxBashAndLeftoverProcsIgnoreInitPIDs(t *testing.T) {
	r := newRig(t)
	bKeep := r.prepared(r.config("bash-and-procs"))

	// session.go:Bash
	if !bKeep.Bash(context.Background(), "echo ok", FyrEnv(), "bash.log") {
		t.Fatal("Bash failed on echo ok")
	}
	if bKeep.Bash(context.Background(), "exit 2", FyrEnv(), "bash2.log") {
		t.Fatal("Bash passed on exit 2")
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
