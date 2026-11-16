//go:build linux

package isolate

import (
	"context"
	"path/filepath"
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
