package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// outsideTemp is a temporary directory outside every git checkout, or the
// test is skipped: guard b refuses anything else by design.
func outsideTemp(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	if r, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = r
	}
	if out, err := exec.Command("git", "-C", tmp, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
		t.Skipf("the test's temporary directory is inside a git checkout (%s): guard b refuses that by design", strings.TrimSpace(string(out)))
	}
	return tmp
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

// TestGuardRefusedRoots ports the kit test of the preamble's refused roots
// (the refused-roots part of the run kit's
// TestKitPreambleKillsLeftoverProcesses, in internal/harness/catalog_test.go
// at 8374f9c): a root resolving to /, the real home or a parent of it, or the
// install source or a parent of it, is refused, and so is a relative one; a
// refused root is never created.
func TestGuardRefusedRoots(t *testing.T) {
	tmp := outsideTemp(t)
	home, src := filepath.Join(tmp, "home"), filepath.Join(tmp, "src", "harness")
	for _, d := range []string{home, src} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	homeLink := filepath.Join(tmp, "home-link")
	if err := os.Symlink(home, homeLink); err != nil {
		t.Fatal(err)
	}
	const realHome, source = "real home", "is the install source"
	for name, c := range map[string]struct{ root, want string }{
		"the real home via /.":        {home + "/.", realHome},
		"a link to the real home":     {homeLink, realHome},
		"a parent of the real home":   {tmp, realHome},
		"/":                           {"/", "resolves to /"},
		"the install source":          {src, source},
		"a parent of the install src": {filepath.Dir(src), source},
		"a relative path":             {"clone", "not an absolute path"},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkRoots(home, src, filepath.Join(tmp, "fine"), c.root)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("checkRoots(%s): want an error holding %q, got %v", c.root, c.want, err)
			}
		})
	}
	fine := filepath.Join(tmp, "sandbox", "not", "yet")
	if err := checkRoots(home, src, fine); err != nil {
		t.Errorf("a new path outside every refused root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "sandbox")); !os.IsNotExist(err) {
		t.Error("checking a root created it")
	}
	// The embedded harness has no tree: only the home and / are refused.
	if err := checkRoots(home, "", filepath.Dir(src)); err != nil {
		t.Errorf("no install source tree: %v", err)
	}
}

// TestGuardOutsideCheckout is check b (BUG-106): a directory inside any
// checkout, at any depth, is refused; one outside every checkout passes.
func TestGuardOutsideCheckout(t *testing.T) {
	tmp := outsideTemp(t)
	if err := outsideCheckout(tmp); err != nil {
		t.Errorf("outside every checkout: %v", err)
	}
	repo := filepath.Join(tmp, "repo")
	gitInit(t, repo)
	deep := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{repo, deep} {
		if err := outsideCheckout(d); err == nil || !strings.Contains(err.Error(), "inside a git checkout") {
			t.Errorf("outsideCheckout(%s) = %v, want a refusal", d, err)
		}
	}
	// An inherited GIT_DIR must not answer for the directory.
	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	if err := outsideCheckout(tmp); err != nil {
		t.Errorf("GIT_DIR in the environment made %s look like a checkout: %v", tmp, err)
	}
}

// TestGuardOutsideCheckoutFailsClosed: only git's own "not a git
// repository" is outside. A directory git cannot enter is inside, and a
// translated git message still reads as git's English.
func TestGuardOutsideCheckoutFailsClosed(t *testing.T) {
	tmp := outsideTemp(t)
	// A .git file whose gitdir is gone (a stale worktree): git says "not a
	// git repository: <path>", which is not its discovery failure.
	stale := filepath.Join(tmp, "stale")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, ".git"), []byte("gitdir: "+filepath.Join(tmp, "gone")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := outsideCheckout(stale); err == nil {
		t.Errorf("a stale worktree's .git file counts as outside every checkout")
	}
	if os.Geteuid() != 0 {
		shut := filepath.Join(tmp, "shut")
		if err := os.Mkdir(shut, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(shut, 0o755) })
		if err := outsideCheckout(shut); err == nil {
			t.Errorf("a directory git cannot enter counts as outside every checkout")
		}
	}
	probe := exec.Command("git", "-C", tmp, "rev-parse", "--git-dir")
	probe.Env = append(withoutGit(os.Environ()), "LC_ALL=en_US.UTF-8", "LANGUAGE=de")
	if out, _ := probe.CombinedOutput(); strings.Contains(string(out), "not a git repository") {
		t.Log("git prints no German here; the translated case is not exercised")
		return
	}
	t.Setenv("LC_ALL", "en_US.UTF-8")
	t.Setenv("LANGUAGE", "de")
	if err := outsideCheckout(tmp); err != nil {
		t.Errorf("under a German locale, %s outside every checkout: %v", tmp, err)
	}
}

