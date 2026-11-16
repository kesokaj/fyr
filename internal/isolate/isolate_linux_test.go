//go:build linux

package isolate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestLandlockUnavailableFailsClosed(t *testing.T) {
	r := newRig(t)
	b := r.prepared(r.config("no-landlock"))

	prev := testSkipLandlock
	testSkipLandlock = true
	t.Cleanup(func() { testSkipLandlock = prev })

	if err := CheckAvailable(); err == nil {
		t.Fatal("CheckAvailable succeeded with Landlock disabled")
	}
	if _, err := ResolveEvalRunner(RunnerIsolate); err == nil {
		t.Fatal("ResolveEvalRunner(isolate) succeeded with Landlock disabled")
	}
	if _, _, err := ChooseRunner(RunnerIsolate, nil); err == nil {
		t.Fatal("ChooseRunner(isolate) succeeded with Landlock disabled")
	}
	if got, _, err := ChooseRunner(RunnerAuto, nil); err == nil || got != "" {
		t.Fatalf("ChooseRunner(auto) returned (%q, %v), want fail closed", got, err)
	}
	if rc := b.Session(context.Background(), r.agy, filepath.Join(r.real, TokenRel), "p", "out.txt"); rc != -1 {
		t.Fatalf("Session with Landlock disabled rc = %d, want -1", rc)
	}
	if r.read("agy.n") != "" {
		t.Fatalf("agy executed despite Landlock disabled: agy.n=%q", r.read("agy.n"))
	}
	if !strings.Contains(r.out.String(), "isolate:") {
		t.Errorf("expected isolate error note, got:\n%s", r.out.String())
	}
}

func TestIsolateChildNotPID1(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux PID namespace test runs on Linux")
	}
	if err := CheckAvailable(); err != nil {
		t.Fatalf("Landlock unavailable on Linux test host: %v", err)
	}
	r := newRig(t)
	probe := filepath.Join(r.bin, "ppid-agy")
	put(t, probe, "#!/bin/bash\necho \"self=$$\" > \"$HOME/pids.txt\"\nbash -c 'echo \"worker_ppid=$PPID\" >> \"$HOME/pids.txt\"'\nexit 0\n", 0o755)

	c := r.config("child-not-pid1")
	c.TestRW = nil
	c.TestRO = []string{r.bin}
	b := r.prepared(c)
	tok := filepath.Join(r.real, TokenRel)
	if rc := b.Session(context.Background(), probe, tok, "p", "out.txt"); rc != 0 {
		errTxt, _ := os.ReadFile(filepath.Join(b.Dir(), "err.txt"))
		t.Fatalf("session rc %d\n%s\nerr.txt:\n%s", rc, r.out.String(), string(errTxt))
	}

	raw, err := os.ReadFile(filepath.Join(b.Home(), "pids.txt"))
	if err != nil {
		t.Fatalf("read pids.txt: %v", err)
	}
	vals := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				t.Fatalf("parse %q: %v", line, err)
			}
			vals[k] = n
		}
	}
	if vals["self"] <= 1 || vals["worker_ppid"] != vals["self"] || vals["worker_ppid"] <= 1 {
		t.Fatalf("isolated child ran as PID 1 or worker PPID mismatched: %v (want self > 1 and worker_ppid == self)\n%s", vals, string(raw))
	}
	if !b.After() {
		t.Errorf("After() = false, reasons %v\n%s", b.Reasons(), r.out.String())
	}
	b.ExitChecks()
	if !b.Intact() {
		t.Errorf("box not intact: %v", b.Reasons())
	}
}
