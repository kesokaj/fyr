//go:build unix

package harness

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// readPlain checked the path with Lstat and then read it by name, so a
// symlink or FIFO swapped in between was followed or blocked (security
// review 2026-10-25, finding 3). openPlain judges the file it opened.
func TestOpenPlainJudgesTheOpenedFile(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("not yours\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "deployed")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the fixture is not a symlink (%v), so it proves nothing", err)
	}
	if b, err := openPlain(link); err == nil {
		t.Errorf("openPlain followed a symlink and read %q", b)
	}
	plain := filepath.Join(dir, "plain")
	os.WriteFile(plain, []byte("ok\n"), 0o644)
	if b, err := openPlain(plain); err != nil || string(b) != "ok\n" {
		t.Errorf("openPlain(regular) = %q, %v", b, err)
	}
	if _, err := openPlain(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("openPlain(missing) err = %v, want ErrNotExist", err)
	}
}
