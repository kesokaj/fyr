package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlanAntigravityOnly: every planned path is under the Antigravity CLI's
// directory or is the binary, and the manifest records the one vendor that a
// reader of state.json (the init-project skill) checks for.
func TestPlanAntigravityOnly(t *testing.T) {
	repo := newRepo(t)
	files, err := Plan(DirSource(repo), []byte("#!/bin/sh\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !strings.HasPrefix(f.Rel, AgyRoot+string(filepath.Separator)) && f.Rel != BinPath {
			t.Errorf("planned %s, outside ~/%s", f.Rel, AgyRoot)
		}
	}
	var hooksData []byte
	for _, f := range files {
		if f.Rel == filepath.Join(AgyRoot, "hooks.json") {
			hooksData = f.Data
		}
	}
	if len(hooksData) == 0 {
		t.Fatalf("expected %s/hooks.json to be planned", AgyRoot)
	}
	for _, want := range []string{
		`"fyr-brain"`, `"SessionStart"`, `"PreInvocation"`, `"PreToolUse"`, `"Stop"`,
		`\"$HOME/bin/fyr\" search --hook session-start`,
		`\"$HOME/.local/bin/fyr\" search --hook session-start`,
		`\"$HOME/bin/fyr\" search --hook pre-invocation`,
		`\"$HOME/.local/bin/fyr\" search --hook pre-invocation`,
		`\"$HOME/bin/fyr\" search --hook pre-tool-use`,
		`\"$HOME/bin/fyr\" search --hook stop`,
		"read_file|view_file|view_file_outline|edit_file|replace_file_content|multi_replace_file_content|write_file|write_to_file|run_command|invoke_subagent|swarm|run_workflow",
	} {
		if !strings.Contains(string(hooksData), want) {
			t.Errorf("hooks.json missing %q:\n%s", want, string(hooksData))
		}
	}
	home := t.TempDir()
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Errorf("an install into an empty home made ~/.claude: %v", err)
	}
	st, found, err := LoadState(home)
	if err != nil || !found || st.Vendor != VendorAntigravity {
		t.Errorf("state = %+v, %v, %v; want vendor %q", st, found, err, VendorAntigravity)
	}
}

// TestPlanRefusesSecretShapedFiles: a credential that slipped past
// .gitignore into a skill directory is invisible to review (git never shows
// it) and install is the one step that would copy it into both vendor
// directories on every machine (security review 2026-09-16).
func TestPlanRefusesSecretShapedFiles(t *testing.T) {
	for _, name := range []string{".env", ".env.local", "server.key", "cert.pem", "creds.tfvars", "application_default_credentials.json", "sa-key.json"} {
		t.Run(name, func(t *testing.T) {
			repo := newRepo(t)
			if err := os.WriteFile(filepath.Join(repo, "skills", "alpha", name), []byte("secret\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Plan(DirSource(repo), nil); err == nil {
				t.Fatalf("%s must not be deployed", name)
			} else if !strings.Contains(err.Error(), name) {
				t.Errorf("the error should name the file, got %v", err)
			}
		})
	}
	t.Run("an ordinary reference file still plans", func(t *testing.T) {
		repo := newRepo(t)
		if err := os.WriteFile(filepath.Join(repo, "skills", "alpha", "keys-explained.md"), []byte("about keys\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		files, err := Plan(DirSource(repo), nil)
		if err != nil {
			t.Fatalf("a normal file must still plan: %v", err)
		}
		var found bool
		for _, f := range files {
			if strings.HasSuffix(f.Rel, "keys-explained.md") {
				found = true
			}
		}
		if !found {
			t.Error("the reference file should have been planned")
		}
	})
}

// TestPlanDropsGroupAndOtherWrite: a skill file that is group-writable in
// the repo must not land group-writable in ~/.gemini/config, where its contents are
// instructions every session reads (security review of v1.0.0).
func TestPlanDropsGroupAndOtherWrite(t *testing.T) {
	repo := newRepo(t)
	loose := filepath.Join(repo, "skills", "alpha", "loose.md")
	if err := os.WriteFile(loose, []byte("reference\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o666); err != nil {
		t.Fatal(err)
	}
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, f := range files {
		if f.Mode&0o022 != 0 {
			t.Errorf("%s would be deployed mode %o — group or other writable", f.Rel, f.Mode)
		}
		if strings.HasSuffix(f.Rel, "loose.md") {
			seen++
			if f.Mode.Perm() != 0o644 {
				t.Errorf("loose.md mode = %o, want 644", f.Mode.Perm())
			}
		}
	}
	if seen == 0 {
		t.Error("the loose file should still be planned")
	}
}

func TestPlanInstallsWorkflowScripts(t *testing.T) {
	repo := newRepo(t)
	wfDir := filepath.Join(repo, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wantBody := "\"\"\"Parallel review workflow.\"\"\"\n\nprint('ok')\n"
	if err := os.WriteFile(filepath.Join(wfDir, "review_parallel.py"), []byte(wantBody), 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantRel := filepath.Join(AgyRoot, "workflows", "review_parallel.py")
	var found bool
	for _, f := range files {
		if f.Rel != wantRel {
			continue
		}
		found = true
		if string(f.Data) != wantBody {
			t.Errorf("planned bytes = %q, want %q", string(f.Data), wantBody)
		}
		if f.Mode.Perm() != 0o644 {
			t.Errorf("workflow script mode = %o, want 644", f.Mode.Perm())
		}
	}
	if !found {
		t.Fatalf("missing planned file %s", wantRel)
	}

	t.Run("rejects bad workflow script name", func(t *testing.T) {
		badRepo := newRepo(t)
		badDir := filepath.Join(badRepo, "workflows")
		if err := os.MkdirAll(badDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(badDir, "-bad.py"), []byte("# x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Plan(DirSource(badRepo), nil); err == nil {
			t.Fatal("expected error for invalid workflow script name -bad.py")
		}
	})

	t.Run("rejects secret-shaped python filename", func(t *testing.T) {
		secRepo := newRepo(t)
		secDir := filepath.Join(secRepo, "workflows")
		if err := os.MkdirAll(secDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(secDir, "oauth-token.py"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Plan(DirSource(secRepo), nil); err == nil {
			t.Fatal("expected error for secret-shaped workflow script name")
		}
	})
}
