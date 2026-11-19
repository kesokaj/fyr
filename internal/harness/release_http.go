package harness

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
