package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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

// Ledger item 47: `record import` stores what it reads as written, as add
// does, and names on stderr each imported record whose ID, title or body the
// redactor would change, by its ID, or by its place in the printed list when
// the ID is what the redactor would change; a clean record prints nothing
// there and the exit stays 0. A dry run names the same records without
// claiming a store, held ones included; a real run names only what it stored.
func TestRecordImportWarnsOnRedactable(t *testing.T) {
	root := recordRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	var errOut bytes.Buffer
	stderr = &errOut
	t.Cleanup(func() { stderr = os.Stderr })
	const token = "dckr_pat_FAKEVALUEFAKEVALUE"
	for rel, text := range map[string]string{
		".agents/docs/plans/2026-01-01-body.md":  "# Body plan\n\n- [ ] left\nlogin with " + token + "\n",
		".agents/docs/plans/2026-01-01-title.md": "# Plan for " + token + "\n\n- [ ] left\n",
		".agents/docs/plans/2026-01-01-clean.md": "# Clean plan\n\n- [ ] left\n",
		// The ID comes from the file name (the phase's security review,
		// round 1), and slugging can hide it from the redactor, so the path
		// is judged too (round 2): the first and fifth records in path order.
		".agents/docs/plans/2026-01-01-xoxb-fakevalue.md":           "# Named plan\n\n- [ ] left\n",
		".agents/docs/plans/2026-01-01-api_key=0123456789abcdef.md": "# Keyed plan\n\n- [ ] left\n",
	} {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(text), 0o644)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "docs"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	wantWarn := func(tail string) string {
		const warn = "fyr record import: warning: "
		return warn + "record 1 in the list above: the ID or the source path holds text the redactor would change" + tail + "\n" +
			warn + "PLAN-2026-01-01-body: the body holds text the redactor would change (first at line 4)" + tail + "\n" +
			warn + "record 4 in the list above: the title holds text the redactor would change" + tail + "\n" +
			warn + "record 4 in the list above: the body holds text the redactor would change (first at line 1)" + tail + "\n" +
			warn + "record 5 in the list above: the ID or the source path holds text the redactor would change" + tail + "\n"
	}
	for _, tc := range []struct {
		args []string
		tail string
	}{
		{[]string{"record", "import", "--dry-run"}, ""},
		{[]string{"record", "import"}, "; stored as written"},
	} {
		errOut.Reset()
		out, err := recordCmd(t, "", append(tc.args, "--home", home)...)
		if exitOf(err) != 0 {
			t.Fatalf("%v: exit %d\n%s", tc.args, exitOf(err), out)
		}
		if got, want := errOut.String(), wantWarn(tc.tail); got != want {
			t.Errorf("%v: stderr\n%q\nwant\n%q", tc.args, got, want)
		}
		for _, leak := range []string{"xoxb-", "0123456789abcdef", "Plan for"} {
			if strings.Contains(errOut.String(), leak) {
				t.Errorf("%v: %q reached stderr", tc.args, leak)
			}
		}
		if strings.Contains(out+errOut.String(), token) {
			t.Errorf("%v: the token reached the output", tc.args)
		}
	}
	errOut.Reset()
	got, _ := recordCmd(t, "", "record", "get", "PLAN-2026-01-01-body", "PLAN-2026-01-01-title", "--home", home)
	if !strings.Contains(got, "login with "+token+"\n") || !strings.Contains(got, "# Plan for "+token+"\n") {
		t.Errorf("the imported records were not stored as written:\n%s", got)
	}

	// Once the scope holds them, a dry run still names every record it read,
	// and a second real import, which stores nothing, names none.
	for _, args := range [][]string{{"add", ".agents/trail.jsonl"}, {"commit", "-qm", "trail"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	errOut.Reset()
	if _, err := recordCmd(t, "", "record", "import", "--dry-run", "--home", home); exitOf(err) != 0 || errOut.String() != wantWarn("") {
		t.Errorf("dry run over held records: exit %d, stderr %q", exitOf(err), errOut.String())
	}
	errOut.Reset()
	if out, err := recordCmd(t, "", "record", "import", "--home", home); exitOf(err) != 0 || !strings.Contains(out, "5 already stored") || errOut.Len() != 0 {
		t.Errorf("re-import: exit %d, out %q, stderr %q; want 0, 5 already stored and no warning", exitOf(err), out, errOut.String())
	}
}
