package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMockFixtureRefusesOutsideThrowawayDir(t *testing.T) {
	if checkoutRootErr != nil {
		t.Fatal(checkoutRootErr)
	}
	bin := buildMockFyr(t)
	fixturePath := filepath.Join(checkoutRoot, ".agents", "eval", "fixture.sh")
	base := t.TempDir()
	allowedTmp := filepath.Join(base, "allowed-tmp")
	ownTmp := filepath.Join(base, "own-tmp")
	fakeHome := filepath.Join(base, "fake-home")
	sb := filepath.Join(base, "sb-holder", "sb")
	for _, d := range []string{allowedTmp, ownTmp, fakeHome, sb} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	putFile := func(t *testing.T, p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		writeMockFile(t, p, body)
	}

	seedStandInRepo := func(t *testing.T, dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"init", "-q", "-b", "main"},
			{"config", "user.name", "precious"},
			{"config", "user.email", "p@localhost"},
			{"config", "commit.gpgsign", "false"},
		} {
			runCmd(t, dir, "git", args...)
		}
		putFile(t, filepath.Join(dir, ".gitignore"), ".agents/scratch/*\n")
		putFile(t, filepath.Join(dir, "PRECIOUS.txt"), "do not delete\n")
		putFile(t, filepath.Join(dir, ".agents", "scratch", "canary.txt"), "scratch state\n")
		runCmd(t, dir, "git", "add", ".gitignore", "PRECIOUS.txt")
		runCmd(t, dir, "git", "commit", "-qm", "precious commit")
	}

	assertPreserved := func(t *testing.T, dir string) {
		t.Helper()
		for _, rel := range []string{".git", "PRECIOUS.txt", filepath.Join(".agents", "scratch", "canary.txt")} {
			if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
				t.Fatalf("expected %s to remain intact in %s: %v", rel, dir, err)
			}
		}
	}

	runFixture := func(dir, scenario, tmpDir, sbDir, homeDir string) ([]byte, int, error) {
		cmd := exec.Command("bash", fixturePath, scenario)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"FYR="+bin,
			"SB="+sbDir,
			"TMPDIR="+tmpDir,
			"HOME="+homeDir,
		)
		out, err := cmd.CombinedOutput()
		code := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
		return out, code, err
	}

	for _, scenario := range []string{"mock-ledger-api", "mock-cloud-worker", "mock-fleet-ops", "bare"} {
		t.Run("outside-throwaway-"+scenario, func(t *testing.T) {
			standIn := filepath.Join(base, "precious-"+scenario)
			seedStandInRepo(t, standIn)
			if scenario == "bare" {
				for _, f := range []string{"fixture.sh", "fixture-extra.sh", "fixture-ast.sh", "fixture-mock.sh"} {
					b, err := os.ReadFile(filepath.Join(checkoutRoot, ".agents", "eval", f))
					if err != nil {
						t.Fatal(err)
					}
					putFile(t, filepath.Join(standIn, ".agents", "eval", f), string(b))
				}
			}
			out, code, err := runFixture(standIn, scenario, allowedTmp, sb, fakeHome)
			if err == nil || code != 2 {
				t.Fatalf("fixture.sh %s outside throwaway dir: err=%v code=%d (want exit 2)\n%s", scenario, err, code, out)
			}
			assertPreserved(t, standIn)
		})
	}

	t.Run("inside-checkout-subdir-refused", func(t *testing.T) {
		fakeCheckout := filepath.Join(allowedTmp, "fake-checkout")
		for _, f := range []string{"fixture.sh", "fixture-mock.sh"} {
			b, err := os.ReadFile(filepath.Join(checkoutRoot, ".agents", "eval", f))
			if err != nil {
				t.Fatal(err)
			}
			putFile(t, filepath.Join(fakeCheckout, ".agents", "eval", f), string(b))
		}
		subDir := filepath.Join(fakeCheckout, "sub")
		seedStandInRepo(t, subDir)
		cmd := exec.Command("bash", filepath.Join(fakeCheckout, ".agents", "eval", "fixture.sh"), "mock-ledger-api")
		cmd.Dir = subDir
		cmd.Env = append(os.Environ(), "FYR="+bin, "SB="+sb, "TMPDIR="+allowedTmp, "HOME="+fakeHome)
		out, err := cmd.CombinedOutput()
		code := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
		if err == nil || code != 2 {
			t.Fatalf("inside checkout subdir: err=%v code=%d (want exit 2)\n%s", err, code, out)
		}
		assertPreserved(t, subDir)
	})

	t.Run("checkout-markers-inside-tmpdir-refused", func(t *testing.T) {
		for name, marker := range map[string]string{
			"search-db":  filepath.Join(".agents", "scratch", "search.db"),
			"fixture-sh": filepath.Join(".agents", "eval", "fixture.sh"),
		} {
			dir := filepath.Join(allowedTmp, "has-"+name)
			seedStandInRepo(t, dir)
			putFile(t, filepath.Join(dir, marker), "sentinel\n")
			out, code, err := runFixture(dir, "mock-ledger-api", allowedTmp, sb, fakeHome)
			if err == nil || code != 2 {
				t.Fatalf("%s marker: err=%v code=%d (want exit 2)\n%s", name, err, code, out)
			}
			assertPreserved(t, dir)
		}
	})

	t.Run("fail-closed-on-bad-roots", func(t *testing.T) {
		cases := []struct {
			name                 string
			dir, tmpDir, sb, hom string
		}{
			{"missing-tmpdir", filepath.Join(allowedTmp, "c1"), filepath.Join(base, "no-such-tmp"), sb, fakeHome},
			{"missing-sb", filepath.Join(allowedTmp, "c2"), allowedTmp, filepath.Join(base, "no-such-sb"), fakeHome},
			{"root-tmpdir", filepath.Join(allowedTmp, "c3"), "/", sb, fakeHome},
			{"home-tmpdir", filepath.Join(fakeHome, "c4"), fakeHome, sb, fakeHome},
			{"tmpdir-root-itself", ownTmp, ownTmp, sb, fakeHome},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				seedStandInRepo(t, tc.dir)
				out, code, err := runFixture(tc.dir, "mock-ledger-api", tc.tmpDir, tc.sb, tc.hom)
				if err == nil || code != 2 {
					t.Fatalf("%s: err=%v code=%d (want exit 2)\n%s", tc.name, err, code, out)
				}
				assertPreserved(t, tc.dir)
			})
		}
	})

	t.Run("worktree-under-tmpdir-refused", func(t *testing.T) {
		parentRepo := filepath.Join(base, "worktree-parent")
		seedStandInRepo(t, parentRepo)
		runCmd(t, parentRepo, "git", "tag", "v0.9.0")
		runCmd(t, parentRepo, "git", "remote", "add", "origin", "https://example.invalid/parent.git")
		wtDir := filepath.Join(allowedTmp, "linked-worktree")
		runCmd(t, parentRepo, "git", "worktree", "add", "-q", "-b", "wt-branch", wtDir)
		out, code, err := runFixture(wtDir, "mock-fleet-ops", allowedTmp, sb, fakeHome)
		if err == nil || code != 2 {
			t.Fatalf("worktree under TMPDIR: err=%v code=%d (want exit 2)\n%s", err, code, out)
		}
		if got := runCmd(t, parentRepo, "git", "tag", "-l"); got == "" {
			t.Fatal("parent repo tag v0.9.0 was wiped through linked worktree")
		}
		if got := runCmd(t, parentRepo, "git", "remote"); got == "" {
			t.Fatal("parent repo remote origin was wiped through linked worktree")
		}
	})

	t.Run("enclosing-git-subdir-and-sb-nesting-refused", func(t *testing.T) {
		enclosing := filepath.Join(allowedTmp, "enclosing-repo")
		seedStandInRepo(t, enclosing)
		nested := filepath.Join(enclosing, "sub")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		putFile(t, filepath.Join(nested, "SUB.txt"), "keep\n")
		out, code, err := runFixture(nested, "mock-cloud-worker", allowedTmp, sb, fakeHome)
		if err == nil || code != 2 {
			t.Fatalf("subdir of enclosing repo under TMPDIR: err=%v code=%d (want exit 2)\n%s", err, code, out)
		}
		assertPreserved(t, enclosing)

		sbParent := filepath.Join(allowedTmp, "sb-inside-pwd")
		seedStandInRepo(t, sbParent)
		nestedSB := filepath.Join(sbParent, ".agents", "scratch", "sb")
		if err := os.MkdirAll(nestedSB, 0o755); err != nil {
			t.Fatal(err)
		}
		out, code, err = runFixture(sbParent, "mock-ledger-api", allowedTmp, nestedSB, fakeHome)
		if err == nil || code != 2 {
			t.Fatalf("SB nested inside PWD: err=%v code=%d (want exit 2)\n%s", err, code, out)
		}
		assertPreserved(t, sbParent)

		sbOuter := filepath.Join(allowedTmp, "outer-sb")
		pwdInsideSB := filepath.Join(sbOuter, "inner-proj")
		seedStandInRepo(t, pwdInsideSB)
		out, code, err = runFixture(pwdInsideSB, "mock-ledger-api", allowedTmp, sbOuter, fakeHome)
		if err == nil || code != 2 {
			t.Fatalf("PWD nested inside SB: err=%v code=%d (want exit 2)\n%s", err, code, out)
		}
		assertPreserved(t, pwdInsideSB)
	})

	t.Run("demo-app-without-own-git-inside-enclosing-repo-refused", func(t *testing.T) {
		enclosing := filepath.Join(allowedTmp, "enclosing-demo")
		seedStandInRepo(t, enclosing)
		runCmd(t, enclosing, "git", "tag", "v0.9.0")
		runCmd(t, enclosing, "git", "remote", "add", "origin", "https://example.invalid/demo-parent.git")
		appNoGit := filepath.Join(enclosing, "app")
		sibSB := filepath.Join(enclosing, "sb")
		for _, d := range []string{appNoGit, sibSB} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for _, f := range []string{"fixture.sh", "fixture-extra.sh", "fixture-ast.sh", "fixture-mock.sh"} {
			b, err := os.ReadFile(filepath.Join(checkoutRoot, ".agents", "eval", f))
			if err != nil {
				t.Fatal(err)
			}
			putFile(t, filepath.Join(appNoGit, ".agents", "eval", f), string(b))
		}
		out, code, err := runFixture(appNoGit, "bare", allowedTmp, sibSB, fakeHome)
		if err == nil || code != 2 {
			t.Fatalf("demo scenario in app/ without own .git: err=%v code=%d (want exit 2)\n%s", err, code, out)
		}
		assertPreserved(t, enclosing)
		if got := runCmd(t, enclosing, "git", "tag", "-l"); got == "" {
			t.Fatal("enclosing repo tag v0.9.0 was wiped by demo_tree/replace_history")
		}
		if got := runCmd(t, enclosing, "git", "remote"); got == "" {
			t.Fatal("enclosing repo remote origin was wiped by demo_tree/replace_history")
		}
	})

	t.Run("git-env-injection-and-valid-tmpdir-runs", func(t *testing.T) {
		standIn := filepath.Join(base, "git-env-victim")
		seedStandInRepo(t, standIn)
		runCmd(t, standIn, "git", "tag", "v0.9.0")
		runCmd(t, standIn, "git", "remote", "add", "origin", "https://example.invalid/victim.git")
		wantHead := runCmd(t, standIn, "git", "rev-parse", "HEAD")

		runWithGitEnv := func(dir, scenario, sbDir string) {
			t.Helper()
			cmd := exec.Command("bash", fixturePath, scenario)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(),
				"FYR="+bin,
				"SB="+sbDir,
				"TMPDIR="+allowedTmp,
				"HOME="+fakeHome,
				"GIT_DIR="+filepath.Join(standIn, ".git"),
				"GIT_WORK_TREE="+standIn,
				"GIT_INDEX_FILE="+filepath.Join(standIn, ".git", "index"),
				"GIT_COMMON_DIR="+filepath.Join(standIn, ".git"),
				"GIT_OBJECT_DIRECTORY="+filepath.Join(standIn, ".git", "objects"),
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("valid throwaway %s in %s failed: %v\n%s", scenario, dir, err, out)
			}
			assertPreserved(t, standIn)
			if got := runCmd(t, standIn, "git", "rev-parse", "HEAD"); got != wantHead {
				t.Fatalf("victim HEAD changed after %s: got %q, want %q", scenario, got, wantHead)
			}
			if got := runCmd(t, standIn, "git", "tag", "-l"); got == "" {
				t.Fatalf("victim tag v0.9.0 wiped after %s", scenario)
			}
			if got := runCmd(t, standIn, "git", "remote"); got == "" {
				t.Fatalf("victim remote origin wiped after %s", scenario)
			}
		}

		validNoGit := filepath.Join(allowedTmp, "valid-mock-no-git")
		if err := os.MkdirAll(validNoGit, 0o755); err != nil {
			t.Fatal(err)
		}
		runWithGitEnv(validNoGit, "mock-fleet-ops", sb)

		validHasGit := filepath.Join(allowedTmp, "valid-mock-has-git")
		seedStandInRepo(t, validHasGit)
		runWithGitEnv(validHasGit, "mock-fleet-ops", sb)

		trialRoot := filepath.Join(allowedTmp, "valid-trial")
		trialApp := filepath.Join(trialRoot, "app")
		trialSB := filepath.Join(trialRoot, "sb")
		seedStandInRepo(t, trialApp)
		if err := os.MkdirAll(trialSB, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"fixture.sh", "fixture-extra.sh", "fixture-ast.sh", "fixture-mock.sh"} {
			b, err := os.ReadFile(filepath.Join(checkoutRoot, ".agents", "eval", f))
			if err != nil {
				t.Fatal(err)
			}
			putFile(t, filepath.Join(trialApp, ".agents", "eval", f), string(b))
		}
		runWithGitEnv(trialApp, "bare", trialSB)
	})
}
