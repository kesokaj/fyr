package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/redact"
)

// Every content kind and every name and path pattern of the credential scan
// has a hit and a benign near-miss, so a typo in a regexp or a dropped name
// fails here (IN46 of the 2026-10-27 audit: half the patterns had no hit
// test, almost none a near-miss).
func TestCredsEveryPattern(t *testing.T) {
	a := func(n int) string { return strings.Repeat("A", n) }
	content := []struct{ kind, hit, miss string }{
		{"github-token", "gho_" + a(30), "gho_" + a(29)},
		{"github-pat", "github_pat_" + a(30), "github_pat_" + a(29)},
		{"private-key", "-----BEGIN OPENSSH PRIVATE KEY-----", "-----BEGIN PUBLIC KEY-----"},
		{"anthropic-key", "sk-ant-" + a(20), "sk-ant-" + a(19)},
		{"openai-key", "sk-proj-" + a(20), "sk-proj-" + a(19)},
		{"refresh-token", `{"refresh_token": "x"}`, "the refresh_token field"},
		{"google-access-token", "ya29." + a(20), "ya29." + a(19)},
		{"google-api-key", "AIza" + a(35), "AIza" + a(34)},
		{"gitlab-token", "glpat-" + a(20), "glpat-" + a(19)},
		{"aws-key", "AKIA" + "QWERTYUIOPASDFGH", "AKIA" + "IOSFODNN7EXAMPLE"},
	}
	kinds := map[string]bool{"aws-key": true}
	for _, c := range credText {
		kinds[c.kind] = true
	}
	for _, c := range content {
		delete(kinds, c.kind)
		dir := t.TempDir()
		put(t, filepath.Join(dir, "hit.txt"), "x "+c.hit+" y\n", 0o600)
		put(t, filepath.Join(dir, "miss.txt"), "x "+c.miss+" y\n", 0o600)
		got := scanCreds(dir)
		if got["hit.txt"].kinds[c.kind] != 1 {
			t.Errorf("%s: hit %q not counted: %+v", c.kind, c.hit, got["hit.txt"])
		}
		if f, ok := got["miss.txt"]; ok {
			t.Errorf("%s: near-miss %q counted: %+v", c.kind, c.miss, f)
		}
		// IN48: what the eval refuses to carry, the dream's redactor removes
		// too. One direction only: a needle with no eval kind is not pinned
		// here.
		if redact.Redact(c.hit) == c.hit {
			t.Errorf("%s: hit %q passes redact.Redact unchanged", c.kind, c.hit)
		}
	}
	if len(kinds) != 0 {
		t.Errorf("content kinds with no row here: %v", kinds)
	}

	names := []struct{ hit, miss string }{
		{"id_rsa", "id_rsa.pub"}, {"id_ecdsa", "id_ecdsa.pub"}, {"id_ed25519", "id_ed25519.pub"}, {"id_dsa", "id_dsa.pub"},
		{".credentials.json", "credentials.json"}, {"application_default_credentials.json", "application_default.json"},
		{"credentials.db", "credentials.db.txt"}, {"access_tokens.db", "access_tokens.txt"},
		{"antigravity-oauth-token", "oauth.txt"}, {"hosts.yml", "hosts.yaml"}, {".netrc", "netrc"},
		{".git-credentials", "git-credentials"}, {"cert.p12", "cert.p12.txt"}, {"cert.pfx", "cert.pfx.txt"},
		{".aws/credentials", ".aws/config"}, {".docker/config.json", ".docker/daemon.json"},
	}
	for _, n := range names {
		dir := t.TempDir()
		put(t, filepath.Join(dir, "hit", n.hit), "plain\n", 0o600)
		put(t, filepath.Join(dir, "miss", n.miss), "plain\n", 0o600)
		got := scanCreds(dir)
		if got[filepath.Join("hit", n.hit)].kinds["name"] != 1 {
			t.Errorf("name %q not counted: %+v", n.hit, got)
		}
		if f, ok := got[filepath.Join("miss", n.miss)]; ok {
			t.Errorf("near-miss name %q counted: %+v", n.miss, f)
		}
	}
}

