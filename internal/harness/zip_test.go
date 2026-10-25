package harness

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zipEntry struct {
	name string
	body string
	mode fs.FileMode // 0 means a regular 0o644 file
}

// writeZip builds an archive at path from entries, in order.
func writeZip(t *testing.T, path string, entries []zipEntry) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// harnessEntries is newRepo's tree as zip entries under prefix.
func harnessEntries(t *testing.T, prefix string) []zipEntry {
	t.Helper()
	repo := newRepo(t)
	var entries []zipEntry
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(repo, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		entries = append(entries, zipEntry{name: prefix + filepath.ToSlash(rel), body: string(data)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// noZipDirs asserts UnpackZip left no zip-* directory under parent.
func noZipDirs(t *testing.T, parent string) {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(parent, "zip-*"))
	if len(left) != 0 {
		t.Errorf("temporary directories left under %s: %v", parent, left)
	}
}

func TestUnpackZipGitHubTopFolder(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "styrman-harness-main.zip")
	entries := harnessEntries(t, "styrman-harness-main/")
	entries = append(entries,
		zipEntry{name: "styrman-harness-main/README.md", body: "readme\n"},
		zipEntry{name: "styrman-harness-main/cmd/fyr/main.go", body: "package main\n"},
		// This repository's own tracked symlink: GitHub's zip keeps it as a
		// symlink entry, and it sits outside the five trees (D6).
		zipEntry{name: "styrman-harness-main/.agents/skills", body: "../skills", mode: fs.ModeSymlink | 0o777},
	)
	writeZip(t, zp, entries)
	parent := t.TempDir()

	tree, _, err := UnpackZip(zp, parent, nil)
	if err != nil {
		t.Fatalf("UnpackZip: %v", err)
	}
	if !isRepo(tree) {
		t.Fatalf("%s is not harness-shaped after unpacking", tree)
	}
	if !strings.HasPrefix(tree, parent+string(filepath.Separator)) {
		t.Errorf("tree %s is not under the parent %s", tree, parent)
	}
	for _, rel := range []string{"skills/alpha/references/notes.md", "agents/reviewer/agent.md", "commands/deploy.md", "rules/go-style.md", "context/dev-setup.md"} {
		if _, err := os.Stat(filepath.Join(tree, rel)); err != nil {
			t.Errorf("%s not unpacked: %v", rel, err)
		}
	}
	// Outside the five trees: name-checked, never written.
	for _, rel := range []string{"README.md", "cmd", ".agents"} {
		if _, err := os.Lstat(filepath.Join(tree, rel)); err == nil {
			t.Errorf("%s was written, but it is outside the five trees", rel)
		}
	}
}

func TestUnpackZipRootLayout(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "flat.zip")
	writeZip(t, zp, harnessEntries(t, ""))
	tree, _, err := UnpackZip(zp, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("UnpackZip: %v", err)
	}
	if !isRepo(tree) {
		t.Fatalf("%s is not harness-shaped", tree)
	}
}

func TestUnpackZipRefuses(t *testing.T) {
	base := func(t *testing.T) []zipEntry { return harnessEntries(t, "top/") }
	cases := []struct {
		name  string
		extra zipEntry
		want  string
	}{
		{"parent element", zipEntry{name: "top/../../escape", body: "x"}, "escape"},
		{"absolute name", zipEntry{name: "/etc/escape", body: "x"}, "escape"},
		{"backslash name", zipEntry{name: `top\..\escape`, body: "x"}, "escape"},
		{"symlink inside a tree", zipEntry{name: "top/skills/alpha/link", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777}, "symlink"},
		{"device-like mode inside a tree", zipEntry{name: "top/skills/alpha/pipe", body: "", mode: fs.ModeNamedPipe | 0o644}, "not a regular file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			zp := filepath.Join(dir, "bad.zip")
			writeZip(t, zp, append(base(t), c.extra))
			// Prove the fixture holds the condition before trusting the refusal.
			zr, err := zip.OpenReader(zp)
			if err != nil {
				t.Fatal(err)
			}
			last := zr.File[len(zr.File)-1]
			if last.Name != c.extra.name || (c.extra.mode != 0 && last.Mode().Type() != c.extra.mode.Type()) {
				t.Fatalf("fixture: last entry %q mode %v, want %q mode %v", last.Name, last.Mode(), c.extra.name, c.extra.mode)
			}
			zr.Close()

			parent := t.TempDir()
			tree, _, err := UnpackZip(zp, parent, nil)
			if err == nil {
				t.Fatalf("UnpackZip accepted %s (tree %s)", c.name, tree)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name %q", err, c.want)
			}
			noZipDirs(t, parent)
			if _, err := os.Lstat(filepath.Join(filepath.Dir(parent), "escape")); err == nil {
				t.Errorf("an escaping entry was written beside the parent")
			}
		})
	}
}

