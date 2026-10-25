package harness

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ResolveSource locates the harness: an explicit path, then $FYR_REPO,
// then the working directory and its parents, then the path the last install
// recorded — so `fyr update` works from anywhere once installed — and
// last, in a release binary only, the files built into it (D14). A path the
// operator named is used or refused, never silently replaced by one the walk
// finds or by the embedded files (D15): install builds and runs the
// checkout's code, so a typo must not hand that to whatever harness-shaped
// tree the shell happens to be in. version is the running binary's.
func ResolveSource(explicit, home, version string) (Source, error) {
	// A RELEASE binary installs the harness built into it — the published
	// repository at the tagged commit, checked against its SHA256SUMS on the way in — and
	// `update` replaces the whole binary to change them. The one tree it
	// installs is a zip the operator names with `update --from-zip`, which
	// never passes through here (ADR-038). Otherwise no tree on this machine
	// takes part: not the working
	// directory, not the one the last install recorded, not one named on the
	// command line. A recorded plain copy silently winning is what stranded a
	// box on months-old content while reporting no drift (ADR-034, the
	// operator: "the binary should only use the published git repo in github
	// always and only"). A named path is REFUSED rather than ignored, because
	// silently doing something other than what the operator typed is the
	// habit D15 exists to prevent.
	if IsReleaseVersion(version) {
		for _, from := range []struct{ origin, path string }{
			{"--repo", explicit},
			{"$FYR_REPO", os.Getenv("FYR_REPO")},
		} {
			if from.path != "" {
				// Name WHERE the path came from: an exported $FYR_REPO in
				// a shell profile makes every command on the machine fail, and
				// "drop it" is unactionable without knowing which one to drop
				// (BUG-076).
				return Source{}, fmt.Errorf("this is the release binary %s: it installs the harness built into it and never a tree on disk, so %s=%s is refused — unset it, or build fyr from that tree and run that build's install", version, from.origin, from.path)
			}
		}
		return embeddedSource(version), nil
	}

	// A development build has nothing of its own to install, so it looks for a
	// tree: an explicit path, then $FYR_REPO, then the working directory
	// and its parents, then the path the last install recorded. A path the
	// operator named is used or refused, never silently replaced by one the
	// walk finds (D15): install builds and runs the checkout's code, so a typo
	// must not hand that to whatever harness-shaped tree the shell is in.
	for _, named := range []string{explicit, os.Getenv("FYR_REPO")} {
		if named == "" {
			continue
		}
		if !isRepo(named) {
			return Source{}, fmt.Errorf("%s is not a harness checkout (no context/AGENTS.md and skills/)", named)
		}
		return absSource(named)
	}
	var candidates []string
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			candidates = append(candidates, dir)
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	// An embedded install records a release and a zip install an archive's
	// hash, never a path (EmbeddedPrefix, ZipPrefix): neither is a candidate.
	zipNote := ""
	if st, found, err := LoadState(home); err == nil && found {
		switch o := st.Origin(); o.Kind {
		case OriginZip:
			zipNote = fmt.Sprintf("this home was installed from a zip (sha256 %s); a development build needs a checkout or --repo — or run `fyr update --from-zip <file>` again. ", o.ShortSum())
		case OriginPath:
			candidates = append(candidates, o.Path)
		}
	}
	for _, c := range candidates {
		if c != "" && isRepo(c) {
			return absSource(c)
		}
	}
	built := "this build carries no version stamp, so it has no harness built in"
	if version != "" && version != "unknown" {
		built = fmt.Sprintf("this build is %s, a development build rather than a vX.Y.Z release, so it has no harness built in", version)
	}
	return Source{}, fmt.Errorf("%sharness repo not found and %s: run from inside the checkout, pass --repo, set FYR_REPO, or install from a release binary (README § Install without cloning)", zipNote, built)
}

// OriginKind classifies what a manifest's repo value names.
type OriginKind int

const (
	// OriginPath is a tree on disk: a checkout or a plain copy.
	OriginPath OriginKind = iota
	// OriginRelease is the harness built into a release binary ("embedded:vX.Y.Z").
	OriginRelease
	// OriginZip is a repository zip the operator passed to update --from-zip
	// ("zip:sha256:<sum>;<trust>"), whose unpacked tree is gone (ADR-038).
	OriginZip
)

