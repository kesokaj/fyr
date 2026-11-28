package report

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRegularNoSymlinkFileMustPassAndMustFail(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	sub := filepath.Join(base, "a", "b")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	realFile := filepath.Join(sub, "ok.json")
	if err := os.WriteFile(realFile, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := RegularNoSymlinkFile(base, "a/./b", "ok.json")
	if !ok || got != realFile {
		t.Fatalf("RegularNoSymlinkFile valid = %q, %v; want %q, true", got, ok, realFile)
	}
	if _, ok := RegularNoSymlinkFile("", "a", "b", "ok.json"); ok {
		t.Error("empty base should fail")
	}
	if _, ok := RegularNoSymlinkFile(base, ".", ""); ok {
		t.Error("empty segments should fail")
	}
	if _, ok := RegularNoSymlinkFile(base, "a", "b"); ok {
		t.Error("directory leaf should fail")
	}
	if _, ok := RegularNoSymlinkFile(base, "a", "b", "ok.json", "nested"); ok {
		t.Error("file as intermediate dir should fail")
	}
	linkFile := filepath.Join(base, "link.json")
	if err := os.Symlink(realFile, linkFile); err == nil {
		if _, ok := RegularNoSymlinkFile(base, "link.json"); ok {
			t.Error("symlink leaf should fail")
		}
	}
	linkDir := filepath.Join(base, "linkdir")
	if err := os.Symlink(sub, linkDir); err == nil {
		if _, ok := RegularNoSymlinkFile(base, "linkdir", "ok.json"); ok {
			t.Error("symlink parent dir should fail")
		}
	}
	data, ok := ReadBoundedRegularFile(base, "a", "b", "ok.json")
	if !ok || !bytes.Equal(data, []byte(`{"ok":true}`)) {
		t.Errorf("ReadBoundedRegularFile = %q, %v", data, ok)
	}
	big := filepath.Join(base, "big.bin")
	if err := os.WriteFile(big, make([]byte, HealthFileLimit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadBoundedRegularFile(base, "big.bin"); ok {
		t.Error("oversized file should fail")
	}
}
