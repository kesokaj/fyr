package harness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ReleaseRepo is the public GitHub repository release binaries update from.
// With no signature on a release (ADR-053), integrity rests on HTTPS from
// github.com (and GitHub's release asset CDN) plus SHA256SUMS.
const ReleaseRepo = "github.com/kesokaj/fyr"

const releaseDefaultBaseURL = "https://" + ReleaseRepo

var (
	releaseHTTPMu  sync.Mutex
	releaseBaseURL = releaseDefaultBaseURL
	releaseClient  *http.Client
)

// SetReleaseHTTPForTest redirects release update HTTP requests in tests to a
// local httptest TLS server and restores the production configuration on Cleanup.
func SetReleaseHTTPForTest(tb interface {
	Helper()
	Cleanup(func())
}, baseURL string, client *http.Client) {
	tb.Helper()
	releaseHTTPMu.Lock()
	prevURL, prevClient := releaseBaseURL, releaseClient
	releaseBaseURL = baseURL
	releaseClient = client
	releaseHTTPMu.Unlock()
	tb.Cleanup(func() {
		releaseHTTPMu.Lock()
		releaseBaseURL = prevURL
		releaseClient = prevClient
		releaseHTTPMu.Unlock()
	})
}

func releaseHTTPConfigSnapshot() (string, *http.Client) {
	releaseHTTPMu.Lock()
	defer releaseHTTPMu.Unlock()
	return releaseBaseURL, releaseClient
}

func newReleaseHTTPClient(cfgClient *http.Client, timeout time.Duration, checkRedirect func(*http.Request, []*http.Request) error) *http.Client {
	c := &http.Client{
		Timeout:       timeout,
		CheckRedirect: checkRedirect,
	}
	if cfgClient != nil && cfgClient.Transport != nil {
		c.Transport = cfgClient.Transport
	}
	return c
}

func parseReleaseBaseURL(baseRaw string) (*url.URL, error) {
	base, err := url.Parse(strings.TrimRight(baseRaw, "/"))
	if err != nil || base.Scheme != "https" || base.Hostname() == "" {
		return nil, fmt.Errorf("release URL %q must use https", baseRaw)
	}
	return base, nil
}

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

// latestRelease resolves the newest published release tag over HTTPS from the
// public releases/latest redirect without invoking gh or requiring login
// (ADR-515, BUG-547). Using the web redirect avoids GitHub's 60 req/hr
// unauthenticated REST API rate limit.
func latestRelease() (string, error) {
	baseRaw, cfgClient := releaseHTTPConfigSnapshot()
	base, err := parseReleaseBaseURL(baseRaw)
	if err != nil {
		return "", err
	}
	client := newReleaseHTTPClient(cfgClient, 30*time.Second, func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	})
	req, err := http.NewRequest(http.MethodGet, base.String()+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "fyr-updater")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("checking latest release: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return extractTagFromLatestRedirect(req.URL, base, resp.Header.Get("Location"))
	case http.StatusNotFound:
		return "", fmt.Errorf("no published release found at %s", base.String())
	default:
		return "", fmt.Errorf("checking latest release: HTTP %d", resp.StatusCode)
	}
}

func extractTagFromLatestRedirect(reqURL, base *url.URL, locRaw string) (string, error) {
	if strings.TrimSpace(locRaw) == "" {
		return "", errors.New("latest release returned no redirect location")
	}
	loc, err := reqURL.Parse(locRaw)
	if err != nil {
		return "", fmt.Errorf("latest release redirect invalid: %w", err)
	}
	if loc.Scheme != "https" || !strings.EqualFold(loc.Hostname(), base.Hostname()) {
		return "", fmt.Errorf("refusing latest release redirect to untrusted location %q", loc.String())
	}
	tagPrefix := strings.TrimRight(base.EscapedPath(), "/") + "/releases/tag/"
	rawTag, ok := strings.CutPrefix(loc.EscapedPath(), tagPrefix)
	if !ok {
		return "", fmt.Errorf("latest release redirected to unexpected path %q", loc.EscapedPath())
	}
	tag, err := url.PathUnescape(rawTag)
	if err != nil {
		return "", fmt.Errorf("decoding release tag: %w", err)
	}
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", errors.New("latest release returned no tag")
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
// dir and returns the binary's bytes (ADR-515, BUG-547).
func downloadRelease(dir, tag, asset string, out io.Writer) ([]byte, error) {
	fmt.Fprintf(out, "downloading %s %s\n", asset, tag)
	baseRaw, cfgClient := releaseHTTPConfigSnapshot()
	base, err := parseReleaseBaseURL(baseRaw)
	if err != nil {
		return nil, err
	}
	escapedTag, escapedAsset := url.PathEscape(tag), url.PathEscape(asset)
	assetURL := base.String() + "/releases/download/" + escapedTag + "/" + escapedAsset
	data, status, err := fetchReleaseAsset(assetURL, base, cfgClient, maxAssetBytes)
	if err != nil {
		if status == http.StatusNotFound {
			return nil, fmt.Errorf("release %s has no %s", tag, asset)
		}
		return nil, fmt.Errorf("release %s: %s is not usable: %w", tag, asset, err)
	}
	if err := os.WriteFile(filepath.Join(dir, asset), data, 0o600); err != nil {
		return nil, fmt.Errorf("writing %s: %w", asset, err)
	}

	sumsURL := base.String() + "/releases/download/" + escapedTag + "/SHA256SUMS"
	sums, sumsStatus, err := fetchReleaseAsset(sumsURL, base, cfgClient, maxSumsBytes)
	if err != nil {
		if sumsStatus == http.StatusNotFound {
			return nil, fmt.Errorf("release %s has no SHA256SUMS", tag)
		}
		return nil, fmt.Errorf("release %s: SHA256SUMS is not usable: %w", tag, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), sums, 0o600); err != nil {
		return nil, fmt.Errorf("writing SHA256SUMS: %w", err)
	}
	return data, nil
}

func allowedReleaseRedirectHost(host, baseHost string) bool {
	if strings.EqualFold(host, baseHost) {
		return true
	}
	if !strings.EqualFold(baseHost, "github.com") {
		return false
	}
	switch strings.ToLower(host) {
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		return true
	default:
		return false
	}
}

func fetchReleaseAsset(rawURL string, base *url.URL, cfgClient *http.Client, maxBytes int64) ([]byte, int, error) {
	baseHost := base.Hostname()
	client := newReleaseHTTPClient(cfgClient, 5*time.Minute, func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" {
			return fmt.Errorf("refusing non-HTTPS redirect to %q", req.URL.String())
		}
		if !allowedReleaseRedirectHost(req.URL.Hostname(), baseHost) {
			return fmt.Errorf("refusing redirect to untrusted host %q", req.URL.Hostname())
		}
		return nil
	})
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "fyr-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, resp.StatusCode, fmt.Errorf("%d bytes is larger than the %d this reads", resp.ContentLength, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if int64(len(data)) > maxBytes {
		return nil, resp.StatusCode, fmt.Errorf("%d bytes is larger than the %d this reads", len(data), maxBytes)
	}
	return data, resp.StatusCode, nil
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