// Origin is a manifest's repo value, parsed once for every reader (IN6).
type Origin struct {
	Kind    OriginKind
	Path    string // OriginPath: the tree
	Release string // OriginRelease: the release tag
	Sum     string // OriginZip: the archive's sha256
	Trust   string // OriginZip: "unverified", "verified vX.Y.Z", or "" for a manifest from before the verdict was recorded
}

// ParseOrigin reads a manifest repo value.
func ParseOrigin(repo string) Origin {
	if release, ok := strings.CutPrefix(repo, EmbeddedPrefix); ok {
		return Origin{Kind: OriginRelease, Release: release}
	}
	if rest, ok := strings.CutPrefix(repo, ZipPrefix+"sha256:"); ok {
		sum, trust, _ := strings.Cut(rest, ";")
		return Origin{Kind: OriginZip, Sum: sum, Trust: trust}
	}
	return Origin{Kind: OriginPath, Path: repo}
}

// Origin is the parsed form of the manifest's repo value.
func (s State) Origin() Origin { return ParseOrigin(s.Repo) }

// ShortSum is the first 12 hex digits of a zip origin's sha256, enough to
// tell zips apart in a message.
func (o Origin) ShortSum() string {
	if len(o.Sum) > 12 {
		return o.Sum[:12]
	}
	return o.Sum
}

// OriginString is what the manifest records as this source: the explicit
// Origin when set (a zip's archive), the release for the embedded files, the
// tree's path otherwise.
func (s Source) OriginString() string {
	switch {
	case s.Origin != "":
		return s.Origin
	case s.Dir == "":
		return EmbeddedPrefix + s.Version
	}
	return s.Dir
}

// ZipOrigin is the manifest value for a zip install: the archive's sha256 and
// the verdict, so status can tell a verified install from an unverified one
// after the output has scrolled away.
func ZipOrigin(sum, verifiedTag string) string {
	if verifiedTag != "" {
		return ZipPrefix + "sha256:" + sum + ";verified " + verifiedTag
	}
	return ZipPrefix + "sha256:" + sum + ";unverified"
}

func absSource(dir string) (Source, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Source{}, err
	}
	return DirSource(abs), nil
}

// isRepo reports whether dir looks like the harness repo.
func isRepo(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "context", "AGENTS.md")); err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "skills"))
	return err == nil && info.IsDir()
}

// RepoInfo is the git state `fyr status` shows.
type RepoInfo struct {
	Branch string
	Commit string
	Dirty  bool
	// StatusUnknown is set when git could not report the tree's state at all.
	// A caller deciding whether to DEPLOY treats it as dirty: "git failed" is
	// not "the tree is clean" .
	StatusUnknown bool
}

// errPlainCopy is returned by Behind and Pull for a tree git cannot update.
var errPlainCopy = errors.New("not its own git checkout; a plain copy cannot be updated with git — `install` still deploys it as it is; to move forward, use a checkout (`git clone git@github.com:kesokaj/fyr.git`) or a release binary (README § Install without cloning)")

// IsCheckout reports whether repo is the top of its own git checkout: a
// `.git` entry sits directly in it (a directory, or the file a worktree
// carries). A harness-shaped tree nested inside another repository (a clone
// kept under review, a copy inside a project) has none and is a plain copy;
// asking git would answer for the enclosing repository, and git's answer
// can be redirected by its environment, so the check never asks git.
func IsCheckout(repo string) bool {
	_, err := os.Lstat(filepath.Join(repo, ".git"))
	return err == nil
}

// Inspect reads branch, commit, and cleanliness. A directory that is not its
// own git checkout yields empty fields rather than an error: the harness
// works from a plain copy too, only `update` needs git.
func Inspect(repo string) RepoInfo {
	if !IsCheckout(repo) {
		return RepoInfo{}
	}
	branch, _ := gitOut(repo, "rev-parse", "--abbrev-ref", "HEAD")
	commit, _ := gitOut(repo, "rev-parse", "--short", "HEAD")
	// --untracked-files=all, never the bare form: with `status.showUntrackedFiles
	// no`, a plain `--porcelain` reports a tree holding a wholly untracked
	// skill directory as clean, which `update` would then deploy.
	status, statusErr := gitOut(repo, "status", "--porcelain", "--untracked-files=all")
	return RepoInfo{Branch: branch, Commit: commit, Dirty: status != "", StatusUnknown: statusErr != nil}
}

