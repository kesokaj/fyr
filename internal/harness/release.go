package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// maxAssetBytes bounds what a release binary download can make this process
// read into memory. Variable so a test can lower it.
var maxAssetBytes int64 = 256 << 20

// ErrNewerRelease is returned by `update --check` on a release binary when a
// newer release exists, so the command can exit 2 as it does for a checkout
// that is behind.
var ErrNewerRelease = fmt.Errorf("a newer release is available")

// assetName is the binary asset's name for one platform.
//
//nolint:unparam // goos varies across GOOS targets
func assetName(goos, goarch string) string {
	return fmt.Sprintf("fyr-%s-%s", goos, goarch)
}

// UpdateFromRelease is `fyr update` on a place that has no checkout: the
// newest release is downloaded, the binary is checked against the release's
// SHA256SUMS, and only then does the binary get replaced and its install run.
// Nothing is replaced when any check fails.
//
// version is the running binary's; installArgs are passed to the new
// binary's install.
func UpdateFromRelease(home, version string, checkOnly bool, installArgs []string, out io.Writer) error {
	tag, err := newerRelease(version, checkOnly, out)
	if err != nil || tag == "" {
		return err
	}

	// Clean any stale release-* scratch directories from interrupted updates
	// using an older binary.
	base, err := ConfigDir(home)
	if err != nil {
		return err
	}
	SweepStaleDirs(base, "release-", time.Hour, out)

	asset := assetName(runtime.GOOS, runtime.GOARCH)
	data, sums, err := downloadRelease(tag, asset, out)
	if err != nil {
		return err
	}
	if err := verifyRelease(tag, asset, data, sums); err != nil {
		return err
	}
	return installRelease(home, tag, asset, data, installArgs, out)
}

// newerRelease returns the newest release's tag when it is newer than the
// running binary's version, and "" when there is nothing to install; with
// checkOnly a newer release is ErrNewerRelease.
func newerRelease(version string, checkOnly bool, out io.Writer) (string, error) {
	tag, err := latestRelease()
	if err != nil {
		return "", err
	}
	// The tag comes from HTTPS redirect target URL, which is not signed: it
	// decides which release URL is constructed, so it must match canonical vX.Y.Z.
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
	if isLinkedPath(home, BinPath) {
		return fmt.Errorf("replacing %s: a symlink is in the way; fyr never writes through a link", dest)
	}
	if err := dirInTheWay(dest); err != nil {
		return fmt.Errorf("replacing %s: %w", dest, err)
	}
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

// downloadRelease fetches the platform's binary and SHA256SUMS over HTTPS into
// memory (ADR-515, BUG-547).
func downloadRelease(tag, asset string, out io.Writer) (data, sums []byte, err error) {
	fmt.Fprintf(out, "downloading %s %s\n", asset, tag)
	baseRaw, cfgClient := releaseHTTPConfigSnapshot()
	base, err := parseReleaseBaseURL(baseRaw)
	if err != nil {
		return nil, nil, err
	}
	escapedTag, escapedAsset := url.PathEscape(tag), url.PathEscape(asset)
	assetURL := base.String() + "/releases/download/" + escapedTag + "/" + escapedAsset
	data, status, err := fetchReleaseAsset(assetURL, base, cfgClient, maxAssetBytes)
	if err != nil {
		if status == http.StatusNotFound {
			return nil, nil, fmt.Errorf("release %s has no %s", tag, asset)
		}
		return nil, nil, fmt.Errorf("release %s: %s is not usable: %w", tag, asset, err)
	}

	sumsURL := base.String() + "/releases/download/" + escapedTag + "/SHA256SUMS"
	sums, sumsStatus, err := fetchReleaseAsset(sumsURL, base, cfgClient, maxSumsBytes)
	if err != nil {
		if sumsStatus == http.StatusNotFound {
			return nil, nil, fmt.Errorf("release %s has no SHA256SUMS", tag)
		}
		return nil, nil, fmt.Errorf("release %s: SHA256SUMS is not usable: %w", tag, err)
	}
	return data, sums, nil
}

// verifyRelease checks the binary against its line in the release's
// SHA256SUMS, and that the file is this release's, before anything is
// replaced.
func verifyRelease(tag, asset string, data, sums []byte) error {
	named, err := parseSums(sums, "release "+tag)
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
