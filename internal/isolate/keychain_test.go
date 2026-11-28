package isolate

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func encodeKeychainToken(t *testing.T, fields map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return keychainPrefix + base64.StdEncoding.EncodeToString(raw)
}

func TestDecodeKeychainToken(t *testing.T) {
	t.Parallel()
	key := "access_" + "token"
	raw := encodeKeychainToken(t, map[string]any{key: "abc", "unknown_field": "drop"})
	got, err := DecodeKeychainToken(raw)
	if err != nil || !strings.Contains(string(got), `"`+key+`":"abc"`) || strings.Contains(string(got), "unknown_field") {
		t.Fatalf("DecodeKeychainToken = (%q, %v)", got, err)
	}
	for _, bad := range []string{"no-prefix", keychainPrefix + "!!!", keychainPrefix + base64.StdEncoding.EncodeToString([]byte(`{"unknown":1}`))} {
		if _, err := DecodeKeychainToken(bad); err == nil {
			t.Errorf("DecodeKeychainToken(%q) succeeded, want error", bad)
		}
	}
}

func TestKeychainSyncCheckAndChangedPathsIn(t *testing.T) {
	defer SetHostOS("darwin")()
	realHome := t.TempDir()
	workHome := t.TempDir()

	kcDir := filepath.Join(realHome, filepath.FromSlash(KeychainsRel))
	if err := os.MkdirAll(kcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kcDir, "login.keychain-db"), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := "access_" + "token"
	rawTok := encodeKeychainToken(t, map[string]any{key: "tok123"})
	defer SetKeychainReader(func() (string, error) { return rawTok, nil })()

	gotCheck, err := CheckWorkLogin(workHome, realHome, "darwin")
	wantPath := filepath.Join(workHome, filepath.FromSlash(WorkTokenRel))
	if err != nil || gotCheck != wantPath {
		t.Fatalf("CheckWorkLogin = (%q, %v), want %q", gotCheck, err, wantPath)
	}
	if _, err := os.Stat(wantPath); !os.IsNotExist(err) {
		t.Fatalf("CheckWorkLogin created %s", wantPath)
	}

	gotSync, err := SyncWorkToken(workHome, realHome, "darwin")
	if err != nil || gotSync != wantPath {
		t.Fatalf("SyncWorkToken = (%q, %v)", gotSync, err)
	}
	if _, err := SyncWorkToken(realHome, realHome, "darwin"); err != nil {
		t.Fatalf("SyncWorkToken(realHome): %v", err)
	}

	data, err := DecodeKeychainToken(rawTok)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	matchPrint := formatRegularEntryPrint(0o600, int64(len(data)), hex.EncodeToString(sum[:]))
	tokKey := filepath.FromSlash(WorkTokenRel)
	if changed := changedPathsIn(realHome, map[string]string{}, map[string]string{tokKey: matchPrint}); len(changed) != 0 {
		t.Fatalf("changedPathsIn kept valid Keychain sync: %v", changed)
	}
	tamperedPrint := formatRegularEntryPrint(0o644, int64(len(data)), hex.EncodeToString(sum[:]))
	if changed := changedPathsIn(realHome, map[string]string{}, map[string]string{tokKey: tamperedPrint}); len(changed) != 1 {
		t.Fatalf("changedPathsIn missed tampered permissions: %v", changed)
	}

	// Symlinked state dir refused by CheckWorkDir and EnsureWorkDir.
	symHome := t.TempDir()
	_ = os.MkdirAll(filepath.Dir(filepath.Join(symHome, harness.StateDir)), 0o700)
	_ = os.Symlink(t.TempDir(), filepath.Join(symHome, harness.StateDir))
	if err := CheckWorkDir(symHome); err == nil {
		t.Fatal("CheckWorkDir allowed symlinked state dir")
	}
	if _, err := EnsureWorkDir(symHome); err == nil {
		t.Fatal("EnsureWorkDir allowed symlinked state dir")
	}
}
