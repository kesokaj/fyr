package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
)

// linkedHome makes a home beside the rig's real one whose rel is a link to
// target, and returns it.
func linkedHome(r *evalRig, rel, target string) string {
	r.t.Helper()
	h := filepath.Join(filepath.Dir(r.real), "linked-home")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(h, rel)), 0o700); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(h, rel)); err != nil {
		r.t.Fatal(err)
	}
	return h
}

// TestEvalRefusesItsHome: a missing --home, the real home, and a home that
// reaches the real state through a link at any level (.config, .config/fyr,
// the old .config/sh-agent, a store file) or through a dangling link are each
// refused with exit 2 before any row runs, and nothing is written under the
// real home — not even the state-dir migration, which the planted
// ~/.config/sh-agent would show (ledger D37 and its security review).
func TestEvalRefusesItsHome(t *testing.T) {
	for name, tc := range map[string]struct {
		home func(r *evalRig) []string // the --home arguments
		want string
	}{
		// As the installed binary: a development build stops at the
		// real-home guard first (BUG-507, TestRealHomeGuardRefusesDevBuild).
		"missing":       {func(r *evalRig) []string { asInstalled(r.t, r.real); return nil }, "eval: --home is required"},
		"the real home": {func(r *evalRig) []string { return []string{"--home", r.real} }, "is the real home"},
		"the real home, spelled with ..": {func(r *evalRig) []string {
			return []string{"--home", filepath.Join(r.real, "..", filepath.Base(r.real)) + "/"}
		}, "is the real home"},
		".config linked": {func(r *evalRig) []string {
			return []string{"--home", linkedHome(r, ".config", filepath.Join(r.real, ".config"))}
		}, "shares its state directory"},
		"the state directory linked": {func(r *evalRig) []string {
			if err := os.MkdirAll(filepath.Join(r.real, harness.StateDir), 0o700); err != nil {
				r.t.Fatal(err)
			}
			return []string{"--home", linkedHome(r, harness.StateDir, filepath.Join(r.real, harness.StateDir))}
		}, "shares its state directory"},
		"the state directory linked to the old one": {func(r *evalRig) []string {
			return []string{"--home", linkedHome(r, harness.StateDir, filepath.Join(r.real, harness.OldStateDir))}
		}, "shares its state directory"},
		"a store linked": {func(r *evalRig) []string {
			real := filepath.Join(r.real, harness.StateDir, "brain.db")
			if err := os.MkdirAll(filepath.Dir(real), 0o700); err != nil {
				r.t.Fatal(err)
			}
			if err := os.WriteFile(real, nil, 0o600); err != nil {
				r.t.Fatal(err)
			}
			return []string{"--home", linkedHome(r, filepath.Join(harness.StateDir, "brain.db"), real)}
		}, "shares its state directory"},
		"a store hard-linked": {func(r *evalRig) []string {
			real := filepath.Join(r.real, harness.StateDir, "metrics.db")
			if err := os.MkdirAll(filepath.Dir(real), 0o700); err != nil {
				r.t.Fatal(err)
			}
			if err := os.WriteFile(real, nil, 0o600); err != nil {
				r.t.Fatal(err)
			}
			h := filepath.Join(filepath.Dir(r.real), "hard-home")
			if err := os.MkdirAll(filepath.Join(h, harness.StateDir), 0o700); err != nil {
				r.t.Fatal(err)
			}
			if err := os.Link(real, filepath.Join(h, harness.StateDir, "metrics.db")); err != nil {
				r.t.Skipf("no hard link here: %v", err)
			}
			return []string{"--home", h}
		}, "shares its state directory"},
		"the registration key hard-linked": {func(r *evalRig) []string {
			real := filepath.Join(r.real, harness.StateDir, "registration.key")
			if err := os.MkdirAll(filepath.Dir(real), 0o700); err != nil {
				r.t.Fatal(err)
			}
			if err := os.WriteFile(real, []byte("k"), 0o600); err != nil {
				r.t.Fatal(err)
			}
			h := filepath.Join(filepath.Dir(r.real), "key-home")
			if err := os.MkdirAll(filepath.Join(h, harness.StateDir), 0o700); err != nil {
				r.t.Fatal(err)
			}
			if err := os.Link(real, filepath.Join(h, harness.StateDir, "registration.key")); err != nil {
				r.t.Skipf("no hard link here: %v", err)
			}
			return []string{"--home", h}
		}, "shares its state directory"},
		"a dangling link": {func(r *evalRig) []string {
			return []string{"--home", linkedHome(r, ".config", filepath.Join(r.real, "not-yet"))}
		}, "shares its state directory"},
		"the account's home, HOME elsewhere": {func(r *evalRig) []string {
			old := accountHomeDir
			accountHomeDir = func() (string, error) { return r.real, nil }
			r.t.Cleanup(func() { accountHomeDir = old })
			r.t.Setenv("HOME", r.t.TempDir())
			return []string{"--home", r.real}
		}, "is the real home"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newEvalRig(t, evalSuite, true)
			old := filepath.Join(r.real, harness.OldStateDir)
			if err := os.MkdirAll(old, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(old, "state.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			args := tc.home(r)
			before := treeOf(t, r.real)
			var out bytes.Buffer
			err := run(append(append([]string{"eval", "--repo", r.harness}, args...), "ok"), &out)
			var code exitCode
			if !errors.As(err, &code) || code != 2 || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("err %v, want exit 2 naming %q:\n%s", err, tc.want, out.String())
			}
			if after := treeOf(t, r.real); after != before {
				t.Errorf("the real home changed:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if n, err := os.ReadFile(filepath.Join(r.rec, "agy.n")); err == nil {
				t.Errorf("agy ran %s times", n)
			}
		})
	}
}

// The migration case above shows only while the real state directory is
// absent; this proves the planted old directory would be migrated by a verb
// that writes, so the snapshot can see it.
func TestEvalHomeSnapshotSeesAMigration(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	old := filepath.Join(r.real, harness.OldStateDir)
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	before := treeOf(t, r.real)
	if err := harness.MigrateStateDir(r.real, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if treeOf(t, r.real) == before {
		t.Fatal("a migration left the snapshot unchanged")
	}
	// A rewrite in place, same size, is seen too: by its modification time.
	f := filepath.Join(r.real, harness.StateDir, "x")
	if err := os.WriteFile(f, []byte("aaaa"), 0o600); err != nil {
		t.Fatal(err)
	}
	before = treeOf(t, r.real)
	if err := os.WriteFile(f, []byte("bbbb"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(f, later, later); err != nil {
		t.Fatal(err)
	}
	if treeOf(t, r.real) == before {
		t.Fatal("a same-size rewrite left the snapshot unchanged")
	}
}

// --list needs no --home: it reads the export and writes nothing. As the
// installed binary: a development build is refused by the real-home guard
// (BUG-507, TestRealHomeGuardRefusesReads).
func TestEvalListNeedsNoHome(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	asInstalled(t, r.real)
	before := treeOf(t, r.real)
	var out bytes.Buffer
	if err := run([]string{"eval", "--repo", r.harness, "--list"}, &out); err != nil || !strings.HasPrefix(out.String(), "ok  -  named  v1  capability\n") {
		t.Fatalf("err %v:\n%s", err, out.String())
	}
	if treeOf(t, r.real) != before {
		t.Error("--list wrote under the real home")
	}
}

// A home that comes to share the real state after the start check — a row's
// agent linked it — is neither ingested nor recorded into (D37's security
// review): ingestRow refuses before any store opens, and recordEval writes no
// record.
func TestEvalRechecksItsHomeBeforeWriting(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	home := linkedHome(r, ".config", filepath.Join(r.real, ".config"))
	if err := os.MkdirAll(filepath.Join(r.real, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	before := treeOf(t, r.real)
	// A sandbox the ingest would read, so a check moved after it would open
	// the real metrics store through the link.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "home", ".gemini", "antigravity-cli", "conversations"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ingestRow(home, eval.Result{Row: "ok", Status: eval.StatusPass, Dir: dir}); err == nil || !strings.Contains(err.Error(), "not ingested") {
		t.Errorf("ingestRow: %v, want the refusal", err)
	}
	trial := metrics.Trial{Run: "s/ok/1", Label: "eval=ok", Harness: "k", Status: "pass", StartedAt: time.Now()}
	if err := recordTrial(home, trial); err == nil || !strings.Contains(err.Error(), "not recorded") {
		t.Errorf("recordTrial: %v, want the refusal", err)
	}
	if _, err := os.Stat(metrics.Path(r.real)); !os.IsNotExist(err) {
		t.Errorf("the real metrics store was opened: %v", err)
	}
	var out bytes.Buffer
	c := &ctx{home: home, homeFlag: home, out: &out}
	err := recordEval(c, r.project, eval.Options{}, harness.Source{}, "k", nil, nil)
	var code exitCode
	if !errors.As(err, &code) || code != 1 || !strings.Contains(out.String(), "eval: record: not written") {
		t.Errorf("recordEval: %v, want exit 1 and no record:\n%s", err, out.String())
	}
	if after := treeOf(t, r.real); after != before {
		t.Errorf("the real home changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// treeOf lists every path under dir with its type, size and modification
// time, so a test sees a file created, removed, moved or rewritten there.
func treeOf(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		fmt.Fprintf(&b, "%s %s %d %d\n", rel, d.Type(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
