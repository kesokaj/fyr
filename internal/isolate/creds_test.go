package isolate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The shapes below are assembled so this file holds no literal credential.
var (
	ghToken    = "gh" + "o_" + strings.Repeat("A", 36)
	awsExample = "AK" + "IA" + "IOSFODNN7EXAMPLE"
	awsKey     = "AK" + "IA" + "Q3EGRK7TNZ2BXW4M"
)

// TestCredsLeftovers ports the kit test of the preamble's credential scan
// (the run kit's TestKitPreambleRefusesLeftoverCredentials, in
// internal/harness/catalog_test.go at 8374f9c): each planted credential is
// found against an empty baseline, by name or by content, and the report
// names paths only.
// Dropped, with no Go equivalent (D15): a shell function shadowing grep, find
// or od; a grep that cannot read for -o; bash -x.
func TestCredsLeftovers(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, h string){
		"an SSH key copied in": func(t *testing.T, h string) { put(t, filepath.Join(h, ".ssh", "id_ed25519"), "", 0o600) },
		"gcloud credentials": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".config", "gcloud", "credentials.db"), "", 0o600)
		},
		"an Antigravity token": func(t *testing.T, h string) { put(t, filepath.Join(h, TokenRel), "", 0o600) },
		"a token in a terminal log": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".gemini", "brain", "term1.env"), "declare -x GH_TOKEN="+ghToken+"\n", 0o644)
		},
		"a private key in a stray file": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "notes.txt"), "-----"+"BEGIN OPENSSH "+"PRIVATE "+"KEY-----\nx\n", 0o644)
		},
		"a symlinked SSH key": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".ssh", "k"), "", 0o600)
			if err := os.Symlink("k", filepath.Join(h, ".ssh", "id_rsa")); err != nil {
				t.Fatal(err)
			}
		},
		"a key under a nested bin": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".local", "bin", "notes"), "-----"+"BEGIN RSA "+"PRIVATE "+"KEY-----\n", 0o644)
		},
		"a token in a binary file": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "conv.db"), "SQLite\x00\x01"+"tok"+"en="+ghToken+"\x00", 0o644)
		},
		"a Google access token": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "trace.log"), "Authorization: Bearer "+"ya"+"29."+strings.Repeat("b", 40)+"\n", 0o644)
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
			put(t, filepath.Join(h, "bin", "stray"), "-----"+"BEGIN EC "+"PRIVATE "+"KEY-----\n", 0o644)
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
		"a refresh token": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "oauth.json"), `{"`+"refresh_"+`token": "x"}`, 0o644)
		},
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

func printableRuns(b []byte, minLen int) []byte {
	var out []byte
	start := -1
	for i, c := range b {
		if c >= 0x20 && c <= 0x7e {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 && i-start >= minLen {
			out = append(out, b[start:i]...)
			out = append(out, '\n')
		}
		start = -1
	}
	if start >= 0 && len(b)-start >= minLen {
		out = append(out, b[start:]...)
		out = append(out, '\n')
	}
	return out
}

func putSparse(t *testing.T, p, prefix string, size int64) {
	t.Helper()
	put(t, p, prefix, 0o755)
	f, err := os.OpenFile(p, os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
}

// TestScanCredsIgnoresFyrOwnPatterns covers BUG-538 and BUG-544: compiled fyr
// .rodata strings, post-Baseline() indexing/transcripts/WAL of the tracked repo
// and seeded brain.db trail records, and >maxScan ELF/Mach-O Go build-cache
// outputs pass, while novel credentials planted anywhere fail closed.
func TestScanCredsIgnoresFyrOwnPatterns(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "fyr")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/fyr")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/fyr: %v\n%s", err, out)
	}
	rawBin, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	runs := printableRuns(rawBin, 4)
	if len(runs) < 4096 {
		t.Fatalf("printable runs from fyr binary = %d bytes, want >= 4096", len(runs))
	}
	strDir := t.TempDir()
	put(t, filepath.Join(strDir, "fyr-strings.txt"), string(runs), 0o644)
	if got := newCreds(nil, scanCreds(strDir)); len(got) != 0 {
		t.Errorf("compiled fyr printable strings flagged: %v", got)
	}

	assertSelfHostingSandbox(t)
	assertSelfHostedRepoIndex(t, root, bin)
}
