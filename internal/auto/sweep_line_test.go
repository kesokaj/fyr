package auto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWithRunnerKeepsVerdict pins the order withRunner writes (BUG-506): a
// runner's reason longer than the whole line, or a long FAIL / eval-gate
// detail before the branch, is capped so the verdict and the kept branch
// survive within lineLimit.
func TestWithRunnerKeepsVerdict(t *testing.T) {
	verdict := "FAIL (no commits); branch work/x kept"
	got := short(withRunner(verdict, "isolate ("+strings.Repeat("r", lineLimit)+")"))
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("precondition: the line reached the %d-rune cut:\n%s", lineLimit, got)
	}
	if want := verdict + "; runner isolate (r"; !strings.HasPrefix(got, want) {
		t.Fatalf("the cut line lost %q:\n%s", want, got)
	}

	longFail := "FAIL (" + strings.Repeat("e", lineLimit) + "); branch work/long-err kept"
	if got := short(withRunner(longFail, "isolate")); !strings.Contains(got, "; branch work/long-err kept; runner isolate") {
		t.Errorf("long FAIL detail pushed branch off line:\n%s", got)
	}
	longGate := "eval-gate not kept (" + strings.Repeat("g", lineLimit) + "); proposal PROP-1; branch work/gate kept"
	if got := short(withRunner(longGate, "isolate")); !strings.Contains(got, "; proposal PROP-1; branch work/gate kept; runner isolate") {
		t.Errorf("long eval-gate detail pushed proposal/branch off line:\n%s", got)
	}
}

func TestRunMarkedTestShellSanitizesGitEnv(t *testing.T) {
	bin := t.TempDir()
	rec := filepath.Join(bin, "env.txt")
	t.Setenv("GIT_DIR", "/tmp/bad-git-dir")
	t.Setenv("GIT_WORK_TREE", "/tmp/bad-work-tree")
	if err := runMarkedTest(bin, "isolate", "env > '"+rec+"'", 5*time.Second); err != nil {
		t.Fatalf("runMarkedTest: %v", err)
	}
	b, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "GIT_DIR=") || strings.Contains(string(b), "GIT_WORK_TREE=") {
		t.Errorf("runMarkedTest leaked GIT_* env to shell:\n%s", b)
	}
}
