package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// The binary × place × command matrix of PLAN.md item 37 (BUG-203): every
// binary kind (a release, a development build) at every kind of place (one
// installed from a checkout, one from a release binary's embedded files, one
// from a zip) running status, update and update --from-zip. borken was the
// development build at a zip place: neither status nor update could resolve a
// source, and the one verb that still ran, --from-zip, never moves a binary.

const (
	matrixRelease = "v1.2.3"
	matrixDevel   = "b9cae29346b2"
)

// matrixRig is what every cell shares: a harness checkout with an upstream,
// a zip of the harness, and a PATH holding only git, so a release update
// stops at its missing gh and a development update never builds.
type matrixRig struct {
	checkout, zip string
}

func newMatrixRig(t *testing.T) matrixRig {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("FYR_REPO", "")
	base := t.TempDir()
	cfg := filepath.Join(base, "gitconfig")
	if err := os.WriteFile(cfg, []byte("[user]\n\temail = fixture@example.invalid\n\tname = fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, kv := range gitCleanEnvStrip() {
		t.Setenv(kv, "") // restored after the test; unset for its length
		os.Unsetenv(kv)
	}
	bin := filepath.Join(base, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	// A git build that keeps its pack helpers beside the binary rather than
	// in its exec-path (this Mac's /usr/local/git) needs them on the PATH
	// too, or a local push fails "git-receive-pack: command not found".
	if real, err := filepath.EvalSymlinks(gitPath); err == nil {
		for _, helper := range []string{"git-receive-pack", "git-upload-pack"} {
			p := filepath.Join(filepath.Dir(real), helper)
			if _, err := os.Stat(p); err != nil {
				continue
			}
			if err := os.Symlink(p, filepath.Join(bin, helper)); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("PATH", bin)
	t.Chdir(t.TempDir())

	repo := plainCopy(t)
	origin := filepath.Join(base, "origin.git")
	for _, args := range [][]string{
		{"init", "--quiet", "--bare", "-b", "main", origin},
		{"-C", repo, "init", "--quiet", "-b", "main"},
		{"-C", repo, "add", "-A"},
		{"-C", repo, "commit", "--quiet", "-m", "harness"},
		{"-C", repo, "remote", "add", "origin", origin},
		{"-C", repo, "push", "--quiet", "-u", "origin", "main"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return matrixRig{checkout: repo, zip: markedZip(t)}
}

// gitCleanEnvStrip names the GIT_* variables that would point a fixture's git
// at another repository.
func gitCleanEnvStrip() []string {
	return []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR"}
}

// place makes a fresh home of the named kind, installed the way such a place
// is: a development build's install --repo, a release binary's install, a
// release binary's update --from-zip.
func (r matrixRig) place(t *testing.T, kind string) string {
	t.Helper()
	home := t.TempDir()
	args := map[string][]string{
		"checkout": {"install", "--repo", r.checkout},
		"binary":   {"install"},
		"zip":      {"update", "--from-zip", r.zip},
	}[kind]
	build := matrixRelease
	if kind == "checkout" {
		build = matrixDevel
	}
	if out, code := runAs(t, build, append(args, "--home", home)...); code != 0 {
		t.Fatalf("making the %s place: exit %d\n%s", kind, code, out)
	}
	return home
}

// runAs runs fyr as the named build and returns what a terminal shows (the
// error line main prints included) and the exit code main would return.
func runAs(t *testing.T, build string, args ...string) (string, int) {
	t.Helper()
	defer func(v string) { version = v }(version)
	version = build
	var out bytes.Buffer
	err := run(args, &out)
	var code exitCode
	switch {
	case err == nil:
		return out.String(), 0
	case errors.As(err, &code):
		return out.String(), int(code)
	}
	return out.String() + "fyr: " + err.Error() + "\n", 1
}

// matrixCell is one command at one kind of place, run by one build.
type matrixCell struct {
	build, place string
	args         []string // before --home; "ZIP" and "CHECKOUT" stand for the rig's
	exit         int
	want, not    []string // substrings of the output, "CHECKOUT" again the rig's
	origin       string   // the manifest's origin kind afterwards: "path", "release" or "zip"
}

// The cells (PLAN.md § Item 37 § Cells, D5). A release binary's cells and the
// development build's at a checkout or binary-only place keep the output
// they had before the fix (recorded from a run of the tree at 73efcfb); the
// development build's at a zip place, and its --from-zip at a checkout place,
// are the fix.
var placeCells = []matrixCell{
	{matrixRelease, "checkout", []string{"status"}, 0, []string{"source     embedded v1.2.3", "from commit ", "drift      none"}, nil, "path"},
	{matrixRelease, "checkout", []string{"update"}, 1, []string{"this install has no checkout; install gh"}, nil, "path"},
	{matrixRelease, "checkout", []string{"update", "--from-zip", "ZIP"}, 0, []string{"installing its harness and the running binary v1.2.3; nothing is downloaded and nothing in the zip replaces the binary"}, []string{"did not move"}, "zip"},
	{matrixRelease, "binary", []string{"status"}, 0, []string{"from release v1.2.3", "drift      none"}, nil, "release"},
	{matrixRelease, "binary", []string{"update"}, 1, []string{"this install has no checkout; install gh"}, nil, "release"},
	{matrixRelease, "binary", []string{"update", "--from-zip", "ZIP"}, 0, []string{"the running binary v1.2.3"}, []string{"did not move"}, "zip"},
	{matrixRelease, "zip", []string{"status"}, 0, []string{"source     embedded v1.2.3", "from zip sha256:", "drift      not compared"}, nil, "zip"},
	{matrixRelease, "zip", []string{"update"}, 1, []string{"this install has no checkout; install gh"}, nil, "zip"},
	{matrixRelease, "zip", []string{"update", "--from-zip", "ZIP"}, 0, []string{"the running binary v1.2.3"}, []string{"did not move"}, "zip"},
	{matrixRelease, "zip", []string{"install", "--repo", "CHECKOUT"}, 1, []string{"this is the release binary v1.2.3", "--repo=CHECKOUT is refused"}, nil, "zip"},

	{matrixDevel, "checkout", []string{"status"}, 0, []string{"repo       CHECKOUT (main @", "drift      none"}, nil, "path"},
	{matrixDevel, "checkout", []string{"update"}, 1, []string{"up to date with upstream", "go is not on PATH"}, nil, "path"},
	{matrixDevel, "checkout", []string{"update", "--from-zip", "ZIP"}, 1, []string{"`fyr update`", "`fyr install --repo 'CHECKOUT'`"}, []string{"installing its harness"}, "path"},
	{matrixDevel, "checkout", []string{"update", "--from-zip", "ZIP", "--dry-run"}, 1, []string{"`fyr install --repo 'CHECKOUT'`"}, []string{"installing its harness"}, "path"},
	{matrixDevel, "binary", []string{"status"}, 1, []string{"harness repo not found and this build is b9cae29346b2"}, nil, "release"},
	{matrixDevel, "binary", []string{"update"}, 1, []string{"harness repo not found and this build is b9cae29346b2"}, nil, "release"},
	{matrixDevel, "binary", []string{"update", "--from-zip", "ZIP"}, 0, []string{"the running binary b9cae29346b2", "did not move", "`fyr install --repo <checkout>`"}, nil, "zip"},
	{matrixDevel, "zip", []string{"status"}, 0, []string{"source     none", "from zip sha256:", "drift      not compared", "`fyr install --repo <checkout>`"}, []string{"harness repo not found"}, "zip"},
	{matrixDevel, "zip", []string{"update"}, 1, []string{"installed from a zip", "release binary", "--from-zip", "`fyr install --repo <checkout>`"}, []string{"harness repo not found"}, "zip"},
	{matrixDevel, "zip", []string{"update", "--check"}, 1, []string{"`fyr install --repo <checkout>`"}, []string{"harness repo not found"}, "zip"},
	{matrixDevel, "zip", []string{"update", "--dry-run"}, 1, []string{"`fyr install --repo <checkout>`"}, []string{"harness repo not found"}, "zip"},
	{matrixDevel, "zip", []string{"update", "--from-zip", "ZIP"}, 0, []string{"the running binary b9cae29346b2", "did not move", "`fyr install --repo <checkout>`"}, nil, "zip"},
	{matrixDevel, "zip", []string{"install", "--repo", "CHECKOUT"}, 0, nil, nil, "path"},
}

func TestPlaceMatrix(t *testing.T) {
	r := newMatrixRig(t)
	kinds := map[harness.OriginKind]string{harness.OriginPath: "path", harness.OriginRelease: "release", harness.OriginZip: "zip"}
	for _, c := range placeCells {
		name := c.build + "/" + c.place + "/" + strings.Join(c.args, " ")
		t.Run(name, func(t *testing.T) {
			home := r.place(t, c.place)
			sub := strings.NewReplacer("ZIP", r.zip, "CHECKOUT", r.checkout)
			var args []string
			for _, a := range c.args {
				args = append(args, sub.Replace(a))
			}
			out, code := runAs(t, c.build, append(args, "--home", home)...)
			if code != c.exit {
				t.Errorf("exit %d, want %d\n%s", code, c.exit, out)
			}
			for _, w := range c.want {
				if w = sub.Replace(w); !strings.Contains(out, w) {
					t.Errorf("output lacks %q\n%s", w, out)
				}
			}
			for _, n := range c.not {
				if strings.Contains(out, n) {
					t.Errorf("output holds %q\n%s", n, out)
				}
			}
			st, _, err := harness.LoadState(home)
			if err != nil {
				t.Fatal(err)
			}
			if got := kinds[st.Origin().Kind]; got != c.origin {
				t.Errorf("the place is a %s place afterwards (%q), want %s", got, st.Repo, c.origin)
			}
		})
	}
}

// The way back from a zip place (§ Cells' last row): a development build's
// install --repo makes it a checkout place, and status then names the
// checkout and exits 0.
func TestZipPlaceWayBack(t *testing.T) {
	r := newMatrixRig(t)
	home := r.place(t, "zip")
	if out, code := runAs(t, matrixDevel, "install", "--repo", r.checkout, "--home", home); code != 0 {
		t.Fatalf("install --repo: exit %d\n%s", code, out)
	}
	out, code := runAs(t, matrixDevel, "status", "--home", home)
	if code != 0 || !strings.Contains(out, "repo       "+r.checkout+" (main @") || strings.Contains(out, "zip") {
		t.Errorf("status after the way back: exit %d, want 0 naming the checkout and no zip\n%s", code, out)
	}
}

// A development build's status at a zip place still exits 1 on a missing
// manifest file, as a release binary's does.
func TestZipPlaceDevelStatusMissing(t *testing.T) {
	r := newMatrixRig(t)
	home := r.place(t, "zip")
	st, _, err := harness.LoadState(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, st.Files[0])); err != nil {
		t.Fatal(err)
	}
	out, code := runAs(t, matrixDevel, "status", "--home", home)
	if code != 1 || !strings.Contains(out, "missing  ~/"+st.Files[0]) {
		t.Errorf("exit %d, want 1 listing ~/%s\n%s", code, st.Files[0], out)
	}
}

// Inside a checkout a development build resolves that checkout, so status and
// diff run at a zip place; their advice must still be the way back, never
// "--from-zip again", which keeps this build and leaves the place as it is
// (BUG-203). A release binary's advice stays --from-zip: that is how its zip
// place moves.
func TestZipPlaceAdviceByBuild(t *testing.T) {
	r := newMatrixRig(t)
	home := r.place(t, "zip")
	t.Chdir(r.checkout)
	for _, verb := range []string{"status", "diff"} {
		out, _ := runAs(t, matrixDevel, verb, "--home", home)
		if !strings.Contains(out, "`fyr install --repo <checkout>`") || strings.Contains(out, "again") {
			t.Errorf("development %s: want the way back and no --from-zip again\n%s", verb, out)
		}
		out, _ = runAs(t, matrixRelease, verb, "--home", home)
		if !strings.Contains(out, "update --from-zip <file> again") {
			t.Errorf("release %s: want --from-zip again\n%s", verb, out)
		}
	}
}

// A manifest that cannot be read refuses a development build's --from-zip:
// the check failing is not evidence the place is not a checkout's, and nothing
// is written.
func TestDevelZipRefusesUnreadableManifest(t *testing.T) {
	r := newMatrixRig(t)
	home := r.place(t, "checkout")
	state := filepath.Join(home, harness.StateDir, "state.json")
	if err := os.WriteFile(state, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := runAs(t, matrixDevel, "update", "--from-zip", r.zip, "--home", home)
	if code != 1 || !strings.Contains(out, "reading the manifest before --from-zip") || strings.Contains(out, "installing its harness") {
		t.Errorf("exit %d, want 1 naming the manifest and installing nothing\n%s", code, out)
	}
	if b, _ := os.ReadFile(state); string(b) != "{not json" {
		t.Errorf("the manifest was rewritten: %q", b)
	}
}

// A home still on ~/.config/sh-agent: a real run migrates it first and then
// refuses from the moved manifest, and a dry run, which moves nothing and so
// cannot read it, refuses too (P3's reviews). Nothing is installed either way.
func TestDevelZipRefusesUnmigratedHome(t *testing.T) {
	r := newMatrixRig(t)
	for _, dry := range []bool{false, true} {
		home := r.place(t, "checkout")
		if err := os.Rename(filepath.Join(home, harness.StateDir), filepath.Join(home, harness.OldStateDir)); err != nil {
			t.Fatal(err)
		}
		args := []string{"update", "--from-zip", r.zip, "--home", home}
		want := "`fyr install --repo '" + r.checkout + "'`"
		if dry {
			args = append(args, "--dry-run")
			want = "~/.config/sh-agent"
		}
		out, code := runAs(t, matrixDevel, args...)
		if code != 1 || !strings.Contains(out, want) || strings.Contains(out, "installing its harness") {
			t.Errorf("dry=%v: exit %d, want 1 naming %s and installing nothing\n%s", dry, code, want, out)
		}
		_, curErr := os.Lstat(filepath.Join(home, harness.StateDir))
		if moved := curErr == nil; moved == dry {
			t.Errorf("dry=%v: ~/.config/fyr present = %v, want %v", dry, moved, !dry)
		}
		if !dry {
			if st, _, _ := harness.LoadState(home); st.Repo != r.checkout {
				t.Errorf("the migrated manifest records %q, want the checkout", st.Repo)
			}
		}
	}
}

// The path a refusal suggests pasting is quoted: a space or a $ in a
// manifest's path must not change the command (finding 3).
func TestDevelZipRefusalQuotesThePath(t *testing.T) {
	isolate(t)
	home := t.TempDir()
	if err := harness.SaveState(home, harness.State{Repo: "/srv/my checkout$x"}); err != nil {
		t.Fatal(err)
	}
	out, code := runAs(t, matrixDevel, "update", "--from-zip", filepath.Join(t.TempDir(), "x.zip"), "--home", home)
	if code != 1 || !strings.Contains(out, "`fyr install --repo '/srv/my checkout$x'`") {
		t.Errorf("exit %d, want 1 with the path single-quoted\n%s", code, out)
	}
}

// A path the operator named keeps its own error at a zip place: the
// zip-place answers never replace a refusal of what was typed (D15).
func TestZipPlaceNamedPathKeepsItsError(t *testing.T) {
	r := newMatrixRig(t)
	home := r.place(t, "zip")
	bogus := filepath.Join(t.TempDir(), "no-harness")
	for name, setup := range map[string]func() []string{
		"--repo":    func() []string { return []string{"--repo", bogus} },
		"$FYR_REPO": func() []string { t.Setenv("FYR_REPO", bogus); return nil },
	} {
		for _, verb := range []string{"status", "update"} {
			args := append([]string{verb, "--home", home}, setup()...)
			out, code := runAs(t, matrixDevel, args...)
			if code != 1 || !strings.Contains(out, bogus+" is not a harness checkout") || strings.Contains(out, "source     none") || strings.Contains(out, "no zip replaces") {
				t.Errorf("%s %s: exit %d, want 1 refusing the named path\n%s", name, verb, code, out)
			}
		}
		t.Setenv("FYR_REPO", "")
	}
}
