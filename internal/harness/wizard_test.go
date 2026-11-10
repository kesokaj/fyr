package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWizardWriteEnvPermissionsAndSymlink pins skills/bash/assets/wizard-template.sh
// write_env: a pre-existing 0644 .env file must be atomically replaced at mode
// 0600 (never left world-readable when secrets are written), symlinked ENV_FILE
// targets must be refused, and active temp files must be cleaned up on exit.
func TestWizardWriteEnvPermissionsAndSymlink(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "skills", "bash", "assets", "wizard-template.sh"))
	if err != nil {
		t.Fatal(err)
	}
	lib, _, ok := strings.Cut(string(raw), "# STAGES:")
	if !ok {
		t.Fatal("wizard-template.sh missing '# STAGES:' marker")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "lib.sh")
	if err := os.WriteFile(script, []byte(lib), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Pre-existing 0644 .env is tightened to 0600 and preserves unrelated keys.
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("KEEP='yes'\nOAUTH_CLIENT_SECRET='old'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(envFile, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", `source "$1"; ENV_FILE="$2"; write_env OAUTH_CLIENT_SECRET "s3cr'et"`, "_", script, envFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write_env on 0644 .env failed: %v\n%s", err, out)
	}
	fi, err := os.Lstat(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf(".env mode after write_env = %04o, want 0600", got)
	}
	gotBody, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotBody), "KEEP='yes'\n") || !strings.Contains(string(gotBody), `OAUTH_CLIENT_SECRET='s3cr'\''et'`) {
		t.Errorf("unexpected .env content:\n%s", gotBody)
	}
	matches, _ := filepath.Glob(envFile + ".tmp.*")
	if len(matches) != 0 {
		t.Errorf("leftover temp files: %v", matches)
	}

	// 2. Symlinked ENV_FILE is refused without modifying the target.
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("untouched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkEnv := filepath.Join(dir, "link.env")
	if err := os.Symlink(target, linkEnv); err != nil {
		t.Fatal(err)
	}
	cmdLink := exec.Command("bash", "-c", `source "$1"; ENV_FILE="$2"; write_env OAUTH_CLIENT_SECRET "leaked"`, "_", script, linkEnv)
	if out, err := cmdLink.CombinedOutput(); err == nil {
		t.Fatalf("write_env on symlinked ENV_FILE succeeded, want refusal:\n%s", out)
	}
	targetBody, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(targetBody) != "untouched\n" {
		t.Errorf("symlink target modified: %q", targetBody)
	}

	// 3. Exit trap removes active _WIZARD_TMP if interrupted mid-run.
	staleTmp := filepath.Join(dir, ".env.tmp.interrupt")
	if err := os.WriteFile(staleTmp, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmdTrap := exec.Command("bash", "-c", `source "$1"; _WIZARD_TMP="$2"; exit 1`, "_", script, staleTmp)
	_ = cmdTrap.Run()
	if _, err := os.Lstat(staleTmp); !os.IsNotExist(err) {
		t.Errorf("expected EXIT trap to remove %s, Lstat err = %v", staleTmp, err)
	}
}
