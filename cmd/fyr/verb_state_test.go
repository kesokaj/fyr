package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/state"
)

func runStateCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(args, &out)
	return out.String(), err
}

func TestStateRealHomeGuardRefusesWithoutHome(t *testing.T) {
	home := guardHome(t, "")
	bundle := filepath.Join(t.TempDir(), "b.zip")
	for _, args := range [][]string{
		{"state", "export"}, {"state", "export", "--out", bundle},
		{"state", "import", bundle}, {"state", "import", bundle, "--dry-run"},
	} {
		if out, err := runStateCLI(t, args...); !isGuardRefusal(err, out) {
			t.Errorf("fyr %v: err=%v out=%q; want BUG-507 refusal", args, err, out)
		}
	}
	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		if e.Name() != ".local" {
			t.Errorf("realhome_guard refusal touched %s in home", e.Name())
		}
	}
}

func TestStateUsageErrorsExit2(t *testing.T) {
	home := t.TempDir()
	for name, args := range map[string][]string{
		"bare state":          {"state", "--home", home},
		"unknown subverb":     {"state", "bogus", "--home", home},
		"export stray arg":    {"state", "export", "extra", "--home", home},
		"export dry-run":      {"state", "export", "--dry-run", "--home", home},
		"export bad since":    {"state", "export", "--since", "not-a-date", "--home", home},
		"export empty out":    {"state", "export", "--out", "", "--home", home},
		"export empty repo":   {"state", "export", "--repo", "", "--home", home},
		"import missing arg":  {"state", "import", "--home", home},
		"import two args":     {"state", "import", "a.zip", "b.zip", "--home", home},
		"import takes no out": {"state", "import", "a.zip", "--out", "b.zip", "--home", home},
		"import no since":     {"state", "import", "a.zip", "--since", "2026-11-20", "--home", home},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runStateCLI(t, args...)
			var code exitCode
			if !errors.As(err, &code) || code != 2 || !strings.Contains(out, "usage: fyr state") {
				t.Fatalf("fyr %v: err=%v\n%s", args, err, out)
			}
		})
	}
}