func TestUnpackZipLimits(t *testing.T) {
	saved := [3]int64{int64(maxZipEntries), maxZipFileBytes, maxZipTotalBytes}
	t.Cleanup(func() {
		maxZipEntries, maxZipFileBytes, maxZipTotalBytes = int(saved[0]), saved[1], saved[2]
	})

	t.Run("too many entries", func(t *testing.T) {
		entries := harnessEntries(t, "top/")
		maxZipEntries = len(entries) + 2
		for i := 0; i < 3; i++ {
			entries = append(entries, zipEntry{name: "top/docs/f" + string(rune('a'+i)), body: "x"})
		}
		zp := filepath.Join(t.TempDir(), "many.zip")
		writeZip(t, zp, entries)
		parent := t.TempDir()
		if _, _, err := UnpackZip(zp, parent, nil); err == nil || !strings.Contains(err.Error(), "entries") {
			t.Errorf("err = %v, want an entry-count refusal", err)
		}
		noZipDirs(t, parent)
	})
	maxZipEntries = int(saved[0])

	t.Run("one file too large", func(t *testing.T) {
		maxZipFileBytes = 1024
		entries := append(harnessEntries(t, "top/"), zipEntry{name: "top/skills/alpha/big.md", body: strings.Repeat("a", 2048)})
		zp := filepath.Join(t.TempDir(), "big.zip")
		writeZip(t, zp, entries)
		parent := t.TempDir()
		if _, _, err := UnpackZip(zp, parent, nil); err == nil || !strings.Contains(err.Error(), "big.md") {
			t.Errorf("err = %v, want a per-file size refusal naming big.md", err)
		}
		noZipDirs(t, parent)
	})
	maxZipFileBytes = saved[1]

	t.Run("total too large", func(t *testing.T) {
		entries := harnessEntries(t, "top/")
		var total int64
		for _, e := range entries {
			total += int64(len(e.body))
		}
		maxZipTotalBytes = total + 100
		entries = append(entries, zipEntry{name: "top/skills/alpha/more.md", body: strings.Repeat("b", 200)})
		zp := filepath.Join(t.TempDir(), "total.zip")
		writeZip(t, zp, entries)
		parent := t.TempDir()
		if _, _, err := UnpackZip(zp, parent, nil); err == nil || !strings.Contains(err.Error(), "total") {
			t.Errorf("err = %v, want a total-size refusal", err)
		}
		noZipDirs(t, parent)
	})
}

