package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// The redactor warning of SD42 (E2E 4 of PLAN-2026-10-27-audit-choices): a
// body the Docker needle matches is stored as written, exits 0 and is named on
// stderr by line, never by its text; a clean body, an edit without --body and
// a refused body print nothing there.
func TestRecordRedactorWarning(t *testing.T) {
	recordRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	var errOut bytes.Buffer
	stderr = &errOut
	t.Cleanup(func() { stderr = os.Stderr })
	const token = "dckr_pat_FAKEVALUEFAKEVALUE"
	step := func(name, body string, wantErr string, args ...string) string {
		t.Helper()
		errOut.Reset()
		out, err := recordCmd(t, body, append(args, "--home", home)...)
		if exitOf(err) != 0 {
			t.Fatalf("%s: exit %d, out %q", name, exitOf(err), out)
		}
		if got := errOut.String(); got != wantErr {
			t.Errorf("%s: stderr %q, want %q", name, got, wantErr)
		}
		if strings.Contains(out+errOut.String(), token) {
			t.Errorf("%s: the token reached the output", name)
		}
		return out
	}
	warn := func(line string) string {
		return "fyr record: warning: the body holds text the redactor would change (first at line " + line + "); stored as written\n"
	}

	if out := step("add with a token", "Context.\nlogin with "+token+"\n", warn("2"), "record", "add", "bug", "--title", "t"); out != "BUG-001\n" {
		t.Errorf("add printed %q, want the ID", out)
	}
	errOut.Reset()
	if out, _ := recordCmd(t, "", "record", "get", "BUG-001", "--home", home); !strings.Contains(out, "login with "+token+"\n") {
		t.Errorf("the body was not stored as written: %q", out)
	}
	step("clean add", "Symptom: x.\n", "", "record", "add", "bug", "--title", "u")
	step("edit without --body", "", "", "record", "edit", "BUG-001", "--status", "fixed")
	step("edit --body with a token", "a\nb\n"+token+"\n", warn("3"), "record", "edit", "BUG-002", "--body")
	step("edit --body clean", "clean now\n", "", "record", "edit", "BUG-002", "--body")

	// A title is exported too (the phase's security review, finding 1): a new
	// one is judged, a kept one is not.
	const titleWarn = "fyr record: warning: the title holds text the redactor would change; stored as written\n"
	step("add with a token title", "clean\n", titleWarn, "record", "add", "bug", "--title", "leaked "+token)
	step("edit keeping that title", "", "", "record", "edit", "BUG-003", "--status", "fixed")
	step("edit --title with a token", "", titleWarn, "record", "edit", "BUG-002", "--title", "now "+token)
	step("edit --title clean", "", "", "record", "edit", "BUG-002", "--title", "clean")

	// A refused body carrying the token prints nothing on stderr: the warning
	// follows the store, never the read.
	for _, args := range [][]string{{"record", "add", "bug", "--title", "v"}, {"record", "edit", "BUG-002", "--body"}} {
		errOut.Reset()
		if _, err := recordCmd(t, token+"\x00\n", append(args, "--home", home)...); exitOf(err) != 1 || errOut.Len() != 0 {
			t.Errorf("%v with a refused body: exit %d, stderr %q; want 1 and nothing", args, exitOf(err), errOut.String())
		}
	}
}
