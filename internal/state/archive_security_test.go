package state

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type rawZipSpec struct {
	name string
	body []byte
	mode fs.FileMode
}

func writeCustomZip(t *testing.T, specs []rawZipSpec) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crafted.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, sp := range specs {
		fh := &zip.FileHeader{Name: sp.name, Method: zip.Deflate}
		fh.SetMode(0o600)
		if sp.mode != 0 {
			fh.SetMode(sp.mode)
		}
		w, err := zw.CreateHeader(fh)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(sp.body)
	}
	_ = zw.Close()
	_ = os.WriteFile(path, buf.Bytes(), 0o600)
	return path
}

func validManifestBytes(t *testing.T, brainSchema, metricsSchema int, brainData, metricsData []byte) []byte {
	t.Helper()
	sumHex := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	raw, err := json.Marshal(Manifest{
		FyrVersion: "v9.6.0", BrainSchema: brainSchema, MetricsSchema: metricsSchema,
		ExportedAt: "2026-11-20T12:00:00Z", RecordClasses: []string{}, RepoIDs: []string{},
		SHA256: map[string]string{BrainRecordsEntry: sumHex(brainData), MetricsSanitizedEntry: sumHex(metricsData)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertRejectedNoDBTouch(t *testing.T, bundlePath, wantSubstr string) {
	t.Helper()
	emptyHome := t.TempDir()
	_, err := Import(ImportOptions{Home: emptyHome, BundlePath: bundlePath, IncludeMemory: true})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(wantSubstr)) {
		t.Fatalf("Import(%s) err = %v, want %q", bundlePath, err, wantSubstr)
	}
	if entries, rerr := os.ReadDir(emptyHome); rerr != nil || len(entries) != 0 {
		t.Fatalf("touched target home: %v (%v)", entries, rerr)
	}
}

func TestImportRejectsInvalidArchivesBeforeOpeningDB(t *testing.T) {
	var empty []byte
	man := validManifestBytes(t, 4, 11, empty, empty)
	for _, tc := range []struct {
		name, want string
		spec       []rawZipSpec
	}{
		{"dotdot", "zip-slip", []rawZipSpec{{name: "../evil.ndjson"}}},
		{"nested dotdot", "zip-slip", []rawZipSpec{{name: "sub/../../evil"}}},
		{"absolute", "zip-slip", []rawZipSpec{{name: "/etc/passwd"}}},
		{"backslash", "backslash", []rawZipSpec{{name: `..\evil`}}},
		{"volume", "volume", []rawZipSpec{{name: "C:manifest.json", body: man}}},
		{"symlink", "symlink", []rawZipSpec{{name: ManifestEntry, body: []byte("/etc/passwd"), mode: fs.ModeSymlink | 0o777}}},
		{"directory", "zip-slip", []rawZipSpec{{name: "subdir/", mode: fs.ModeDir | 0o755}}},
		{"duplicate", "duplicate", []rawZipSpec{{name: ManifestEntry, body: man}, {name: BrainRecordsEntry}, {name: BrainRecordsEntry}, {name: MetricsSanitizedEntry}}},
		{"unexpected", "unexpected entry", []rawZipSpec{{name: ManifestEntry, body: man}, {name: BrainRecordsEntry}, {name: MetricsSanitizedEntry}, {name: "extra.json"}}},
		{"missing", "missing required entry", []rawZipSpec{{name: ManifestEntry, body: man}, {name: BrainRecordsEntry}}},
		{"tampered brain sha256", "sha256 mismatch", []rawZipSpec{{name: ManifestEntry, body: validManifestBytes(t, 4, 11, []byte("a\n"), empty)}, {name: BrainRecordsEntry, body: []byte("b\n")}, {name: MetricsSanitizedEntry}}},
		{"tampered metrics sha256", "sha256 mismatch", []rawZipSpec{{name: ManifestEntry, body: validManifestBytes(t, 4, 11, empty, []byte("a\n"))}, {name: BrainRecordsEntry}, {name: MetricsSanitizedEntry, body: []byte("b\n")}}},
		{"future brain_schema 5", "unsupported brain_schema 5", []rawZipSpec{{name: ManifestEntry, body: validManifestBytes(t, 5, 11, empty, empty)}, {name: BrainRecordsEntry}, {name: MetricsSanitizedEntry}}},
		{"future metrics_schema 12", "unsupported metrics_schema 12", []rawZipSpec{{name: ManifestEntry, body: validManifestBytes(t, 4, 12, empty, empty)}, {name: BrainRecordsEntry}, {name: MetricsSanitizedEntry}}},
		{"oversize manifest > 4 MiB", "oversize", []rawZipSpec{{name: ManifestEntry, body: append([]byte(`{"brain_schema":4`), bytes.Repeat([]byte(" "), int(maxManifestBytes)+64)...)}, {name: BrainRecordsEntry}, {name: MetricsSanitizedEntry}}},
		{"corrupt NDJSON", "brain_records.ndjson:1", []rawZipSpec{{name: ManifestEntry, body: validManifestBytes(t, 4, 11, []byte("{bad}\n"), empty)}, {name: BrainRecordsEntry, body: []byte("{bad}\n")}, {name: MetricsSanitizedEntry}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertRejectedNoDBTouch(t, writeCustomZip(t, tc.spec), tc.want)
		})
	}

	oldLimit := maxEntryBytes
	maxEntryBytes = 1024
	t.Cleanup(func() { maxEntryBytes = oldLimit })
	big := bytes.Repeat([]byte("a"), 2048)
	assertRejectedNoDBTouch(t, writeCustomZip(t, []rawZipSpec{
		{name: ManifestEntry, body: validManifestBytes(t, 4, 11, big, empty)},
		{name: BrainRecordsEntry, body: big}, {name: MetricsSanitizedEntry},
	}), "oversize")
}
