package eval

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// The shapes below are assembled so this file holds no literal credential.
var (
	ghToken    = "gh" + "o_" + strings.Repeat("A", 36)
	awsExample = "AKIA" + "IOSFODNN7EXAMPLE"
	awsKey     = "AKIA" + "Q3EGRK7TNZ2BXW4M"
)

func put(t *testing.T, p, s string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
}

// TestCredsLeftovers ports the kit test of the preamble's credential scan
// (catalog_test.go:1719-1748): each planted credential is found against an
// empty baseline, by name or by content, and the report names paths only.
// Dropped, with no Go equivalent (D15): a shell function shadowing grep,
// find or od; a grep that cannot read for -o; bash -x.
func TestCredsLeftovers(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, h string){
		"an SSH key copied in": func(t *testing.T, h string) { put(t, filepath.Join(h, ".ssh", "id_ed25519"), "", 0o600) },
		"gcloud credentials": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".config", "gcloud", "credentials.db"), "", 0o600)
		},
		"an Antigravity token": func(t *testing.T, h string) { put(t, filepath.Join(h, tokenRel), "", 0o600) },
		"a token in a terminal log": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".gemini", "brain", "term1.env"), "declare -x GH_TOKEN="+ghToken+"\n", 0o644)
		},
		"a private key in a stray file": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "notes.txt"), "-----BEGIN OPENSSH PRIVATE KEY-----\nx\n", 0o644)
		},
		"a symlinked SSH key": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".ssh", "k"), "", 0o600)
			if err := os.Symlink("k", filepath.Join(h, ".ssh", "id_rsa")); err != nil {
				t.Fatal(err)
			}
		},
		"a key under a nested bin": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".local", "bin", "notes"), "-----BEGIN RSA PRIVATE KEY-----\n", 0o644)
		},
		"a token in a binary file": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "conv.db"), "SQLite\x00\x01token="+ghToken+"\x00", 0o644)
		},
		"a Google access token": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "trace.log"), "Authorization: Bearer ya29."+strings.Repeat("b", 40)+"\n", 0o644)
		},
		"a .netrc":           func(t *testing.T, h string) { put(t, filepath.Join(h, ".netrc"), "", 0o600) },
		"an unreadable file": func(t *testing.T, h string) { put(t, filepath.Join(h, "locked"), "", 0o000) },
		"an unreadable directory": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "sealed", "id_ed25519"), "", 0o600)
			os.Chmod(filepath.Join(h, "sealed"), 0o000)
		},
		"a symlinked .ssh directory": func(t *testing.T, h string) {
			out := filepath.Join(filepath.Dir(h), "outside")
			put(t, filepath.Join(out, "id_ed25519"), "", 0o600)
			if err := os.Symlink(out, filepath.Join(h, ".ssh")); err != nil {
				t.Fatal(err)
			}
		},
		"a dangling link": func(t *testing.T, h string) { os.Symlink(filepath.Join(h, "gone"), filepath.Join(h, "stale")) },
		"a key in the sandbox's own bin": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "bin", "stray"), "-----BEGIN EC PRIVATE KEY-----\n", 0o644)
		},
		"an AWS access key": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "trace.log"), "aws_access_key_id = "+awsKey+"\n", 0o644)
		},
		"an AWS key beside the example key": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "conv.db"), "SQLite\x00"+awsExample+"\x00"+awsKey+"\x00", 0o644)
		},
		"a GitHub token holding the example": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "trace.log"), "gh"+"p_"+awsExample+strings.Repeat("Z", 16)+"\n", 0o644)
		},
		"an Anthropic key": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "x.log"), "sk-"+"ant-"+strings.Repeat("k", 24), 0o644)
		},
		"a refresh token": func(t *testing.T, h string) { put(t, filepath.Join(h, "oauth.json"), `{"refresh_token": "x"}`, 0o644) },
	} {
		t.Run(name, func(t *testing.T) {
			if strings.HasPrefix(name, "an unreadable") && os.Geteuid() == 0 {
				t.Skip("root reads through permissions")
			}
			tmp := t.TempDir()
			h := filepath.Join(tmp, "home")
			if err := os.MkdirAll(h, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				filepath.WalkDir(tmp, func(p string, d os.DirEntry, err error) error { os.Chmod(p, 0o755); return nil })
			})
			plant(t, h)
			left := newCreds(nil, scanCreds(h))
			if len(left) == 0 {
				t.Fatal("the scan found nothing")
			}
			for _, l := range left {
				if strings.Contains(l, ghToken) || strings.Contains(l, awsKey) || strings.Contains(l, "BEGIN") {
					t.Errorf("the report carries content, not a path: %q", l)
				}
			}
		})
	}
}

