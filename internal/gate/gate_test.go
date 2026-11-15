package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func addWorktreeBranch(t *testing.T, repo, branch string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), branch)
	runGit(t, repo, "worktree", "add", "-b", branch, wt, "main")
	t.Cleanup(func() {
		_, _ = gitBytes(context.Background(), repo, "worktree", "remove", "--force", wt)
		_, _ = gitBytes(context.Background(), repo, "branch", "-D", branch)
	})
	return wt
}

func hasFinding(rep GateReport, check, substr string) bool {
	for _, f := range rep.Findings {
		if f.Check == check && (substr == "" || strings.Contains(f.Detail, substr) || strings.Contains(f.Path, substr)) {
			return true
		}
	}
	return false
}

func TestGateChecksAndMustFail(t *testing.T) {
	ctx := context.Background()
	repo := initTestGoRepo(t)

	t.Run("happy worktree and lease-file", func(t *testing.T) {
		wt := addWorktreeBranch(t, repo, "feat-ok")
		writeFile(t, filepath.Join(wt, "pkg", "ok.go"), "package pkg\n\nfunc Ok() int { return 1 }\n")
		runGit(t, wt, "add", "pkg/ok.go")
		runGit(t, wt, "commit", "-m", "add pkg/ok")

		leasePath := filepath.Join(t.TempDir(), "brief.md")
		writeFile(t, leasePath, "## Task\nFiles:\n- pkg/**/*.go\n- other.go\n\n## Notes\nignore text\n")

		rep, err := Gate(ctx, GateOptions{Dir: repo, Target: wt, LeaseFile: leasePath})
		if err != nil || !rep.OK {
			t.Fatalf("Gate happy failed: err=%v rep=%+v", err, rep)
		}
		if rep.FilesChanged != 1 || rep.LinesAdded != 3 {
			t.Fatalf("unexpected numstat summary: %+v", rep)
		}
	})

	t.Run("must-fail dirty worktree", func(t *testing.T) {
		wt := addWorktreeBranch(t, repo, "feat-dirty")
		writeFile(t, filepath.Join(wt, "untracked.txt"), "dirty")
		_, err := Gate(ctx, GateOptions{Dir: repo, Target: wt})
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("expected ErrRefused for dirty worktree, got %v", err)
		}
	})

	t.Run("must-fail out of lease", func(t *testing.T) {
		wt := addWorktreeBranch(t, repo, "feat-lease")
		writeFile(t, filepath.Join(wt, "outside.go"), "package main\n")
		runGit(t, wt, "add", "outside.go")
		runGit(t, wt, "commit", "-m", "touch outside lease")

		rep, err := Gate(ctx, GateOptions{Dir: repo, Target: wt, Lease: []string{"pkg/**"}})
		if !errors.Is(err, ErrFailed) || !hasFinding(rep, "lease", "outside.go") {
			t.Fatalf("expected lease finding, err=%v findings=%+v", err, rep.Findings)
		}
	})

	t.Run("must-fail symlink mode 120000", func(t *testing.T) {
		wt := addWorktreeBranch(t, repo, "feat-symlink")
		linkPath := filepath.Join(wt, "pkg", "sym")
		_ = os.MkdirAll(filepath.Dir(linkPath), 0o755)
		if err := os.Symlink("/etc/passwd", linkPath); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		runGit(t, wt, "add", "pkg/sym")
		runGit(t, wt, "commit", "-m", "add symlink")

		rep, err := Gate(ctx, GateOptions{Dir: repo, Target: wt, Lease: []string{"pkg/**"}})
		if !errors.Is(err, ErrFailed) || !hasFinding(rep, "lease", "120000") {
			t.Fatalf("expected mode 120000 finding, err=%v findings=%+v", err, rep.Findings)
		}
	})

	t.Run("must-fail binary diff", func(t *testing.T) {
		wt := addWorktreeBranch(t, repo, "feat-bin")
		binPath := filepath.Join(wt, "pkg", "blob.bin")
		_ = os.MkdirAll(filepath.Dir(binPath), 0o755)
		if err := os.WriteFile(binPath, []byte{0, 1, 2, 0, 255}, 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, wt, "add", "pkg/blob.bin")
		runGit(t, wt, "commit", "-m", "add binary")

		rep, err := Gate(ctx, GateOptions{Dir: repo, Target: wt})
		if !errors.Is(err, ErrFailed) || !hasFinding(rep, "numstat", "binary") {
			t.Fatalf("expected binary diff finding, err=%v findings=%+v", err, rep.Findings)
		}
	})

	t.Run("must-fail max-files and max-lines", func(t *testing.T) {
		wt := addWorktreeBranch(t, repo, "feat-size")
		var lines strings.Builder
		for i := range 10 {
			fmt.Fprintf(&lines, "line %d\n", i)
		}
		writeFile(t, filepath.Join(wt, "a.txt"), lines.String())
		writeFile(t, filepath.Join(wt, "b.txt"), "1\n")
		runGit(t, wt, "add", "a.txt", "b.txt")
		runGit(t, wt, "commit", "-m", "exceed size")

		rep, err := Gate(ctx, GateOptions{Dir: repo, Target: wt, MaxFiles: 1, MaxLines: 5})
		if !errors.Is(err, ErrFailed) || !hasFinding(rep, "numstat", "files") || !hasFinding(rep, "numstat", "lines") {
			t.Fatalf("expected file and line cap findings, err=%v findings=%+v", err, rep.Findings)
		}
	})

	t.Run("must-fail secret filename", func(t *testing.T) {
		wt := addWorktreeBranch(t, repo, "feat-env")
		writeFile(t, filepath.Join(wt, ".env.local"), "PLAIN=hello\n")
		runGit(t, wt, "add", "-f", ".env.local")
		runGit(t, wt, "commit", "-m", "add env file")

		rep, err := Gate(ctx, GateOptions{Dir: repo, Target: wt})
		if !errors.Is(err, ErrFailed) || !hasFinding(rep, "lease", "secret file") {
			t.Fatalf("expected secret file finding, err=%v findings=%+v", err, rep.Findings)
		}
	})

	t.Run("must-fail added secret line without leaking secret", func(t *testing.T) {
		wt := addWorktreeBranch(t, repo, "feat-secret")
		pat := "ghp_" + "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		pem := "-----BEGIN RSA " + "PRIVATE KEY-----"
		writeFile(t, filepath.Join(wt, "cfg.txt"), pat+"\n"+pem+"\n")
		runGit(t, wt, "add", "cfg.txt")
		runGit(t, wt, "commit", "-m", "add secret")

		rep, err := Gate(ctx, GateOptions{Dir: repo, Target: wt})
		if !errors.Is(err, ErrFailed) || !hasFinding(rep, "secrets", "cfg.txt") {
			t.Fatalf("expected secrets finding, err=%v findings=%+v", err, rep.Findings)
		}
		if strings.Contains(err.Error(), pat) || strings.Contains(err.Error(), pem) {
			t.Fatalf("secret text leaked in error: %v", err)
		}
		for _, f := range rep.Findings {
			if strings.Contains(f.Detail, "ghp_") || strings.Contains(f.Detail, "PRIVATE KEY") {
				t.Fatalf("secret text leaked in finding detail: %+v", f)
			}
		}
	})

	t.Run("must-fail commit trailers", func(t *testing.T) {
		wt := addWorktreeBranch(t, repo, "feat-trailer")
		writeFile(t, filepath.Join(wt, "x.txt"), "hi\n")
		runGit(t, wt, "add", "x.txt")
		runGit(t, wt, "commit", "-m", "bad commit", "-m", "Co-Authored-By: Bot <bot@example.com>")

		rep, err := Gate(ctx, GateOptions{Dir: repo, Target: wt})
		if !errors.Is(err, ErrFailed) || !hasFinding(rep, "trailers", "Co-Authored-By") {
			t.Fatalf("expected Co-Authored-By finding, err=%v findings=%+v", err, rep.Findings)
		}

		wt2 := addWorktreeBranch(t, repo, "feat-claude")
		writeFile(t, filepath.Join(wt2, "y.txt"), "hi\n")
		runGit(t, wt2, "add", "y.txt")
		runGit(t, wt2, "commit", "-m", "bad commit", "-m", "Claude-Session: abc-123")

		rep2, err2 := Gate(ctx, GateOptions{Dir: repo, Target: wt2})
		if !errors.Is(err2, ErrFailed) || !hasFinding(rep2, "trailers", "Claude-Session") {
			t.Fatalf("expected Claude-Session finding, err=%v findings=%+v", err2, rep2.Findings)
		}
	})

	t.Run("must-fail added ++ line with secret and preserve path", func(t *testing.T) {
		wt := addWorktreeBranch(t, repo, "feat-plusplus-secret")
		pat := "ghp_" + "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		writeFile(t, filepath.Join(wt, "cfg.txt"), "++ innocent-heading\n++ "+pat+"\n")
		runGit(t, wt, "add", "cfg.txt")
		runGit(t, wt, "commit", "-m", "add plusplus secret")

		rep, err := Gate(ctx, GateOptions{Dir: repo, Target: wt})
		if !errors.Is(err, ErrFailed) {
			t.Fatalf("expected ErrFailed for ++ secret line, got err=%v findings=%+v", err, rep.Findings)
		}
		var sec *GateFinding
		for i := range rep.Findings {
			if rep.Findings[i].Check == "secrets" {
				sec = &rep.Findings[i]
				break
			}
		}
		if sec == nil || sec.Path != "cfg.txt" || sec.Line != 2 {
			t.Fatalf("expected secrets finding on cfg.txt:2 without corrupting path, got %+v", rep.Findings)
		}
	})

	t.Run("must-fail default ssh private key name while allowing .pub", func(t *testing.T) {
		wt := addWorktreeBranch(t, repo, "feat-ssh-priv")
		writeFile(t, filepath.Join(wt, "keys", "id_ed25519"), "non-pem-body\n")
		runGit(t, wt, "add", "keys/id_ed25519")
		runGit(t, wt, "commit", "-m", "add raw key name")

		rep, err := Gate(ctx, GateOptions{Dir: repo, Target: wt})
		if !errors.Is(err, ErrFailed) || !hasFinding(rep, "lease", "secret file") {
			t.Fatalf("expected secret file finding for id_ed25519, err=%v findings=%+v", err, rep.Findings)
		}

		wtPub := addWorktreeBranch(t, repo, "feat-ssh-pub")
		writeFile(t, filepath.Join(wtPub, "keys", "id_ed25519.pub"), "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest user@host\n")
		runGit(t, wtPub, "add", "keys/id_ed25519.pub")
		runGit(t, wtPub, "commit", "-m", "add public key")

		repPub, errPub := Gate(ctx, GateOptions{Dir: repo, Target: wtPub, Lease: []string{"keys/**"}})
		if errPub != nil || !repPub.OK {
			t.Fatalf("expected .pub key to pass, err=%v rep=%+v", errPub, repPub)
		}
	})

	t.Run("branch matching local directory name resolves branch not dir", func(t *testing.T) {
		writeFile(t, filepath.Join(repo, "rules", "base.md"), "base\n")
		runGit(t, repo, "add", "rules/base.md")
		runGit(t, repo, "commit", "-m", "add rules dir")

		wt := addWorktreeBranch(t, repo, "rules")
		writeFile(t, filepath.Join(wt, "rules", "added.md"), "branch rule\n")
		runGit(t, wt, "add", "rules/added.md")
		runGit(t, wt, "commit", "-m", "add branch rule")
		runGit(t, repo, "worktree", "remove", wt)

		t.Chdir(repo)
		rep, err := Gate(ctx, GateOptions{Dir: repo, Target: "rules", Lease: []string{"rules/**"}})
		if err != nil || !rep.OK || rep.Branch != "rules" || rep.FilesChanged != 1 {
			t.Fatalf("expected branch rules to gate cleanly, err=%v rep=%+v", err, rep)
		}
	})
}
