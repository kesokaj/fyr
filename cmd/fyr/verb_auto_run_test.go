package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/auto"
	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
)

// quietEnv is a machine with nobody at it; only its process check is real, so
// a test can put a process behind it.
type quietEnv struct{}

func (quietEnv) Supplies() ([]auto.Supply, error)          { return nil, nil }
func (quietEnv) Pmset(...string) (string, error)           { return "'AC Power'", nil }
func (quietEnv) Sessions(int) ([]map[string]string, error) { return nil, nil }
func (quietEnv) PTYs(int) ([]time.Time, error)             { return nil, nil }
func (quietEnv) Ioreg(...string) (string, error)           { return "", nil }
func (quietEnv) AgySession(goos string, uid int) (bool, error) {
	return dream.AgySession(goos, uid)
}
func (quietEnv) Usage(_, _ string) (string, error)      { return "", nil }
func (quietEnv) Targets(string) ([]dream.Target, error) { return nil, nil }
func (quietEnv) LastConversation(string, string, time.Time) (time.Time, error) {
	return time.Time{}, nil
}

// TestAutoDryRunNamesG5 is E2E (a) with a process list that holds a stand-in:
// a copy of sleep called agy, which the real /proc check reads as a session.
func TestAutoDryRunNamesG5(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip(err)
	}
	data, err := os.ReadFile(sleep)
	if err != nil {
		t.Skip(err)
	}
	agy := filepath.Join(t.TempDir(), "agy")
	if err := os.WriteFile(agy, data, 0o755); err != nil {
		t.Fatal(err)
	}
	home, _ := autoHome(t)
	var out bytes.Buffer
	if err := run([]string{"auto", "on", "--home", home}, &out); err != nil {
		t.Fatalf("%v, %s", err, out.String())
	}
	autoEnv = quietEnv{}
	t.Cleanup(func() { autoEnv = nil })

	dry := func() string {
		out.Reset()
		if err := run([]string{"auto", "run", "--dry-run", "--home", home}, &out); err != nil {
			t.Fatalf("%v, %s", err, out.String())
		}
		return out.String()
	}
	proc := exec.Command(agy, "60")
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			proc.Process.Kill()
			proc.Wait()
		}
	}
	t.Cleanup(stop)
	// exec has replaced the fork's image once /proc shows the new name.
	for i := 0; i < 200 && !strings.Contains(dry(), "G5 agy session"); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	got := dry()
	if !strings.Contains(got, "G5 agy session: an interactive agy is running") {
		t.Fatalf("%s", got)
	}
	if strings.Contains(got, "would run") {
		t.Errorf("a run started past G5: %s", got)
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir, "auto")); err == nil {
		t.Error("a dry run made auto/")
	}
	stop()
}

func TestChecksInstallFindings(t *testing.T) {
	home, _ := autoHome(t)
	var out bytes.Buffer
	// A checkout with nothing installed: the timer is not there and every
	// file differs, and each comes back as a finding.
	root := repoRoot(t)
	got := autoInstallFindings(root, home)
	var rules []string
	for _, f := range got {
		rules = append(rules, f.Rule)
	}
	joined := strings.Join(rules, " ")
	if !strings.Contains(joined, "timer") || !strings.Contains(joined, "drift") {
		t.Errorf("%v", got)
	}
	if err := run([]string{"auto", "on", "--home", home}, &out); err != nil {
		t.Fatalf("%v, %s", err, out.String())
	}
	for _, f := range autoInstallFindings(root, home) {
		if f.Rule == "timer" {
			t.Errorf("the timer is installed: %v", f)
		}
	}
}

func TestChecksMetricsFindingsEmptyStore(t *testing.T) {
	home := t.TempDir()
	if got, err := autoMetricsFindings(home); len(got) != 0 || err != nil {
		t.Errorf("%v, %v", got, err)
	}
}
