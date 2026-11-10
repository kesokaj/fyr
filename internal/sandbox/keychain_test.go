package sandbox

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestDecodeKeychainToken(t *testing.T) {
	good := `{"access_token":"ya29.secret-tok","refresh_token":"1//ref","token_type":"Bearer"}`
	encoded := keychainPrefix + base64.StdEncoding.EncodeToString([]byte(good))
	got, err := DecodeKeychainToken("  " + encoded + "\n")
	if err != nil {
		t.Fatalf("DecodeKeychainToken: %v", err)
	}
	if string(got) != good+"\n" {
		t.Errorf("got %q, want %q", got, good+"\n")
	}

	// Extra fields in Keychain JSON are stripped on re-marshaling, while nested
	// StoredToken ({"token":{...},"auth_method":...}) is preserved.
	withExtra := `{"access_token":"ya29.secret-tok","extra":"dropped","refresh_token":"1//ref","token_type":"Bearer"}`
	gotExtra, err := DecodeKeychainToken(keychainPrefix + base64.StdEncoding.EncodeToString([]byte(withExtra)))
	if err != nil || string(gotExtra) != good+"\n" {
		t.Errorf("withExtra: got %q, %v; want %q", gotExtra, err, good+"\n")
	}
	nested := `{"token":{"access_token":"ya29.secret-tok","refresh_token":"1//ref","token_type":"Bearer"},"auth_method":"oauth"}`
	gotNested, err := DecodeKeychainToken(keychainPrefix + base64.StdEncoding.EncodeToString([]byte(nested)))
	if err != nil || string(gotNested) != nested+"\n" {
		t.Errorf("nested: got %q, %v; want %q", gotNested, err, nested+"\n")
	}

	for name, raw := range map[string]string{
		"empty":          "",
		"missing prefix": base64.StdEncoding.EncodeToString([]byte(good)),
		"bad base64":     keychainPrefix + "not-base64-!!!-secret-value",
		"json array":     keychainPrefix + base64.StdEncoding.EncodeToString([]byte(`["secret-value"]`)),
		"invalid json":   keychainPrefix + base64.StdEncoding.EncodeToString([]byte(`{secret-value}`)),
		"missing fields": keychainPrefix + base64.StdEncoding.EncodeToString([]byte(`{"other":"secret-value"}`)),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeKeychainToken(raw)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if strings.Contains(err.Error(), "secret-value") || strings.Contains(err.Error(), "ya29") {
				t.Errorf("error leaked secret: %v", err)
			}
		})
	}
}