func TestStateCLIRoundTripMemoryOptInDryRunIdempotentAndOperatorProvenance(t *testing.T) {
	srcHome, projA, projB := t.TempDir(), recordRepo(t), recordRepo(t)
	runGitCmd(t, projA, "remote", "add", "origin", "git@github.com:org/alpha.git")
	runGitCmd(t, projB, "remote", "add", "origin", "https://github.com/org/beta.git")
	tOld := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	tNew := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	_, _ = brain.Add(brain.Options{Home: srcHome, Dir: projA, Now: func() time.Time { return tOld }}, brain.AddInput{Kind: "adr", ID: "ADR-001", Title: "Old alpha ADR", Body: strings.NewReader("Early decision")})
	_, _ = brain.Add(brain.Options{Home: srcHome, Dir: projA, Now: func() time.Time { return tNew }}, brain.AddInput{Kind: "change", Title: "New alpha change", Body: strings.NewReader("Added Pulsar pipeline")})
	_, _ = brain.Add(brain.Options{Home: srcHome, Dir: projB, Now: func() time.Time { return tNew }}, brain.AddInput{Kind: "bug", ID: "BUG-001", Title: "Beta bug", Body: strings.NewReader("Beta issue")})
	_, _ = brain.MemoryAdd(brain.Options{Home: srcHome, Dir: projA, Now: func() time.Time { return tNew }}, brain.MemoryEntry{Type: "semantic", Name: "shared-key", Description: "derived src", Provenance: "derived"}, strings.NewReader("derived text from src"))
	if err := metrics.RecordTrial(srcHome, metrics.TrialInput{Run: "cli/alpha/1", Label: "eval=alpha", Status: "pass", StartedAt: tNew, Seconds: 3, Model: "gemini-4-neon-high", ProjectRoot: projA}); err != nil {
		t.Fatal(err)
	}

	defaultZip := filepath.Join(t.TempDir(), "default.zip")
	if out, err := runStateCLI(t, "state", "export", "--out", defaultZip, "--home", srcHome, "-C", projA); err != nil || !strings.Contains(out, "include_memory=false") || !strings.Contains(out, "brain_records=3") {
		t.Fatalf("state export default: %v\n%s", err, out)
	}
	memZip := filepath.Join(t.TempDir(), "mem.zip")
	jsonOut, err := runStateCLI(t, "state", "export", "--include-memory", "--out", memZip, "--json", "--home", srcHome, "-C", projA)
	if err != nil {
		t.Fatal(err)
	}
	var expRes state.ExportResult
	if err := json.Unmarshal([]byte(jsonOut), &expRes); err != nil || !expRes.Manifest.IncludeMemory || expRes.Manifest.Counts.BrainRecords != 4 {
		t.Fatalf("manifest = %+v (%v)", expRes.Manifest, err)
	}
	sinceOut, err := runStateCLI(t, "state", "export", "--repo", "current", "--since", "2026-11-15", "--out", filepath.Join(t.TempDir(), "since.zip"), "--json", "--home", srcHome, "-C", projA)
	if err != nil {
		t.Fatal(err)
	}
	var sinceRes state.ExportResult
	if err := json.Unmarshal([]byte(sinceOut), &sinceRes); err != nil || sinceRes.Manifest.Counts.BrainRecords != 1 {
		t.Fatalf("since export = %+v (%v)", sinceRes, err)
	}

	dstHome, dstProjA := t.TempDir(), recordRepo(t)
	runGitCmd(t, dstProjA, "remote", "add", "origin", "git@github.com:org/alpha.git")
	if dryOut, err := runStateCLI(t, "state", "import", memZip, "--include-memory", "--dry-run", "--home", dstHome, "-C", dstProjA); err != nil || !strings.Contains(dryOut, "[dry-run] state import: insert=6") {
		t.Fatalf("dry-run: %v\n%s", err, dryOut)
	}
	if entries, _ := os.ReadDir(dstHome); len(entries) != 0 {
		t.Fatalf("dry-run wrote into dstHome: %v", entries)
	}
	_, _ = brain.MemoryAdd(brain.Options{Home: dstHome, Dir: dstProjA, Now: func() time.Time { return tOld }}, brain.MemoryEntry{Type: "semantic", Name: "shared-key", Description: "operator dst", Provenance: "operator"}, strings.NewReader("operator body on dst"))
	if impNoMem, err := runStateCLI(t, "state", "import", memZip, "--repo", "current", "--home", dstHome, "-C", dstProjA); err != nil || !strings.Contains(impNoMem, "insert=4") {
		t.Fatalf("import --repo current: %v\n%s", err, impNoMem)
	}
	if impWithMem, err := runStateCLI(t, "state", "import", memZip, "--repo", "current", "--include-memory", "--home", dstHome, "-C", dstProjA); err != nil || !strings.Contains(impWithMem, "conflict=1 operator_preserved=1") {
		t.Fatalf("import --include-memory: %v\n%s", err, impWithMem)
	}
	if impIdem, err := runStateCLI(t, "state", "import", memZip, "--repo", "current", "--home", dstHome, "-C", dstProjA); err != nil || !strings.Contains(impIdem, "insert=0 updated=0 skip_duplicate=4 conflict=0") {
		t.Fatalf("idempotent import: %v\n%s", err, impIdem)
	}
	if searchOut, err := runStateCLI(t, "search", "Pulsar", "--home", dstHome, "-C", dstProjA); err != nil || !strings.Contains(searchOut, "New alpha change") {
		t.Errorf("search after import: %v\n%s", err, searchOut)
	}
}

func TestStateCLIRejectsCorruptZipSlipAndFutureSchema(t *testing.T) {
	writeZip := func(entries map[string][]byte, symlink bool) string {
		p := filepath.Join(t.TempDir(), "bad.zip")
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, body := range entries {
			fh := &zip.FileHeader{Name: name, Method: zip.Deflate}
			fh.SetMode(0o600)
			if symlink {
				fh.SetMode(fs.ModeSymlink | 0o777)
			}
			w, _ := zw.CreateHeader(fh)
			_, _ = w.Write(body)
		}
		_ = zw.Close()
		_ = os.WriteFile(p, buf.Bytes(), 0o600)
		return p
	}
	s := sha256.Sum256(nil)
	eh := hex.EncodeToString(s[:])
	for name, zp := range map[string]string{
		"corrupt sha256": writeZip(map[string][]byte{"manifest.json": []byte(`{"brain_schema":4,"metrics_schema":11,"sha256":{"brain_records.ndjson":"` + eh + `","metrics_sanitized.ndjson":"` + eh + `"}}`), "brain_records.ndjson": []byte("tampered\n"), "metrics_sanitized.ndjson": nil}, false),
		"zip-slip":       writeZip(map[string][]byte{"../escape.txt": []byte("evil")}, false),
		"future schema":  writeZip(map[string][]byte{"manifest.json": []byte(`{"brain_schema":99,"metrics_schema":11,"sha256":{"brain_records.ndjson":"` + eh + `","metrics_sanitized.ndjson":"` + eh + `"}}`), "brain_records.ndjson": nil, "metrics_sanitized.ndjson": nil}, false),
		"symlink entry":  writeZip(map[string][]byte{"manifest.json": []byte("/etc/passwd")}, true),
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if _, err := runStateCLI(t, "state", "import", zp, "--home", home); err == nil {
				t.Fatalf("expected error for %s", name)
			}
			if entries, _ := os.ReadDir(home); len(entries) != 0 {
				t.Errorf("%s touched target home: %v", name, entries)
			}
		})
	}
}

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(gitCleanEnv(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
