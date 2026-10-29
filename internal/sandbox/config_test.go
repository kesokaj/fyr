package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// TestGuardFingerprintSkipStores is D45 of PLAN-2026-10-29-workers: under
// SkipStores a write to the session-written stores at the top of the state
// directory changes nothing, every other watched path still does, and
// without it (eval's check c) a store write is still seen.
func TestGuardFingerprintSkipStores(t *testing.T) {
	real := filepath.Join(outsideTemp(t), "real")
	write := func(rel string) {
		t.Helper()
		p := filepath.Join(real, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString("x")
		f.Close()
	}
	state := harness.StateDir + "/"
	for _, rel := range []string{"brain.db", "metrics.db", "search.db", "state.json", "sub/brain.db"} {
		write(state + rel)
	}
	write(".gemini/config/rules/x.md")
	for _, c := range []struct {
		rel     string
		skip    bool
		changes bool
	}{
		{state + "brain.db", true, false},
		{state + "brain.db-wal", true, false},
		{state + "brain.db-shm", true, false},
		{state + "metrics.db", true, false},
		{state + "metrics.db-wal", true, false},
		{state + "search.db", true, false},
		{state + "search.db-shm", true, false},
		{state + "state.json", true, false},
		{state + "sub/brain.db", true, true},
		{state + "brain.db.bak", true, true},
		{state + "other.json", true, true},
		{".gemini/config/rules/x.md", true, true},
		{state + "brain.db", false, true},
		{state + "state.json", false, true},
		{state + "metrics.db-wal", false, true},
	} {
		before := fingerprint(real, c.skip)
		write(c.rel)
		if got := changedPaths(before, fingerprint(real, c.skip)); (len(got) > 0) != c.changes {
			t.Errorf("skip %v, a write at %s: changed paths %v, want a change %v", c.skip, c.rel, got, c.changes)
		}
	}
}

// TestBoxSkipStoresExitChecks is the same through the Box: a session that
// writes the real brain.db fails real config changed without SkipStores and
// passes with it.
func TestBoxSkipStoresExitChecks(t *testing.T) {
	for _, skip := range []bool{false, true} {
		r := newRig(t)
		put(t, filepath.Join(r.real, harness.StateDir, "brain.db"), "store", 0o600)
		c := r.config("stores")
		c.SkipStores = skip
		b := r.prepared(c)
		put(t, filepath.Join(r.real, harness.StateDir, "brain.db"), "written by a session", 0o600)
		b.ExitChecks()
		if got := b.Has(ReasonRealConfig); got == skip {
			t.Errorf("SkipStores %v: real config changed %v, reasons %v", skip, got, b.Reasons())
		}
	}
}

// TestBoxSessionModel: a Model passes --model to agy, and none passes no
// --model (eval's arguments unchanged).
func TestBoxSessionModel(t *testing.T) {
	for _, model := range []string{"", "gemini-x"} {
		r := newRig(t)
		c := r.config("model")
		c.Model = model
		b := r.prepared(c)
		if rc := b.Session(context.Background(), r.agy, filepath.Join(r.real, TokenRel), "hi", "out.txt"); rc != 0 {
			t.Fatalf("rc %d\n%s", rc, r.out.String())
		}
		args := strings.Split(strings.TrimSpace(r.read("agy.args.1")), "\n")
		want := []string{"-p", "The project root is the current directory, " + b.App() + "; ignore any other checkout. hi", "--mode", "accept-edits", "--print-timeout", "20m"}
		if model != "" {
			want = append(want, "--model", model)
		}
		if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("model %q: agy args %q, want %q", model, args, want)
		}
	}
}

// TestBoxRemove: an intact sandbox is removed whole and nothing outside it;
// one whose clone the session turned into a link is kept, and so is the
// link's target.
func TestBoxRemove(t *testing.T) {
	r := newRig(t)
	b := r.prepared(r.config("gone"))
	// A Go module cache the session filled is read-only.
	ro := filepath.Join(b.Home(), "go", "pkg", "mod", "m@v1")
	put(t, filepath.Join(ro, "x.go"), "package m", 0o444)
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	b.ExitChecks()
	if !b.Remove() {
		t.Fatalf("Remove refused an intact sandbox: %s", r.out.String())
	}
	if _, err := os.Lstat(b.Dir()); !os.IsNotExist(err) {
		t.Errorf("the sandbox is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.src, "README.md")); err != nil {
		t.Errorf("the source went with it: %v", err)
	}

	b = r.prepared(r.config("swapped"))
	if err := os.Rename(b.App(), b.App()+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(r.src, b.App()); err != nil {
		t.Fatal(err)
	}
	b.ExitChecks()
	if b.Remove() {
		t.Error("Remove removed a sandbox whose clone is a link")
	}
	if _, err := os.Stat(filepath.Join(r.src, "README.md")); err != nil {
		t.Errorf("the link's target was removed: %v", err)
	}
	if _, err := os.Lstat(b.Dir()); err != nil {
		t.Errorf("the replaced sandbox was not kept: %v", err)
	}
}
