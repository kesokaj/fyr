package search

import (
	"bytes"
	"io"
	"os"
)

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
