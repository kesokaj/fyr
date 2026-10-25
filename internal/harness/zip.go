package harness

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ZipPrefix marks a manifest's repo value written by an install from a zip:
// "zip:sha256:<hex>;verified vX.Y.Z" or "zip:sha256:<hex>;unverified" names
// the archive the operator passed and whether it was checked against a
// release's SHA256SUMS, never a path — the unpacked tree is deleted
// once the install is done (ADR-038, ADR-039). A manifest written before the
// verdict was recorded carries the bare "zip:sha256:<hex>".
const ZipPrefix = "zip:"

// Limits on an archive handed to UnpackZip. Variables so tests can lower
// them; sizes count the bytes actually written, never a header's claim.
var (
	maxZipEntries          = 5000
	maxZipFileBytes  int64 = 16 << 20
	maxZipTotalBytes int64 = 128 << 20
	// maxZipArchiveBytes caps the file read into memory: it is hashed and
	// parsed from one buffer, so the sum names the bytes that were unpacked.
	maxZipArchiveBytes int64 = 64 << 20
	// Names are bounded too: the size limits count file bytes, and deep
	// names would otherwise create directories without bound.
	maxZipNameBytes = 1024
	maxZipNameDepth = 32
)

// zipTrees are the only top-level directories an install reads — the same
// five a release binary embeds. Nothing else in the archive is written.
var zipTrees = map[string]bool{"context": true, "skills": true, "agents": true, "commands": true, "rules": true}

// UnpackZip unpacks the harness in the zip at zipPath into a new directory
// under parent and returns it with the archive's sha256; the caller removes
// the directory, and creates parent beforehand. The file is read once, and
// the sum and the unpack both come from that one buffer. Everything is
// checked before anything is written:
// every entry's name (relative, no "..", no backslash, bounded length and
// depth), the entry count, and for the five trees the entry kinds, duplicate
// names and sizes, counted by decompressing. GitHub's single top folder is
// stripped; entries outside the five trees are never written. On any error
// the directory this call made is removed.
func UnpackZip(zipPath, parent string, check func(sum string) error) (dir, sum string, err error) {
	data, err := readCapped(zipPath, maxZipArchiveBytes)
	if err != nil {
		return "", "", fmt.Errorf("%s: reading the archive: %w", zipPath, err)
	}
	h := sha256.Sum256(data)
	sum = hex.EncodeToString(h[:])
	// The check — the release's SHA256SUMS — sees the hash of the very bytes
	// that will be unpacked, and a refusal leaves nothing behind.
	if check != nil {
		if err := check(sum); err != nil {
			return "", "", err
		}
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", "", fmt.Errorf("%s: not a readable zip: %w", zipPath, err)
	}
	if len(zr.File) > maxZipEntries {
		return "", "", fmt.Errorf("%s: %d entries, more than the %d allowed", zipPath, len(zr.File), maxZipEntries)
	}
	for _, f := range zr.File {
		if err := zipNameSafe(f.Name); err != nil {
			return "", "", fmt.Errorf("%s: entry %q would escape the unpack directory: %v", zipPath, f.Name, err)
		}
	}
	prefix, ok := zipRoot(zr.File)
	if !ok {
		return "", "", fmt.Errorf("%s: not a harness zip: no context/AGENTS.md at its root or under one top folder", zipPath)
	}
	plan, err := checkZipTrees(zipPath, zr.File, prefix)
	if err != nil {
		return "", "", err
	}

	// The parent must already exist: the caller creates it and removes it
	// again on any failure of the whole install, which can still refuse the
	// zip after this returns (BUG-093). An empty parent means beside the zip —
	// the no-config dry run — never the system temp directory (IN10).
	if parent == "" {
		parent = filepath.Dir(zipPath)
	}
	tmp, err := os.MkdirTemp(parent, "zip-*")
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			if rerr := os.RemoveAll(tmp); rerr != nil {
				err = errors.Join(err, fmt.Errorf("could not remove %s: %w", tmp, rerr))
			}
		}
	}()
	var total int64
	for _, e := range plan {
		dest := filepath.Join(tmp, filepath.FromSlash(e.rel))
		if e.file == nil {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return "", "", err
			}
			continue
		}
		n, err := unpackFile(e.file, dest)
		if err != nil {
			return "", "", fmt.Errorf("%s: entry %q: %w", zipPath, e.file.Name, err)
		}
		if total += n; total > maxZipTotalBytes {
			return "", "", fmt.Errorf("%s: more than %d bytes in total under the harness trees", zipPath, maxZipTotalBytes)
		}
	}
	if !isRepo(tmp) {
		return "", "", fmt.Errorf("%s: not a harness zip: the unpacked tree has no context/AGENTS.md or skills/", zipPath)
	}
	return tmp, sum, nil
}

