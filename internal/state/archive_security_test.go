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
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
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
		if _, err := w.Write(sp.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
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

	// Spoof UncompressedSize64 below maxEntryBytes while deflate payload decompresses to 2048 bytes:
	// validateZipEntry sees 128 <= 1024 and passes, so readCappedEntry fails closed inside archive/zip's bounded reader.
	spoofPath := writeCustomZip(t, []rawZipSpec{
		{name: ManifestEntry, body: validManifestBytes(t, 4, 11, big, empty)},
		{name: BrainRecordsEntry, body: big}, {name: MetricsSanitizedEntry},
	})
	rawZip, err := os.ReadFile(spoofPath)
	if err != nil {
		t.Fatal(err)
	}
	patched := 0
	for _, sig := range []struct {
		hdr     []byte
		nameOff int
		sizeOff int
	}{
		{[]byte{'P', 'K', 3, 4}, 30, 22},
		{[]byte{'P', 'K', 1, 2}, 46, 24},
	} {
		for pos := 0; pos < len(rawZip); {
			i := bytes.Index(rawZip[pos:], sig.hdr)
			if i < 0 {
				break
			}
			at := pos + i
			if at+sig.nameOff+len(BrainRecordsEntry) <= len(rawZip) &&
				string(rawZip[at+sig.nameOff:at+sig.nameOff+len(BrainRecordsEntry)]) == BrainRecordsEntry {
				rawZip[at+sig.sizeOff], rawZip[at+sig.sizeOff+1], rawZip[at+sig.sizeOff+2], rawZip[at+sig.sizeOff+3] = 128, 0, 0, 0
				patched++
			}
			pos = at + 4
		}
	}
	if patched != 2 {
		t.Fatalf("patched %d zip headers for %s, want 2", patched, BrainRecordsEntry)
	}
	if err := os.WriteFile(spoofPath, rawZip, 0o600); err != nil {
		t.Fatal(err)
	}
	assertRejectedNoDBTouch(t, spoofPath, "not a valid zip file")
}

func TestImportSecurityHardeningRegressions(t *testing.T) {
	empty := []byte{}
	manWithTrailing := append(validManifestBytes(t, 4, 11, empty, empty), []byte(` {"extra":true}`)...)
	assertRejectedNoDBTouch(t, writeCustomZip(t, []rawZipSpec{
		{name: ManifestEntry, body: manWithTrailing},
		{name: BrainRecordsEntry}, {name: MetricsSanitizedEntry},
	}), "trailing data")

	srcHome := t.TempDir()
	srcRepo := initGitRepo(t, "https://github.com/org/sec-state.git")
	tok := "ghp_" + strings.Repeat("a", 36)
	if _, err := brain.Add(brain.Options{Home: srcHome, Dir: srcRepo}, brain.AddInput{
		Kind: "adr", ID: "ADR-001", Title: "Token in " + srcRepo,
		Body: strings.NewReader("Path /Users/alice/work/file and token " + tok),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.Add(brain.Options{Home: srcHome, Global: true}, brain.AddInput{
		Kind: "feedback", Title: "Global feedback",
		Body: strings.NewReader("path /Users/alice/work/file"), Project: "/home/alice/local-proj",
	}); err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(t.TempDir(), "bundle.zip")
	if _, err := Export(ExportOptions{Home: srcHome, IncludeMemory: true, OutPath: bundlePath}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	var exportedNDJSON string
	for _, f := range zr.File {
		if f.Name == BrainRecordsEntry {
			rc, oerr := f.Open()
			if oerr != nil {
				t.Fatal(oerr)
			}
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(rc)
			_ = rc.Close()
			exportedNDJSON = buf.String()
		}
	}
	_ = zr.Close()
	for _, forbidden := range []string{srcRepo, "/Users/alice", "/home/alice", tok, `"scope":`} {
		if strings.Contains(exportedNDJSON, forbidden) {
			t.Fatalf("exported brain_records.ndjson leaked %q:\n%s", forbidden, exportedNDJSON)
		}
	}
	if !strings.Contains(exportedNDJSON, "~/work/file") {
		t.Fatalf("expected scrubbed home path ~/work/file in exportedNDJSON:\n%s", exportedNDJSON)
	}

	roDir := t.TempDir()
	roBundle := filepath.Join(roDir, "ro-bundle.zip")
	rawZip, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(roBundle, rawZip, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(roDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o700) })

	dstHome := t.TempDir()
	dstRepo := initGitRepo(t, "https://github.com/org/sec-state.git")
	if err := metrics.RecordTrial(dstHome, metrics.TrialInput{Run: "seed", Label: "t1", Status: "pass", Seconds: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.MemoryAdd(brain.Options{Home: dstHome, Dir: dstRepo}, brain.MemoryEntry{
		Type: "semantic", Name: "local-op", Description: "sacred", Provenance: "operator",
	}, strings.NewReader("keep local operator")); err != nil {
		t.Fatal(err)
	}
	if res, err := Import(ImportOptions{Home: dstHome, Dir: dstRepo, BundlePath: roBundle, DryRun: true}); err != nil || res.Insert != 1 {
		t.Fatalf("dry-run in sandbox with read-only bundle dir = %+v, err = %v", res, err)
	}
	sandboxPath := filepath.Join(filepath.Dir(metrics.Path(dstHome)), "sandbox")
	_ = os.RemoveAll(sandboxPath)
	if err := os.Symlink(t.TempDir(), sandboxPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(ImportOptions{Home: dstHome, Dir: dstRepo, BundlePath: roBundle, DryRun: true}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink sandbox rejection, got err = %v", err)
	}
	_ = os.Remove(sandboxPath)

	futureOp, _ := json.Marshal(brain.Record{
		RepoID: brain.ResolveRepoID(dstRepo), ID: "MEMORY-local-op", Kind: "memory", Status: "closed",
		Date: "2026-11-21", Title: "hijack", Body: "type: semantic\n\noverwritten", Provenance: "operator",
		Updated: time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	})
	hijackZip := writeCustomZip(t, []rawZipSpec{
		{name: ManifestEntry, body: validManifestBytes(t, 4, 11, append(futureOp, '\n'), empty)},
		{name: BrainRecordsEntry, body: append(futureOp, '\n')},
		{name: MetricsSanitizedEntry},
	})
	resOp, err := Import(ImportOptions{Home: dstHome, Dir: dstRepo, BundlePath: hijackZip, IncludeMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	if resOp.OperatorPreserved != 1 || resOp.Updated != 0 {
		t.Fatalf("expected OperatorPreserved=1 Updated=0, got %+v", resOp)
	}

	badRepo, _ := json.Marshal(brain.Record{
		RepoID: "/tmp/victim-repo", ID: "ADR-002", Kind: "adr", Status: "accepted",
		Date: "2026-11-21", Title: "bad repo", Body: "body",
	})
	assertRejectedNoDBTouch(t, writeCustomZip(t, []rawZipSpec{
		{name: ManifestEntry, body: validManifestBytes(t, 4, 11, append(badRepo, '\n'), empty)},
		{name: BrainRecordsEntry, body: append(badRepo, '\n')},
		{name: MetricsSanitizedEntry},
	}), "invalid repo_id")
}
