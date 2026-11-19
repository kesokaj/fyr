package harness

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	if IsReleaseVersion(version) {
		return releaseSource(explicit, version)
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
	candidates, zipNote := sourceCandidates(home)
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

// releaseSource is ResolveSource for a release binary.
func releaseSource(explicit, version string) (Source, error) {
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

// sourceCandidates is a development build's walk after the named paths: the
// working directory and its parents, then the tree the last install recorded.
// zipNote explains a home installed from a zip, which records no tree.
func sourceCandidates(home string) (candidates []string, zipNote string) {
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
	if st, found, err := LoadState(home); err == nil && found {
		switch o := st.Origin(); o.Kind {
		case OriginZip:
			// Never "run --from-zip again": that keeps this build and this
			// place as they are, which is how borken was stranded (BUG-203).
			zipNote = fmt.Sprintf("this home was installed from a zip (sha256 %s), and a development build installs only from a checkout: build fyr in one and run `fyr install --repo <checkout>`, which makes this a checkout place. ", o.ShortSum())
		case OriginPath:
			candidates = append(candidates, o.Path)
		}
	}
	return candidates, zipNote
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
	scratch := filepath.Join(repo, ".agents", "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return fmt.Errorf("creating build directory: %w", err)
	}
	dir, err := os.MkdirTemp(scratch, "fyr-build-*")
	if err != nil {
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
