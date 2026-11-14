package harness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The release binary this fixture ships: a script that records the arguments
// it was installed with, so the test can tell that the NEW binary ran.
const fakeReleaseBinary = `#!/bin/sh
printf 'install %s\n' "$*" >> "$FYR_FAKE_MARKER"
`

type release struct {
	assets string // what the test TLS server serves
	tag    string
}

// newRelease builds a release: the platform's binary and SHA256SUMS over it.
// No key is involved (ADR-053).
func newRelease(t *testing.T, tag string) release {
	t.Helper()
	dir := t.TempDir()
	asset := assetName(runtime.GOOS, runtime.GOARCH)
	writeFile(t, filepath.Join(dir, asset), fakeReleaseBinary)
	writeSums(t, dir, asset, tag)
	return release{assets: dir, tag: tag}
}

// writeSums writes SHA256SUMS over the asset as it is on disk now. Called
// again after changing the asset when a case needs the checksum to match,
// and NOT called again when it must not.
func writeSums(t *testing.T, dir, asset, tag string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, asset))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	// The marker /preflight writes, naming the release (plan D26).
	writeFile(t, filepath.Join(dir, "SHA256SUMS"),
		"# fyr "+tag+"\n"+hex.EncodeToString(sum[:])+"  "+asset+"\n")
}

// env points UpdateFromRelease at a local httptest.NewTLSServer serving r.assets
// and empties PATH so neither real gh nor real github.com is reached. GH_TAG
// and GH_FAIL_VIEW remain supported as live test knobs for redirect status and
// target tag overrides without restarting the TLS server.
func (r release) env(t *testing.T, withLatest bool) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if logPath := os.Getenv("GH_ARGS_LOG"); logPath != "" {
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err == nil {
				_, _ = f.WriteString(req.Host + req.URL.Path + "\n")
				_ = f.Close()
			}
		}
		tag := os.Getenv("GH_TAG")
		if os.Getenv("GH_TAG_SET") == "" {
			tag = r.tag
		}
		switch req.URL.Path {
		case "/kesokaj/fyr/releases/latest":
			if !withLatest {
				http.NotFound(w, req)
				return
			}
			if os.Getenv("GH_FAIL_VIEW") != "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Location", "/kesokaj/fyr/releases/tag/"+tag)
			w.WriteHeader(http.StatusFound)
			return
		case "/kesokaj/fyr/releases/tag/" + tag:
			w.WriteHeader(http.StatusOK)
			return
		}
		prefix := "/kesokaj/fyr/releases/download/" + tag + "/"
		if !strings.HasPrefix(req.URL.Path, prefix) {
			http.NotFound(w, req)
			return
		}
		name := filepath.Base(strings.TrimPrefix(req.URL.Path, prefix))
		data, err := os.ReadFile(filepath.Join(r.assets, name))
		if err != nil {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	SetReleaseHTTPForTest(t, srv.URL+"/kesokaj/fyr", srv.Client())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GH_TAG", r.tag)
	t.Setenv("GH_TAG_SET", "1")
	t.Setenv("GH_ASSETS", r.assets)
	t.Setenv("GH_FAIL_VIEW", "")
	os.Unsetenv("GH_FAIL_VIEW")
}

