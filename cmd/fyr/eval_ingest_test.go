package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kesokaj/fyr/internal/eval"
)

// TestEvalIngestSkipsReplaced: a sandbox whose home the agent replaced with
// a link (to the real home, here a home holding one conversation) is never
// ingested; the same arrangement without that reason is, which shows the
// link would have been read.
func TestEvalIngestSkipsReplaced(t *testing.T) {
	tmp := t.TempDir()
	home, real, dir := filepath.Join(tmp, "fyr-home"), filepath.Join(tmp, "real"), filepath.Join(tmp, "sandbox")
	agyHome(t, real, "C1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "home")); err != nil {
		t.Fatal(err)
	}
	replaced := eval.Result{Row: "x", Status: eval.StatusFail, Reasons: []string{eval.ReasonReplaced}, Dir: dir}
	if err := ingestRow(home, replaced); err != nil {
		t.Fatal(err)
	}
	if k, _ := passK(t, home, "eval=x"); k != 0 {
		t.Errorf("a replaced sandbox was ingested: k %d", k)
	}
	// Without the reason, a linked home is still not followed (IN35 of the
	// 2026-10-27 audit): only a real directory is a sandbox home.
	if err := ingestRow(home, eval.Result{Row: "link", Status: eval.StatusFail, Reasons: []string{eval.ReasonCheck}, Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if k, _ := passK(t, home, "eval=link"); k != 0 {
		t.Errorf("a linked sandbox home was ingested without the replaced reason: k %d", k)
	}
	// A link one component down, home/.gemini to the real home's, is not
	// followed either (security review of the fix, finding 2).
	deep := filepath.Join(tmp, "deep")
	if err := os.MkdirAll(filepath.Join(deep, "home"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(real, ".gemini"), filepath.Join(deep, "home", ".gemini")); err != nil {
		t.Fatal(err)
	}
	if err := ingestRow(home, eval.Result{Row: "deep", Status: eval.StatusFail, Reasons: []string{eval.ReasonCheck}, Dir: deep}); err != nil {
		t.Fatal(err)
	}
	if k, _ := passK(t, home, "eval=deep"); k != 0 {
		t.Errorf("a sandbox whose .gemini links to the real home was ingested: k %d", k)
	}
	// A row that left processes is not read: one that ignored SIGTERM could
	// swap a directory after the check (security review of the fix, round 2).
	procs := filepath.Join(tmp, "procs")
	agyHome(t, filepath.Join(procs, "home"), "C3")
	if err := ingestRow(home, eval.Result{Row: "procs", Status: eval.StatusFail, Reasons: []string{eval.ReasonProcs}, Dir: procs}); err != nil {
		t.Fatal(err)
	}
	if k, _ := passK(t, home, "eval=procs"); k != 0 {
		t.Errorf("a row that left processes was ingested: k %d", k)
	}
	// The control: the same conversation under a real home directory is read.
	ctl := filepath.Join(tmp, "control")
	agyHome(t, filepath.Join(ctl, "home"), "C2")
	if err := ingestRow(home, eval.Result{Row: "ctl", Status: eval.StatusFail, Reasons: []string{eval.ReasonCheck}, Dir: ctl}); err != nil {
		t.Fatal(err)
	}
	if k, _ := passK(t, home, "eval=ctl"); k != 1 {
		t.Errorf("the control was not ingested: k %d", k)
	}
}