// TestCredsNotCredentials: a public key, an executable (the installed fyr
// carries the redaction patterns as text), and AWS's published example key
// are not credentials; a clean home has none.
func TestCredsNotCredentials(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, h string){
		"a clean home": func(t *testing.T, h string) {},
		"a public key": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".ssh", "id_ed25519.pub"), "ssh-ed25519 AAAA", 0o644)
		},
		"an ELF binary": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".local", "bin", "fyr"), "\x7fELF-----BEGIN RSA PRIVATE KEY-----", 0o755)
		},
		"a Mach-O binary": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".local", "bin", "fyr"), "\xcf\xfa\xed\xfe"+ghToken, 0o755)
		},
		"only the example": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".gemini", "brain", "transcript.jsonl"), `{"user_quote":"`+awsExample+`"}`, 0o644)
			put(t, filepath.Join(h, "conv.db"), "SQLite\x00"+awsExample+"\x00", 0o644)
		},
		"a link to a file": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "notes"), "nothing", 0o644)
			os.Symlink("notes", filepath.Join(h, "n"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := t.TempDir()
			plant(t, h)
			if left := newCreds(nil, scanCreds(h)); len(left) != 0 {
				t.Errorf("found %v", left)
			}
		})
	}
	// The directory itself a link: reported, never followed (the scan only
	// ever gets eval's own sandbox directory).
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real-home")
	put(t, filepath.Join(real, ".ssh", "id_ed25519"), "", 0o600)
	link := filepath.Join(tmp, "home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if left := newCreds(nil, scanCreds(link)); len(left) != 1 || left[0] != ". (a link, not followed)" {
		t.Errorf("a linked directory holding a key: %v", left)
	}
	// A home that is a dangling link is reported, never passed.
	if err := os.Symlink(filepath.Join(tmp, "gone"), filepath.Join(tmp, "dangling")); err != nil {
		t.Fatal(err)
	}
	if left := newCreds(nil, scanCreds(filepath.Join(tmp, "dangling"))); len(left) != 1 {
		t.Errorf("a dangling home: %v", left)
	}
}

// TestCredsBaseline is D17: what the fixture brought in before the token
// was linked (this harness's trail quotes a key header, A5) is not a leak;
// a new path, a new kind or a grown count is, and a problem always is.
func TestCredsBaseline(t *testing.T) {
	h := t.TempDir()
	store := filepath.Join(h, harness.StateDir, "brain.db")
	put(t, store, "SQLite\x00-----BEGIN OPENSSH PRIVATE KEY-----\x00", 0o600)
	base := scanCreds(h)
	if got := newCreds(base, scanCreds(h)); len(got) != 0 {
		t.Errorf("the unchanged baseline is reported: %v", got)
	}
	put(t, store, "SQLite\x00-----BEGIN OPENSSH PRIVATE KEY-----\x00-----BEGIN RSA PRIVATE KEY-----\x00", 0o600)
	if got := newCreds(base, scanCreds(h)); len(got) != 1 || !strings.Contains(got[0], "private-key") {
		t.Errorf("a grown count: %v", got)
	}
	put(t, store, "SQLite\x00-----BEGIN OPENSSH PRIVATE KEY-----\x00"+ghToken, 0o600)
	if got := newCreds(base, scanCreds(h)); len(got) != 1 || !strings.Contains(got[0], "github-token") {
		t.Errorf("a new kind in a baseline file: %v", got)
	}
	put(t, store, "SQLite\x00-----BEGIN OPENSSH PRIVATE KEY-----\x00", 0o600)
	put(t, filepath.Join(h, "new.log"), ghToken, 0o600)
	if got := newCreds(base, scanCreds(h)); len(got) != 1 || !strings.HasPrefix(got[0], "new.log") {
		t.Errorf("a new path: %v", got)
	}
	if os.Geteuid() != 0 {
		os.Remove(filepath.Join(h, "new.log"))
		put(t, filepath.Join(h, "locked"), "", 0o000)
		base = scanCreds(h)
		if got := newCreds(base, scanCreds(h)); len(got) != 1 || !strings.Contains(got[0], "unreadable") {
			t.Errorf("an unreadable file already in the baseline passes: %v", got)
		}
	}
}

// TestCredsLinkProblems: a link problem the baseline held with the same
// target is the fixture's; the same path re-pointed, or a new one, counts;
// an unreadable or oversized file counts baseline or not.
func TestCredsLinkProblems(t *testing.T) {
	h := t.TempDir()
	put(t, filepath.Join(h, "sub", "f"), "x", 0o644)
	if err := os.Symlink("sub", filepath.Join(h, "dirlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("gone", filepath.Join(h, "stale")); err != nil {
		t.Fatal(err)
	}
	base := scanCreds(h)
	if got := newCreds(base, scanCreds(h)); len(got) != 0 {
		t.Errorf("unchanged links are reported: %v", got)
	}
	os.Remove(filepath.Join(h, "dirlink"))
	if err := os.Symlink(h, filepath.Join(h, "dirlink")); err != nil {
		t.Fatal(err)
	}
	if got := newCreds(base, scanCreds(h)); len(got) != 1 || got[0] != "dirlink (a link to a directory, not walked)" {
		t.Errorf("a re-pointed directory link: %v", got)
	}
	old := maxScan
	maxScan = 4
	t.Cleanup(func() { maxScan = old })
	put(t, filepath.Join(h, "big"), "more than four bytes", 0o644)
	base = scanCreds(h)
	if got := newCreds(base, scanCreds(h)); !slices.Contains(got, "big (larger than 4 bytes, not read)") {
		t.Errorf("an oversized file already in the baseline passes: %v", got)
	}
}
