package eval

import (
	"path/filepath"
	"strings"
	"testing"
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
