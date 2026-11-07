package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEvalVerbStageSecondsAndTurns pins Item 24 end to end through `fyr eval`:
// a 13-column holdout --suite-file row with timeout and 3 scripted turns runs
// 3 sessions on the same sandbox home, stores setup_seconds, session_seconds
// and check_seconds on the trial row in metrics.db, and leaves all three stage
// columns NULL when setup fails (ADR-509).
func TestEvalVerbStageSecondsAndTurns(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	r.template("1", "C1")
	r.template("2", "C1", "C2")
	r.template("3", "C1", "C2", "C3")
	suiteBody := "| id | setup | prompt | prompt2 | check | agent | grants | version | kind | cluster | expect | timeout | turns |\n" +
		"|---|---|---|---|---|---|---|---|---|---|---|---|---|\n" +
		"| `pipe` | `true` | plan it | go all approved | `grep -q 'agy says ok' \"$OUT3\"` |  | named | 1 | benchmark | pipe |  | 45m | 3 |\n" +
		"| `failsetup` | `exit 1` | plan it |  | `true` |  | named | 1 | benchmark | pipe |  | 30m | 1 |\n"
	holdout := filepath.Join(t.TempDir(), "holdout-bench.md")
	if err := os.WriteFile(holdout, []byte(suiteBody), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := r.eval("--suite-file", holdout, "pipe")
	if code != 0 || !strings.Contains(out, "eval: 1 of 1 PASS, 0 errors; record TEST-") {
		t.Fatalf("eval pipe: exit %d\n%s", code, out)
	}
	if n := strings.TrimSpace(readFile(t, filepath.Join(r.rec, "agy.n"))); n != "3" {
		t.Fatalf("agy ran %s times, want 3", n)
	}
	trials := exported(t, r.home, "trials")
	if len(trials) != 1 {
		t.Fatalf("got %d trials, want 1: %v", len(trials), trials)
	}
	tr := trials[0]
	if tr["label"] != "eval=holdout/pipe" || tr["kind"] != "benchmark" || tr["setup_seconds"] == nil || tr["session_seconds"] == nil || tr["check_seconds"] == nil {
		t.Errorf("trial row = %v, want eval=holdout/pipe benchmark with non-nil setup_seconds, session_seconds, check_seconds", tr)
	}
	sessions := exported(t, r.home, "sessions")
	if len(sessions) != 3 {
		t.Errorf("got %d ingested sessions, want 3", len(sessions))
	}

	outErr, codeErr := r.eval("--suite-file", holdout, "failsetup")
	if codeErr != 1 || !strings.Contains(outErr, "ERROR (setup; setup failed)") {
		t.Fatalf("eval failsetup: exit %d\n%s", codeErr, outErr)
	}
	for _, x := range exported(t, r.home, "trials") {
		if x["label"] == "eval=holdout/failsetup" {
			if x["setup_seconds"] != nil || x["session_seconds"] != nil || x["check_seconds"] != nil {
				t.Errorf("failsetup trial = %v, want nil stage seconds", x)
			}
		}
	}
}
