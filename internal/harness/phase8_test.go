package harness

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// No verb may write under the system temp directory: scratch is the project's
// or the home's, never /tmp (global Hard rules; IN10). A bare "" as the
// MkdirTemp/CreateTemp dir means the system temp directory.
func TestNoSystemTempDirInSource(t *testing.T) {
	// Every Go package of the module, so a new package cannot bring the system
	// temp directory back unnoticed.
	for _, root := range []string{"../../cmd", "../../internal"} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, needle := range []string{`MkdirTemp("", `, `CreateTemp("", `} {
				if strings.Contains(string(src), needle) {
					t.Errorf("%s uses the system temp directory (%s)", path, needle)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// watchedTempDir points TMPDIR at an empty directory the test owns, so a
// write to the system temp directory shows up as an entry there.
func watchedTempDir(t *testing.T) string {
	t.Helper()
	w := filepath.Join(t.TempDir(), "systmp")
	if err := os.Mkdir(w, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", w)
	return w
}

func assertEmpty(t *testing.T, dir, what string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("%s holds %v, want nothing", what, names)
	}
}

// VerifyZipSums writes nothing: not under the system temp directory, not
// beside the zip, on either exit path.
func TestVerifyZipSumsWritesNothingUnderTempDir(t *testing.T) {
	watched := watchedTempDir(t)
	dir, zp := releaseZipDir(t, "v1.2.3")
	before, _ := os.ReadDir(dir)
	if _, err := VerifyZipSums(zp, zipSum(t, zp)); err != nil {
		t.Fatalf("VerifyZipSums: %v", err)
	}
	assertEmpty(t, watched, "the system temp directory after a verified zip")
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Errorf("the zip's folder holds %d entries after verification, had %d: something was left behind", len(after), len(before))
	}
	// The error path: a checksum that cannot match still leaves nothing.
	if _, err := VerifyZipSums(zp, strings.Repeat("0", 64)); err == nil {
		t.Fatal("a wrong checksum verified")
	}
	assertEmpty(t, watched, "the system temp directory after a refused zip")
	after, _ = os.ReadDir(dir)
	if len(after) != len(before) {
		t.Errorf("the zip's folder holds %d entries after a refusal, had %d", len(after), len(before))
	}
}

// An empty parent unpacks beside the zip (the no-config dry run), not under
// the system temp directory; a refused zip leaves nothing there either.
func TestUnpackZipEmptyParentUnpacksBesideTheZip(t *testing.T) {
	watched := watchedTempDir(t)
	zdir := t.TempDir()
	zp := filepath.Join(zdir, "styrman-harness-main.zip")
	writeZip(t, zp, harnessEntries(t, "styrman-harness-main/"))
	tree, _, err := UnpackZip(zp, "", nil)
	if err != nil {
		t.Fatalf("UnpackZip: %v", err)
	}
	if filepath.Dir(tree) != zdir {
		t.Errorf("tree %s is not beside the zip in %s", tree, zdir)
	}
	assertEmpty(t, watched, "the system temp directory after an unpack with an empty parent")
	if err := os.RemoveAll(tree); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("refused by the check")
	if _, _, err := UnpackZip(zp, "", func(string) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("err = %v, want the check's error", err)
	}
	entries, _ := os.ReadDir(zdir)
	if len(entries) != 1 {
		t.Errorf("a refused unpack left %d entries beside the zip, want the zip alone", len(entries))
	}
	assertEmpty(t, watched, "the system temp directory after a refused unpack")
}

// readCapped is the one capped file read: it refuses a file over the cap
// before reading it and returns a smaller one whole.
func TestReadCappedRefusesOversize(t *testing.T) {
	dir := t.TempDir()
	small, big := filepath.Join(dir, "small"), filepath.Join(dir, "big")
	if err := os.WriteFile(small, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(big, make([]byte, 33), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readCapped(small, 32)
	if err != nil || string(got) != "ok" {
		t.Fatalf("readCapped(small) = %q, %v", got, err)
	}
	if _, err := readCapped(big, 32); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("readCapped(big) err = %v, want a refusal naming the cap", err)
	}
	if _, err := readCapped(filepath.Join(dir, "missing"), 32); err == nil {
		t.Fatal("a missing file read")
	}
}

// parseStrict lints and parses in one pass over the block: the frontmatter
// a lenient parse would return, or the lint error.
func TestParseStrict(t *testing.T) {
	good := "name: x\ndescription: \"a: b\"\n"
	fm, err := parseStrict(good)
	if err != nil {
		t.Fatalf("parseStrict(good): %v", err)
	}
	if want := Parse(good); fm["description"] != want["description"] || fm["name"] != want["name"] {
		t.Errorf("parseStrict = %v, Parse = %v", fm, want)
	}
	bad := "name: x\ndescription: a #b\n"
	_, strictErr := parseStrict(bad)
	if strictErr == nil || !strings.Contains(strictErr.Error(), "#") {
		t.Fatalf("parseStrict(bad) err = %v, want the comment refusal", strictErr)
	}
}

// MkdirAllTracked creates only the missing directories and returns them
// outermost first; RemoveCreated removes exactly those, so a refused unpack
// leaves a pre-existing parent as it was.
func TestMkdirAllTrackedAndRemoveCreated(t *testing.T) {
	base := t.TempDir()
	existing := filepath.Join(base, "keep")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(existing, "a", "b", "c")
	created, err := MkdirAllTracked(target)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(existing, "a"), filepath.Join(existing, "a", "b"), target}
	if len(created) != len(want) {
		t.Fatalf("created %v, want %v", created, want)
	}
	for i := range want {
		if created[i] != want[i] {
			t.Fatalf("created[%d] = %s, want %s", i, created[i], want[i])
		}
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("target not created: %v", err)
	}
	RemoveCreated(created)
	if _, err := os.Stat(filepath.Join(existing, "a")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("RemoveCreated left %s", filepath.Join(existing, "a"))
	}
	if _, err := os.Stat(existing); err != nil {
		t.Errorf("RemoveCreated removed the pre-existing parent %s", existing)
	}
	if c, err := MkdirAllTracked(""); err != nil || c != nil {
		t.Errorf("MkdirAllTracked(\"\") = %v, %v; want nil, nil", c, err)
	}
	if c, err := MkdirAllTracked(existing); err != nil || len(c) != 0 {
		t.Errorf("MkdirAllTracked(existing) = %v, %v; want nothing created", c, err)
	}
}

// A stream whose Stat size says nothing still fails closed one byte past the
// cap: a FIFO fed max+1 bytes is refused, never cut short.
func TestReadCappedRefusesOversizeStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs")
	}
	dir := t.TempDir()
	fifo := filepath.Join(dir, "in")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	const max = 8
	go func() {
		w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		w.Write([]byte(strings.Repeat("x", max+1)))
		w.Close()
	}()
	if _, err := readCapped(fifo, max); err == nil {
		t.Fatal("a stream one byte past the cap was read in full")
	}
}
