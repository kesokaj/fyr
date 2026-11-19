package auto

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/isolate"
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
	wtTestDir := t.TempDir()
	for _, sub := range []string{"home", "tmp"} {
		if err := os.MkdirAll(filepath.Join(wtTestDir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	rec := filepath.Join(bin, "env.txt")
	t.Setenv("GIT_DIR", "/tmp/bad-git-dir")
	t.Setenv("GIT_WORK_TREE", "/tmp/bad-work-tree")
	t.Setenv("ANTIGRAVITY_SOURCE_METADATA", `{"commandLine":"GIT_DIR=/nonexistent GIT_WORK_TREE=/x go test"}`)
	o := Options{Home: wtTestDir, RealHome: wtTestDir, ReadLimit: 5 * time.Second, TestUnconfined: isolate.CheckAvailable() != nil}
	if err := runMarkedTest(context.Background(), o, "", bin, wtTestDir, "isolate", "env -0 > "+isolate.ShellQuote(rec)); err != nil {
		t.Fatalf("runMarkedTest: %v", err)
	}
	b, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	sawPrompt := false
	for _, entry := range strings.Split(string(b), "\x00") {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") || strings.HasPrefix(entry, "ANTIGRAVITY_SOURCE_METADATA=") {
			t.Errorf("runMarkedTest leaked host env to shell: %s", entry)
		}
		if entry == "GIT_TERMINAL_PROMPT=0" {
			sawPrompt = true
		}
	}
	if !sawPrompt {
		t.Errorf("expected allowlisted GIT_TERMINAL_PROMPT=0 in env:\n%s", b)
	}
}

func TestRunMarkedTestConfinedAndScrubsHostEnv(t *testing.T) {
	home := t.TempDir()
	realHome := t.TempDir()
	outDir := t.TempDir()
	envDump := filepath.Join(outDir, "env.txt")
	realEscape := filepath.Join(realHome, "escape.txt")
	escapeRC := filepath.Join(outDir, "escape.rc")

	secretKey := "FYR_TEST_" + "CRED"
	secretVal := "gh" + "p_testtoken1234567890"
	t.Setenv(secretKey, secretVal)
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(realHome, "agent.sock"))
	t.Setenv("HOME", realHome)

	probe := fmt.Sprintf(
		`printf 'wt-ok' > ./wt-probe.txt && test "$(cat ./wt-probe.txt)" = wt-ok && env -0 > %s && { (printf 'escaped' > %s) 2>/dev/null; printf '%%s' "$?" > %s; }`,
		isolate.ShellQuote(envDump), isolate.ShellQuote(realEscape), isolate.ShellQuote(escapeRC),
	)
	agents := "# AGENTS.md\n\n## Setup commands\n\n```bash\n" + probe + "   # auto: test\n```\n"
	root := applyFixture(t, home, agents)
	baseHead := git(t, root, "rev-parse", "HEAD")
	writeConfig(t, home, harnessApplyConfig)

	var out bytes.Buffer
	o := applyOpts(home, root, &out)
	o.RealHome = realHome
	o.TestRW = []string{outDir}
	o.TestUnconfined = isolate.CheckAvailable() != nil
	o.Work = fakePassWork(t, root, baseHead, "HANDOFF-2026-11-01-1156-confine", nil)
	if err := Run(o, false); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}

	rawEnv, err := os.ReadFile(envDump)
	if err != nil {
		t.Fatalf("read envDump: %v", err)
	}
	sawHome := ""
	for _, entry := range strings.Split(string(rawEnv), "\x00") {
		if k, v, ok := strings.Cut(entry, "="); ok {
			switch k {
			case secretKey:
				t.Errorf("leaked custom secret env %s to marked test", secretKey)
			case "SSH_AUTH_SOCK":
				if v != "" {
					t.Errorf("leaked SSH_AUTH_SOCK=%q to marked test", v)
				}
			case "HOME":
				sawHome = v
			}
		}
	}
	if sawHome == "" || sawHome == realHome {
		t.Errorf("HOME = %q, want non-empty scratch home != realHome (%q)", sawHome, realHome)
	}
	if isolate.CheckAvailable() == nil {
		if _, err := os.Stat(realEscape); !os.IsNotExist(err) {
			t.Errorf("confined marked test wrote inside realHome (%s): err=%v", realEscape, err)
		}
		if rc := strings.TrimSpace(readFile(t, escapeRC)); rc == "" || rc == "0" {
			t.Errorf("write inside realHome returned rc=%q, want non-zero", rc)
		}
	}
}

func TestReportBodyDistinguishesWorkerRan(t *testing.T) {
	fs := []Finding{{"worker", "retro-lite", "work/k", "PASS; branch work/k kept"}}
	workerBody := reportBodyWithApplied("proj", "", true, false, fs)
	if strings.Contains(workerBody, "from the free checks; nothing was changed") {
		t.Errorf("worker-ran report falsely claimed free-checks-only:\n%s", workerBody)
	}
	if !strings.Contains(workerBody, "worker ran; no checkout commit applied") {
		t.Errorf("worker-ran report missing worker note:\n%s", workerBody)
	}
	freeBody := reportBody("proj", fs)
	if !strings.Contains(freeBody, "from the free checks; nothing was changed") {
		t.Errorf("free-checks report changed unexpectedly:\n%s", freeBody)
	}
}
