package auto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/work"
)

// TestWithRunnerKeepsVerdict pins the order withRunner writes (BUG-506): a
// runner's reason longer than the whole line, or a long FAIL / eval-gate
// detail before the branch, is capped so the verdict and the kept branch
// survive within lineLimit.
func TestWithRunnerKeepsVerdict(t *testing.T) {
	verdict := "FAIL (no commits); branch work/x kept"
	got := short(withRunner(verdict, "host ("+strings.Repeat("r", lineLimit)+")"))
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("precondition: the line reached the %d-rune cut:\n%s", lineLimit, got)
	}
	if want := verdict + "; runner host (r"; !strings.HasPrefix(got, want) {
		t.Fatalf("the cut line lost %q:\n%s", want, got)
	}

	longFail := "FAIL (" + strings.Repeat("e", lineLimit) + "); branch work/long-err kept"
	if got := short(withRunner(longFail, "host (reason)")); !strings.Contains(got, "; branch work/long-err kept; runner host") {
		t.Errorf("long FAIL detail pushed branch off line:\n%s", got)
	}
	longGate := "eval-gate not kept (" + strings.Repeat("g", lineLimit) + "); proposal PROP-1; branch work/gate kept"
	if got := short(withRunner(longGate, "docker")); !strings.Contains(got, "; proposal PROP-1; branch work/gate kept; runner docker") {
		t.Errorf("long eval-gate detail pushed proposal/branch off line:\n%s", got)
	}
}

func TestRunMarkedTestDockerStripsPlatformEnv(t *testing.T) {
	bin := t.TempDir()
	rec := filepath.Join(bin, "env.txt")
	docker := filepath.Join(bin, "docker")
	script := "#!/bin/sh\nenv > '" + rec + "'\n"
	if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_DEFAULT_PLATFORM", "linux/amd64")
	if err := runMarkedTest(bin, work.RunnerDocker, "true", 5*time.Second); err != nil {
		t.Fatalf("runMarkedTest: %v", err)
	}
	b, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "DOCKER_DEFAULT_PLATFORM=") {
		t.Errorf("runMarkedTest leaked DOCKER_DEFAULT_PLATFORM to docker:\n%s", b)
	}
}