func TestCheckLoginMacKeychainTokenSync(t *testing.T) {
	tmp := outsideTemp(t)
	real := filepath.Join(tmp, "real")
	scratch := filepath.Join(tmp, "scratch")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	kc := filepath.Join(real, filepath.FromSlash(KeychainsRel))
	if err := os.MkdirAll(kc, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kc, "login.keychain-db"), []byte("keychain"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := `{"access_token":"tok-1","token_type":"Bearer"}`
	raw := keychainPrefix + base64.StdEncoding.EncodeToString([]byte(good))
	t.Cleanup(SetKeychainReader(func() (string, error) { return raw, nil }))

	// CheckWorkLogin is read-only: it validates the Keychain token without creating work/ or writing WorkTokenRel.
	wantPath := filepath.Join(real, filepath.FromSlash(WorkTokenRel))
	tok, err := CheckWorkLogin(real, real, "darwin")
	if err != nil || tok != wantPath {
		t.Fatalf("CheckWorkLogin: %q, %v; want %q", tok, err, wantPath)
	}
	if _, err := os.Lstat(wantPath); !os.IsNotExist(err) {
		t.Fatalf("CheckWorkLogin wrote %s during read-only check: %v", wantPath, err)
	}

	// SyncWorkToken with a separate --home writes under scratch, never under realHome.
	scratchTok := filepath.Join(scratch, filepath.FromSlash(WorkTokenRel))
	tok, err = SyncWorkToken(scratch, real, "darwin")
	if err != nil || tok != scratchTok {
		t.Fatalf("SyncWorkToken(scratch): %q, %v; want %q", tok, err, scratchTok)
	}
	if _, err := os.Lstat(wantPath); !os.IsNotExist(err) {
		t.Fatalf("SyncWorkToken(scratch) wrote to realHome: %v", err)
	}

	// SyncWorkToken in realHome writes WorkTokenRel at 0600 in a 0700 work/ dir.
	tok, err = SyncWorkToken(real, real, "darwin")
	if err != nil || tok != wantPath {
		t.Fatalf("SyncWorkToken(real): %q, %v; want %q", tok, err, wantPath)
	}
	b, err := os.ReadFile(wantPath)
	if err != nil || string(b) != good+"\n" {
		t.Errorf("synced token = %q, %v", b, err)
	}
	st, err := os.Lstat(wantPath)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("token mode = %v, %v; want 0600", st.Mode(), err)
	}
	dirSt, err := os.Lstat(filepath.Dir(wantPath))
	if err != nil || dirSt.Mode().Perm() != 0o700 {
		t.Errorf("work dir mode = %v, %v; want 0700", dirSt.Mode(), err)
	}

	// When Keychain lookup fails later, an existing regular WorkTokenRel is used.
	t.Cleanup(SetKeychainReader(func() (string, error) { return "", errors.New("locked") }))
	tok, err = CheckWorkLogin(real, real, "darwin")
	if err != nil || tok != wantPath {
		t.Errorf("fallback to existing WorkTokenRel: %q, %v", tok, err)
	}
	tok, err = SyncWorkToken(real, real, "darwin")
	if err != nil || tok != wantPath {
		t.Errorf("SyncWorkToken fallback to existing WorkTokenRel: %q, %v", tok, err)
	}

	// A symlink at WorkTokenRel is refused before any Keychain read or write.
	os.Remove(wantPath)
	elsewhere := filepath.Join(tmp, "elsewhere")
	os.WriteFile(elsewhere, []byte("keep"), 0o600)
	if err := os.Symlink(elsewhere, wantPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(SetKeychainReader(func() (string, error) { return raw, nil }))
	if _, err := CheckWorkLogin(real, real, "darwin"); err == nil || !strings.Contains(err.Error(), wantPath) {
		t.Errorf("symlink at WorkTokenRel (CheckWorkLogin): %v", err)
	}
	if _, err := SyncWorkToken(real, real, "darwin"); err == nil || !strings.Contains(err.Error(), wantPath) {
		t.Errorf("symlink at WorkTokenRel (SyncWorkToken): %v", err)
	}
	if b, _ := os.ReadFile(elsewhere); string(b) != "keep" {
		t.Errorf("symlink target overwritten: %q", b)
	}

	// A symlinked work/ directory is refused by both CheckWorkLogin and SyncWorkToken.
	os.RemoveAll(filepath.Dir(wantPath))
	if err := os.Symlink(tmp, filepath.Dir(wantPath)); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckWorkLogin(real, real, "darwin"); err == nil {
		t.Error("symlinked work/ directory accepted by CheckWorkLogin")
	}
	if _, err := SyncWorkToken(real, real, "darwin"); err == nil {
		t.Error("symlinked work/ directory accepted by SyncWorkToken")
	}
}

func TestFingerprintWorkDir(t *testing.T) {
	real := filepath.Join(outsideTemp(t), "real")
	fyr := filepath.Join(real, harness.StateDir)
	write := func(rel, content string, mode os.FileMode) {
		t.Helper()
		p := filepath.Join(fyr, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("brain.db", "store", 0o600)
	write("work/agy-linux", "elf-v1", 0o755)
	write("work/config.json", `{"workers":8}`, 0o600)

	// A token created or overwritten by a worker during a run fails check c,
	// even at 0600 (BUG-511, ADR-503), while sandbox/ and dev/ writes are skipped.
	before := fingerprintWith(real, false, nil)
	write("work/antigravity-oauth-token", `{"access_token":"t1"}`, 0o600)
	if got := changedPathsIn(real, before, fingerprintWith(real, false, nil)); !slices.Equal(got, []string{filepath.FromSlash(WorkTokenRel)}) {
		t.Errorf("a 0600 token created during a run: got %v, want it flagged", got)
	}
	before = fingerprintWith(real, false, nil)
	write("work/antigravity-oauth-token", `{"access_token":"t2-longer"}`, 0o600)
	write("sandbox/fyr/2026-11-04-120000-1/fix-a/home/x", "worker", 0o644)
	write("dev/fyr-eval/home/x", "dev", 0o644)
	if got := changedPathsIn(real, before, fingerprintWith(real, false, nil)); !slices.Equal(got, []string{filepath.FromSlash(WorkTokenRel)}) {
		t.Errorf("in-place token overwrite: got %v, want only WorkTokenRel flagged", got)
	}

	// A concurrent SyncWorkToken from realHome's macOS Keychain under slots.lock
	// passes changedPathsIn on first creation and refresh (BUG-513), while
	// tampering with the synced file is still flagged.
	os.Remove(filepath.Join(real, filepath.FromSlash(WorkTokenRel)))
	t.Cleanup(SetHostOS("darwin"))
	kc := filepath.Join(real, filepath.FromSlash(KeychainsRel))
	if err := os.MkdirAll(kc, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kc, "login.keychain-db"), []byte("kc"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw := keychainPrefix + base64.StdEncoding.EncodeToString([]byte(`{"access_token":"kc-tok-1","token_type":"Bearer"}`))
	t.Cleanup(SetKeychainReader(func() (string, error) { return raw, nil }))
	before = fingerprintWith(real, false, nil)
	if _, err := SyncWorkToken(real, real, "darwin"); err != nil {
		t.Fatalf("SyncWorkToken: %v", err)
	}
	if got := changedPathsIn(real, before, fingerprintWith(real, false, nil)); len(got) != 0 {
		t.Errorf("concurrent Keychain SyncWorkToken flagged by check c: %v", got)
	}
	write("work/antigravity-oauth-token", `{"access_token":"planted"}`, 0o600)
	if got := changedPathsIn(real, before, fingerprintWith(real, false, nil)); !slices.Equal(got, []string{filepath.FromSlash(WorkTokenRel)}) {
		t.Errorf("planted token over Keychain sync: got %v, want it flagged", got)
	}

	// Modifying agy-linux, config.json, or token permissions fails check c.
	before = fingerprintWith(real, false, nil)
	write("work/agy-linux", "elf-tampered", 0o755)
	if got := changedPaths(before, fingerprintWith(real, false, nil)); !slices.Equal(got, []string{filepath.FromSlash(WorkAgyRel)}) {
		t.Errorf("agy-linux tamper: got %v", got)
	}
	before = fingerprintWith(real, false, nil)
	write("work/config.json", `{"workers":32}`, 0o600)
	if got := changedPaths(before, fingerprintWith(real, false, nil)); !slices.Equal(got, []string{filepath.FromSlash(WorkConfigRel)}) {
		t.Errorf("config.json tamper: got %v", got)
	}
	before = fingerprintWith(real, false, nil)
	os.Chmod(filepath.Join(real, filepath.FromSlash(WorkTokenRel)), 0o644)
	if got := changedPaths(before, fingerprintWith(real, false, nil)); !slices.Equal(got, []string{filepath.FromSlash(WorkTokenRel)}) {
		t.Errorf("token chmod: got %v", got)
	}
}

func TestProjectRootAndDockerLimits(t *testing.T) {
	if got := ProjectRoot("/h", "sandbox", "/Users/alice/Workspace/fyr"); got != filepath.Join("/h", harness.StateDir, "sandbox", "fyr") {
		t.Errorf("ProjectRoot = %q", got)
	}
	for _, odd := range []string{"", ".", "/", "-flag", "a\x01b"} {
		if got := ProjectName(odd); got != "default" {
			t.Errorf("ProjectName(%q) = %q, want default", odd, got)
		}
	}
	d := &Docker{Image: "fyr-work:local", Agy: "/opt/agy", Stamp: "s1", Name: "w1", Network: "fyr-work-s1",
		Memory: "12g", CPUs: "4", TmpfsSize: "1g"}
	got, err := dockerArgs(d, "/h/app", "/h/home", "/h/tok", "/h/w1.cid", 1000, 1000, []string{"HOME=/h/home"}, []string{"-p", "x"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"--memory 12g --memory-swap 12g --cpus 4", "size=1g,mode=1777"} {
		if !strings.Contains(joined, want) {
			t.Errorf("dockerArgs missing %q:\n%s", want, joined)
		}
	}
}

// CheckWorkDir refuses what EnsureWorkDir refuses, in its words, and
// creates and changes nothing; what it passes, EnsureWorkDir makes (fyr
// work --check, ADR-504a). Paths are under the test's directory, where
// elsewhere is a real directory.
func TestCheckWorkDir(t *testing.T) {
	for _, c := range []struct {
		name, want  string   // want: "" passes, else in both refusals
		dirs, files []string // made before the walk
		link        []string // a symlink: its path, then its target
	}{
		{name: "nothing", dirs: []string{"home"}},
		{name: "config-only", dirs: []string{"home/.config"}},
		{name: "all-real", dirs: []string{"home/.config/fyr/work"}},
		{name: "config-symlink", want: "home/.config is not a regular directory", dirs: []string{"home"}, link: []string{"home/.config", "elsewhere"}},
		{name: "fyr-symlink", want: "home/.config/fyr is not a regular directory", dirs: []string{"home/.config"}, link: []string{"home/.config/fyr", "elsewhere"}},
		{name: "work-symlink", want: "fyr/work is not a regular directory", dirs: []string{"home/.config/fyr"}, link: []string{"home/.config/fyr/work", "elsewhere"}},
		{name: "dangling-symlink", want: "home/.config is not a regular directory", dirs: []string{"home"}, link: []string{"home/.config", "gone"}},
		{name: "work-file", want: "fyr/work is not a regular directory", files: []string{"home/.config/fyr/work"}},
		{name: "home-file", want: "is not a directory", files: []string{"home"}},
		{name: "home-missing", want: "is not a directory"},
		{name: "home-symlink", want: "is not a directory", link: []string{"home", "elsewhere"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			tmp := t.TempDir()
			for _, d := range append(c.dirs, "elsewhere") {
				p := filepath.Join(tmp, filepath.FromSlash(d))
				// 0755, so a chmod to EnsureWorkDir's 0700 changes the tree.
				if err := os.MkdirAll(p, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(p, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, f := range c.files {
				put(t, filepath.Join(tmp, filepath.FromSlash(f)), "x", 0o600)
			}
			if c.link != nil {
				if err := os.Symlink(filepath.Join(tmp, c.link[1]), filepath.Join(tmp, filepath.FromSlash(c.link[0]))); err != nil {
					t.Fatal(err)
				}
			}
			home := filepath.Join(tmp, "home")
			before := treeOf(t, tmp)
			cerr := CheckWorkDir(home)
			if after := treeOf(t, tmp); !maps.Equal(before, after) {
				t.Errorf("CheckWorkDir changed the tree:\nbefore %v\nafter  %v", before, after)
			}
			_, eerr := EnsureWorkDir(home)
			switch {
			case c.want == "" && (cerr != nil || eerr != nil):
				t.Errorf("CheckWorkDir %v, EnsureWorkDir %v; want both to pass", cerr, eerr)
			case c.want != "" && (cerr == nil || eerr == nil || cerr.Error() != eerr.Error() || !strings.Contains(cerr.Error(), c.want)):
				t.Errorf("CheckWorkDir %v, EnsureWorkDir %v; want the same refusal, saying %q", cerr, eerr, c.want)
			}
		})
	}
}

// treeOf is every path under root, symlinks unfollowed, with its mode.
func treeOf(t *testing.T, root string) map[string]fs.FileMode {
	t.Helper()
	tree := map[string]fs.FileMode{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		tree[p] = info.Mode()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}
