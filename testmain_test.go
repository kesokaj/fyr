package fyr

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain drops every GIT_* variable before the package's tests run. git
// exports GIT_DIR, GIT_INDEX_FILE and their kin inside a hook, and an
// operator may export them too; with one of them set, the fixtures' git
// calls, and the code under test that runs git in a fixture checkout, would
// act on another repository. A test that needs one sets it itself.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	// Nor the operator's git config: a global hooksPath or signing key
	// never reaches a fixture commit (the v5.0.0 gate's security review).
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Exit(m.Run())
}

// TestGitTestPackagesDropGitVariables pins the rule above for every package:
// one whose test files name the git binary (a literal "git") has a TestMain
// that drops GIT_* (internal/check had none until the v3.1.0 gate's security
// review) and sets GIT_CONFIG_GLOBAL (the v5.0.0 gate's). A package whose tests reach git only through another package's
// helper, such as trail.Command, is not seen by this pin.
func TestGitTestPackagesDropGitVariables(t *testing.T) {
	runsGit, drops := map[string]bool{}, map[string]bool{}
	for _, top := range []string{".", "cmd", "internal"} {
		err := filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && top == "." && p != "." {
				return fs.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(p, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			dir, text := filepath.Dir(p), string(data)
			if strings.Contains(text, `"git"`) {
				runsGit[dir] = true
			}
			if strings.Contains(text, "func TestMain(") && strings.Contains(text, `"GIT_"`) && strings.Contains(text, `"GIT_CONFIG_GLOBAL"`) {
				drops[dir] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(runsGit) == 0 {
		t.Fatal("found no test package that runs git: the walk is wrong")
	}
	for dir := range runsGit {
		if !drops[dir] {
			t.Errorf("%s: its tests run git, but no TestMain drops the GIT_* variables and sets GIT_CONFIG_GLOBAL", dir)
		}
	}
}
