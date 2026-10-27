package harness

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// The arms of UnpackZip's write loop that its split moves into a helper,
// pinned before the split (PLAN-2026-10-27-funclen Phase 5, D13). Each one
// leaves no directory behind. Exempt (D12): the write loop's own total check,
// which no archive reaches: it counts the same buffer's entries a second
// time, and checkZipTrees has already refused a total over the same cap.

// A directory entry whose path an earlier file entry already holds fails the
// unpack with the MkdirAll error itself.
func TestUnpackZipDirOverFile(t *testing.T) {
	zp := filepath.Join(t.TempDir(), "h.zip")
	writeZip(t, zp, append(harnessEntries(t, ""),
		zipEntry{name: "skills/a", body: "x"},
		zipEntry{name: "skills/a/", mode: fs.ModeDir | 0o755}))
	parent := t.TempDir()
	_, _, err := UnpackZip(zp, parent, nil)
	var pe *fs.PathError
	if !errors.As(err, &pe) || pe.Op != "mkdir" || strings.Contains(err.Error(), zp) {
		t.Errorf("err = %v, want the bare mkdir error", err)
	}
	noZipDirs(t, parent)
}

// A file entry whose path an earlier directory entry already holds fails the
// unpack, naming the entry.
func TestUnpackZipFileOverDir(t *testing.T) {
	zp := filepath.Join(t.TempDir(), "h.zip")
	writeZip(t, zp, append(harnessEntries(t, ""),
		zipEntry{name: "skills/b/", mode: fs.ModeDir | 0o755},
		zipEntry{name: "skills/b", body: "x"}))
	parent := t.TempDir()
	_, _, err := UnpackZip(zp, parent, nil)
	if err == nil || !strings.Contains(err.Error(), zp+`: entry "skills/b": `) || !errors.Is(err, fs.ErrExist) {
		t.Errorf("err = %v, want the entry named and the O_EXCL refusal", err)
	}
	noZipDirs(t, parent)
}

// An archive with context/AGENTS.md but no skills/ passes every check before
// the write and is refused after it.
func TestUnpackZipNoSkillsTree(t *testing.T) {
	zp := filepath.Join(t.TempDir(), "h.zip")
	writeZip(t, zp, []zipEntry{{name: "context/AGENTS.md", body: "# AGENTS.md\n"}})
	parent := t.TempDir()
	_, _, err := UnpackZip(zp, parent, nil)
	if err == nil || !strings.Contains(err.Error(), "the unpacked tree has no context/AGENTS.md or skills/") {
		t.Errorf("err = %v, want the unpacked-tree refusal", err)
	}
	noZipDirs(t, parent)
}

// The check sees the sum before the archive is parsed: a file that is not a
// zip at all is refused for its sum, never for its format (ADR-039).
func TestUnpackZipChecksBeforeParsing(t *testing.T) {
	zp := filepath.Join(t.TempDir(), "h.zip")
	writeFile(t, zp, "not a zip")
	refused := errors.New("checksum refused")
	parent := t.TempDir()
	if _, _, err := UnpackZip(zp, parent, func(string) error { return refused }); !errors.Is(err, refused) {
		t.Errorf("err = %v, want the check's refusal", err)
	}
	noZipDirs(t, parent)
}
