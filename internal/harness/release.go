package harness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ReleaseRepo is the repository a release binary updates from, with its host:
// gh takes the host from GH_HOST when --repo names none, and with no signature
// on a release the host is the whole of where a binary comes from (security
// review 2026-10-05, ADR-053).
const ReleaseRepo = "github.com/kesokaj/fyr"

// sumsVersionMarker is the line `/preflight` writes at the top of
// SHA256SUMS, naming the release those hashes belong to, so one release's
// checksums are never taken for another's (security review 2026-09-17).
// `sha256sum -c` ignores a line starting with '#', so the file stays readable
// by it. One marker, `# fyr vX.Y.Z`, since v3.0.0: no release this binary
// takes was published under the name before the rename (plan fyr D5a). It is
// matched on any line, and a file whose markers name two releases is refused
// (readSums, IN28).
//
// The checksums are not signed (ADR-053): they say a download is whole and is
// the file the release lists, not who made the release. That rests on the
// operator's GitHub credential and HTTPS, as a checkout place's pull does.
var sumsVersionMarker = regexp.MustCompile(`(?m)^#\s*fyr\s+(v[0-9]+\.[0-9]+\.[0-9]+)\s*$`)

// maxAssetBytes and maxSumsBytes bound what a release can make this process
// read into memory. Variables so a test can lower them.
var (
	maxAssetBytes int64 = 256 << 20
	maxSumsBytes  int64 = 1 << 20
)

// ErrNewerRelease is returned by `update --check` on a release binary when a
// newer release exists, so the command can exit 2 as it does for a checkout
// that is behind.
var ErrNewerRelease = errors.New("a newer release is available")

// assetName is the binary asset's name for one platform.
func assetName(goos, goarch string) string {
	return fmt.Sprintf("fyr-%s-%s", goos, goarch)
}

// latestRelease asks gh for the newest release's tag. gh carries the
// operator's own GitHub credential, which is the point: a release binary has
// no checkout and no other way in (R8).
func latestRelease() (string, error) {
	out, err := exec.Command("gh", "release", "view", "--repo", ReleaseRepo, "--json", "tagName", "--jq", ".tagName").Output()
	if err != nil {
		return "", fmt.Errorf("gh release view failed: %s", firstLineOfError(err))
	}
	tag := strings.TrimSpace(string(out))
	if tag == "" {
		return "", errors.New("gh release view returned no tag")
	}
	return tag, nil
}

// UpdateFromRelease is `fyr update` on a place that has no checkout: the
// newest release is downloaded, the binary is checked against the release's
// SHA256SUMS, and only then does the binary get replaced and its install run. Nothing is replaced when any check fails, and the download
// directory is removed on every path out.
//
// version is the running binary's; installArgs are passed to the new
// binary's install.
func UpdateFromRelease(home, version string, checkOnly bool, installArgs []string, out io.Writer) error {
	tag, err := newerRelease(version, checkOnly, out)
	if err != nil || tag == "" {
		return err
	}

	// A random directory, and one whose siblings from an interrupted earlier
	// update are cleared first: a predictable name is one another local
	// account can pre-create as a symlink for these writes to follow. An
	// hour is the zip sweep's threshold too; a younger sibling may be a
	// second update still running in this home.
	base, err := ConfigDir(home)
	if err != nil {
		return err
	}
	SweepStaleDirs(base, "release-", time.Hour, out)
	dir, err := os.MkdirTemp(base, "release-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	asset := assetName(runtime.GOOS, runtime.GOARCH)
	data, err := downloadRelease(dir, tag, asset, out)
	if err != nil {
		return err
	}
	if err := verifyRelease(dir, tag, asset, data); err != nil {
		return err
	}
	return installRelease(home, tag, asset, data, installArgs, out)
}

// newerRelease returns the newest release's tag when it is newer than the
// running binary's version, and "" when there is nothing to install; with
// checkOnly a newer release is ErrNewerRelease.
func newerRelease(version string, checkOnly bool, out io.Writer) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", errors.New("this install has no checkout; install gh and run gh auth login, or download a newer release by hand (README § Install without cloning)")
	}
	tag, err := latestRelease()
	if err != nil {
		return "", err
	}
	// The tag comes from the GitHub API, which is not signed: it reaches argv
	// as a positional argument and decides which release is installed, so it
	// must look like a release before either happens (a `-`-leading tag would
	// be read by gh as a flag).
	if !IsReleaseVersion(tag) {
		return "", fmt.Errorf("the newest release is tagged %q, which is not a vX.Y.Z release; nothing installed", tag)
	}
	switch {
	case tag == version:
		fmt.Fprintf(out, "up to date (%s)\n", version)
		return "", nil
	// Never sideways or backwards: a deleted or re-pointed release would
	// otherwise install a genuine binary the operator has moved past
	// (security review 2026-09-17).
	case !IsReleaseVersion(version):
		fmt.Fprintf(out, "this binary reports version %q, which is not a release; leaving it alone — download %s by hand (README § Install without cloning)\n", version, tag)
		return "", nil
	case compareRelease(tag, version) <= 0:
		fmt.Fprintf(out, "the newest release %s is not newer than this binary %s; nothing installed\n", tag, version)
		return "", nil
	}
	if checkOnly {
		fmt.Fprintf(out, "newer release %s available\n", tag)
		return "", ErrNewerRelease
	}
	return tag, nil
}

