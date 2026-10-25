package harness

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	fyr "github.com/kesokaj/fyr"
)

// Source is where the harness's own files are read from: a tree on disk (a
// checkout or a plain copy), or the files embedded in a release binary
// (ADR-033). Every reader takes one, so both install the same way.
type Source struct {
	// FS holds context/, skills/, agents/, commands/, workflows/ and rules/,
	// addressed with slash-separated paths.
	FS fs.FS
	// Dir is the tree's path on disk, or "" for the embedded files. Only a
	// tree on disk can hold a link, so the link refusals (BUG-036) run when
	// Dir is set; embed cannot store one.
	Dir string
	// Version is the release the embedded files came with, and "" for a tree
	// on disk, whose git state says what it is.
	Version string
	// Origin, when set, is what the manifest records as the source instead of
	// Dir: a zip's tree is unpacked into a directory that is deleted once the
	// install is done, so the manifest names the archive (ZipPrefix).
	Origin string
}

// DirSource reads the tree at dir.
func DirSource(dir string) Source {
	return Source{FS: os.DirFS(dir), Dir: dir}
}

// embeddedSource reads the files built into this binary, which is the
// release named by version.
func embeddedSource(version string) Source {
	return Source{FS: fyr.Files, Version: version}
}

// EmbeddedPrefix marks a manifest's repo value written by an install from the
// embedded files: "embedded:vX.Y.Z" names a release, never a path.
const EmbeddedPrefix = "embedded:"

// Canonical only: `v1.02.2` compared equal to `v1.2.2` and reached the
// download (security audit 2026-09-18).
var releaseVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// IsReleaseVersion reports whether a binary's version is a release tag. Only
// a release binary installs its embedded files: a development build carries
// whatever the tree held when it was built, which is stale the moment the
// tree moves on (D14).
func IsReleaseVersion(version string) bool {
	if !releaseVersion.MatchString(version) {
		return false
	}
	// A component that does not fit an int would compare as 0.
	for _, part := range strings.Split(strings.TrimPrefix(version, "v"), ".") {
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

// path names a file for a message: its disk path for a tree on disk, its
// path inside the source otherwise.
func (s Source) path(name string) string {
	if s.Dir == "" {
		return name
	}
	return filepath.Join(s.Dir, filepath.FromSlash(name))
}

// readRegular reads a file the source ships, refusing a link or anything
// that is not a regular file. checkManagedTree covers the managed directories
// and every entry in them; this guards the one read it cannot reach,
// skills/<name>/SKILL.md, because skills/<name> may itself be a link that
// os.DirFS follows (BUG-036).
func (s Source) readRegular(name string) ([]byte, error) {
	var info fs.FileInfo
	var err error
	if s.Dir != "" {
		info, err = os.Lstat(s.path(name))
	} else {
		info, err = fs.Stat(s.FS, name)
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink — the repo ships regular files only", s.path(name))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file — the repo ships regular files only", s.path(name))
	}
	return fs.ReadFile(s.FS, name)
}