// installedBinary is the sentinel this machine already has, so every refusal
// can be checked against "the binary was not replaced".
func installedBinary(t *testing.T, home string) string {
	t.Helper()
	dest := filepath.Join(home, BinPath)
	writeFile(t, dest, "#!/bin/sh\necho the old binary\n")
	if err := os.Chmod(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	return dest
}

func TestUpdateFromReleaseReplacesAndInstalls(t *testing.T) {
	r := newRelease(t, "v9.9.9")
	r.env(t, true)
	home := t.TempDir()
	dest := installedBinary(t, home)
	marker := filepath.Join(home, "installed.txt")
	t.Setenv("FYR_FAKE_MARKER", marker)

	var out bytes.Buffer
	// The process running this update is not the file it installs — the
	// devbox shape — and a PATH entry reaches it by name (BUG-079).
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	byName := filepath.Join(elsewhere, "fyr")
	if err := os.Symlink(self, byName); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+elsewhere)
	t.Setenv("HOME", home) // the note is for the real home only
	if err := UpdateFromRelease(home, "v1.0.0", false, []string{"--home", home}, &out); err != nil {
		t.Fatalf("update: %v\n%s", err, out.String())
	}
	wantNote := "note: this update ran from " + byName + ", not from the installed binary " + dest + "; update replaces only the installed one"
	if !strings.Contains(out.String(), wantNote) {
		t.Errorf("a replacing update did not name the binary it ran from:\nwant %q\n%s", wantNote, out.String())
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != fakeReleaseBinary {
		t.Errorf("the installed binary is not the release's:\n%s", got)
	}
	if info, err := os.Stat(dest); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("the installed binary is not executable: %v %v", info.Mode(), err)
	}
	ran, err := os.ReadFile(marker)
	if err != nil || !strings.Contains(string(ran), "install --home "+home) {
		t.Errorf("the new binary's install did not run: %q %v", ran, err)
	}
	for _, want := range []string{"downloading fyr-", "verified and installed fyr-", "v9.9.9"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	// The download directory is gone, whatever happened.
	left, _ := filepath.Glob(filepath.Join(home, StateDir, "release-*"))
	if len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

func TestUpdateFromReleaseUpToDateAndCheck(t *testing.T) {
	r := newRelease(t, "v1.0.0")
	r.env(t, true)
	home := t.TempDir()
	dest := installedBinary(t, home)
	before, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}

	// The newest release IS this binary: nothing is downloaded or replaced,
	// on either mode.
	for _, checkOnly := range []bool{false, true} {
		var out bytes.Buffer
		if err := UpdateFromRelease(home, "v1.0.0", checkOnly, nil, &out); err != nil {
			t.Fatalf("checkOnly=%v: %v", checkOnly, err)
		}
		if !strings.Contains(out.String(), "up to date (v1.0.0)") {
			t.Errorf("checkOnly=%v output: %q", checkOnly, out.String())
		}
	}

	// A newer release, --check: named, exit-2 sentinel, nothing replaced.
	t.Setenv("GH_TAG", "v9.9.9")
	var out bytes.Buffer
	err = UpdateFromRelease(home, "v1.0.0", true, nil, &out)
	if !errors.Is(err, ErrNewerRelease) {
		t.Errorf("--check with a newer release: err = %v, want ErrNewerRelease", err)
	}
	if !strings.Contains(out.String(), "newer release v9.9.9 available") {
		t.Errorf("--check output: %q", out.String())
	}
	if after, _ := os.ReadFile(dest); string(after) != string(before) {
		t.Error("--check replaced the binary")
	}
}

// TestUpdateFromReleaseRefusals: every way a release can fail its checks
// leaves the installed binary exactly as it was, names the check that failed,
// and keeps nothing (plan § Failure modes).
func TestUpdateFromReleaseRefusals(t *testing.T) {
	asset := assetName(runtime.GOOS, runtime.GOARCH)
	for _, tc := range []struct {
		name    string
		want    string
		gh      bool
		prepare func(t *testing.T, r release)
	}{
		{name: "latest release endpoint returns 404", want: "no published release found", gh: false},
		{
			name: "latest release endpoint returns HTTP 401", want: "checking latest release: HTTP 401",
			gh:      true,
			prepare: func(t *testing.T, _ release) { t.Setenv("GH_FAIL_VIEW", "1") },
		},
		{
			name: "no asset for this platform", want: "has no fyr-",
			gh: true,
			prepare: func(t *testing.T, r release) {
				if err := os.Remove(filepath.Join(r.assets, asset)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			// The asset changes AFTER the checksums were written: only the hash
			// catches it.
			name: "binary does not match its checksum", want: "checksum mismatch for fyr-",
			gh: true,
			prepare: func(t *testing.T, r release) {
				writeFile(t, filepath.Join(r.assets, asset), "#!/bin/sh\necho not the binary that was listed\n")
			},
		},
		{
			name: "SHA256SUMS altered", want: "checksum mismatch for fyr-",
			gh: true,
			prepare: func(t *testing.T, r release) {
				writeFile(t, filepath.Join(r.assets, "SHA256SUMS"),
					"# fyr "+r.tag+"\n"+strings.Repeat("0", 64)+"  "+asset+"\n")
			},
		},
		{
			// The hash matches; only the version the file names says this is
			// another release's SHA256SUMS served under this tag.
			name: "checksums of another release", want: "SHA256SUMS says v1.2.3 but this is release v9.9.9",
			gh:      true,
			prepare: func(t *testing.T, r release) { writeSums(t, r.assets, asset, "v1.2.3") },
		},
		{
			name: "checksums naming no version", want: "names no release version",
			gh: true,
			prepare: func(t *testing.T, r release) {
				data, err := os.ReadFile(filepath.Join(r.assets, asset))
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(data)
				writeFile(t, filepath.Join(r.assets, "SHA256SUMS"), hex.EncodeToString(sum[:])+"  "+asset+"\n")
			},
		},
		{
			// One marker since v3.0.0 (plan fyr D5a): the name before the
			// rename is no release this binary takes.
			name: "checksums under the old marker", want: "names no release version",
			gh: true,
			prepare: func(t *testing.T, r release) {
				data, err := os.ReadFile(filepath.Join(r.assets, asset))
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(data)
				writeFile(t, filepath.Join(r.assets, "SHA256SUMS"), "# styrman-harness "+r.tag+"\n"+hex.EncodeToString(sum[:])+"  "+asset+"\n")
			},
		},
		{
			name: "no checksums in the release", want: "has no SHA256SUMS",
			gh: true,
			prepare: func(t *testing.T, r release) {
				if err := os.Remove(filepath.Join(r.assets, "SHA256SUMS")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRelease(t, "v9.9.9")
			r.env(t, tc.gh)
			if tc.prepare != nil {
				tc.prepare(t, r)
			}
			home := t.TempDir()
			dest := installedBinary(t, home)
			before, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(home, "installed.txt")
			t.Setenv("FYR_FAKE_MARKER", marker)

			var out bytes.Buffer
			err = UpdateFromRelease(home, "v1.0.0", false, nil, &out)
			if err == nil {
				t.Fatalf("update succeeded, want a refusal naming %q\n%s", tc.want, out.String())
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
			if after, _ := os.ReadFile(dest); string(after) != string(before) {
				t.Error("the installed binary was replaced by a release that failed its checks")
			}
			if _, err := os.Stat(marker); err == nil {
				t.Error("an install ran although the release failed its checks")
			}
			left, _ := filepath.Glob(filepath.Join(home, StateDir, "release-*"))
			if len(left) != 0 {
				t.Errorf("left behind: %v", left)
			}
		})
	}
}

// The names a v3.0.0 release publishes and a release binary fetches: one
// asset set, one repository (plan fyr D5a, Interfaces § The crossing).
func TestReleaseNames(t *testing.T) {
	if got := assetName("linux", "amd64"); got != "fyr-linux-amd64" {
		t.Errorf("assetName = %s, want fyr-linux-amd64", got)
	}
	if ReleaseRepo != "github.com/kesokaj/fyr" {
		t.Errorf("ReleaseRepo = %s, want github.com/kesokaj/fyr", ReleaseRepo)
	}
	for sums, want := range map[string]string{
		"# fyr v3.0.0\n":             "v3.0.0",
		"#fyr v3.0.0 \n":             "v3.0.0",
		"# styrman-harness v3.0.0\n": "",
		"# fyrx v3.0.0\n":            "",
	} {
		got := ""
		if m := sumsVersionMarker.FindStringSubmatch(sums); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("marker of %q = %q, want %q", sums, got, want)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "# fyr v1.2.3\n" +
		strings.Repeat("a", 64) + "  fyr-linux-amd64\n" +
		strings.Repeat("B", 64) + " *fyr-darwin-arm64\n" +
		"not-a-line\n" +
		"deadbeef  fyr-linux-arm64\n" +
		strings.Repeat("c", 64) + "  twice\n" +
		strings.Repeat("d", 64) + "  twice\n" +
		strings.Repeat("e", 64) + "  same\n" +
		strings.Repeat("e", 64) + "  same\n"
	for _, tc := range []struct {
		name, want, wantErr string
	}{
		{name: "fyr-linux-amd64", want: strings.Repeat("a", 64)},
		// A binary-mode '*' and an upper-case hash are both sha256sum's own
		// spellings.
		{name: "fyr-darwin-arm64", want: strings.Repeat("b", 64)},
		{name: "fyr-linux-arm64", wantErr: "does not carry a SHA-256"},
		{name: "fyr-windows-amd64", wantErr: "no line names"},
		// A file that says two things about one name is a reason to stop, not
		// to take the first.
		{name: "twice", wantErr: "two different hashes"},
		{name: "same", want: strings.Repeat("e", 64)},
	} {
		got, err := checksumFor(sums, tc.name)
		switch {
		case tc.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("checksumFor(%q) err = %v, want %q", tc.name, err, tc.wantErr)
			}
		case err != nil:
			t.Errorf("checksumFor(%q): %v", tc.name, err)
		case got != tc.want:
			t.Errorf("checksumFor(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCompareRelease(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.1.0", "v1.0.9", 1},
		{"v2.0.0", "v1.99.99", 1},
		{"v1.0.0", "v1.0.1", -1},
		{"v1.9.0", "v1.10.0", -1}, // not a string comparison
		{"v0.9.0", "v1.0.0", -1},
	} {
		if got := compareRelease(tc.a, tc.b); got != tc.want {
			t.Errorf("compareRelease(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestUpdateFromReleaseNeverGoesBackwards: a release that was deleted or
// re-pointed would otherwise install a genuine binary the operator has moved
// past, and
// `--check` would keep announcing it (security review 2026-09-17, finding 1).
func TestUpdateFromReleaseNeverGoesBackwards(t *testing.T) {
	r := newRelease(t, "v0.9.0")
	r.env(t, true)
	home := t.TempDir()
	dest := installedBinary(t, home)
	before, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "installed.txt")
	t.Setenv("FYR_FAKE_MARKER", marker)

	for _, checkOnly := range []bool{false, true} {
		var out bytes.Buffer
		if err := UpdateFromRelease(home, "v1.0.0", checkOnly, nil, &out); err != nil {
			t.Fatalf("checkOnly=%v: %v", checkOnly, err)
		}
		if !strings.Contains(out.String(), "the newest release v0.9.0 is not newer than this binary v1.0.0; nothing installed") {
			t.Errorf("checkOnly=%v output: %q", checkOnly, out.String())
		}
		if after, _ := os.ReadFile(dest); string(after) != string(before) {
			t.Errorf("checkOnly=%v replaced the binary with an older release", checkOnly)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Errorf("checkOnly=%v ran an install", checkOnly)
		}
	}

	// A tag that is not a release never reaches argv, where a leading dash
	// would be read by gh as a flag.
	for _, tag := range []string{"-rf", "v1", "latest", "v1.0.0-rc.1", ""} {
		t.Setenv("GH_TAG", tag)
		var out bytes.Buffer
		err := UpdateFromRelease(home, "v1.0.0", false, nil, &out)
		if err == nil || !strings.Contains(err.Error(), "not a vX.Y.Z release") {
			if tag == "" && err != nil && strings.Contains(err.Error(), "no tag") {
				continue // gh answering nothing at all is its own error
			}
			t.Errorf("tag %q: err = %v, want a refusal", tag, err)
		}
	}

	// A binary with no release version of its own does not install over
	// itself from a release either: it says so and leaves the file alone.
	t.Setenv("GH_TAG", "v9.9.9")
	var out bytes.Buffer
	if err := UpdateFromRelease(home, "b64ffe886729+dirty", false, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "which is not a release; leaving it alone") {
		t.Errorf("development build: %q", out.String())
	}
	if after, _ := os.ReadFile(dest); string(after) != string(before) {
		t.Error("a development build was overwritten from a release")
	}
}

// TestUpdateFromReleaseSweepStaysInHome runs the update's own sweep of
// leftover download directories from a home whose path holds a glob
// metacharacter: the leftover in this home goes, the sibling home's stays
// (security review 2026-09-30, finding 1; the helper's test is
// TestSweepStaleDirsStaysInParent, this one proves the update calls it).
func TestUpdateFromReleaseSweepStaysInHome(t *testing.T) {
	r := newRelease(t, "v9.9.9")
	r.env(t, true)
	root := t.TempDir()
	home := filepath.Join(root, "u[1]")
	own := filepath.Join(home, StateDir, "release-old")
	sibling := filepath.Join(root, "u1", StateDir, "release-keep")
	// Younger than the hour the sweep waits: a second update may own it.
	young := filepath.Join(home, StateDir, "release-young")
	for _, d := range []string{own, sibling, young} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if d == young {
			continue
		}
		old := time.Now().Add(-2 * time.Hour)
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatal(err)
		}
	}
	installedBinary(t, home)
	t.Setenv("FYR_FAKE_MARKER", filepath.Join(home, "installed.txt"))
	var out bytes.Buffer
	if err := UpdateFromRelease(home, "v1.0.0", false, []string{"--home", home}, &out); err != nil {
		t.Fatalf("update: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		t.Errorf("the leftover in this home should be removed, got %v", err)
	}
	for _, keep := range []string{sibling, young} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%s should survive the sweep: %v", keep, err)
		}
	}
}

// TestUpdateFromReleasePinsTheHost: release updates ignore GH_HOST/GH_TOKEN
// and hit only the pinned release host and repository path (ADR-053).
func TestUpdateFromReleasePinsTheHost(t *testing.T) {
	if releaseDefaultBaseURL != "https://github.com/kesokaj/fyr" {
		t.Fatalf("releaseDefaultBaseURL = %q, want https://github.com/kesokaj/fyr", releaseDefaultBaseURL)
	}
	r := newRelease(t, "v9.9.9")
	r.env(t, true)
	home := t.TempDir()
	installedBinary(t, home)
	t.Setenv("FYR_FAKE_MARKER", filepath.Join(home, "installed.txt"))
	t.Setenv("GH_HOST", "example.invalid")
	t.Setenv("GH_TOKEN", "invalid-token")
	log := filepath.Join(t.TempDir(), "http-reqs")
	t.Setenv("GH_ARGS_LOG", log)
	var out bytes.Buffer
	if err := UpdateFromRelease(home, "v1.0.0", false, []string{"--home", home}, &out); err != nil {
		t.Fatalf("update: %v\n%s", err, out.String())
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(calls) < 3 {
		t.Fatalf("HTTP server saw %d calls, want latest, asset, and SHA256SUMS:\n%s", len(calls), data)
	}
	for _, c := range calls {
		if strings.Contains(c, "example.invalid") || !strings.Contains(c, "/kesokaj/fyr/releases/") {
			t.Errorf("request leaked host or missed /kesokaj/fyr/releases/: %s", c)
		}
	}
}