func TestUnpackZipNotHarness(t *testing.T) {
	zp := filepath.Join(t.TempDir(), "other.zip")
	writeZip(t, zp, []zipEntry{{name: "proj/README.md", body: "x"}, {name: "proj/main.go", body: "package main"}})
	parent := t.TempDir()
	if _, _, err := UnpackZip(zp, parent, nil); err == nil || !strings.Contains(err.Error(), "not a harness zip") {
		t.Errorf("err = %v, want not a harness zip", err)
	}
	noZipDirs(t, parent)

	notZip := filepath.Join(t.TempDir(), "plain.zip")
	if err := os.WriteFile(notZip, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := UnpackZip(notZip, parent, nil); err == nil || !strings.Contains(err.Error(), "not a readable zip") {
		t.Errorf("err = %v, want not a readable zip", err)
	}
	noZipDirs(t, parent)
}

func TestInstallRecordsZipOrigin(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	src := DirSource(repo)
	src.Origin = ZipPrefix + "sha256:abc123"
	files, err := Plan(src, []byte("#!/bin/sh\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(src, home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	st, found, err := LoadState(home)
	if err != nil || !found {
		t.Fatalf("manifest: found=%v err=%v", found, err)
	}
	if st.Repo != src.Origin || st.Commit != "" {
		t.Errorf("manifest repo=%q commit=%q, want %q and no commit — never the unpack directory %s", st.Repo, st.Commit, src.Origin, repo)
	}
}

func TestResolveSourceSkipsZipManifest(t *testing.T) {
	t.Setenv("FYR_REPO", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	if _, err := ResolveSource("", t.TempDir(), ""); err == nil {
		t.Skip("the temp directory happens to be inside a harness repo")
	}
	origin := ZipPrefix + "sha256:0123456789abcdef"
	// A harness-shaped directory whose relative name IS the manifest value:
	// a walk that treated the value as a path would find it here.
	decoy := filepath.Join(cwd, origin)
	if err := os.MkdirAll(filepath.Join(decoy, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(decoy, "context"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(decoy, "context", "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isRepo(origin) {
		t.Fatalf("fixture: %s must look like a harness tree relative to %s", origin, cwd)
	}
	home := t.TempDir()
	if err := SaveState(home, State{Repo: origin}); err != nil {
		t.Fatal(err)
	}
	src, err := ResolveSource("", home, "")
	if err == nil {
		t.Fatalf("ResolveSource returned %+v from a zip manifest value", src)
	}
	if !strings.Contains(err.Error(), "installed from a zip (sha256 0123456789ab)") || !strings.Contains(err.Error(), "update --from-zip") {
		t.Errorf("err = %v, want it to name the zip install and the --from-zip way forward first", err)
	}
	if !strings.Contains(err.Error(), "harness repo not found") {
		t.Errorf("err = %v, want the existing not-found message kept", err)
	}
}

func TestUnpackZipRefusesDuplicateName(t *testing.T) {
	entries := harnessEntries(t, "top/")
	entries = append(entries, zipEntry{name: "top/skills/alpha/SKILL.md", body: "---\nname: alpha\ndescription: second copy\n---\n"})
	zp := filepath.Join(t.TempDir(), "dup.zip")
	writeZip(t, zp, entries)
	zr, err := zip.OpenReader(zp)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, f := range zr.File {
		if f.Name == "top/skills/alpha/SKILL.md" {
			seen++
		}
	}
	zr.Close()
	if seen != 2 {
		t.Fatalf("fixture: %d entries named top/skills/alpha/SKILL.md, want 2", seen)
	}
	parent := t.TempDir()
	if _, _, err := UnpackZip(zp, parent, nil); err == nil || !strings.Contains(err.Error(), "SKILL.md") {
		t.Errorf("err = %v, want the duplicate entry refused, never overwritten", err)
	}
	noZipDirs(t, parent)
}

// A refusal found after the names pass — a symlink, a duplicate, a size —
// must still leave no parent directory behind (BUG-093's second half).
func TestUnpackZipLateRefusalCreatesNoParent(t *testing.T) {
	cases := map[string]struct {
		entry zipEntry
		want  string
	}{
		"symlink":   {zipEntry{name: "top/skills/alpha/link", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777}, "is a symlink"},
		"duplicate": {zipEntry{name: "top/skills/alpha/SKILL.md", body: "---\nname: alpha\ndescription: again\n---\n"}, "appears twice"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			zp := filepath.Join(t.TempDir(), "late.zip")
			writeZip(t, zp, append(harnessEntries(t, "top/"), c.entry))
			home := t.TempDir()
			parent := filepath.Join(home, StateDir)
			if _, _, err := UnpackZip(zp, parent, nil); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want the %q refusal", err, c.want)
			}
			if entries, _ := os.ReadDir(home); len(entries) != 0 {
				t.Errorf("a late refusal left %d entries under the home (the parent chain)", len(entries))
			}
		})
	}
}

func TestUnpackZipRefusesDeepOrLongNames(t *testing.T) {
	deep := "top/" + strings.Repeat("d/", 40) + "f"
	long := "top/" + strings.Repeat("n", 1100)
	for label, name := range map[string]string{"deep": deep, "long": long} {
		t.Run(label, func(t *testing.T) {
			zp := filepath.Join(t.TempDir(), "names.zip")
			writeZip(t, zp, append(harnessEntries(t, "top/"), zipEntry{name: name, body: "x"}))
			parent := t.TempDir()
			if _, _, err := UnpackZip(zp, parent, nil); err == nil || !strings.Contains(err.Error(), "too ") {
				t.Errorf("err = %v, want a too-deep or too-long refusal", err)
			}
		})
	}
}

// The sum names the very bytes that were unpacked: one read, hashed and
// parsed from the same buffer.
func TestUnpackZipSumIsOfTheBytesRead(t *testing.T) {
	zp := filepath.Join(t.TempDir(), "sum.zip")
	writeZip(t, zp, harnessEntries(t, "top/"))
	data, err := os.ReadFile(zp)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(data))
	_, sum, err := UnpackZip(zp, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum != want {
		t.Errorf("sum = %q, want %q", sum, want)
	}
}

func TestUnpackZipRefusesOversizedArchive(t *testing.T) {
	saved := maxZipArchiveBytes
	t.Cleanup(func() { maxZipArchiveBytes = saved })
	zp := filepath.Join(t.TempDir(), "whole.zip")
	writeZip(t, zp, harnessEntries(t, "top/"))
	maxZipArchiveBytes = 100
	if _, _, err := UnpackZip(zp, t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "archive") {
		t.Errorf("err = %v, want the archive-size refusal", err)
	}
}

// The Antigravity CLI's token file is secret-shaped: a skill tree carrying
// one is refused like a key file.
func TestSecretShapedCLITokenFiles(t *testing.T) {
	for _, name := range []string{"antigravity-oauth-token", ".credentials.json"} {
		if !SecretShaped(name) {
			t.Errorf("SecretShaped(%q) = false, want true", name)
		}
	}
}

// releaseZipDir builds a release-shaped folder: the zip and its SHA256SUMS
// naming it under the release marker. No key is involved (ADR-053).
func releaseZipDir(t *testing.T, tag string) (dir, zipPath string) {
	t.Helper()
	dir = t.TempDir()
	name := "fyr-" + tag + ".zip"
	zipPath = filepath.Join(dir, name)
	writeZip(t, zipPath, harnessEntries(t, "fyr-"+tag+"/"))
	writeSums(t, dir, name, tag)
	return dir, zipPath
}

func zipSum(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func TestVerifyZipSums(t *testing.T) {
	t.Run("verified", func(t *testing.T) {
		_, zp := releaseZipDir(t, "v1.2.3")
		tag, err := VerifyZipSums(zp, zipSum(t, zp))
		if err != nil || tag != "v1.2.3" {
			t.Errorf("tag=%q err=%v, want v1.2.3 and no error", tag, err)
		}
	})
	t.Run("no sums", func(t *testing.T) {
		zp := filepath.Join(t.TempDir(), "styrman-harness-main.zip")
		writeZip(t, zp, harnessEntries(t, "styrman-harness-main/"))
		if _, err := VerifyZipSums(zp, zipSum(t, zp)); !errors.Is(err, ErrZipNoSums) {
			t.Errorf("err = %v, want ErrZipNoSums", err)
		}
	})
	// A signature beside the zip, from a release before ADR-053, changes
	// nothing: only SHA256SUMS is read.
	t.Run("stray signature ignored", func(t *testing.T) {
		dir, zp := releaseZipDir(t, "v1.2.3")
		writeFile(t, filepath.Join(dir, "SHA256SUMS.sig"), "not a signature\n")
		if tag, err := VerifyZipSums(zp, zipSum(t, zp)); err != nil || tag != "v1.2.3" {
			t.Errorf("tag=%q err=%v, want v1.2.3 and no error", tag, err)
		}
	})
	t.Run("checksum mismatch", func(t *testing.T) {
		_, zp := releaseZipDir(t, "v1.2.3")
		if _, err := VerifyZipSums(zp, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Errorf("err = %v, want checksum mismatch", err)
		}
	})
	t.Run("stale sums of another release", func(t *testing.T) {
		dir, _ := releaseZipDir(t, "v1.2.3")
		newer := filepath.Join(dir, "fyr-v1.3.0.zip")
		writeZip(t, newer, harnessEntries(t, "fyr-v1.3.0/"))
		_, err := VerifyZipSums(newer, zipSum(t, newer))
		if err == nil || !strings.Contains(err.Error(), "v1.2.3") || !strings.Contains(err.Error(), "its own release") {
			t.Errorf("err = %v, want it to say the sums are v1.2.3's and to pair the zip with its own release's", err)
		}
		if err != nil && strings.Contains(err.Error(), "download that release") {
			t.Errorf("the refusal points at the stale release: %v", err)
		}
	})
	t.Run("sig present, sums missing: no sums", func(t *testing.T) {
		dir, zp := releaseZipDir(t, "v1.2.3")
		writeFile(t, filepath.Join(dir, "SHA256SUMS.sig"), "not a signature\n")
		if err := os.Remove(filepath.Join(dir, "SHA256SUMS")); err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyZipSums(zp, zipSum(t, zp)); !errors.Is(err, ErrZipNoSums) {
			t.Errorf("err = %v, want ErrZipNoSums", err)
		}
	})
	t.Run("sums unreadable: refused, not absent", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads through permissions")
		}
		dir, zp := releaseZipDir(t, "v1.2.3")
		// A folder that cannot be searched: stat fails with a permission
		// error, which is not "the files are absent".
		locked := filepath.Join(dir, "locked")
		if err := os.Mkdir(locked, 0o755); err != nil {
			t.Fatal(err)
		}
		inner := filepath.Join(locked, filepath.Base(zp))
		if err := os.Rename(zp, inner); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"SHA256SUMS"} {
			if err := os.Rename(filepath.Join(dir, f), filepath.Join(locked, f)); err != nil {
				t.Fatal(err)
			}
		}
		sum := zipSum(t, inner)
		if err := os.Chmod(locked, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o755) })
		if _, statErr := os.Stat(filepath.Join(locked, "SHA256SUMS")); statErr == nil || errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("fixture: stat must fail with a non-absence error, got %v", statErr)
		}
		if _, err := VerifyZipSums(inner, sum); err == nil || errors.Is(err, ErrZipNoSums) {
			t.Errorf("err = %v, want a refusal, never ErrZipNoSums", err)
		}
	})
	t.Run("renamed zip", func(t *testing.T) {
		dir, zp := releaseZipDir(t, "v1.2.3")
		renamed := filepath.Join(dir, "fyr-v1.2.3 (1).zip")
		if err := os.Rename(zp, renamed); err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyZipSums(renamed, zipSum(t, renamed)); err == nil || !strings.Contains(err.Error(), "no line names") {
			t.Errorf("err = %v, want no line names", err)
		}
	})
	// GitHub's own "Source code (zip)" beside the release's sums: a different
	// name and different bytes. Renaming it only trades one refusal for a
	// checksum mismatch, so both refusals name the file the release published.
	t.Run("github source archive", func(t *testing.T) {
		dir, _ := releaseZipDir(t, "v1.2.3")
		src := filepath.Join(dir, "fyr-1.2.3.zip")
		writeZip(t, src, harnessEntries(t, "fyr-1.2.3/"))
		_, err := VerifyZipSums(src, zipSum(t, src))
		if err == nil || !strings.Contains(err.Error(), "publishes fyr-v1.2.3.zip") {
			t.Errorf("err = %v, want it to name the zip the release publishes", err)
		}
		renamed := filepath.Join(dir, "fyr-v1.2.3.zip")
		if err := os.Rename(src, renamed); err != nil {
			t.Fatal(err)
		}
		_, err = VerifyZipSums(renamed, zipSum(t, renamed))
		if err == nil || !strings.Contains(err.Error(), "checksum mismatch") || !strings.Contains(err.Error(), "download fyr-v1.2.3.zip from the release's assets") {
			t.Errorf("err = %v, want a mismatch that names the release's own zip", err)
		}
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "unverified") {
			t.Errorf("the refusal suggests installing unverified (a bypass): %v", err)
		}
	})
}

// A check's error stops UnpackZip before anything is written.
func TestUnpackZipCheckStopsBeforeWriting(t *testing.T) {
	zp := filepath.Join(t.TempDir(), "z.zip")
	writeZip(t, zp, harnessEntries(t, "top/"))
	parent := t.TempDir()
	stop := errors.New("refused by the check")
	var seen string
	_, _, err := UnpackZip(zp, parent, func(sum string) error { seen = sum; return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("err = %v, want the check's error unchanged", err)
	}
	if seen != zipSum(t, zp) {
		t.Errorf("check saw %q, want the sha256 of the file", seen)
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 0 {
		t.Errorf("a refusing check left %d entries", len(entries))
	}
}
