package harness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func servePublicRelease(t *testing.T, tag, assetsDir string, redirectAssetTo string) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/kesokaj/fyr/releases/latest":
			http.Redirect(w, r, "/kesokaj/fyr/releases/tag/"+tag, http.StatusFound)
		case "/kesokaj/fyr/releases/tag/" + tag:
			w.WriteHeader(http.StatusOK)
		default:
			prefix := "/kesokaj/fyr/releases/download/" + tag + "/"
			if !strings.HasPrefix(r.URL.Path, prefix) {
				http.NotFound(w, r)
				return
			}
			name := filepath.Base(strings.TrimPrefix(r.URL.Path, prefix))
			if redirectAssetTo != "" && name == assetName(runtime.GOOS, runtime.GOARCH) {
				http.Redirect(w, r, redirectAssetTo, http.StatusFound)
				return
			}
			data, err := os.ReadFile(filepath.Join(assetsDir, name))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		}
	}))
}

// TestUpdateFromReleaseWorksWithoutGhAuth pins BUG-547: an unauthenticated gh
// binary on PATH (exiting 4 with "gh auth login") or absent gh must never stop
// a release-binary update against public github.com/kesokaj/fyr.
func TestUpdateFromReleaseWorksWithoutGhAuth(t *testing.T) {
	r := newRelease(t, "v9.9.9")
	srv := servePublicRelease(t, r.tag, r.assets, "")
	defer srv.Close()
	SetReleaseHTTPForTest(t, srv.URL+"/kesokaj/fyr", srv.Client())

	// Reproduce borken: gh installed on PATH but not logged in.
	bin := t.TempDir()
	unauthGh := filepath.Join(bin, "gh")
	writeFile(t, unauthGh, "#!/bin/sh\necho 'To get started with GitHub CLI, please run:  gh auth login' >&2\nexit 4\n")
	if err := os.Chmod(unauthGh, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	home := t.TempDir()
	dest := installedBinary(t, home)
	marker := filepath.Join(home, "installed.txt")
	t.Setenv("FYR_FAKE_MARKER", marker)

	var out bytes.Buffer
	if err := UpdateFromRelease(home, "v1.0.0", false, []string{"--home", home}, &out); err != nil {
		t.Fatalf("update failed with unauthenticated gh on PATH: %v\n%s", err, out.String())
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != fakeReleaseBinary {
		t.Fatalf("installed binary mismatch (%v): %q", err, got)
	}

	// Also works with an empty PATH (no gh binary at all).
	t.Setenv("PATH", t.TempDir())
	homeNoGh := t.TempDir()
	destNoGh := installedBinary(t, homeNoGh)
	t.Setenv("FYR_FAKE_MARKER", filepath.Join(homeNoGh, "installed.txt"))
	out.Reset()
	if err := UpdateFromRelease(homeNoGh, "v1.0.0", false, []string{"--home", homeNoGh}, &out); err != nil {
		t.Fatalf("update failed with no gh on PATH: %v\n%s", err, out.String())
	}
	if got2, err := os.ReadFile(destNoGh); err != nil || string(got2) != fakeReleaseBinary {
		t.Fatalf("installed binary without gh mismatch (%v): %q", err, got2)
	}
}

func TestUpdateFromReleaseRedirectGuardsAndSizeCap(t *testing.T) {
	t.Run("non-https base URL rejected", func(t *testing.T) {
		SetReleaseHTTPForTest(t, "http://github.com/kesokaj/fyr", nil)
		home := t.TempDir()
		installedBinary(t, home)
		err := UpdateFromRelease(home, "v1.0.0", false, nil, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "https") {
			t.Fatalf("expected https scheme error, got: %v", err)
		}
	})

	t.Run("untrusted redirect host rejected", func(t *testing.T) {
		r := newRelease(t, "v9.9.9")
		srv := servePublicRelease(t, r.tag, r.assets, "https://evil.invalid/payload")
		defer srv.Close()
		SetReleaseHTTPForTest(t, srv.URL+"/kesokaj/fyr", srv.Client())
		t.Setenv("PATH", t.TempDir())
		home := t.TempDir()
		installedBinary(t, home)
		err := UpdateFromRelease(home, "v1.0.0", false, nil, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "untrusted host") {
			t.Fatalf("expected untrusted host error, got: %v", err)
		}
	})

	t.Run("http redirect downgrade rejected", func(t *testing.T) {
		r := newRelease(t, "v9.9.9")
		srv := servePublicRelease(t, r.tag, r.assets, "http://127.0.0.1/downgrade")
		defer srv.Close()
		SetReleaseHTTPForTest(t, srv.URL+"/kesokaj/fyr", srv.Client())
		t.Setenv("PATH", t.TempDir())
		home := t.TempDir()
		installedBinary(t, home)
		err := UpdateFromRelease(home, "v1.0.0", false, nil, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "non-HTTPS redirect") {
			t.Fatalf("expected non-HTTPS redirect error, got: %v", err)
		}
	})

	t.Run("oversized asset fails closed", func(t *testing.T) {
		r := newRelease(t, "v9.9.9")
		asset := assetName(runtime.GOOS, runtime.GOARCH)
		big := bytes.Repeat([]byte("A"), 2048)
		writeFile(t, filepath.Join(r.assets, asset), string(big))
		sum := sha256.Sum256(big)
		writeFile(t, filepath.Join(r.assets, "SHA256SUMS"), "# fyr "+r.tag+"\n"+hex.EncodeToString(sum[:])+"  "+asset+"\n")
		defer func(prev int64) { maxAssetBytes = prev }(maxAssetBytes)
		maxAssetBytes = 512

		srv := servePublicRelease(t, r.tag, r.assets, "")
		defer srv.Close()
		SetReleaseHTTPForTest(t, srv.URL+"/kesokaj/fyr", srv.Client())
		t.Setenv("PATH", t.TempDir())
		home := t.TempDir()
		installedBinary(t, home)
		err := UpdateFromRelease(home, "v1.0.0", false, nil, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "larger than") {
			t.Fatalf("expected size cap error, got: %v", err)
		}
	})
}
