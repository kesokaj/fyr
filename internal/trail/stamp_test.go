package trail

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRegularAndFollow(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(realFile, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkFile := filepath.Join(dir, "link.txt")
	if err := os.Symlink(realFile, linkFile); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	f, err := OpenRegular(realFile)
	if err != nil {
		t.Fatalf("OpenRegular(real): %v", err)
	}
	_ = f.Close()

	if f, err := OpenRegular(linkFile); err == nil {
		_ = f.Close()
		t.Fatal("OpenRegular followed symlink")
	} else if !errors.Is(err, ErrStampSymlink) {
		t.Fatalf("OpenRegular(symlink) = %v, want ErrStampSymlink", err)
	}

	f, err = OpenRegularFollow(linkFile, true)
	if err != nil {
		t.Fatalf("OpenRegularFollow(link, true): %v", err)
	}
	_ = f.Close()

	if f, err := OpenRegular(dir); err == nil {
		_ = f.Close()
		t.Fatal("OpenRegular opened directory")
	} else if !errors.Is(err, ErrStampNotRegular) {
		t.Fatalf("OpenRegular(dir) = %v, want ErrStampNotRegular", err)
	}

	nb, err := OpenNonBlocking(realFile)
	if err != nil {
		t.Fatalf("OpenNonBlocking(real): %v", err)
	}
	_ = nb.Close()
}

func TestReadVerifyStampAndSymlinkGuards(t *testing.T) {
	root := t.TempDir()
	scratch := filepath.Join(root, ".agents", "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	stampPath := filepath.Join(root, filepath.FromSlash(VerifyStampRelPath))

	// Valid stamp (camelCase and legacy snake_case).
	if err := os.WriteFile(stampPath, []byte(`{"treeSha256":"abc","updatedAt":"2026-11-18T00:00:00Z","ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := ReadVerifyStamp(root)
	if err != nil || !st.OK || st.TreeSHA256 != "abc" {
		t.Fatalf("ReadVerifyStamp valid = %+v, err=%v", st, err)
	}

	if err := os.WriteFile(stampPath, []byte(`{"tree_sha256":"legacy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err = ReadVerifyStamp(root)
	if err != nil || st.TreeSHA256Sn != "legacy" {
		t.Fatalf("ReadVerifyStamp legacy = %+v, err=%v", st, err)
	}

	// Oversized stamp (>4096 bytes) must be rejected.
	big := `{"treeSha256":"` + strings.Repeat("a", 4096) + `"}`
	if err := os.WriteFile(stampPath, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadVerifyStamp(root); err == nil {
		t.Fatal("ReadVerifyStamp accepted > 4096 byte file")
	}

	// Symlinked stamp must return ErrStampSymlink.
	target := filepath.Join(t.TempDir(), "outside.json")
	_ = os.WriteFile(target, []byte(`{"treeSha256":"abc","ok":true}`), 0o600)
	_ = os.Remove(stampPath)
	if err := os.Symlink(target, stampPath); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := ReadVerifyStamp(root); !errors.Is(err, ErrStampSymlink) {
		t.Fatalf("ReadVerifyStamp(symlink) = %v, want ErrStampSymlink", err)
	}

	// Symlinked .agents/scratch must return ErrStampSymlink.
	_ = os.RemoveAll(scratch)
	if err := os.Symlink(t.TempDir(), scratch); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := CheckScratchNoSymlinks(root); !errors.Is(err, ErrStampSymlink) {
		t.Fatalf("CheckScratchNoSymlinks(symlink) = %v, want ErrStampSymlink", err)
	}
}