// Behind fetches the upstream and reports how many commits HEAD lags it.
func Behind(repo string) (int, error) {
	if !IsCheckout(repo) {
		return 0, errPlainCopy
	}
	if _, err := gitOut(repo, "fetch", "--quiet"); err != nil {
		return 0, fmt.Errorf("git fetch: %w", err)
	}
	n, err := gitOut(repo, "rev-list", "--count", "HEAD..@{upstream}")
	if err != nil {
		return 0, fmt.Errorf("no upstream for the current branch: %w", err)
	}
	return strconv.Atoi(n)
}

// Pull fast-forwards to upstream. It refuses a dirty tree: pulling over
// uncommitted edits to the harness is how local changes get lost. Git runs
// in the operator's own environment on purpose: GIT_* variables, git config,
// GIT_SSH_COMMAND and PATH are theirs to set, exactly as for a `git pull`
// typed by hand; only the plain-copy check above is kept independent of
// git so a typo or a nested copy can never redirect a build.
func Pull(repo string, out io.Writer) error {
	if !IsCheckout(repo) {
		return errPlainCopy
	}
	// `update` refuses a dirty checkout before it gets here, with a message
	// that also covers the up-to-date path (BUG-046); this one guards a
	// direct call to Pull. Both say the same thing: uncommitted work is never
	// pulled over and never deployed, and an unreadable status counts as
	// dirty rather than clean.
	if info := Inspect(repo); info.Dirty || info.StatusUnknown {
		return errors.New("the harness repo has uncommitted changes, or git could not report its state; commit or stash them before `fyr update`")
	}
	cmd := exec.Command("git", "-C", repo, "pull", "--ff-only", "--quiet")
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git pull --ff-only: %w", err)
	}
	return nil
}

// ChildEnv marks the process Reinstall spawns from a fresh build, so that
// process does the plain install instead of rebuilding again.
const ChildEnv = "FYR_CHILD"

// Reinstall builds the binary from the repo's source and runs that build's
// install. It is the only path that changes the deployed binary: `go run`
// leaves no VCS stamp, and an installed copy re-installing itself never
// changes (BUG-006). The build lands in the repo's scratch space, one
// directory per process, and is removed afterwards.
func Reinstall(repo string, args []string, out io.Writer) error {
	if _, err := exec.LookPath("go"); err != nil {
		return errors.New("go is not on PATH; install Go to rebuild fyr")
	}
	dir := filepath.Join(repo, ".agents", "scratch", fmt.Sprintf("fyr-build-%d", os.Getpid()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating build directory: %w", err)
	}
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "fyr")
	fmt.Fprintf(out, "building fyr from %s\n", repo)
	build := exec.Command("go", "build", "-o", bin, "./cmd/fyr")
	build.Dir = repo
	// GOTOOLCHAIN=local, always, and not because of anything in go.mod —
	// the toolchain directive that prompted this was withdrawn in ADR-029.
	// The rule stands on its own: installing must never depend on fetching a
	// compiler. Under the default GOTOOLCHAIN=auto, any future `toolchain`
	// line, or a `go` directive above the host's version, makes this build
	// try to DOWNLOAD a toolchain, which exits 1 with "toolchain not
	// available" on a host with no cached copy and no reachable proxy —
	// measured. This is how every consumer installs, so it is pinned to what
	// the host already has.
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	build.Stdout, build.Stderr = out, out
	if err := build.Run(); err != nil {
		return fmt.Errorf("go build ./cmd/fyr: %w", err)
	}
	cmd := exec.Command(bin, append([]string{"install", "--repo", repo}, args...)...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), ChildEnv+"=1")
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fyr install: %w", err)
	}
	return nil
}

func gitOut(repo string, args ...string) (string, error) {
	return gitRun(exec.Command("git", append([]string{"-C", repo}, args...)...), args)
}

func gitRun(cmd *exec.Cmd, args []string) (string, error) {
	data, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
