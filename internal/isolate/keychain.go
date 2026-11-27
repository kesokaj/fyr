package isolate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// WorkTokenRel is the legacy path under a home's state directory where
// internal/isolate keeps the OAuth token synced from macOS Keychain (ADR-503, ADR-522).
const (
	WorkTokenRel  = harness.StateDir + "/work/antigravity-oauth-token"
	workSlotsLock = "slots.lock"

	keychainPrefix  = "go-keyring-base64:"
	keychainService = "gemini"
	keychainAccount = "antigravity"
)

// readKeychainToken reads the raw secret string from kc/login.keychain-db in
// the macOS Keychain; tests replace it via SetKeychainReader so no unit test
// queries the real Keychain unless testing the live integration.
var readKeychainToken = readKeychainCmd

// SetKeychainReader overrides the macOS Keychain reader in tests and returns a
// function that restores the previous reader.
func SetKeychainReader(fn func() (string, error)) func() {
	old := readKeychainToken
	readKeychainToken = func(kc string) (string, error) {
		db := filepath.Join(kc, "login.keychain-db")
		if st, err := os.Stat(db); err != nil || !st.Mode().IsRegular() {
			return "", errors.New("no login.keychain-db")
		}
		return fn()
	}
	return func() { readKeychainToken = old }
}

func readKeychainCmd(kc string) (string, error) {
	db := filepath.Join(kc, "login.keychain-db")
	if st, err := os.Stat(db); err != nil || !st.Mode().IsRegular() {
		return "", errors.New("no login.keychain-db")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", keychainService, "-a", keychainAccount, "-w", db)
	cmd.Env = WithoutGit(os.Environ())
	out := trail.NewCappedBuffer(64*1024, errors.New("keychain output exceeds 64 KiB"))
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("keychain lookup failed: %v", err)
	}
	return strings.TrimSpace(out.String()), nil
}

type oauthToken struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	Expiry       string `json:"expiry,omitempty"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
}

type storedToken struct {
	Token           *oauthToken `json:"token,omitempty"`
	AccessToken     string      `json:"access_token,omitempty"`
	RefreshToken    string      `json:"refresh_token,omitempty"`
	TokenType       string      `json:"token_type,omitempty"`
	Expiry          string      `json:"expiry,omitempty"`
	ExpiresIn       int64       `json:"expires_in,omitempty"`
	AuthMethod      string      `json:"auth_method,omitempty"`
	IDTokenEmail    string      `json:"id_token_email,omitempty"`
	ProjectID       string      `json:"project_id,omitempty"`
	Region          string      `json:"region,omitempty"`
	UserTier        string      `json:"user_tier,omitempty"`
	TierDisplayName string      `json:"tier_display_name,omitempty"`
	WriterID        string      `json:"writer_id,omitempty"`
}

func (t storedToken) valid() bool {
	if t.AccessToken != "" || t.RefreshToken != "" {
		return true
	}
	return t.Token != nil && (t.Token.AccessToken != "" || t.Token.RefreshToken != "")
}

// DecodeKeychainToken decodes the "go-keyring-base64:<base64(JSON)>" value
// stored in macOS Keychain by agy's keyring backend into a canonical,
// trailing-newline JSON object byte slice (stripping any unknown fields),
// without ever echoing the secret in an error.
func DecodeKeychainToken(raw string) ([]byte, error) {
	b64, ok := strings.CutPrefix(strings.TrimSpace(raw), keychainPrefix)
	if !ok || b64 == "" {
		return nil, errors.New("keychain entry lacks go-keyring-base64: prefix")
	}
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("keychain entry is not valid base64")
	}
	trimmed := bytes.TrimSpace(decoded)
	var tok storedToken
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' || json.Unmarshal(trimmed, &tok) != nil || !tok.valid() {
		return nil, errors.New("keychain entry is not a valid OAuth token JSON object")
	}
	out, err := json.Marshal(tok)
	if err != nil {
		return nil, errors.New("keychain entry is not a valid OAuth token JSON object")
	}
	return append(out, '\n'), nil
}

func readDecodedKeychainToken(realHome string) ([]byte, bool, error) {
	kc := filepath.Join(realHome, filepath.FromSlash(KeychainsRel))
	if info, err := os.Lstat(kc); err != nil || !info.IsDir() {
		return nil, false, nil
	}
	raw, err := readKeychainToken(kc)
	if err != nil {
		return nil, false, nil
	}
	data, err := DecodeKeychainToken(raw)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// isKeychainSyncedWorkToken reports whether afterPrint is the entryPrint of
// realHome's WorkTokenRel as written by SyncWorkToken from the macOS Keychain
// under the work root's slots.lock (BUG-513, ADR-503).
func isKeychainSyncedWorkToken(realHome, afterPrint string) bool {
	if hostOS != "darwin" || realHome == "" || afterPrint == "" || CheckWorkDir(realHome) != nil {
		return false
	}
	workDir := filepath.Join(realHome, filepath.FromSlash(filepath.Dir(WorkTokenRel)))
	unlock, err := lockWorkSlots(workDir, false)
	if err != nil {
		return false
	}
	defer unlock()
	data, ok, err := readDecodedKeychainToken(realHome)
	if err != nil || !ok {
		return false
	}
	sum := sha256.Sum256(data)
	want := formatRegularEntryPrint(0o600, int64(len(data)), hex.EncodeToString(sum[:]))
	return afterPrint == want
}