// installRelease replaces the installed binary with the verified one and runs
// the new binary's install.
func installRelease(home, tag, asset string, data []byte, installArgs []string, out io.Writer) error {
	// Replaced through a temporary file in the destination's own directory
	// and a rename, so an interrupted update leaves the old binary whole
	// rather than a half-written one.
	dest := filepath.Join(home, BinPath)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := WriteAtomic(dest, data, 0o755); err != nil {
		return fmt.Errorf("replacing %s: %w", dest, err)
	}
	fmt.Fprintf(out, "verified and installed %s %s\n", asset, tag)

	// The new binary installs the harness it carries; this process is the old
	// one and must not do it (BUG-006's rule, from the other direction).
	cmd := exec.Command(dest, append([]string{"install"}, installArgs...)...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		// The binary is already replaced; the harness deployed is still the
		// old one's, which status then shows as drift (IN29).
		return fmt.Errorf("%s install: %w — %s %s is already in place at %s, but the harness it carries was not installed; run `fyr install` to finish", dest, err, asset, tag, dest)
	}
	NoteRunningElsewhere(home, out)
	return nil
}

// downloadRelease fetches the platform's binary and the checksum files into
// dir and returns the binary's bytes.
func downloadRelease(dir, tag, asset string, out io.Writer) ([]byte, error) {
	fmt.Fprintf(out, "downloading %s %s\n", asset, tag)
	cmd := exec.Command("gh", "release", "download", tag,
		"--repo", ReleaseRepo,
		"--pattern", asset,
		"--pattern", "SHA256SUMS",
		"--dir", dir)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	downloadErr := cmd.Run()
	// A release without this platform's asset is the likelier cause of a
	// failure here than anything else, and gh's own message for it is not
	// one the operator can act on, so the missing file is named first.
	path := filepath.Join(dir, asset)
	if _, err := os.Stat(path); err != nil {
		if line := firstStderrLine(stderr.String()); line != "" {
			return nil, fmt.Errorf("release %s has no %s (gh: %s)", tag, asset, line)
		}
		return nil, fmt.Errorf("release %s has no %s", tag, asset)
	}
	data, err := readCapped(path, maxAssetBytes)
	if err != nil {
		return nil, fmt.Errorf("release %s: %s is not usable: %w", tag, asset, err)
	}
	if downloadErr != nil {
		return nil, fmt.Errorf("gh release download %s failed: %s", tag, firstStderrLine(stderr.String()))
	}
	return data, nil
}

