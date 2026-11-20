package auto

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutoLogSubverb(t *testing.T) {
	// 1. Missing auto.log prints "no auto log yet\n" and exits 0.
	home := t.TempDir()
	var out bytes.Buffer
	if err := Log(Options{Home: home, Out: &out}, 0, false); err != nil {
		t.Fatalf("missing log err = %v, want nil", err)
	}
	if out.String() != "no auto log yet\n" {
		t.Fatalf("missing log out = %q, want %q", out.String(), "no auto log yet\n")
	}

	// 2. 70 lines: default = 50, lines = 12, all = 70.
	for i := 1; i <= 70; i++ {
		if err := appendLog(home, runAt, fmt.Sprintf("entry-%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	if err := Log(Options{Home: home, Out: &out}, 0, false); err != nil {
		t.Fatalf("default 50 lines: %v", err)
	}
	gotDefault := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(gotDefault) != 50 || !strings.HasSuffix(gotDefault[0], "entry-21") || !strings.HasSuffix(gotDefault[49], "entry-70") {
		t.Fatalf("default lines = %d (first=%q last=%q), want 50 (entry-21..entry-70)", len(gotDefault), gotDefault[0], gotDefault[len(gotDefault)-1])
	}

	out.Reset()
	if err := Log(Options{Home: home, Out: &out}, 12, false); err != nil {
		t.Fatalf("--lines 12: %v", err)
	}
	got12 := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(got12) != 12 || !strings.HasSuffix(got12[0], "entry-59") || !strings.HasSuffix(got12[11], "entry-70") {
		t.Fatalf("lines=12 count = %d, want 12 (entry-59..entry-70)", len(got12))
	}

	out.Reset()
	if err := Log(Options{Home: home, Out: &out}, 0, true); err != nil {
		t.Fatalf("--all: %v", err)
	}
	gotAll := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(gotAll) != 70 || !strings.HasSuffix(gotAll[0], "entry-01") || !strings.HasSuffix(gotAll[69], "entry-70") {
		t.Fatalf("all=true count = %d, want 70", len(gotAll))
	}

	// 3. Invalid flag combinations exit 2.
	if err := Log(Options{Home: home, Out: &out}, -1, false); exit(err) != 2 {
		t.Errorf("negative lines exit = %d, want 2", exit(err))
	}
	if err := Log(Options{Home: home, Out: &out}, 10, true); exit(err) != 2 {
		t.Errorf("lines+all exit = %d, want 2", exit(err))
	}

	// 4. Symlinked auto.log is refused via O_NOFOLLOW (exit 1).
	symHome := t.TempDir()
	if err := ensureDir(symHome); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(target, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir(symHome), "auto.log")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Log(Options{Home: symHome, Out: &out}, 0, false); exit(err) != 1 || strings.Contains(out.String(), "secret") {
		t.Fatalf("symlinked auto.log: exit=%d out=%q, want exit 1", exit(err), out.String())
	}
}

func TestAutoDailyReportAndDryRun(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	hooksPath := filepath.Join(home, ".gemini", "config", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o700); err != nil {
		t.Fatal(err)
	}
	const origHooks = `{"hooks":{"SessionStart":[]}}`
	if err := os.WriteFile(hooksPath, []byte(origHooks), 0o600); err != nil {
		t.Fatal(err)
	}

	reportCalls := 0
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.Report = func() error {
		reportCalls++
		return nil
	}

	// Dry-run never invokes Report.
	if err := Run(o, true); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if reportCalls != 0 {
		t.Fatalf("dry-run invoked Report %d times, want 0", reportCalls)
	}

	// Real sweep invokes Report and leaves hooks.json untouched.
	out.Reset()
	if err := Run(o, false); err != nil {
		t.Fatalf("real run: %v", err)
	}
	if reportCalls != 1 {
		t.Fatalf("real run invoked Report %d times, want 1", reportCalls)
	}
	gotHooks, err := os.ReadFile(hooksPath)
	if err != nil || string(gotHooks) != origHooks {
		t.Fatalf("hooks.json changed: err=%v body=%q", err, gotHooks)
	}
}
