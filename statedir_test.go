package fyr_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
)

// The state directory has one home in the code, harness.StateDir: the rename
// touched every path into it, and one literal missed would split a machine's
// state across two directories without a word (plan fyr D22). A string
// literal naming the directory, old name or new, anywhere in the Go tree but
// the lines of StateDir and OldStateDir fails here, tests included.
func TestStateDirHasOneHome(t *testing.T) {
	if harness.StateDir != ".config/fyr" {
		t.Errorf("StateDir = %q, want .config/fyr", harness.StateDir)
	}
	home := t.TempDir()
	if got, want := metrics.Path(home), filepath.Join(home, ".config", "fyr", "metrics.db"); got != want {
		t.Errorf("metrics.Path = %s, want %s", got, want)
	}
	dir, err := harness.ConfigDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "fyr"); dir != want {
		t.Errorf("ConfigDir = %s, want %s", dir, want)
	}

	q := `"`
	var needles []string
	for _, name := range []string{"sh-" + "agent", "fy" + "r"} {
		needles = append(needles, q+".config"+q+", "+q+name+q, q+".config/"+name)
	}
	self := "statedir_test.go"
	err = filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (p == ".agents" || p == "skills" || p == ".git") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || p == self {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			// The two constants' own lines; OldStateDir is the name the
			// migration moves from (plan fyr D13).
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "StateDir = ") || strings.HasPrefix(trimmed, "const StateDir = ") || strings.HasPrefix(trimmed, "const OldStateDir = ") {
				continue
			}
			for _, n := range needles {
				if strings.Contains(line, n) {
					t.Errorf("%s:%d names the state directory as a literal (%s); use harness.StateDir", p, i+1, n)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
