package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestUpdateFromZip(t *testing.T) {
	isolate(t)
	defer func(v string) { version = v }(version)
	version = "v1.2.3" // a release binary: its source would otherwise be the embedded files
	zp := markedZip(t)
	home := t.TempDir()

	var out bytes.Buffer
	if err := run([]string{"update", "--from-zip", zp, "--home", home}, &out); err != nil {
		t.Fatalf("update --from-zip: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "UNVERIFIED") || !strings.Contains(out.String(), "nothing in the zip replaces") || !strings.Contains(out.String(), "sha256") {
		t.Errorf("output does not say what was installed and that the binary stays:\n%s", out.String())
	}
	ctx, err := os.ReadFile(filepath.Join(home, harness.AgyRoot, "rules", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ctx), "ZIPMARKER") {
		t.Errorf("installed context is not the zip's (no ZIPMARKER): the release binary's embedded files won")
	}
	st, found, err := harness.LoadState(home)
	if err != nil || !found {
		t.Fatalf("manifest: found=%v err=%v", found, err)
	}
	if !strings.HasPrefix(st.Repo, harness.ZipPrefix+"sha256:") || st.Commit != "" {
		t.Errorf("manifest repo=%q commit=%q, want zip:sha256:… and no commit", st.Repo, st.Commit)
	}
	noZipDirsUnder(t, home)
}

func TestStatusAfterZip(t *testing.T) {
	isolate(t)
	defer func(v string) { version = v }(version)
	version = "v1.2.3"
	zp := markedZip(t)
	home := t.TempDir()
	if err := run([]string{"update", "--from-zip", zp, "--home", home}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"status", "--home", home}, &out); err != nil {
		t.Fatalf("status after a zip install: %v\n%s", err, out.String())
	}
	for _, want := range []string{"from zip sha256:", "(unverified)", "installed from a zip", "not compared", "manifest files present"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "differ") {
		t.Errorf("status compared a zip install against the embedded files:\n%s", out.String())
	}

	st, _, _ := harness.LoadState(home)
	gone := st.Files[0]
	if err := os.Remove(filepath.Join(home, gone)); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := run([]string{"status", "--home", home}, &out)
	var code exitCode
	if !errors.As(err, &code) || code != 1 {
		t.Errorf("status with a missing manifest file: err = %v, want exit 1", err)
	}
	if !strings.Contains(out.String(), "missing  ~/"+gone) {
		t.Errorf("status does not list the missing %s:\n%s", gone, out.String())
	}
}

// A manifest written before the verdict was recorded ("zip:sha256:<hex>", no
// ";…" part) still reads: status names the zip and claims no verdict.
func TestStatusLegacyZipManifest(t *testing.T) {
	isolate(t)
	defer func(v string) { version = v }(version)
	version = "v1.2.3"
	home := t.TempDir()
	if err := harness.SaveState(home, harness.State{Repo: harness.ZipPrefix + "sha256:0123456789abcdef0123"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_ = run([]string{"status", "--home", home}, &out)
	if !strings.Contains(out.String(), "from zip sha256:0123456789ab ") {
		t.Errorf("status does not name the legacy zip install:\n%s", out.String())
	}
	if strings.Contains(out.String(), "verified") {
		t.Errorf("status claims a verdict the legacy manifest never recorded:\n%s", out.String())
	}
}

func TestKnownCheckoutZip(t *testing.T) {
	isolate(t)
	home := t.TempDir()
	if err := harness.SaveState(home, harness.State{Repo: harness.ZipPrefix + "sha256:0123456789abcdef0123"}); err != nil {
		t.Fatal(err)
	}
	err := knownCheckout("", home, "/some/checkout")
	if err == nil || !strings.Contains(err.Error(), "installed from a zip (sha256 0123456789ab)") || !strings.Contains(err.Error(), "--repo /some/checkout") {
		t.Errorf("err = %v, want the zip install named and the --repo way out", err)
	}
}

func TestDiffAfterZip(t *testing.T) {
	isolate(t)
	defer func(v string) { version = v }(version)
	version = "v1.2.3"
	zp := markedZip(t)
	home := t.TempDir()
	if err := run([]string{"update", "--from-zip", zp, "--home", home}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"diff", "--home", home}, &out); err != nil {
		t.Fatalf("diff after a zip install: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "not compared") || strings.Contains(out.String(), "ZIPMARKER") {
		t.Errorf("diff compared a zip install against the embedded files:\n%s", out.String())
	}
}
