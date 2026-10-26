package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// A zip whose SHA256SUMS beside it lists another hash for it is refused, and
// the home stays as it was.
func TestUpdateFromZipWrongChecksumRefused(t *testing.T) {
	isolate(t)
	zp := markedZip(t)
	dir := filepath.Dir(zp)
	sums := "# fyr v9.9.9\n" + strings.Repeat("0", 64) + "  " + filepath.Base(zp) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	var out bytes.Buffer
	err := run([]string{"update", "--from-zip", zp, "--home", home}, &out)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want the checksum refusal\n%s", err, out.String())
	}
	if strings.Contains(strings.ToLower(err.Error()), "unverified") {
		t.Errorf("a failed-check refusal suggests installing unverified (ADR-039): %v", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("a refused zip left %d entries", len(entries))
	}
}

// A zip with nothing beside it installs, marked UNVERIFIED.
func TestUpdateFromZipUnsignedSaysUnverified(t *testing.T) {
	isolate(t)
	zp := markedZip(t)
	var out bytes.Buffer
	if err := run([]string{"update", "--from-zip", zp, "--home", t.TempDir()}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "UNVERIFIED") || strings.Contains(out.String(), "CHECKED against") {
		t.Errorf("an unsigned zip is not marked UNVERIFIED:\n%s", out.String())
	}
}

// A signature alone beside a zip, the shape of a pre-ADR-053 release folder
// missing its SHA256SUMS, is not a checksum file: the zip installs, marked
// UNVERIFIED (R9, ADR-041, ADR-053).
func TestUpdateFromZipSignatureAloneSaysUnverified(t *testing.T) {
	isolate(t)
	zp := markedZip(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(zp), "SHA256SUMS.sig"), []byte("not a signature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"update", "--from-zip", zp, "--home", home}, &out); err != nil {
		t.Fatalf("a zip with only a signature beside it was refused: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "UNVERIFIED") || strings.Contains(out.String(), "CHECKED against") {
		t.Errorf("not marked UNVERIFIED:\n%s", out.String())
	}
	st, found, err := harness.LoadState(home)
	if err != nil || !found || !strings.HasSuffix(st.Repo, ";unverified") {
		t.Errorf("manifest repo = %q (found=%v, err=%v), want the ;unverified verdict", st.Repo, found, err)
	}
}

// A release zip with its SHA256SUMS beside it installs and says VERIFIED; the
// same zip with one byte changed is refused, home untouched.
func TestUpdateFromZipVerifiedAndTampered(t *testing.T) {
	isolate(t)
	t.Setenv("PATH", t.TempDir())
	src := markedZip(t)
	dir := t.TempDir()
	zp := filepath.Join(dir, "fyr-v1.2.3.zip")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zp, data, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte("# fyr v1.2.3\n"+hex.EncodeToString(sum[:])+"  fyr-v1.2.3.zip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	vhome := t.TempDir()
	if err := run([]string{"update", "--from-zip", zp, "--home", vhome}, &out); err != nil {
		t.Fatalf("verified zip: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "CHECKED against the SHA256SUMS of release v1.2.3 (integrity only)") {
		t.Errorf("a verified zip does not say so:\n%s", out.String())
	}
	// The verdict outlives the output: status reads it from the manifest. It
	// needs a release version to resolve a source, as in TestStatusAfterZip.
	defer func(v string) { version = v }(version)
	version = "v1.2.3"
	var st bytes.Buffer
	if err := run([]string{"status", "--home", vhome}, &st); err != nil {
		t.Fatalf("status after a verified zip: %v\n%s", err, st.String())
	}
	if !strings.Contains(st.String(), "(verified v1.2.3)") {
		t.Errorf("status does not record the verified install:\n%s", st.String())
	}

	data[len(data)/2] ^= 0xff
	if err := os.WriteFile(zp, data, 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	err = run([]string{"update", "--from-zip", zp, "--home", home}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("tampered zip: err = %v, want checksum mismatch", err)
	}
	if strings.Contains(fmt.Sprint(err), "move SHA256SUMS") {
		t.Errorf("the refusal suggests removing the checksums: %v", err)
	}
	if strings.Contains(strings.ToLower(fmt.Sprint(err)), "unverified") {
		t.Errorf("a failed-check refusal suggests installing unverified (ADR-039): %v", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("a tampered zip left %d entries", len(entries))
	}
}
