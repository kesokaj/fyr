package state

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type verifiedArchive struct {
	manifest                 Manifest
	brainBytes, metricsBytes []byte
}

var allowedBundleEntries = map[string]bool{
	ManifestEntry: true, BrainRecordsEntry: true, MetricsSanitizedEntry: true,
}

func readAndVerifyArchive(bundlePath string) (verifiedArchive, error) {
	rawZip, err := readBundleFile(bundlePath)
	if err != nil {
		return verifiedArchive{}, err
	}
	zr, err := zip.NewReader(bytes.NewReader(rawZip), int64(len(rawZip)))
	if err != nil {
		return verifiedArchive{}, fmt.Errorf("state import: %s: invalid zip archive: %w", bundlePath, err)
	}
	contents, actualSHA, err := extractCheckedEntries(zr)
	if err != nil {
		return verifiedArchive{}, fmt.Errorf("state import: %s: %w", bundlePath, err)
	}
	manifest, err := parseAndValidateManifest(contents[ManifestEntry], actualSHA)
	if err != nil {
		return verifiedArchive{}, fmt.Errorf("state import: %s: %w", bundlePath, err)
	}
	return verifiedArchive{
		manifest: manifest, brainBytes: contents[BrainRecordsEntry], metricsBytes: contents[MetricsSanitizedEntry],
	}, nil
}

func readBundleFile(bundlePath string) ([]byte, error) {
	if strings.TrimSpace(bundlePath) == "" {
		return nil, errors.New("state import: bundle path required")
	}
	f, err := os.Open(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("state import: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("state import: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("state import: %s is not a regular file", bundlePath)
	}
	if fi.Size() > maxArchiveBytes {
		return nil, fmt.Errorf("state import: %s exceeds maximum archive size (%d bytes)", bundlePath, maxArchiveBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxArchiveBytes+1))
	if err != nil || int64(len(data)) > maxArchiveBytes {
		if err != nil {
			return nil, fmt.Errorf("state import: %w", err)
		}
		return nil, fmt.Errorf("state import: %s exceeds maximum archive size (%d bytes)", bundlePath, maxArchiveBytes)
	}
	return data, nil
}

func extractCheckedEntries(zr *zip.Reader) (map[string][]byte, map[string]string, error) {
	seen, contents, actualSHA := map[string]bool{}, map[string][]byte{}, map[string]string{}
	var total int64
	for _, f := range zr.File {
		limit, err := validateZipEntry(f, seen)
		if err != nil {
			return nil, nil, err
		}
		data, sum, err := readCappedEntry(f, limit)
		if err != nil {
			return nil, nil, err
		}
		if total += int64(len(data)); total > maxTotalBytes {
			return nil, nil, fmt.Errorf("total uncompressed size exceeds %d bytes", maxTotalBytes)
		}
		contents[f.Name] = data
		if f.Name != ManifestEntry {
			actualSHA[f.Name] = sum
		}
	}
	for req := range allowedBundleEntries {
		if !seen[req] {
			return nil, nil, fmt.Errorf("missing required entry %q", req)
		}
	}
	return contents, actualSHA, nil
}

func validateZipEntry(f *zip.File, seen map[string]bool) (int64, error) {
	if err := validateZipEntryName(f.Name); err != nil {
		return 0, err
	}
	if mode := f.Mode(); mode&fs.ModeSymlink != 0 {
		return 0, fmt.Errorf("entry %q is a symlink (zip-slip refused)", f.Name)
	} else if mode.IsDir() || !mode.IsRegular() {
		return 0, fmt.Errorf("entry %q is not a regular file", f.Name)
	}
	if seen[f.Name] {
		return 0, fmt.Errorf("duplicate entry %q", f.Name)
	}
	seen[f.Name] = true
	if !allowedBundleEntries[f.Name] {
		return 0, fmt.Errorf("unexpected entry %q", f.Name)
	}
	limit := maxEntryBytes
	if f.Name == ManifestEntry {
		limit = maxManifestBytes
	}
	if f.UncompressedSize64 > uint64(limit) {
		return 0, fmt.Errorf("entry %q is oversize (%d > %d bytes)", f.Name, f.UncompressedSize64, limit)
	}
	return limit, nil
}

func validateZipEntryName(name string) error {
	if name == "" || len(name) > 256 || strings.Contains(name, `\`) {
		return fmt.Errorf("entry %q has invalid or backslash path (zip-slip refused)", name)
	}
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return fmt.Errorf("entry %q has absolute or directory path (zip-slip refused)", name)
	}
	if filepath.VolumeName(name) != "" || (len(name) >= 2 && name[1] == ':') {
		return fmt.Errorf("entry %q has volume prefix (zip-slip refused)", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("entry %q contains control character", name)
		}
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("entry %q contains traversal segment (zip-slip refused)", name)
		}
	}
	return nil
}

func readCappedEntry(f *zip.File, limit int64) ([]byte, string, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, "", fmt.Errorf("entry %q: %w", f.Name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, "", fmt.Errorf("entry %q: %w", f.Name, err)
	}
	if int64(len(data)) > limit {
		return nil, "", fmt.Errorf("entry %q is oversize (exceeds %d bytes)", f.Name, limit)
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

func parseAndValidateManifest(raw []byte, actualSHA map[string]string) (Manifest, error) {
	var m Manifest
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("invalid manifest.json: %w", err)
	}
	if m.BrainSchema < 0 || m.BrainSchema > MaxBrainSchema {
		return Manifest{}, fmt.Errorf("unsupported brain_schema %d (supported <= %d); update fyr", m.BrainSchema, MaxBrainSchema)
	}
	if m.MetricsSchema < 0 || m.MetricsSchema > MaxMetricsSchema {
		return Manifest{}, fmt.Errorf("unsupported metrics_schema %d (supported <= %d); update fyr", m.MetricsSchema, MaxMetricsSchema)
	}
	if len(m.SHA256) == 0 {
		return Manifest{}, errors.New("manifest.json missing sha256 digests")
	}
	for name := range m.SHA256 {
		if name != BrainRecordsEntry && name != MetricsSanitizedEntry {
			return Manifest{}, fmt.Errorf("manifest.json lists unexpected sha256 entry %q", name)
		}
	}
	for _, req := range []string{BrainRecordsEntry, MetricsSanitizedEntry} {
		want := strings.ToLower(strings.TrimSpace(m.SHA256[req]))
		if len(want) != 64 || subtle.ConstantTimeCompare([]byte(want), []byte(actualSHA[req])) != 1 {
			return Manifest{}, fmt.Errorf("sha256 mismatch for %s (declared %q, computed %q)", req, want, actualSHA[req])
		}
	}
	return m, nil
}