// TestGuardPreflight: a root that is no checkout and a home inside a
// checkout (the BUG-106 message) are refused before anything is created.
func TestGuardPreflight(t *testing.T) {
	tmp := outsideTemp(t)
	project, real := filepath.Join(tmp, "project"), filepath.Join(tmp, "real")
	gitInit(t, project)
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	o := Options{Root: project, Home: filepath.Join(tmp, "home"), RealHome: real}
	if err := Preflight(o); err != nil {
		t.Errorf("a checkout and a home outside it: %v", err)
	}
	inside := o
	inside.Home = filepath.Join(project, ".agents", "scratch", "home")
	err := Preflight(inside)
	if err == nil || !strings.Contains(err.Error(), "is inside a git checkout (BUG-106); use a --home outside every checkout") {
		t.Errorf("a home inside a checkout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents")); !os.IsNotExist(err) {
		t.Error("the refused pre-flight created the home")
	}
	notRepo := o
	notRepo.Root = real
	if err := Preflight(notRepo); err == nil || !strings.Contains(err.Error(), "is not a git checkout") {
		t.Errorf("a root that is no checkout: %v", err)
	}
}

// TestGuardFingerprint is check c: a change of content, mode, name or link
// target under a watched path (the real ~/.config/fyr, ~/.config/sh-agent,
// ~/.gemini/config and the CLI's settings.json) changes the fingerprint and
// names the path; the eval/ directory, the token and the conversations do
// not count; a home with none of them is no error (BUG-158).
func TestGuardFingerprint(t *testing.T) {
	tmp := outsideTemp(t)
	real := filepath.Join(tmp, "real")
	if len(fingerprint(real)) != 0 {
		t.Error("a home with no config directory has entries")
	}
	fyr, old := filepath.Join(real, harness.StateDir), filepath.Join(real, harness.OldStateDir)
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(fyr, "brain.db"), "store")
	write(filepath.Join(fyr, "sub", "notes"), "a")
	write(filepath.Join(old, "feedback.jsonl"), "{}\n")
	cli := filepath.Join(real, ".gemini", "antigravity-cli")
	write(filepath.Join(real, ".gemini", "config", "rules", "AGENTS.md"), "rules")
	write(filepath.Join(cli, "settings.json"), `{"permissions":{}}`)
	write(filepath.Join(cli, "antigravity-oauth-token"), "token")
	write(filepath.Join(tmp, "elsewhere"), "linked")
	if err := os.Symlink(filepath.Join(tmp, "elsewhere"), filepath.Join(fyr, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tmp, filepath.Join(fyr, "dirlink")); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		change func()
		path   string
	}{
		"a line appended":                      {func() { write(filepath.Join(fyr, "brain.db"), "store\nmore") }, harness.StateDir + "/brain.db"},
		"the same size rewritten":              {func() { write(filepath.Join(fyr, "sub", "notes"), "b") }, harness.StateDir + "/sub/notes"},
		"a chmod":                              {func() { os.Chmod(filepath.Join(fyr, "brain.db"), 0o600) }, harness.StateDir + "/brain.db"},
		"a new file":                           {func() { write(filepath.Join(fyr, "metrics.db"), "") }, harness.StateDir + "/metrics.db"},
		"a removed file":                       {func() { os.Remove(filepath.Join(old, "feedback.jsonl")) }, harness.OldStateDir + "/feedback.jsonl"},
		"a linked file changed":                {func() { write(filepath.Join(tmp, "elsewhere"), "LINKED") }, harness.StateDir + "/link"},
		"a rule written into the CLI's config": {func() { write(filepath.Join(real, ".gemini", "config", "rules", "AGENTS.md"), "rules and more") }, ".gemini/config/rules/AGENTS.md"},
		"a grant written into the CLI's settings": {func() {
			write(filepath.Join(cli, "settings.json"), `{"permissions":{"allow":["command(*)"]}}`)
		}, ".gemini/antigravity-cli/settings.json"},
		"a link re-pointed": {func() {
			os.Remove(filepath.Join(fyr, "dirlink"))
			os.Symlink(real, filepath.Join(fyr, "dirlink"))
		}, harness.StateDir + "/dirlink"},
	} {
		t.Run(name, func(t *testing.T) {
			before := fingerprint(real)
			c.change()
			got := changedPaths(before, fingerprint(real))
			if len(got) != 1 || got[0] != filepath.FromSlash(c.path) {
				t.Errorf("changed paths %v, want [%s]", got, c.path)
			}
		})
	}
	before := fingerprint(real)
	write(filepath.Join(fyr, "eval", "2026-10-26-080000", "row", "home", "x"), "a run writes here")
	if got := changedPaths(before, fingerprint(real)); len(got) != 0 {
		t.Errorf("a write under eval/ counts as a real-config change: %v", got)
	}
	// The token a sandboxed session refreshes through its link, and the
	// conversations the operator's own sessions write, are not watched.
	before = fingerprint(real)
	write(filepath.Join(cli, "antigravity-oauth-token"), "refreshed")
	write(filepath.Join(cli, "conversations", "c.db"), "a session")
	if got := changedPaths(before, fingerprint(real)); len(got) != 0 {
		t.Errorf("the token or a conversation counts as a real-config change: %v", got)
	}
	// The directory itself a link (a dotfiles setup) is followed.
	moved := filepath.Join(tmp, "dotfiles-fyr")
	if err := os.Rename(fyr, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, fyr); err != nil {
		t.Fatal(err)
	}
	before = fingerprint(real)
	write(filepath.Join(moved, "brain.db"), "changed through the link")
	if got := changedPaths(before, fingerprint(real)); len(got) != 1 {
		t.Errorf("a change inside a linked config directory: %v", got)
	}
}
