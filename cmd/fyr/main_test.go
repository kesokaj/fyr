package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
)

// allFlags is a FlagSet holding the global flags and every verb's flags, for
// the reorder tests: the value flags are read from it, as run does per verb.
func allFlags() *flag.FlagSet {
	c := &ctx{fs: flag.NewFlagSet("all", flag.ContinueOnError)}
	c.fs.StringVar(&c.repoFlag, "repo", "", "")
	c.fs.StringVar(&c.homeFlag, "home", "", "")
	for _, v := range verbs {
		if v.flags == nil {
			continue
		}
		// each verb registers only its own flags; a name two verbs share
		// (dry-run, global) would panic on the second definition
		sub := &ctx{fs: flag.NewFlagSet("verb", flag.ContinueOnError)}
		v.flags(sub)
		sub.fs.VisitAll(func(f *flag.Flag) {
			if c.fs.Lookup(f.Name) == nil {
				c.fs.Var(f.Value, f.Name, f.Usage)
			}
		})
	}
	return c.fs
}

func TestReorder(t *testing.T) {
	cases := map[string][]string{
		// BUG-002: the flag package stops at the first positional argument.
		"adopt my-skill --home /x":          {"--home", "/x", "adopt", "my-skill"},
		"adopt a --force":                   {"--force", "adopt", "a"},
		"adopt -- --looks-like-a-flag":      {"--", "adopt", "--looks-like-a-flag"}, // one marker, before every positional: the flag package stops there
		"--repo r --home h adopt x --force": {"--repo", "r", "--home", "h", "--force", "adopt", "x"},
		// A value flag reorder does not know swallows the next argument:
		// --project would take "--dry-run" as its value and the run would
		// call the model when a dry run was asked (BUG-002's shape).
		"dream --project /x --dry-run": {"--project", "/x", "--dry-run", "dream"},
		"dream --dry-run":              {"--dry-run", "dream"},
	}
	for in, want := range cases {
		if got := reorder(allFlags(), strings.Fields(in)); !reflect.DeepEqual(got, want) {
			t.Errorf("reorder(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShouldRebuild(t *testing.T) {
	cases := []struct{ goOnPath, child, own, want bool }{
		{true, false, true, true},
		{true, true, true, false},
		{false, false, true, false},
		{false, true, true, false},
		{true, false, false, false}, // a plain copy never rebuilds (BUG-015)
		{false, false, false, false},
	}
	for _, c := range cases {
		if got := shouldRebuild(c.goOnPath, c.child, c.own); got != c.want {
			t.Errorf("shouldRebuild(%v, %v, %v) = %v", c.goOnPath, c.child, c.own, got)
		}
	}
}

func TestBinaryOlder(t *testing.T) {
	cases := []struct {
		rev, commit string
		want        bool
	}{
		{"b64ffe886729", "b64ffe8", false},
		{"b64ffe8", "b64ffe886729", false},
		{"b64ffe886729+dirty", "b64ffe8", false},
		{"50ac771abcde", "b64ffe8", true},
		{"(devel)", "b64ffe8", true},
		{"unknown", "b64ffe8", true},
		{"", "b64ffe8", false},
		{"b64ffe886729", "", false},
	}
	for _, c := range cases {
		if got := binaryOlder(c.rev, c.commit); got != c.want {
			t.Errorf("binaryOlder(%q, %q) = %v", c.rev, c.commit, got)
		}
	}
}

func TestVersionFromInfo(t *testing.T) {
	stamp := func(rev, modified string) *debug.BuildInfo {
		return &debug.BuildInfo{
			Main:     debug.Module{Version: "(devel)"},
			Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: modified}},
		}
	}
	cases := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"clean", stamp("b64ffe886729abcdef0123456789abcdef012345", "false"), "b64ffe886729"},
		{"dirty", stamp("b64ffe886729abcdef0123456789abcdef012345", "true"), "b64ffe886729+dirty"},
		{"short revision kept whole", stamp("b64ffe8", "false"), "b64ffe8"},
		{"no revision", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, "v1.2.3"},
	}
	for _, c := range cases {
		if got := versionFromInfo(c.info); got != c.want {
			t.Errorf("%s: versionFromInfo = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestRunInstallStatusUninstall drives the CLI end to end against this
// checkout and a scratch home. ChildEnv is set so install takes the plain
// path: under `go test` the running binary is the test binary, and a rebuild
// here would be a real `go build` of the repo for nothing.
func TestRunInstallStatusUninstall(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	home := t.TempDir()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"install", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "files managed") {
		t.Errorf("install output:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, harness.BinPath)); err != nil {
		t.Errorf("binary not installed: %v", err)
	}
	out.Reset()
	if err := run([]string{"status", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("status: %v\n%s", err, out.String())
	}
	for _, want := range []string{"binary     ", "drift      none"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status missing %q:\n%s", want, out.String())
		}
	}
	// The binary line reads the installed file's build info: the test binary
	// was copied there, so the line must carry its version, never "unknown".
	if strings.Contains(out.String(), "binary     unknown") {
		t.Errorf("status could not read the installed binary's build info:\n%s", out.String())
	}
	out.Reset()
	if err := run([]string{"uninstall", "--home", home}, &out); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	// The vendor roots themselves are kept on purpose (pruneEmptyDirs stops
	// there); everything the manifest listed under them is gone.
	for _, rel := range []string{filepath.Join(harness.AgyRoot, "rules", "AGENTS.md"), filepath.Join(harness.AgyRoot, "skills"), filepath.Join(harness.AgyRoot, "agents"), harness.BinPath} {
		if _, err := os.Stat(filepath.Join(home, rel)); !os.IsNotExist(err) {
			t.Errorf("~/%s should be gone after uninstall, stat err = %v", rel, err)
		}
	}
	out.Reset()
	if err := run([]string{"status", "--repo", repo, "--home", home}, &out); err == nil {
		t.Error("status after uninstall should report exit 1")
	}
}

func TestKnownCheckout(t *testing.T) {
	home := t.TempDir()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FYR_REPO", "")
	if err := knownCheckout("", home, repo); err != nil {
		t.Errorf("first install (no manifest) must pass: %v", err)
	}
	if err := harness.SaveState(home, harness.State{Repo: repo}); err != nil {
		t.Fatal(err)
	}
	if err := knownCheckout("", home, repo); err != nil {
		t.Errorf("same checkout must pass: %v", err)
	}
	other := t.TempDir()
	if err := knownCheckout("", home, other); err == nil {
		t.Error("a checkout found only by the directory walk must be refused when another is installed")
	}
	if err := knownCheckout(other, home, other); err != nil {
		t.Errorf("explicit --repo must pass: %v", err)
	}
	t.Setenv("FYR_REPO", other)
	if err := knownCheckout("", home, other); err != nil {
		t.Errorf("$FYR_REPO must pass: %v", err)
	}
}

// TestDryRunNeverBuilds: with a toolchain present, no child guard and a
// checkout-shaped repo, a dry run must report the rebuild instead of
// performing it, and write nothing.
func TestDryRunNeverBuilds(t *testing.T) {
	t.Setenv(harness.ChildEnv, "")
	// A checkout-SHAPED repo of its own, not this checkout (`../..`): when
	// the test runs inside a plain copy with no .git — an unpacked bundle, or
	// once a copy vendored inside a consumer project — `../..` is not a
	// checkout, the dry run rightly prints the plain-copy line, and the
	// assertion below failed for a shape the test never meant to cover
	// (styrman-crew BUG-058). IsCheckout asks only for a `.git` entry and
	// Inspect tolerates one git cannot read, so an empty directory is enough.
	repo := checkoutShaped(t)
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"install", "--dry-run", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	for _, want := range []string{"[dry-run] the binary would be rebuilt from", "[dry-run] "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
	// Reinstall removes its build directory itself, so its absence proves
	// nothing; what a rebuild would leave is an installed binary and a
	// child's output, and a dry run must leave neither.
	for _, rel := range []string{harness.BinPath, ".gemini", ".config"} {
		if _, err := os.Stat(filepath.Join(home, rel)); !os.IsNotExist(err) {
			t.Errorf("dry run wrote ~/%s: stat err = %v", rel, err)
		}
	}
	if strings.Contains(out.String(), "building fyr from") {
		t.Errorf("dry run ran a build:\n%s", out.String())
	}
}

// checkoutShaped is plainCopy plus the one thing IsCheckout looks for: a
// `.git` entry. It is what the dry-run test needs — "this is a checkout"
// without being THIS checkout — and it is not a git repository, which
// Inspect tolerates (empty fields) and nothing on the install path needs.
func checkoutShaped(t *testing.T) string {
	t.Helper()
	dir := plainCopy(t)
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// plainCopy copies this checkout's deployable trees into a directory with
// no .git: what an unpacked bundle is (R8).
// checkoutRoot is this checkout, resolved once at package load: tests that
// move the working directory (isolate) still find it.
var checkoutRoot, checkoutRootErr = filepath.Abs("../..")

func plainCopy(t *testing.T) string {
	t.Helper()
	src, err := checkoutRoot, checkoutRootErr
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	for _, top := range []string{"context", "skills", "agents", "commands", "rules"} {
		root := filepath.Join(src, top)
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			if d.IsDir() {
				return os.MkdirAll(filepath.Join(dst, rel), 0o755)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if harness.IsCheckout(dst) {
		t.Fatal("fixture: the plain copy must not carry .git")
	}
	return dst
}

// TestPlainCopyNeverRebuilds: an unpacked bundle's binary was built from
// the same tree, the tree may be read-only to the installing user, and it
// cannot be updated with git; install from it must deploy the running
// binary without a rebuild, without the no-toolchain warning, and
// without creating a scratch directory inside the copy (BUG-015).
func TestPlainCopyNeverRebuilds(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH: the rebuild path is unreachable, nothing to prove")
	}
	t.Setenv(harness.ChildEnv, "")
	os.Unsetenv(harness.ChildEnv)
	copyDir := plainCopy(t)
	home := t.TempDir()

	var out bytes.Buffer
	if err := run([]string{"install", "--repo", copyDir, "--home", home}, &out); err != nil {
		t.Fatalf("install from a plain copy: %v\n%s", err, out.String())
	}
	for _, never := range []string{"building fyr from", "would be rebuilt", "warning: go is not on PATH"} {
		if strings.Contains(out.String(), never) {
			t.Errorf("install from a plain copy printed %q:\n%s", never, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(copyDir, ".agents")); !os.IsNotExist(err) {
		t.Errorf("install created %s inside the plain copy (err=%v)", filepath.Join(copyDir, ".agents"), err)
	}
	if _, err := os.Stat(filepath.Join(home, harness.BinPath)); err != nil {
		t.Errorf("binary not installed from the plain copy: %v", err)
	}

	out.Reset()
	if err := run([]string{"install", "--dry-run", "--repo", copyDir, "--home", home}, &out); err != nil {
		t.Fatalf("dry run from a plain copy: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "would be rebuilt") {
		t.Errorf("dry run from a plain copy announced a rebuild:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[dry-run] plain copy") {
		t.Errorf("dry run from a plain copy must say so as a dry run:\n%s", out.String())
	}
}

// TestInstallLeavesClaudeCodeAlone: the files an operator keeps under
// ~/.claude — the settings an install before ADR-062 wrote two keys into, a
// hand-written CLAUDE.md — are read by no verb: install and status leave
// their bytes as they are, write nothing beside them, and print no line about
// them (plan fyr Phase 2, D2).
func TestInstallLeavesClaudeCodeAlone(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	home := t.TempDir()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := map[string]string{
		"settings.json": `{"attribution": {"commit": ""}, "autoCompactWindow": 300000, "theme": "dark"}` + "\n",
		"CLAUDE.md":     "my own notes\n",
	}
	for name, body := range mine {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	for _, args := range [][]string{{"install"}, {"status"}} {
		if err := run(append(args, "--repo", repo, "--home", home), &out); err != nil {
			t.Fatalf("%s: %v\n%s", args[0], err, out.String())
		}
	}
	for name, body := range mine {
		if got, _ := os.ReadFile(filepath.Join(dir, name)); string(got) != body {
			t.Errorf("~/.claude/%s changed: %q", name, got)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != len(mine) {
		t.Errorf("a verb wrote under ~/.claude: %v", entries)
	}
	if s := strings.ReplaceAll(out.String(), repo, "<repo>"); strings.Contains(s, ".claude") || strings.Contains(s, "settings") { // the repo path of a worktree under .claude/ is not a line about ~/.claude
		t.Errorf("install or status printed a line about ~/.claude:\n%s", out.String())
	}
}

func gitCleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "GIT_DIR="),
			strings.HasPrefix(kv, "GIT_WORK_TREE="),
			strings.HasPrefix(kv, "GIT_INDEX_FILE="),
			strings.HasPrefix(kv, "GIT_OBJECT_DIRECTORY="),
			strings.HasPrefix(kv, "GIT_COMMON_DIR="),
			strings.HasPrefix(kv, "GIT_CONFIG"):
			continue
		}
		env = append(env, kv)
	}
	return env
}

// gitFixture builds a real checkout with an upstream: a bare origin, a clone
// with a trivial cmd/fyr that records that it ran, and a first commit
// pushed. Behind() needs a real remote, and Reinstall() needs something to
// build, so the guard under test is exercised on the path it actually
// guards. Returns the work tree and the marker path the built binary writes.
func gitFixture(t *testing.T) (repo, marker string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	repo = filepath.Join(base, "work")
	marker = filepath.Join(base, "installed.txt")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(gitCleanEnv(), "GIT_CONFIG_GLOBAL="+filepath.Join(base, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run(base, "git", "init", "--quiet", "--bare", "-b", "main", origin)
	run(base, "git", "clone", "--quiet", origin, repo)
	run(repo, "git", "config", "user.email", "fixture@example.invalid")
	run(repo, "git", "config", "user.name", "fixture")
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module fixture\n\ngo 1.24\n")
	// Stands in for the real installer: records that an install ran at all.
	write("cmd/fyr/main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.WriteFile(os.Getenv(\"FIXTURE_MARKER\"), []byte(\"installed\"), 0o644)\n}\n")
	write("context/AGENTS.md", "# AGENTS.md\n")
	run(repo, "git", "add", "-A")
	run(repo, "git", "commit", "--quiet", "-m", "fixture")
	run(repo, "git", "push", "--quiet", "-u", "origin", "HEAD:refs/heads/main")
	run(repo, "git", "branch", "--quiet", "--set-upstream-to=origin/main")
	t.Setenv("FIXTURE_MARKER", marker)
	return repo, marker
}

// advanceOrigin pushes one commit to the fixture's origin from another
// clone, so the fixture's work tree is one commit behind.
func advanceOrigin(t *testing.T, repo string) {
	t.Helper()
	other := t.TempDir()
	base := filepath.Dir(repo)
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(gitCleanEnv(), "GIT_CONFIG_GLOBAL="+filepath.Join(base, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run(other, "git", "clone", "--quiet", filepath.Join(base, "origin.git"), filepath.Join(other, "c"))
	c := filepath.Join(other, "c")
	run(c, "git", "config", "user.email", "fixture@example.invalid")
	run(c, "git", "config", "user.name", "fixture")
	if err := os.WriteFile(filepath.Join(c, "context", "AGENTS.md"), []byte("# upstream moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(c, "git", "commit", "--quiet", "-am", "upstream")
	run(c, "git", "push", "--quiet", "origin", "HEAD:refs/heads/main")
}

// `update --vendor bogus` checked the vendor only after it had fetched and
// pulled the checkout, exiting 1; on --from-zip it exited 1 and under
// --dry-run not at all (the release gate's review of v2.6.2..13c5bde,
// BUG-171). The flag is gone with the second vendor (plan fyr Phase 2), and
// any --vendor, a once-valid value too, is a usage error: exit 2 before update
// does anything, on every path.
func TestUpdateRefusesTheVendorFlagFirst(t *testing.T) {
	repo, marker := gitFixture(t)
	// run() resolves --repo as a harness checkout, which needs skills/.
	if err := os.MkdirAll(filepath.Join(repo, "skills", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills", "x", "SKILL.md"), []byte("---\nname: x\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "--quiet", "-m", "skills"}, {"push", "--quiet", "origin", "HEAD:refs/heads/main"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(gitCleanEnv(), "GIT_CONFIG_GLOBAL="+filepath.Join(filepath.Dir(repo), "gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	advanceOrigin(t, repo)
	head := func() string {
		out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	before := head()
	for name, args := range map[string][]string{
		"checkout": {"update", "--repo", repo, "--vendor", "agy"},
		"dry run":  {"update", "--dry-run", "--repo", repo, "--vendor", "agy"},
		"zip":      {"update", "--from-zip", filepath.Join(t.TempDir(), "x.zip"), "--vendor", "agy"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(append(args, "--home", t.TempDir()), &out)
			var code exitCode
			if !errors.As(err, &code) || code != 2 || !strings.Contains(out.String(), "flag provided but not defined: -vendor") {
				t.Errorf("err = %v, want exit 2 naming the flag\n%s", err, out.String())
			}
		})
	}
	if got := head(); got != before {
		t.Errorf("the checkout moved from %s to %s before the flag was refused", before, got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("an install ran")
	}
}

// TestUpdateNeverInstallsDirtyTree is BUG-046: `update` deploys the working
// tree, not the commit it fetched, so a dirty checkout must not be installed
// on EITHER path. Pull already refused it when behind; the up-to-date path
// had no guard, and an hourly timer put a session's uncommitted edits into
// the operator's real home. --check only reports, so it still works dirty.
func TestUpdateNeverInstallsDirtyTree(t *testing.T) {
	installed := func(marker string) bool {
		_, err := os.Stat(marker)
		return err == nil
	}
	dirty := func(t *testing.T, repo string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, "context", "AGENTS.md"), []byte("# edited, not committed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	behind := advanceOrigin

	t.Run("clean and up to date installs", func(t *testing.T) {
		repo, marker := gitFixture(t)
		var out bytes.Buffer
		if err := update(repo, t.TempDir(), false, "", &out); err != nil {
			t.Fatalf("clean update: %v\n%s", err, out.String())
		}
		if !installed(marker) {
			t.Error("a clean checkout should have been installed")
		}
	})

	t.Run("dirty and up to date refuses", func(t *testing.T) {
		repo, marker := gitFixture(t)
		dirty(t, repo)
		var out bytes.Buffer
		err := update(repo, t.TempDir(), false, "", &out)
		if err == nil {
			t.Fatal("a dirty checkout must not be installed")
		}
		if !strings.Contains(err.Error(), "uncommitted changes") {
			t.Errorf("error should name the uncommitted changes, got %v", err)
		}
		if installed(marker) {
			t.Error("nothing should have been installed from a dirty tree")
		}
	})

	t.Run("dirty and behind refuses without pulling", func(t *testing.T) {
		repo, marker := gitFixture(t)
		before := harness.Inspect(repo).Commit
		behind(t, repo)
		dirty(t, repo)
		var out bytes.Buffer
		err := update(repo, t.TempDir(), false, "", &out)
		if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
			t.Fatalf("dirty and behind must refuse, got %v", err)
		}
		if installed(marker) {
			t.Error("nothing should have been installed")
		}
		if after := harness.Inspect(repo).Commit; after != before {
			t.Errorf("the tree moved from %s to %s; a refused update pulls nothing", before, after)
		}
	})

	// --check and --dry-run resolve the same guard as the real run: a dirty
	// checkout is refused, exit 1, whether or not it is behind; a clean one
	// keeps the documented exit 2 when behind (audit 2026-09-30, IN4).
	t.Run("dirty and behind with --check refuses like the real run", func(t *testing.T) {
		repo, marker := gitFixture(t)
		behind(t, repo)
		dirty(t, repo)
		var out bytes.Buffer
		err := update(repo, t.TempDir(), true, "", &out)
		var code exitCode
		if err == nil || errors.As(err, &code) || !strings.Contains(err.Error(), "uncommitted changes") {
			t.Fatalf("--check on a dirty checkout must refuse with the real run's message, got %v", err)
		}
		if installed(marker) {
			t.Error("a check installed something")
		}
	})

	t.Run("clean and behind with --check exits 2", func(t *testing.T) {
		repo, _ := gitFixture(t)
		behind(t, repo)
		var out bytes.Buffer
		err := update(repo, t.TempDir(), true, "", &out)
		var code exitCode
		if !errors.As(err, &code) || code != 2 {
			t.Fatalf("--check clean and behind: %v, want exit 2", err)
		}
	})

	t.Run("clean and behind pulls then installs", func(t *testing.T) {
		repo, marker := gitFixture(t)
		before := harness.Inspect(repo).Commit
		behind(t, repo)
		var out bytes.Buffer
		if err := update(repo, t.TempDir(), false, "", &out); err != nil {
			t.Fatalf("clean and behind: %v\n%s", err, out.String())
		}
		if after := harness.Inspect(repo).Commit; after == before {
			t.Error("a clean checkout behind upstream should have been fast-forwarded")
		}
		if !installed(marker) {
			t.Error("it should have been installed after the pull")
		}
	})

	// Until 2026-09-30 --check reported "up to date" on a dirty tree and
	// exited 0, a promise the real run refuses; a read-only mode resolves
	// every guard the real run does (plan audit-fix D32, IN4).
	t.Run("--check on a dirty tree refuses like the real run and installs nothing", func(t *testing.T) {
		repo, marker := gitFixture(t)
		dirty(t, repo)
		var out bytes.Buffer
		err := update(repo, t.TempDir(), true, "", &out)
		if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
			t.Fatalf("--check on a dirty tree must refuse with the real run's message, got %v", err)
		}
		if !strings.Contains(out.String(), "up to date") {
			t.Errorf("--check should still report the state first, got %q", out.String())
		}
		if installed(marker) {
			t.Error("--check installs nothing")
		}
	})
}

// TestReadSettingsProblems covers the reader's refusals, including the open
// failure the verifier reported as untested. A problem is named without a
// byte of the file's contents.
// TestUpdateRefusesWhenStatusIsUnknown closes the coverage gap the round-2
// verifier named: `update`'s StatusUnknown branch had no cmd-level test.
// "git could not answer" must refuse, not deploy — a checkout whose state
// cannot be read is not a clean one.
func TestUpdateRefusesWhenStatusIsUnknown(t *testing.T) {
	repo, marker := gitFixture(t)
	// Break git's view of the tree while leaving the checkout recognisable:
	// the objects are gone, so `status` fails where `rev-parse` may not.
	if err := os.RemoveAll(filepath.Join(repo, ".git", "objects")); err != nil {
		t.Fatal(err)
	}
	if info := harness.Inspect(repo); !info.StatusUnknown && !info.Dirty {
		t.Skip("this git reports a readable status without its object store; the branch is covered in internal/harness")
	}
	var out bytes.Buffer
	err := update(repo, t.TempDir(), false, "", &out)
	if err == nil {
		t.Fatal("a checkout whose status git cannot report must not be installed")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("nothing should have been installed")
	}
}

// An embedded install's manifest records a release, not a path: knownCheckout
// says so and never resolves it as one (security review 2026-09-17).
func TestKnownCheckoutRefusesEmbeddedManifest(t *testing.T) {
	t.Setenv("FYR_REPO", "")
	home := t.TempDir()
	if err := harness.SaveState(home, harness.State{Repo: harness.EmbeddedPrefix + "v1.2.3"}); err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	err = knownCheckout("", home, repo)
	if err == nil || !strings.Contains(err.Error(), "installed from the release binary v1.2.3") {
		t.Errorf("knownCheckout = %v, want a refusal naming the release", err)
	}
	// Named explicitly, it is the operator's decision, as for any checkout.
	if err := knownCheckout(repo, home, repo); err != nil {
		t.Errorf("knownCheckout with --repo: %v", err)
	}
}

// TestReleaseFromInfo: a release build uses -trimpath, which keeps -ldflags
// out of the build info, so status reads an installed release binary's
// version from the module version Go stamps at a clean tag; anything else
// falls back to the revision.
func TestReleaseFromInfo(t *testing.T) {
	rev := debug.BuildSetting{Key: "vcs.revision", Value: "b64ffe886729abcdef"}
	cases := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"built at a clean tag", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}, Settings: []debug.BuildSetting{rev, {Key: "vcs.modified", Value: "false"}}}, "v1.2.3"},
		{"go install at a tag", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, "v1.2.3"},
		{"pseudo-version", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.4-0.20260917090000-b64ffe886729+dirty"}, Settings: []debug.BuildSetting{rev, {Key: "vcs.modified", Value: "true"}}}, "b64ffe886729+dirty"},
	}
	for _, c := range cases {
		if got := releaseFromInfo(c.info); got != c.want {
			t.Errorf("%s: releaseFromInfo = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestReleaseBinaryWithoutCheckout: a release binary run where no harness tree
// is found installs the files built into it, and every command follows the
// per-command table in the release-binary plan (D13, D14, D15, D17). A
// development build in the same place keeps today's error.
func TestReleaseBinaryWithoutCheckout(t *testing.T) {
	t.Setenv("FYR_REPO", "")
	t.Setenv(harness.ChildEnv, "")
	t.Chdir(t.TempDir())
	home := t.TempDir()
	var out bytes.Buffer

	if err := run([]string{"install", "--home", home}, &out); err == nil || !strings.Contains(err.Error(), "harness repo not found") {
		t.Fatalf("development build, no tree: err = %v, want harness repo not found", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini")); !os.IsNotExist(err) {
		t.Fatalf("a refused install wrote into home: %v", err)
	}

	defer func(v string) { version = v }(version)
	version = "v9.9.9"

	// A release binary takes no tree at all, so --repo is refused whatever it
	// points at — and refused, not ignored (ADR-034).
	out.Reset()
	if err := run([]string{"install", "--repo", t.TempDir(), "--home", home}, &out); err == nil || !strings.Contains(err.Error(), "never a tree on disk") {
		t.Errorf("release binary, --repo at a non-tree: err = %v, want the refusal", err)
	}
	out.Reset()
	realTree := filepath.Join(t.TempDir(), "harness")
	for rel, body := range map[string]string{
		"context/AGENTS.md": "# ctx\n",
		"skills/s/SKILL.md": "---\nname: s\ndescription: \"x\"\n---\nbody\n",
	} {
		full := filepath.Join(realTree, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := run([]string{"install", "--repo", realTree, "--home", home}, &out); err == nil || !strings.Contains(err.Error(), "never a tree on disk") {
		t.Errorf("release binary, --repo at a real tree: err = %v, want the same refusal", err)
	}

	out.Reset()
	if err := run([]string{"install", "--home", home}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "release binary v9.9.9") || strings.Contains(out.String(), "building") {
		t.Errorf("install output does not name the release binary, or built:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, harness.BinPath)); err != nil {
		t.Errorf("the running binary was not installed: %v", err)
	}
	st, found, err := harness.LoadState(home)
	if err != nil || !found || st.Repo != "embedded:v9.9.9" || st.Commit != "" {
		t.Errorf("state = %+v (found %v, err %v), want repo embedded:v9.9.9 and no commit", st, found, err)
	}

	out.Reset()
	if err := run([]string{"status", "--home", home}, &out); err != nil {
		t.Fatalf("status: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"source     embedded v9.9.9 (release binary; update downloads the newest release)",
		"from release v9.9.9",
		"binary     ",
		"drift      none",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "repo       ") {
		t.Errorf("status prints a repo line for an embedded install:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"diff", "--home", home}, &out); err != nil {
		t.Errorf("diff: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"list", "--home", home}, &out); err != nil || !strings.Contains(out.String(), "### Skills") {
		t.Errorf("list: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"render", "--home", home}, &out); err != nil || !strings.HasPrefix(out.String(), harness.Marker) {
		t.Errorf("render: %v\n%.200s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"adopt", "anything", "--home", home}, &out); err == nil || err.Error() != "adopt needs a harness checkout (`git clone git@github.com:kesokaj/fyr.git`, then run adopt from a build of it); this install is a release binary and carries no tree to adopt into" {
		t.Errorf("adopt: err = %v", err)
	}
	// `update` on a release binary goes to the release flow (Phase 4). PATH is
	// emptied for this call so the real gh is never reached: an earlier version
	// of this test called the real repository over the network (2026-09-17).
	out.Reset()
	func() {
		t.Setenv("PATH", t.TempDir())
		if err := run([]string{"update", "--home", home}, &out); err == nil || !strings.Contains(err.Error(), "install gh and run gh auth login") {
			t.Errorf("update with no gh: err = %v, want the gh refusal", err)
		}
	}()
}

// TestReleaseBinaryUpdateCheckExitsTwo: on a release binary, `update --check`
// reports a newer release and exits 2, the same code a checkout that is behind
// gives (plan § Per command with an embedded source).
func TestReleaseBinaryUpdateCheckExitsTwo(t *testing.T) {
	t.Setenv("FYR_REPO", "")
	t.Chdir(t.TempDir())
	defer func(v string) { version = v }(version)
	version = "v1.0.0"

	// A gh that answers the one call --check makes, and nothing else on PATH.
	bin := t.TempDir()
	gh := filepath.Join(bin, "gh")
	script := "#!/bin/sh" + "\n" + "printf " + "'v9.9.9" + "\\n" + "'" + "\n"
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	var out bytes.Buffer
	err := run([]string{"update", "--check", "--home", t.TempDir()}, &out)
	var code exitCode
	if !errors.As(err, &code) || int(code) != 2 {
		t.Fatalf("err = %v, want exit code 2", err)
	}
	if !strings.Contains(out.String(), "newer release v9.9.9 available") {
		t.Errorf("output: %q", out.String())
	}
}

// TestHarnessMap pins the map in README.md § How it works to the binary: the
// map is the contract for install, update and release (R9), so it must exist,
// name only verbs the binary has, and carry the verbs those procedures and the
// dream run through. A verb renamed or removed fails here, not silently in a
// picture nobody re-reads. Nothing sends experience off the machine any more
// (ADR-062), so `report` is neither a verb nor on the map.
func TestHarnessMap(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(data), "\n## How it works\n")
	if !ok {
		t.Fatal("README.md has no § How it works; the map is required by R9")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	_, block, ok := strings.Cut(section, "```mermaid\n")
	if !ok {
		t.Fatal("README.md § How it works has no mermaid map")
	}
	block, _, _ = strings.Cut(block, "```")
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`fyr ([a-z]+)`).FindAllStringSubmatch(block, -1) {
		seen[m[1]] = true
		if !strings.Contains(usage, "\n  "+m[1]+" ") {
			t.Errorf("the map names `fyr %s`, which the binary does not have", m[1])
		}
	}
	for _, verb := range []string{"install", "update", "dream"} {
		if !seen[verb] {
			t.Errorf("the map does not show `fyr %s`", verb)
		}
	}
	if seen["report"] || strings.Contains(block, "styrman-harness-feedback") {
		t.Error("the map still draws the report path back to the feedback repository")
	}
	if _, ok := verbs["report"]; ok {
		t.Error("the binary still has a `report` verb")
	}
}

// A verified release binary was installed to /usr/local/bin and then ignored,
// because two earlier PATH entries held other fyr binaries; install said it
// succeeded and status said drift none, both about a binary that was not the
// one the shell ran (BUG-078). install warns and status carries a `shadowed`
// line, and both go quiet when the earlier entry is a link to the same file.
func TestInstallAndStatusNameAPathShadow(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	installedBin := filepath.Join(home, harness.BinPath)

	// An earlier PATH entry holding a different fyr.
	earlier := t.TempDir()
	other := filepath.Join(earlier, "fyr")
	if err := os.WriteFile(other, []byte("#!/bin/sh\necho not-the-installed-one\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", earlier+string(os.PathListSeparator)+filepath.Dir(installedBin))

	var out bytes.Buffer
	if err := run([]string{"install", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	wantInstall := "warning: " + other + " comes before " + installedBin + " on PATH, so `fyr` runs that one"
	if !strings.Contains(out.String(), wantInstall) {
		t.Errorf("install missing %q:\n%s", wantInstall, out.String())
	}

	out.Reset()
	if err := run([]string{"status", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("status: %v\n%s", err, out.String())
	}
	wantStatus := "shadowed   " + other + " comes first on PATH, so `fyr` runs that, not the binary above"
	if !strings.Contains(out.String(), wantStatus) {
		t.Errorf("status missing %q:\n%s", wantStatus, out.String())
	}

	// The fix the operator applied: point the earlier entry at the installed
	// binary. It runs the same file, so neither command may still warn.
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(installedBin, other); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"status", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("status after symlink: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "shadowed") {
		t.Errorf("status still reports a shadow for a link to the installed binary:\n%s", out.String())
	}
}

// One situation, one message. When this home's bin dir is not on PATH at all,
// pathHint owns it and names what answers instead; the shadow warning stays
// quiet, so a scratch-home install — which this repo does constantly — does
// not print a warning the operator learns to skip past.
func TestOffPathInstallNamesWhatAnswersAndDoesNotWarnTwice(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	binDir := filepath.Join(home, filepath.Dir(harness.BinPath))

	elsewhere := t.TempDir()
	other := filepath.Join(elsewhere, "fyr")
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", elsewhere) // this home's bin dir is NOT on PATH

	var out bytes.Buffer
	if err := run([]string{"install", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	want := "note: " + binDir + " is not on PATH, so `fyr` runs " + other + " instead; put " + binDir + " ahead of it"
	if !strings.Contains(out.String(), want) {
		t.Errorf("install missing %q:\n%s", want, out.String())
	}
	// Not the generic "warning:" — setting PATH to one directory also drops
	// `go`, and install rightly warns about that. Only the shadow warning
	// must be absent.
	if strings.Contains(out.String(), "comes before") {
		t.Errorf("install warned twice about one situation:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"status", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("status: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "shadowed") {
		t.Errorf("status reported a shadow for a home whose bin dir is off PATH:\n%s", out.String())
	}

	// A dry run installs nothing, so it says nothing about PATH: on a fresh
	// home it would otherwise call the bin dir "not on PATH" whether it is or
	// not, a false diagnosis from a read-only mode.
	fresh := t.TempDir()
	out.Reset()
	if err := run([]string{"install", "--dry-run", "--repo", repo, "--home", fresh}, &out); err != nil {
		t.Fatalf("install --dry-run: %v\n%s", err, out.String())
	}
	// The bin-dir phrases only: PATH here is one directory, so install rightly
	// warns that `go` is not on it, and that warning is not what this pins.
	for _, claim := range []string{"add it so `fyr` is available everywhere", "so `fyr` runs", "comes before"} {
		if strings.Contains(out.String(), claim) {
			t.Errorf("install --dry-run made a PATH claim about a binary it did not write (%q):\n%s", claim, out.String())
		}
	}
}

// `update` replaces ~/.local/bin/fyr and nothing else. On devbox it was
// run by /usr/local/bin/fyr (v1.2.1), which stayed v1.2.1 while
// ~/.local/bin became v1.2.2, and bash's command hash kept running the old
// file (BUG-079). UpdateFromRelease prints the note itself when it REPLACED
// the binary (its positive case is beside it, in internal/harness). An update
// that installed nothing — up to date, --check, a fresh home — says nothing:
// there is nothing for the running file to be behind, and on the
// older-release branch the running file is the newer one (security review
// 2026-09-18, round 2).
func TestUpToDateUpdateSaysNothingAboutTheRunningBinary(t *testing.T) {
	t.Setenv("FYR_REPO", "")
	t.Chdir(t.TempDir())
	defer func(v string) { version = v }(version)
	version = "v1.0.0"

	// A gh that answers the one call the up-to-date path makes.
	ghDir := t.TempDir()
	script := "#!/bin/sh" + "\n" + "printf " + "'v1.0.0" + "\\n" + "'" + "\n"
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// A PATH directory whose fyr IS this process's executable — the
	// shape of /usr/local/bin/fyr on devbox.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(self, filepath.Join(elsewhere, "fyr")); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	installed := filepath.Join(home, harness.BinPath)
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+elsewhere)

	for _, args := range [][]string{
		{"update", "--home", home},
		{"update", "--check", "--home", home},
	} {
		var out bytes.Buffer
		if err := run(args, &out); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		if !strings.Contains(out.String(), "up to date (v1.0.0)") {
			t.Fatalf("%v: expected the up-to-date path:\n%s", args, out.String())
		}
		if strings.Contains(out.String(), "not from the installed binary") {
			t.Errorf("%v installed nothing and still described the running binary as behind:\n%s", args, out.String())
		}
	}
}

// The checkout path of update has the same shape: Reinstall writes
// ~/.local/bin/fyr, and the process that ran update — a copy at a
// system path, or a `go run` build — is not that file (BUG-079).
func TestCheckoutUpdateNamesTheBinaryItRanFrom(t *testing.T) {
	repo, _ := gitFixture(t)
	home := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	byName := filepath.Join(elsewhere, "fyr")
	if err := os.Symlink(self, byName); err != nil {
		t.Fatal(err)
	}
	// Appended, so git and go stay reachable; no earlier entry holds THIS
	// executable under the name, so the report is the appended one.
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+elsewhere)
	t.Setenv("HOME", home) // the note is for the real home only
	installed := filepath.Join(home, harness.BinPath)
	// The fixture's built binary writes a marker, not ~/.local/bin/fyr;
	// the note requires an installed file to be behind, so plant one — a
	// different file from this process, as after any real install.
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := "note: this update ran from " + byName + ", not from the installed binary " + installed

	var out bytes.Buffer
	if err := update(repo, home, true, "", &out); err != nil {
		t.Fatalf("update --check: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "not from the installed binary") {
		t.Errorf("--check, which changes nothing, described a replacement:\n%s", out.String())
	}
	out.Reset()
	if err := update(repo, home, false, "", &out); err != nil {
		t.Fatalf("update: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), want) {
		t.Errorf("checkout update did not name the binary it ran from:\nwant %q\n%s", want, out.String())
	}
}

// plainCopyOf is the unpacked-zip place: this checkout's five harness
// directories copied without .git.
func plainCopyOf(t *testing.T) string {
	t.Helper()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	for _, dir := range []string{"context", "skills", "agents", "commands", "rules"} {
		src := filepath.Join(repo, dir)
		err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(repo, path)
			target := filepath.Join(dst, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			info, _ := d.Info()
			return os.WriteFile(target, data, info.Mode().Perm())
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

// scratchClone clones this checkout into a temp dir, for a test that must
// let `install` rebuild and run a child install without touching this tree.
func scratchClone(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "clone")
	cmd := exec.Command("git", "clone", "-q", repo, dst)
	cmd.Env = append(gitCleanEnv(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	return dst
}

// The audit of 2026-09-18 (`reviews/2026-09-18-review-harness-merged.md`,
// findings 2, 8, 9): a plain copy is a real kind of place, and every refusal
// on it names the cause and the alternative.
func TestUpdateOnPlainCopyNamesTheAlternative(t *testing.T) {
	copy := plainCopyOf(t)
	home := t.TempDir()
	var out bytes.Buffer
	err := run([]string{"update", "--repo", copy, "--home", home}, &out)
	if err == nil {
		t.Fatalf("update on a plain copy succeeded:\n%s", out.String())
	}
	for _, want := range []string{"plain copy", "cannot be updated with git", "checkout", "release binary"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
}

func TestAdoptOnPlainCopySaysWhereToCommit(t *testing.T) {
	copy := plainCopyOf(t)
	home := t.TempDir()
	src := filepath.Join(t.TempDir(), "widget")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: widget\ndescription: a widget\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"adopt", src, "--repo", copy, "--home", home}, &out); err != nil {
		t.Fatalf("adopt on a plain copy: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "commit and push the repo") {
		t.Errorf("a plain copy has no repository to commit to:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "checkout") {
		t.Errorf("the next step does not name a checkout:\n%s", out.String())
	}
}

func TestAdoptOnReleaseBinaryNamesTheAlternative(t *testing.T) {
	t.Setenv("FYR_REPO", "")
	t.Chdir(t.TempDir())
	defer func(v string) { version = v }(version)
	version = "v9.9.9"
	var out bytes.Buffer
	err := run([]string{"adopt", "widget", "--home", t.TempDir()}, &out)
	if err == nil {
		t.Fatal("adopt on a release binary succeeded")
	}
	for _, want := range []string{"release binary", "checkout", "git clone"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
}

func TestStatusOnPlainCopyNamesTheRefusal(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	copy := plainCopyOf(t)
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"install", "--repo", copy, "--home", home}, &out); err != nil {
		t.Fatalf("install from a plain copy: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"status", "--repo", copy, "--home", home}, &out); err != nil {
		t.Fatalf("status: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "(plain copy, not its own git checkout; update is refused)") {
		t.Errorf("status does not say a plain copy cannot update:\n%s", out.String())
	}
}

func TestSyncIsInstall(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"sync", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("sync: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "files managed") {
		t.Errorf("sync did not install:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir, "state.json")); err != nil {
		t.Errorf("sync wrote no manifest: %v", err)
	}
}

func TestDiffVerbShowsDrift(t *testing.T) {
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
	victim := filepath.Join(home, harness.AgyRoot, "rules", "AGENTS.md")
	if err := os.WriteFile(victim, []byte("drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	_ = run([]string{"diff", "--repo", repo, "--home", home}, &out)
	if !strings.Contains(out.String(), "-drifted") || !strings.Contains(out.String(), "AGENTS.md") {
		t.Errorf("diff printed no hunk for the drifted file:\n%s", out.String())
	}
}

// BUG-080: a relative --home beside --repo resolved against the repo
// directory on a real install (Reinstall runs the build's install with
// cmd.Dir = repo) and against the invoking directory on --dry-run. --home is
// made absolute before anything runs.
func TestRelativeHomeResolvesAgainstTheInvokingDirectory(t *testing.T) {
	repo := scratchClone(t) // the rebuild's child runs with cmd.Dir = repo
	cwd := t.TempDir()
	t.Chdir(cwd)
	want := filepath.Join(cwd, "rel-home")
	var out bytes.Buffer
	if err := run([]string{"install", "--repo", repo, "--home", "rel-home"}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(want, harness.StateDir, "state.json")); err != nil {
		t.Errorf("the install did not land under the invoking directory's rel-home: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "rel-home")); err == nil {
		t.Errorf("a rel-home directory appeared inside the repository (the child install resolved it against cmd.Dir)")
	}
}

// TestVerbRefusesAnotherVerbsFlag: every verb has its own FlagSet (IN5), so a
// flag that belongs to another verb is a usage error — exit 2, named on the
// caller's writer — instead of being parsed and silently ignored.
func TestVerbRefusesAnotherVerbsFlag(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"list", "--check", "--force"},
		{"version", "--dry-run"},
		{"index", "--kind", "bug"},
		{"status", "--rebuild"},
		// The CLI refuses these before search.validate sees them, so its
		// index and --rebuild refusals were removed as unreachable (project
		// review 2026-10-09, row 23).
		{"index", "--refs", "BUG-001"},
		{"index", "--open"},
		{"search", "--rebuild", "x"},
	} {
		home := t.TempDir()
		var out bytes.Buffer
		err := run(append(args, "--repo", repo, "--home", home), &out)
		var code exitCode
		if !errors.As(err, &code) || code != 2 {
			t.Errorf("%q: err = %v, want exit 2", args, err)
		}
		if !strings.Contains(out.String(), "flag provided but not defined") {
			t.Errorf("%q: the refusal should name the flag on the writer, got:\n%s", args, out.String())
		}
	}
	// index takes no words either.
	var words bytes.Buffer
	var code exitCode
	if err := run([]string{"index", "some", "words", "--repo", repo, "--home", t.TempDir()}, &words); !errors.As(err, &code) || code != 2 {
		t.Errorf("index with words: err = %v, want exit 2\n%s", err, words.String())
	}
	// The verb's own flags still parse, in any order around positionals.
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"list", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("list with only global flags: %v\n%s", err, out.String())
	}
}

// `--` ends the flags for the flag package and for reorder alike: what follows
// is positional, so a verb-scoped flag after it is never parsed (security
// review 2026-10-01, f2), and a verb that takes no positional arguments
// refuses it instead of ignoring it.
func TestDoubleDashKeepsFollowingArgumentsPositional(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	fs.String("from-zip", "", "")
	fs.String("home", "", "") // the global flags sit on every verb's FlagSet in run()
	got := reorder(fs, []string{"--home", "h", "--", "--from-zip", "z"})
	want := []string{"--home", "h", "--", "--from-zip", "z"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("reorder dropped the -- marker: got %q", got)
	}
	// A positional before the marker: the marker still moves ahead of every
	// positional, so Args() is exactly the words (`search foo -- bar` searches
	// "foo bar", not "foo -- bar").
	got = reorder(fs, []string{"foo", "--", "bar", "--from-zip"})
	want = []string{"--", "foo", "bar", "--from-zip"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("a positional before -- left the marker in place: got %q", got)
	}
}

// A verb that takes no positional arguments refuses a stray one with a usage
// error: `list - --check` and `version extra` exit 2 instead of running with
// the argument silently ignored (security review 2026-10-01, f3).
func TestVerbRefusesStrayArguments(t *testing.T) {
	isolate(t) // no PATH, a scratch cwd: a regression here must never reach the real home or run git
	t.Setenv("HOME", t.TempDir())
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"version", "extra"},
		{"list", "-", "--check"},
		{"update", "--", "--from-zip", "nowhere.zip"},
	} {
		var out bytes.Buffer
		err := run(append([]string{args[0], "--repo", repo, "--home", t.TempDir()}, args[1:]...), &out)
		var code exitCode
		if !errors.As(err, &code) || code != 2 {
			t.Errorf("%v: want exit 2 for a stray argument, got %v (output %q)", args, err, out.String())
		}
	}
}

// `update --home -- --from-zip FILE`: the flag package takes `--` as --home's
// value, so a scan of the raw arguments that stops at `--` never sees the zip
// flag; the branch is decided from the parsed FlagSet instead, and the zip path
// refuses the missing file rather than running a normal update.
func TestUpdateFromZipDecidedByParsedFlags(t *testing.T) {
	isolate(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(harness.ChildEnv, "1")
	var out bytes.Buffer
	err := run([]string{"update", "--home", "--", "--from-zip", "nowhere.zip"}, &out)
	if err == nil {
		t.Fatalf("a missing zip was accepted (output %q)", out.String())
	}
	if !strings.Contains(err.Error(), "nowhere.zip") && !strings.Contains(out.String(), "nowhere.zip") {
		t.Fatalf("the zip branch did not run: err %v, output %q", err, out.String())
	}
}

// TestOneRepositoryName: the clone URL a reader is told to use names the
// repository a release binary updates from (harness.ReleaseRepo), never the
// name before the rename (code review of v2.7.0..881389a, 2026-10-25).
func TestOneRepositoryName(t *testing.T) {
	_, name, _ := strings.Cut(harness.ReleaseRepo, "/")
	want := "git@github.com:" + name + ".git"
	for _, rel := range []string{"README.md", "DEPLOY-remote", "cmd/fyr/verb_install.go", "internal/harness/repo.go"} {
		var data []byte
		if rel == "DEPLOY-remote" {
			// The deploy guide is a record since the trail's migration.
			data = []byte(trailRecord(t, rel))
		} else {
			var err error
			if data, err = os.ReadFile(filepath.Join("..", "..", rel)); err != nil {
				t.Fatal(err)
			}
		}
		if strings.Contains(string(data), "kesokaj/styrman-harness") {
			t.Errorf("%s names kesokaj/styrman-harness, the repository before the rename", rel)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not give the clone URL %s", rel, want)
		}
	}
}

// TestInstallSaysATurnedOffTimerStaysOff: install finds this home's dream
// unit under the old name but turned off; the dry run and the install each
// say so, the old files go, and no fyr timer is turned on (security review
// of v2.7.0..881389a, 2026-10-25; plan fyr D31).
func TestInstallSaysATurnedOffTimerStaysOff(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the systemd half; the launchd half is TestReplaceOldTimer's")
	}
	t.Setenv(harness.ChildEnv, "1")
	t.Setenv("FYR_REPO", "")
	fakeBin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\necho \"$*\" >> \"" + calls + "\"\ncase \"$*\" in *is-enabled*) echo disabled; exit 1 ;; esac\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	home := t.TempDir()
	unit, _ := dream.UnitFor(home)
	old := strings.Replace(unit, "fyr-dream", "sh-agent-dream", 1)
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{".service", ".timer"} {
		if err := os.WriteFile(filepath.Join(unitDir, old+ext), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := run([]string{"install", "--dry-run", "--repo", checkoutRoot, "--home", home}, &out); err != nil {
		t.Fatalf("install --dry-run: %v\n%s", err, out.String())
	}
	if want := "[dry-run] the dream timer " + old + " is turned off: its files would be removed and no fyr timer installed\n"; !strings.Contains(out.String(), want) {
		t.Errorf("the dry run does not say %q:\n%s", want, out.String())
	}
	out.Reset()
	if err := run([]string{"install", "--repo", checkoutRoot, "--home", home}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	if want := "removed the dream timer " + old + ", which was turned off; no fyr timer installed (fyr dream --install-timer turns one on)\n"; !strings.Contains(out.String(), want) {
		t.Errorf("install does not say %q:\n%s", want, out.String())
	}
	if left, _ := filepath.Glob(filepath.Join(unitDir, "*-dream-*")); len(left) != 0 {
		t.Errorf("unit files left or written: %v", left)
	}
	if b, _ := os.ReadFile(calls); strings.Contains(string(b), "enable --now") {
		t.Errorf("a timer was turned on:\n%s", b)
	}
}