// zipItem is one entry to write: a directory when file is nil.
type zipItem struct {
	rel  string
	file *zip.File
}

// checkZipTrees validates the entries under the five trees and returns what
// to write, in archive order. Nothing is written: sizes are counted by
// decompressing into io.Discard, so a header that understates a size is
// caught here too.
func checkZipTrees(zipPath string, files []*zip.File, prefix string) ([]zipItem, error) {
	var plan []zipItem
	seen := map[string]bool{}
	var total int64
	for _, f := range files {
		rel := strings.TrimSuffix(strings.TrimPrefix(f.Name, prefix), "/")
		if rel == "" || !zipTrees[strings.SplitN(rel, "/", 2)[0]] {
			continue
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			plan = append(plan, zipItem{rel: rel})
			continue
		case mode&fs.ModeSymlink != 0:
			return nil, fmt.Errorf("%s: entry %q is a symlink — the harness ships regular files only", zipPath, f.Name)
		case !mode.IsRegular():
			return nil, fmt.Errorf("%s: entry %q is not a regular file — the harness ships regular files only", zipPath, f.Name)
		}
		if seen[rel] {
			return nil, fmt.Errorf("%s: entry %q appears twice — refused rather than overwritten", zipPath, f.Name)
		}
		seen[rel] = true
		n, err := entrySize(f)
		if err != nil {
			return nil, fmt.Errorf("%s: entry %q: %w", zipPath, f.Name, err)
		}
		if total += n; total > maxZipTotalBytes {
			return nil, fmt.Errorf("%s: more than %d bytes in total under the harness trees", zipPath, maxZipTotalBytes)
		}
		plan = append(plan, zipItem{rel: rel, file: f})
	}
	return plan, nil
}

// entrySize decompresses f into io.Discard, reading at most one byte past the
// per-file limit, and returns the byte count.
func entrySize(f *zip.File) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	return copyCapped(io.Discard, rc, maxZipFileBytes)
}

// copyCapped copies r into w, reading at most one byte past max, and refuses
// an entry that exceeds it — the one place the per-file cap is applied.
func copyCapped(w io.Writer, r io.Reader, max int64) (int64, error) {
	n, err := io.Copy(w, io.LimitReader(r, max+1))
	if err != nil {
		return n, err
	}
	if n > max {
		return n, fmt.Errorf("%w (%d bytes)", errTooLarge, max)
	}
	return n, nil
}

