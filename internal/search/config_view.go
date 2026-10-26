package search

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// configView lists the global index's own sources in ~/.config/fyr/,
// keyed by their SHA-256 since git does not hold them.
func configView(dir string) ([]entry, error) {
	names := []string{"feedback.jsonl", "retros.jsonl"}
	// Listed by name, never through a pattern built from the config path: a
	// `[` in it would be read as a character class (the golang rule).
	if entries, err := os.ReadDir(filepath.Join(dir, "proposals")); err == nil {
		var props []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				props = append(props, "proposals/"+e.Name())
			}
		}
		sort.Strings(props)
		names = append(names, props...)
	}
	// Plain reads: ~/.config/fyr is the operator's own state, and a
	// dotfiles arrangement may symlink its proposals directory.
	var out []entry
	for _, n := range names {
		fi, err := os.Stat(filepath.Join(dir, n))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		out = append(out, entry{path: n, oid: "sha256:" + hex.EncodeToString(sum[:]), state: "tracked"})
	}
	return out, nil
}

// readAll reads one file through the root, so the read cannot leave it. On
// a Go before 1.25.12, os.Root.Open carries advisory GO-2026-4970 (an escape
// through a path ending in "/"); every call here follows an Lstat of the
// same path that reported a regular file, which such a path never is.
func readAll(fsRoot *os.Root, rel string) ([]byte, error) {
	f, err := fsRoot.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// readIndexable returns a file's text, or ok=false when it is too large,
// binary, gone, or outside the root.
func readIndexable(fsRoot *os.Root, rel string) (string, bool) {
	fi, err := fsRoot.Lstat(rel)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFileBytes {
		return "", false
	}
	data, err := readAll(fsRoot, rel)
	if err != nil {
		return "", false
	}
	sniff := data
	if len(sniff) > binarySniff {
		sniff = sniff[:binarySniff]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return "", false
	}
	return string(data), true
}