// verifyRelease checks the binary against its line in the release's
// SHA256SUMS, and that the file is this release's, before anything is
// replaced.
func verifyRelease(dir, tag, asset string, data []byte) error {
	sums, named, err := readSums(dir, "release "+tag)
	if err != nil {
		return err
	}
	if named != tag {
		return fmt.Errorf("SHA256SUMS says %s but this is release %s", named, tag)
	}
	want, err := checksumFor(string(sums), asset)
	if err != nil {
		return fmt.Errorf("SHA256SUMS of %s: %w", tag, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}
	return nil
}

// readSums reads sumsDir's SHA256SUMS and returns it and the release it
// names. what names the subject in messages.
func readSums(sumsDir, what string) (sums []byte, tag string, err error) {
	sumsPath := filepath.Join(sumsDir, "SHA256SUMS")
	if _, err := os.Stat(sumsPath); err != nil {
		return nil, "", fmt.Errorf("%s has no SHA256SUMS", what)
	}
	sums, err = readCapped(sumsPath, maxSumsBytes)
	if err != nil {
		return nil, "", fmt.Errorf("%s: SHA256SUMS is not usable: %w", what, err)
	}
	markers := sumsVersionMarker.FindAllSubmatch(sums, -1)
	if markers == nil {
		return nil, "", fmt.Errorf("the SHA256SUMS of %s names no release version; it cannot be told apart from another release's", what)
	}
	// The marker may stand on any line; a second one naming another release
	// is refused, never settled by which comes first (IN28).
	for _, m := range markers[1:] {
		if !bytes.Equal(m[1], markers[0][1]) {
			return nil, "", fmt.Errorf("the SHA256SUMS of %s names two releases, %s and %s; it belongs to one", what, markers[0][1], m[1])
		}
	}
	return sums, string(markers[0][1]), nil
}

// checksumFor reads one file's hash out of a sha256sum-format listing, in
// which a binary-mode line marks the name with a leading '*'. The hash must
// look like one, and two lines naming the same file are refused rather than
// resolved by order: an ambiguity in it is a reason to stop, not to guess.
func checksumFor(sums, name string) (string, error) {
	var found string
	for _, line := range strings.Split(sums, "\n") {
		hash, file, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if strings.TrimPrefix(strings.TrimSpace(file), "*") != name {
			continue
		}
		hash = strings.ToLower(hash)
		if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
			return "", fmt.Errorf("the line naming %s does not carry a SHA-256", name)
		}
		if found != "" && found != hash {
			return "", fmt.Errorf("two different hashes are given for %s", name)
		}
		found = hash
	}
	if found == "" {
		return "", fmt.Errorf("no line names %s", name)
	}
	return found, nil
}

// readCapped is the one capped file read: it refuses a file over max bytes
// before reading it (a release asset, a settings file, a checksum file), so no
// caller streams an unbounded file into memory.
func readCapped(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > max {
		return nil, fmt.Errorf("%d bytes is larger than the %d this reads", info.Size(), max)
	}
	// One byte past the cap: a stream whose Stat size says nothing (a FIFO, a
	// special file) fails closed instead of being cut short.
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("more than the %d bytes this reads", max)
	}
	return data, nil
}

// compareRelease orders two vX.Y.Z tags. Both are known to match
// IsReleaseVersion, so every part parses.
func compareRelease(a, b string) int {
	pa, pb := releaseParts(a), releaseParts(b)
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func releaseParts(v string) [3]int {
	var out [3]int
	for i, part := range strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3) {
		n, err := strconv.Atoi(part)
		if err != nil {
			return [3]int{}
		}
		out[i] = n
	}
	return out
}

// firstLineOfError renders a failed command's first line of stderr, which is
// the part that says what went wrong; the rest is usually a usage dump.
func firstLineOfError(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if line := firstStderrLine(string(exit.Stderr)); line != "" {
			return line
		}
	}
	return err.Error()
}

func firstStderrLine(s string) string {
	return strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
}