// MkdirAllTracked is os.MkdirAll that returns the directories it created,
// outermost first, so a caller can remove exactly those again (RemoveCreated).
// "" creates nothing.
func MkdirAllTracked(dir string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	var missing []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil {
			break
		}
		missing = append(missing, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	var created []string
	for k := len(missing) - 1; k >= 0; k-- {
		err := os.Mkdir(missing[k], 0o700)
		if errors.Is(err, fs.ErrExist) {
			continue // made by someone else meanwhile: not ours to remove
		}
		if err != nil {
			RemoveCreated(created)
			return nil, err
		}
		created = append(created, missing[k])
	}
	return created, nil
}

// RemoveCreated removes what MkdirAllTracked created, deepest first; a
// directory that is no longer empty is left alone.
func RemoveCreated(created []string) {
	for k := len(created) - 1; k >= 0; k-- {
		os.Remove(created[k])
	}
}

var errTooLarge = errors.New("larger than the per-file limit")

// unpackFile writes one entry to dest, counting what is written: at most
// maxZipFileBytes+1 bytes are read, so a header that understates the size
// cannot carry more through.
func unpackFile(f *zip.File, dest string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	// O_EXCL: a name that appears twice is refused, never overwritten.
	w, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := copyCapped(w, rc, maxZipFileBytes)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// zipNameSafe refuses a name that is absolute, holds a backslash, or has a
// ".." element: any of them could land a file outside the unpack directory.
func zipNameSafe(name string) error {
	if len(name) > maxZipNameBytes {
		return fmt.Errorf("a name too long (more than %d bytes)", maxZipNameBytes)
	}
	if strings.Count(strings.TrimSuffix(name, "/"), "/") >= maxZipNameDepth {
		return fmt.Errorf("a name too deep (%d or more levels)", maxZipNameDepth)
	}
	if strings.Contains(name, `\`) {
		return errors.New("a backslash in the name")
	}
	if strings.HasPrefix(name, "/") {
		return errors.New("an absolute name")
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return errors.New("a .. element")
		}
	}
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed != "" && !filepath.IsLocal(filepath.FromSlash(trimmed)) {
		return errors.New("not a local path")
	}
	return nil
}

// zipRoot finds where the harness sits in the archive: at its root, or under
// the single top folder every entry shares (GitHub's layout). It returns the
// prefix to strip.
func zipRoot(files []*zip.File) (string, bool) {
	names := make(map[string]bool, len(files))
	tops := map[string]bool{}
	for _, f := range files {
		names[f.Name] = true
		tops[strings.SplitN(f.Name, "/", 2)[0]] = true
	}
	if names["context/AGENTS.md"] {
		return "", true
	}
	if len(tops) == 1 {
		for top := range tops {
			if names[top+"/context/AGENTS.md"] {
				return top + "/", true
			}
		}
	}
	return "", false
}

// ErrZipNoSums: no SHA256SUMS beside a zip — a zip of a branch, which no
// release lists, or a release zip downloaded alone. The caller installs it
// marked UNVERIFIED (R9, ADR-041, ADR-053).
var ErrZipNoSums = errors.New("no SHA256SUMS beside the zip")

// VerifyZipSums checks a release zip against the SHA256SUMS beside it: the
// zip's own line, by its file name, against sum. It returns the release the
// file names. No SHA256SUMS is ErrZipNoSums; anything else that fails is a
// refusal (plan D7).
func VerifyZipSums(zipPath, sum string) (string, error) {
	dir := filepath.Dir(zipPath)
	// Only "does not exist" means there are none. Any other error (a folder
	// that cannot be read, say) is a refusal: an unreadable checksum file is
	// not the absence of one.
	if _, err := os.Stat(filepath.Join(dir, "SHA256SUMS")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrZipNoSums
		}
		return "", fmt.Errorf("cannot check for checksums beside %s: %w", filepath.Base(zipPath), err)
	}
	what := filepath.Base(zipPath)
	sums, tag, err := readSums(dir, what)
	if err != nil {
		return "", err
	}
	want, err := checksumFor(string(sums), what)
	if err != nil {
		return "", fmt.Errorf("the SHA256SUMS beside it are release %s's and do not list %s: %w — release %s publishes %s: if that is the release you want, download that file from its assets (GitHub's \"Source code\" archive is a different file); otherwise put this zip in a folder with its own release's SHA256SUMS", tag, what, err, tag, zipNamesIn(sums))
	}
	if want != sum {
		return "", fmt.Errorf("checksum mismatch for %s against the SHA256SUMS of %s: the zip is not the one that release published — download %s from the release's assets again (GitHub's \"Source code\" archive is a different file)", what, tag, zipNamesIn(sums))
	}
	return tag, nil
}

// zipNamesIn lists the .zip files a SHA256SUMS names, for a refusal that has
// to say which file the release actually published.
func zipNamesIn(sums []byte) string {
	var names []string
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.HasSuffix(f[1], ".zip") {
			names = append(names, strings.TrimPrefix(f[1], "*"))
		}
	}
	if len(names) == 0 {
		return "no zip"
	}
	return strings.Join(names, ", ")
}
