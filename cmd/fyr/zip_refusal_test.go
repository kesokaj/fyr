package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestUpdateFromZipRefusals(t *testing.T) {
	isolate(t)
	zp := markedZip(t)
	for _, args := range [][]string{
		{"update", "--from-zip", zp, "--check"},
		{"update", "--from-zip", zp, "--repo", "../.."},
		{"install", "--from-zip", zp},
	} {
		home := t.TempDir()
		var out bytes.Buffer
		err := run(append(args, "--home", home), &out)
		// A usage error: exit 2, the reason on the writer (BUG-168).
		var code exitCode
		if !errors.As(err, &code) || code != 2 || !strings.Contains(out.String(), "from-zip") {
			t.Errorf("%v: err = %v, out = %q, want an exit-2 refusal naming --from-zip", args, err, out.String())
		}
		if entries, _ := os.ReadDir(home); len(entries) != 0 {
			t.Errorf("%v: refused, yet wrote %d entries under the home", args, len(entries))
		}
	}
}

// A refused zip writes nothing under the home, not even ~/.config/fyr.
func TestUpdateFromZipRefusedZipWritesNothing(t *testing.T) {
	isolate(t)
	zp := markedZip(t)
	zr, err := zip.OpenReader(zp)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b := new(bytes.Buffer)
		b.ReadFrom(rc)
		rc.Close()
		w.Write(b.Bytes())
	}
	zr.Close()
	w, err := zw.Create("styrman-harness-main/x/../../escape")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("x"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	slip := filepath.Join(t.TempDir(), "slip.zip")
	if err := os.WriteFile(slip, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	err = run([]string{"update", "--from-zip", slip, "--home", home}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "would escape") {
		t.Fatalf("err = %v, want the escaping entry refused", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("a refused zip left %d entries under the home", len(entries))
	}
}

// --from-zip belongs to update: every other command refuses it before doing
// anything — uninstall must not uninstall.
func TestFromZipRefusedOnOtherCommands(t *testing.T) {
	// No git and no gh on PATH: if the refusal ever regresses, nothing can
	// reach the network. A red run of this test before the refusal existed
	// pushed a real report, through the verb ADR-062 removed (BUG-094).
	isolate(t)
	for _, cmd := range []string{"install", "uninstall", "status", "diff", "list", "render", "version"} {
		home := t.TempDir()
		var out bytes.Buffer
		err := run([]string{cmd, "--from-zip", "x.zip", "--home", home}, &out)
		// A usage error: exit 2, the reason on the writer and nothing else
		// (the command printed nothing before refusing; BUG-168).
		var code exitCode
		if !errors.As(err, &code) || code != 2 {
			t.Errorf("%s --from-zip: err = %v, want exit 2", cmd, err)
		}
		if entries, _ := os.ReadDir(home); len(entries) != 0 {
			t.Errorf("%s --from-zip wrote %d entries before refusing", cmd, len(entries))
		}
		if want := "fyr " + cmd + ": --from-zip belongs to update: fyr update --from-zip <file>\n"; out.String() != want {
			t.Errorf("%s --from-zip printed before refusing, or not the refusal:\n%s", cmd, out.String())
		}
	}
}

// An empty value is refused, never read as "no --from-zip": that would fall
// through to a normal update, which on a release binary downloads and
// replaces the binary.
func TestFromZipEmptyValueRefused(t *testing.T) {
	isolate(t)
	for _, args := range [][]string{{"update", "--from-zip="}, {"update", "--from-zip", ""}} {
		home := t.TempDir()
		var out bytes.Buffer
		err := run(append(args, "--home", home), &out)
		var code exitCode
		if !errors.As(err, &code) || code != 2 || !strings.Contains(out.String(), "--from-zip needs a file") {
			t.Errorf("%q: err = %v, out = %q, want the empty-value refusal as exit 2", args, err, out.String())
		}
		if entries, _ := os.ReadDir(home); len(entries) != 0 {
			t.Errorf("%q wrote %d entries", args, len(entries))
		}
	}
}

// A zip whose content the install refuses (here a skill with no description)
// leaves a fresh home as it was: the directory chain updateFromZip created
// is removed on any failure, not only an unpack refusal (BUG-093).
func TestUpdateFromZipRefusedContentWritesNothing(t *testing.T) {
	isolate(t)
	tree := plainCopy(t)
	bad := filepath.Join(tree, "skills", "zz-broken", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("---\nname: zz-broken\n---\nno description\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	zp := zipOfTree(t, tree)
	home := t.TempDir()
	if err := run([]string{"update", "--from-zip", zp, "--home", home}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "has no description") {
		t.Fatalf("err = %v, want the install's own refusal of a skill with no description", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("a refused install left %d entries under a fresh home", len(entries))
	}
}

// A value that looks like a flag is refused: `update --from-zip --dry-run`
// must not read --dry-run as the file and run a real update.
func TestFromZipDashValueRefused(t *testing.T) {
	isolate(t)
	home := t.TempDir()
	var out bytes.Buffer
	err := run([]string{"update", "--from-zip", "--dry-run", "--home", home}, &out)
	var code exitCode
	if !errors.As(err, &code) || code != 2 || !strings.Contains(out.String(), "--from-zip needs a file") {
		t.Errorf("err = %v, out = %q, want the file-name refusal as exit 2", err, out.String())
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("wrote %d entries", len(entries))
	}
}

// A zip the INSTALL refuses (it unpacks fine) sweeps nothing either: the sweep
// sits after installFiles, not between the unpack and the install.
func TestUpdateFromZipInstallRefusalSweepsNothing(t *testing.T) {
	isolate(t)
	tree := plainCopy(t)
	bad := filepath.Join(tree, "skills", "zz-broken", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("---\nname: zz-broken\n---\nno description\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	zp := zipOfTree(t, tree)
	home := t.TempDir()
	stale := filepath.Join(home, harness.StateDir, "zip-old")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"update", "--from-zip", zp, "--home", home}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "has no description") {
		t.Fatalf("err = %v, want the install's own refusal of a skill with no description", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("an install refusal swept %s", stale)
	}
}
