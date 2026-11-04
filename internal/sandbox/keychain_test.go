package sandbox

import (
	"encoding/base64"
	"errors"
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

	// CheckLogin is read-only: it validates the Keychain token without creating work/ or writing WorkTokenRel.
	wantPath := filepath.Join(real, filepath.FromSlash(WorkTokenRel))
	tok, err := CheckLogin(real, "darwin", true)
	if err != nil || tok != wantPath {
		t.Fatalf("CheckLogin: %q, %v; want %q", tok, err, wantPath)
	}
	if _, err := os.Lstat(wantPath); !os.IsNotExist(err) {
		t.Fatalf("CheckLogin wrote %s during read-only check: %v", wantPath, err)
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
	tok, err = CheckLogin(real, "darwin", true)
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
	if _, err := CheckLogin(real, "darwin", true); err == nil || !strings.Contains(err.Error(), wantPath) {
		t.Errorf("symlink at WorkTokenRel (CheckLogin): %v", err)
	}
	if _, err := SyncWorkToken(real, real, "darwin"); err == nil || !strings.Contains(err.Error(), wantPath) {
		t.Errorf("symlink at WorkTokenRel (SyncWorkToken): %v", err)
	}
	if b, _ := os.ReadFile(elsewhere); string(b) != "keep" {
		t.Errorf("symlink target overwritten: %q", b)
	}

	// A symlinked work/ directory is refused by both CheckLogin and SyncWorkToken.
	os.Remove(wantPath)
	os.Remove(filepath.Dir(wantPath))
	if err := os.Symlink(tmp, filepath.Dir(wantPath)); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckLogin(real, "darwin", true); err == nil {
		t.Error("symlinked work/ directory accepted by CheckLogin")
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

	// First-time creation of antigravity-oauth-token at 0600 or in-place token
	// refresh during a run does not fail check c; sandbox/ and dev/ are skipped.
	before := fingerprint(real, false)
	write("work/antigravity-oauth-token", `{"access_token":"t1"}`, 0o600)
	if got := changedPaths(before, fingerprint(real, false)); len(got) != 0 {
		t.Errorf("first-time 0600 token sync changed fingerprint: %v", got)
	}
	before = fingerprint(real, false)
	write("work/antigravity-oauth-token", `{"access_token":"t2-longer"}`, 0o600)
	write("sandbox/fyr/2026-11-04-120000-1/fix-a/home/x", "worker", 0o644)
	write("dev/fyr-eval/home/x", "dev", 0o644)
	if got := changedPaths(before, fingerprint(real, false)); len(got) != 0 {
		t.Errorf("in-place token refresh or sandbox/dev write changed fingerprint: %v", got)
	}

	// Modifying agy-linux, config.json, or token permissions fails check c.
	before = fingerprint(real, false)
	write("work/agy-linux", "elf-tampered", 0o755)
	if got := changedPaths(before, fingerprint(real, false)); !slices.Equal(got, []string{filepath.FromSlash(WorkAgyRel)}) {
		t.Errorf("agy-linux tamper: got %v", got)
	}
	before = fingerprint(real, false)
	write("work/config.json", `{"workers":32}`, 0o600)
	if got := changedPaths(before, fingerprint(real, false)); !slices.Equal(got, []string{filepath.FromSlash(WorkConfigRel)}) {
		t.Errorf("config.json tamper: got %v", got)
	}
	before = fingerprint(real, false)
	os.Chmod(filepath.Join(real, filepath.FromSlash(WorkTokenRel)), 0o644)
	if got := changedPaths(before, fingerprint(real, false)); !slices.Equal(got, []string{filepath.FromSlash(WorkTokenRel)}) {
		t.Errorf("token chmod: got %v", got)
	}
}

func TestProjectRootAndDockerLimits(t *testing.T) {
	if got := ProjectRoot("/h", "sandbox", "/Users/spgo/Workspace/fyr"); got != filepath.Join("/h", harness.StateDir, "sandbox", "fyr") {
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