// TestScanCredsDistinguishesPKCS8KeysAndBareMarkers covers BUG-542: distinct
// PKCS#8 P-256 and RSA-2048 private keys that share their first 48 base64
// ASN.1 header bytes still fail closed, and bare markers or Go struct tags in
// baseline source never excuse novel unparsed or non-colon leaks.
func TestScanCredsDistinguishesPKCS8KeysAndBareMarkers(t *testing.T) {
	sb := t.TempDir()
	beginPKCS8 := "-----" + "BEGIN " + "PRIVATE KEY-----"
	endPKCS8 := "-----END PRIVATE KEY-----"
	p256ASN1 := "MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg"
	rsaASN1 := "MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIB"

	baseP256 := beginPKCS8 + "\n" + p256ASN1 + strings.Repeat("A", 64) + "\n" + strings.Repeat("B", 56) + "==\n" + endPKCS8
	leakedP256 := beginPKCS8 + "\n" + p256ASN1 + strings.Repeat("Z", 64) + "\n" + strings.Repeat("Y", 56) + "==\n" + endPKCS8
	baseRSA := beginPKCS8 + "\n" + rsaASN1 + strings.Repeat("C", 64) + "\n" + strings.Repeat("D", 192) + "\n" + endPKCS8
	leakedRSA := beginPKCS8 + "\n" + rsaASN1 + strings.Repeat("W", 64) + "\n" + strings.Repeat("X", 192) + "\n" + endPKCS8
	structTag := "type tok struct { R string `json:\"" + "refresh_" + "token\"` }"
	knownRef := `{"` + "refresh_" + `token": "fixture-val"}`

	fixture := strings.Join([]string{baseP256, baseRSA, structTag, knownRef, beginPKCS8}, "\n") + "\n"
	put(t, filepath.Join(sb, "app", "fixture_test.go"), fixture, 0o644)
	base := scanCreds(sb)

	// Struct tag and baseline PEMs indexed into search.db pass.
	sdb := filepath.Join(sb, "app", ".agents", "scratch", "search.db")
	put(t, sdb, "SQLite format 3\x00"+strings.Join([]string{baseP256, baseRSA, structTag, knownRef}, "\n")+"\n", 0o600)
	if got := newCreds(base, scanCreds(sb)); len(got) != 0 {
		t.Fatalf("indexed baseline PKCS#8 and struct tag flagged: %v", got)
	}

	for _, tc := range []struct {
		name, rel, payload, wantKind string
	}{
		{"distinct PKCS#8 P-256", "app/leaked-p256.pem", leakedP256 + "\n", "private-key"},
		{"distinct PKCS#8 RSA-2048", "app/leaked-rsa.pem", leakedRSA + "\n", "private-key"},
		{"unparsed short PEM block", "app/leaked-short.pem", beginPKCS8 + "\nshortsecret\n" + endPKCS8 + "\n", "private-key"},
		{"equals refresh_token leak", "app/leaked.env", `"refresh_` + `token" = "novelSecret123"` + "\n", "refresh-token"},
		{"mixed known+unparsed refresh_token", "app/mixed.txt", knownRef + "\n" + `["refresh_` + `token", "novelSecret456"]` + "\n", "refresh-token"},
	} {
		p := filepath.Join(sb, filepath.FromSlash(tc.rel))
		put(t, p, tc.payload, 0o600)
		got := newCreds(base, scanCreds(sb))
		_ = os.Remove(p)
		if len(got) != 1 || !strings.Contains(got[0], tc.wantKind) {
			t.Errorf("%s: got %v, want 1 hit containing %q", tc.name, got, tc.wantKind)
		}
	}
}
