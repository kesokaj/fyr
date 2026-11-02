package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/eval"
)

// TestEvalTrackedSuiteWins: a checkout with the tracked suite file reads it
// instead of the GUIDE-eval record (ADR-500); --list reads the working file,
// and a run refuses it until it is committed, then reads HEAD's.
func TestEvalTrackedSuiteWins(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	path := filepath.Join(r.project, filepath.FromSlash(eval.SuitePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	tracked := "| id | setup | prompt | prompt2 | check | agent | grants |\n|---|---|---|---|---|---|---|\n| `tracked` |  | say ok |  | `true` |  | named |\n"
	if err := os.WriteFile(path, []byte(tracked), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := r.eval("--list"); code != 0 || out != "tracked  -  named  v1  capability\n" {
		t.Fatalf("--list: exit %d:\n%s", code, out)
	}
	if out, code := r.eval("tracked"); code == 0 || !strings.Contains(out, eval.SuitePath+" is not committed") {
		t.Errorf("an uncommitted suite file: exit %d:\n%s", code, out)
	}
	if out, code := r.eval("ok"); code == 0 || !strings.Contains(out, eval.SuitePath+" is not committed") {
		t.Errorf("the record's row while the file exists: exit %d:\n%s", code, out)
	}
	r.git("add", eval.SuitePath)
	r.git("commit", "-qm", "suite")
	if out, code := r.eval("ok"); code == 0 || !strings.Contains(out, `no row "ok"`) {
		t.Errorf("a record row once the file is committed: exit %d:\n%s", code, out)
	}
}
