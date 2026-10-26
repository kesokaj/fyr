package main

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// zipOfTree writes tree's files into a zip under one top folder, the way
// GitHub's "Download ZIP" lays them out, and returns the zip's path.
func zipOfTree(t *testing.T, tree string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(tree, p)
		w, err := zw.Create("styrman-harness-main/" + filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zp := filepath.Join(t.TempDir(), "styrman-harness-main.zip")
	if err := os.WriteFile(zp, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return zp
}

// markedZip is this checkout's harness with a marker line in its context,
// so an install can be told apart from the embedded files.
func markedZip(t *testing.T) string {
	t.Helper()
	tree := plainCopy(t)
	ctx := filepath.Join(tree, "context", "AGENTS.md")
	b, err := os.ReadFile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ctx, append([]byte("<!-- ZIPMARKER -->\n"), b...), 0o644); err != nil {
		t.Fatal(err)
	}
	return zipOfTree(t, tree)
}

// isolate cuts a test's reach: no git or gh on PATH and a scratch working
// directory, so a regression that falls through to a real update
// cannot fetch, pull or push (BUG-094).
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("FYR_REPO", "")
	t.Setenv("PATH", t.TempDir())
	t.Chdir(t.TempDir())
}

func noZipDirsUnder(t *testing.T, home string) {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(home, harness.StateDir, "zip-*"))
	if len(left) != 0 {
		t.Errorf("unpack directories left behind: %v", left)
	}
}
