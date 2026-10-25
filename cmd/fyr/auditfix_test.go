package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
)

// TestListHelpExitsZero: help is a successful ask; the FlagSet prints the
// verb's flags on the writer and the verb returns no error (IN15).
func TestListHelpExitsZero(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		var out bytes.Buffer
		if err := run([]string{"list", flag, "--home", t.TempDir()}, &out); err != nil {
			t.Errorf("list %s: %v, want no error", flag, err)
		}
		if !strings.Contains(out.String(), "-home") {
			t.Errorf("list %s printed no flags:\n%s", flag, out.String())
		}
	}
}

// TestUnknownCommandExits2OnTheWriter: an unknown command is a usage error
// like every other, exit 2 with the reason and the usage on the writer (IN16).
func TestUnknownCommandExits2OnTheWriter(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"no-such-verb"}, &out)
	var code exitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(out.String(), `unknown command "no-such-verb"`) || !strings.Contains(out.String(), "Commands:") {
		t.Errorf("the reason and the usage are not on the writer:\n%s", out.String())
	}
}

// TestUninstallAndRemoveTimerRemoveTheOldNamedUnit: this home's dream timer
// under the name before the rename (sh-agent-dream, with the home's suffix)
// is removed by uninstall and by dream --remove-timer, as the fyr-named one
// is (plan 2026-10-24-fyr-agy-only.md:21; Phase 4b report item 2, TR9).
func TestUninstallAndRemoveTimerRemoveTheOldNamedUnit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the systemd half")
	}
	for _, args := range [][]string{{"uninstall"}, {"dream", "--remove-timer"}} {
		t.Run(args[0], func(t *testing.T) {
			fakeBin := t.TempDir()
			calls := filepath.Join(t.TempDir(), "calls")
			if err := os.WriteFile(filepath.Join(fakeBin, "systemctl"), []byte("#!/bin/sh\necho \"$*\" >> \""+calls+"\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
			home := t.TempDir()
			unit, _ := dream.UnitFor(home)
			old := strings.Replace(unit, "fyr-dream", "sh-agent-dream", 1)
			unitDir := filepath.Join(home, ".config", "systemd", "user")
			for _, ext := range []string{".service", ".timer"} {
				if err := os.MkdirAll(unitDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(unitDir, old+ext), []byte("x\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			if err := run(append(args, "--dry-run", "--home", home), &out); err != nil {
				t.Fatalf("%v --dry-run: %v\n%s", args, err, out.String())
			}
			if left, _ := filepath.Glob(filepath.Join(unitDir, old+".*")); len(left) != 2 {
				t.Fatalf("the dry run removed files: %v", left)
			}
			if !strings.Contains(out.String(), "would remove") {
				t.Errorf("the dry run does not say it would remove the old unit:\n%s", out.String())
			}
			out.Reset()
			if err := run(append(args, "--home", home), &out); err != nil {
				t.Fatalf("%v: %v\n%s", args, err, out.String())
			}
			if left, _ := filepath.Glob(filepath.Join(unitDir, old+".*")); len(left) != 0 {
				t.Errorf("%v left the old-named unit: %v\n%s", args, left, out.String())
			}
			if b, _ := os.ReadFile(calls); !strings.Contains(string(b), "disable --now "+old+".timer") {
				t.Errorf("%v did not stop %s:\n%s", args, old, b)
			}
			if !strings.Contains(out.String(), old) {
				t.Errorf("%v does not name %s:\n%s", args, old, out.String())
			}
		})
	}
}

// pathWithout is pathEnv without the entries that hold a file named name: a
// test whose status output must not name a leftover sh-agent stays true on a
// machine that still has one (this box's ~/.local/bin did).
func pathWithout(pathEnv, name string) string {
	var kept []string
	for _, dir := range filepath.SplitList(pathEnv) {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			continue
		}
		kept = append(kept, dir)
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

// TestStatusNamesALeftoverShAgent: after a crossing that left the old binary
// (F2), status names every sh-agent on PATH (F4 of plan
// 2026-10-24-fyr-agy-only.md:33, TR9).
func TestStatusNamesALeftoverShAgent(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"install", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	bin := t.TempDir()
	old := filepath.Join(bin, "sh-agent")
	if err := os.WriteFile(old, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// git stays reachable for the repo line; its directory must not hold an
	// sh-agent of its own, or the case without one cannot be built.
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	gitDir := filepath.Dir(git)
	if _, err := os.Stat(filepath.Join(gitDir, "sh-agent")); err == nil {
		t.Skipf("%s holds an sh-agent of its own", gitDir)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+gitDir)
	out.Reset()
	_ = run([]string{"status", "--repo", repo, "--home", home}, &out)
	if !strings.Contains(out.String(), "leftover") || !strings.Contains(out.String(), old) {
		t.Errorf("status does not name the leftover %s:\n%s", old, out.String())
	}
	t.Setenv("PATH", gitDir)
	out.Reset()
	_ = run([]string{"status", "--repo", repo, "--home", home}, &out)
	if strings.Contains(out.String(), "leftover") {
		t.Errorf("status names a leftover with none on PATH:\n%s", out.String())
	}
}
