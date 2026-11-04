package sandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/kesokaj/fyr/internal/harness"
)

// WorkTokenRel, WorkAgyRel and WorkConfigRel are the paths under a home's
// state directory where fyr work keeps the container OAuth token synced from
// macOS Keychain, the Linux agy binary, and the worker queue/resource config
// (D7–D10 of PLAN-2026-11-02-1806-fyr-speed, ADR-503).
const (
	WorkTokenRel  = harness.StateDir + "/work/antigravity-oauth-token"
	WorkAgyRel    = harness.StateDir + "/work/agy-linux"
	WorkConfigRel = harness.StateDir + "/work/config.json"

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
	var out bytes.Buffer
	cmd.Stdout = &limitWriter{w: &out, n: 64 * 1024}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("keychain lookup failed: %v", err)
	}
	return strings.TrimSpace(out.String()), nil
}

type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if len(p) > l.n {
		return 0, errors.New("keychain output exceeds 64 KiB")
	}
	l.n -= len(p)
	return l.w.Write(p)
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

// EnsureWorkDir creates <home>/.config/fyr/work with 0700 permissions,
// refusing any symlink along the path from home down to work/.
func EnsureWorkDir(home string) (string, error) {
	cur, err := walkWorkDir(home, true)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(cur, 0o700); err != nil {
		return "", err
	}
	return cur, nil
}

// CheckWorkDir is EnsureWorkDir's refusals, read only, in its words: it
// creates and changes nothing, and passes a directory that is not there
// yet, which EnsureWorkDir would create (fyr work --check, ADR-504a).
func CheckWorkDir(home string) error {
	_, err := walkWorkDir(home, false)
	return err
}

// walkWorkDir walks from home down to work/, refusing a home that is no
// directory and any symlink or other non-directory on the way. With mk it
// creates each directory that is missing; without it, the walk passes at
// the first one that is missing.
func walkWorkDir(home string, mk bool) (string, error) {
	cur := filepath.Clean(home)
	if st, err := os.Lstat(cur); err != nil || !st.IsDir() {
		return "", fmt.Errorf("home %s is not a directory", home)
	}
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(WorkTokenRel)), "/") {
		cur = filepath.Join(cur, part)
		if mk {
			if err := mkdirExisting(cur); err != nil {
				return "", err
			}
		}
		st, err := os.Lstat(cur)
		switch {
		case !mk && errors.Is(err, fs.ErrNotExist):
			return cur, nil
		case err != nil:
			return "", err
		case !st.IsDir():
			return "", fmt.Errorf("%s is not a regular directory", cur)
		}
	}
	return cur, nil
}

// mkdirExisting makes dir 0700, passing when something is there already,
// which the walk's Lstat then checks.
func mkdirExisting(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

func writeWorkToken(realHome, wp string, data []byte) error {
	workDir, err := EnsureWorkDir(realHome)
	if err != nil {
		return err
	}
	if st, err := os.Lstat(wp); err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", wp)
	}
	tmp, err := os.CreateTemp(workDir, ".token-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, wp); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func checkWorkToken(realHome, p, goos string) (string, error) {
	wp := filepath.Join(realHome, filepath.FromSlash(WorkTokenRel))
	wst, werr := os.Lstat(wp)
	if werr == nil && !wst.Mode().IsRegular() {
		return "", fmt.Errorf("no Antigravity login at %s", wp)
	}
	if dst, derr := os.Lstat(filepath.Dir(wp)); derr == nil && !dst.IsDir() {
		return "", fmt.Errorf("no Antigravity login at %s (%s is not a regular directory)", wp, filepath.Dir(wp))
	}
	if goos == "darwin" {
		kc := filepath.Join(realHome, filepath.FromSlash(KeychainsRel))
		if info, kerr := os.Lstat(kc); kerr == nil && info.IsDir() {
			if raw, err := readKeychainToken(kc); err == nil {
				if _, derr := DecodeKeychainToken(raw); derr != nil {
					return "", fmt.Errorf("no Antigravity login at %s (%v)", wp, derr)
				}
				return wp, nil
			}
		}
	}
	if werr == nil && wst.Mode().IsRegular() {
		return wp, nil
	}
	return "", fmt.Errorf("no Antigravity login at %s", p)
}

// SyncWorkToken materializes the container OAuth token file for a docker-mode
// run after all preflight refusals have passed: it returns realHome's TokenRel
// when present, or on darwin extracts the macOS Keychain token and writes it
// atomically (0600 in a 0700 directory) to WorkTokenRel under home (or
// realHome when home is empty), falling back to an existing regular
// WorkTokenRel when Keychain lookup is unavailable (D7, ADR-503).
func SyncWorkToken(home, realHome, goos string) (string, error) {
	p := filepath.Join(realHome, filepath.FromSlash(TokenRel))
	if st, err := os.Lstat(p); err == nil {
		if !st.Mode().IsRegular() {
			return "", fmt.Errorf("no Antigravity login at %s", p)
		}
		return p, nil
	}
	targetHome := home
	if targetHome == "" {
		targetHome = realHome
	}
	wp := filepath.Join(targetHome, filepath.FromSlash(WorkTokenRel))
	wst, werr := os.Lstat(wp)
	if werr == nil && !wst.Mode().IsRegular() {
		return "", fmt.Errorf("no Antigravity login at %s", wp)
	}
	rwp := filepath.Join(realHome, filepath.FromSlash(WorkTokenRel))
	rwst, rwerr := os.Lstat(rwp)
	if rwerr == nil && !rwst.Mode().IsRegular() {
		return "", fmt.Errorf("no Antigravity login at %s", rwp)
	}
	if goos == "darwin" {
		kc := filepath.Join(realHome, filepath.FromSlash(KeychainsRel))
		if info, kerr := os.Lstat(kc); kerr == nil && info.IsDir() {
			if raw, err := readKeychainToken(kc); err == nil {
				data, derr := DecodeKeychainToken(raw)
				if derr != nil {
					return "", fmt.Errorf("no Antigravity login at %s (%v)", wp, derr)
				}
				if err := writeWorkToken(targetHome, wp, data); err != nil {
					return "", fmt.Errorf("no Antigravity login at %s (%v)", wp, err)
				}
				return wp, nil
			}
		}
	}
	if werr == nil && wst.Mode().IsRegular() {
		return wp, nil
	}
	if rwerr == nil && rwst.Mode().IsRegular() {
		return rwp, nil
	}
	return "", fmt.Errorf("no Antigravity login at %s", p)
}

// ProjectName returns a safe single-segment directory name for projectRoot.
func ProjectName(projectRoot string) string {
	if projectRoot == "" {
		return "default"
	}
	b := filepath.Base(filepath.Clean(projectRoot))
	if b == "" || b == "." || b == ".." || b == string(filepath.Separator) || strings.HasPrefix(b, "-") || strings.IndexFunc(b, unicode.IsControl) >= 0 {
		return "default"
	}
	return b
}

// ProjectRoot is where a verb makes its per-project sandboxes under fyr's
// home: <home>/.config/fyr/<kind>/<project> (D9 of PLAN-2026-11-02-1806-fyr-speed).
func ProjectRoot(home, kind, projectRoot string) string {
	return filepath.Join(Root(home, kind), ProjectName(projectRoot))
}
