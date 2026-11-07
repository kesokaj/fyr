package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// PrepareSelf builds ./cmd/fyr once per run from a fyr checkout and Install
// passes FYR_CHILD=1 to the built binary so N sandboxes do not run N builds
// (PLAN-2026-11-02-1806-fyr-speed Item 30).
func TestPrepareSelfBuildsOnceAndSetsChildEnv(t *testing.T) {
	r := newRig(t)
	repo := filepath.Join(r.tmp, "fyr-repo")
	gitInit(t, repo)
	put(t, filepath.Join(repo, "cmd", "fyr", "main.go"), "package main\nfunc main() {}\n", 0o644)

	buildCountFile := filepath.Join(r.rec, "go.builds")
	childEnvFile := filepath.Join(r.rec, "install.child")
	fakeBuiltFyr := "#!/bin/bash\n" +
		"echo \"FYR_CHILD=$FYR_CHILD args=$*\" >> " + ShellQuote(childEnvFile) + "\n" +
		"case \"$1\" in\n" +
		"install) mkdir -p \"$3/.local/bin\"; printf '#!/bin/bash\\necho \"fyr $*\"\\n' > \"$3/.local/bin/fyr\"; chmod +x \"$3/.local/bin/fyr\" ;;\n" +
		"esac\n"
	fakeGo := "#!/bin/bash\n" +
		"if [ \"$1\" = \"build\" ] && [ \"$2\" = \"-o\" ]; then\n" +
		"  n=$(( $(cat " + ShellQuote(buildCountFile) + " 2>/dev/null || echo 0) + 1 ))\n" +
		"  echo \"$n\" > " + ShellQuote(buildCountFile) + "\n" +
		"  cat > \"$3\" <<'EOF'\n" + fakeBuiltFyr + "EOF\n" +
		"  chmod +x \"$3\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	put(t, filepath.Join(r.bin, "go"), fakeGo, 0o755)
	t.Setenv("PATH", r.bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	testExe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	bin, cleanup := PrepareSelf(ctx, repo, testExe)
	defer cleanup()
	if bin == testExe {
		t.Fatalf("PrepareSelf returned original self %q, want built binary", bin)
	}

	for _, name := range []string{"s1", "s2", "s3"} {
		cfg := r.config(name)
		cfg.Repo = repo
		cfg.Self = bin
		b := r.box(cfg)
		if !b.Make() || !b.Clone(ctx) || !b.Install(ctx) {
			t.Fatalf("%s: %v\n%s", name, b.Reasons(), r.out.String())
		}
	}

	if got := strings.TrimSpace(r.read("go.builds")); got != "1" {
		t.Errorf("go build ran %q times across 3 sandboxes, want 1", got)
	}
	lines := strings.Split(strings.TrimSpace(r.read("install.child")), "\n")
	if len(lines) != 3 {
		t.Fatalf("install lines = %d (%q), want 3", len(lines), lines)
	}
	for i, l := range lines {
		if !strings.HasPrefix(l, "FYR_CHILD=1 ") {
			t.Errorf("sandbox %d install env %q, want FYR_CHILD=1", i+1, l)
		}
	}

	cleanup()
	if _, err := os.Stat(filepath.Dir(bin)); !os.IsNotExist(err) {
		t.Errorf("build dir %s still exists after cleanup: %v", filepath.Dir(bin), err)
	}
}

// Clone refreshes the git index right after git clone so fyr search in the
// sandbox does not see a racily clean index and re-hash every tracked file
// (PLAN-2026-11-02-1806-fyr-speed Item 30).
func TestCloneRefreshesGitIndex(t *testing.T) {
	r := newRig(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	gitLog := filepath.Join(r.rec, "git.calls")
	fakeGit := "#!/bin/bash\n" +
		"printf '%s\\n' \"$*\" >> " + ShellQuote(gitLog) + "\n" +
		"exec " + ShellQuote(realGit) + " \"$@\"\n"
	put(t, filepath.Join(r.bin, "git"), fakeGit, 0o755)
	t.Setenv("PATH", r.bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	b := r.box(r.config("idx"))
	if !b.Make() || !b.Clone(context.Background()) {
		t.Fatalf("Clone: %v\n%s", b.Reasons(), r.out.String())
	}
	calls := r.read("git.calls")
	want := "-C " + b.App() + " update-index -q --refresh"
	if !strings.Contains(calls, want) {
		t.Errorf("git calls after Clone lack %q:\n%s", want, calls)
	}
}
