package main

import (
	"errors"
	"strings"
	"testing"
)

// The arms of runEval that its split moves into helpers, pinned before the
// split (PLAN-2026-10-27-funclen Phase 6, D13). Exempt (D12): the --fixture
// path's filepath.Abs error, which needs getcwd to fail.

// --list in a checkout whose trail holds no suite is a refusal.
func TestEvalListWithoutSuite(t *testing.T) {
	r := newEvalRig(t, "", true)
	out, code := r.eval("--list")
	if code != 2 || !strings.HasPrefix(out, "eval: ") {
		t.Errorf("exit %d, want 2 and a refusal:\n%s", code, out)
	}
}

// No home directory is a refusal, before any row runs.
func TestEvalRefusesWithoutHome(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	t.Setenv("HOME", "")
	out, code := r.eval("ok")
	if code != 2 || !strings.HasPrefix(out, "eval: no home directory: ") {
		t.Errorf("exit %d: %q", code, out)
	}
}

// No running fyr to install in the sandboxes is a refusal, before any row
// runs.
func TestEvalRefusesWithoutSelf(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	evalSelf = func() (string, error) { return "", errors.New("no executable") }
	out, code := r.eval("ok")
	if code != 2 || out != "eval: the running fyr: no executable\n" {
		t.Errorf("exit %d: %q", code, out)
	}
}
