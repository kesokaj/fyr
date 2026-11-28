package creds

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The shapes below are assembled so this file holds no literal credential.
var (
	ghSample   = "gh" + "o_" + strings.Repeat("A", 36)
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
	t.Parallel()
	for name, plant := range map[string]func(t *testing.T, h string){
		"an SSH key copied in": func(t *testing.T, h string) { put(t, filepath.Join(h, ".ssh", "id_ed25519"), "", 0o600) },
		"gcloud credentials": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".config", "gcloud", "credentials.db"), "", 0o600)
		},
		"an Antigravity token": func(t *testing.T, h string) { put(t, filepath.Join(h, testOAuthPath), "", 0o600) },
		"a token in a terminal log": func(t *testing.T, h string) {
			put(t, filepath.Join(h, ".gemini", "brain", "term1.env"), "declare -x GH_"+"TOK"+"EN="+ghSample+"\n", 0o644)
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
			put(t, filepath.Join(h, "conv.db"), "SQLite\x00\x01"+"tok"+"en="+ghSample+"\x00", 0o644)
		},
		"a Google access token": func(t *testing.T, h string) {
			put(t, filepath.Join(h, "trace.log"), "Auth"+"orization: "+"Bear"+"er "+"ya"+"29."+strings.Repeat("b", 40)+"\n", 0o644)
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
			put(t, filepath.Join(h, "oauth.json"), `{"`+"ref"+"resh_"+"tok"+`en": "x"}`, 0o644)
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
			left := New(nil, Scan(h))
			if len(left) == 0 {
				t.Fatal("the scan found nothing")
			}
			for _, l := range left {
				if strings.Contains(l, ghSample) || strings.Contains(l, awsKey) || strings.Contains(l, "BEGIN") {
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

var (
	sharedFyrOnce sync.Once
	sharedFyrPath string
	sharedFyrRoot string
	sharedFyrErr  error
)

func goEnvVal(key string) (string, error) {
	out, err := exec.Command("go", "env", key).Output()
	if err != nil {
		return "", fmt.Errorf("go env %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// sharedFyrBin builds ./cmd/fyr once per test binary invocation so creds and
// Landlock tests reuse the same compiled binary instead of running go build
// twice.
func sharedFyrBin(t *testing.T) (bin, root string) {
	t.Helper()
	sharedFyrOnce.Do(func() {
		sharedFyrRoot, sharedFyrErr = filepath.Abs(filepath.Join("..", "..", ".."))
		if sharedFyrErr != nil {
			return
		}
		goModCache, err := goEnvVal("GOMODCACHE")
		if err != nil {
			sharedFyrErr = err
			return
		}
		goBuildCache, err := goEnvVal("GOCACHE")
		if err != nil {
			sharedFyrErr = err
			return
		}
		dir, err := os.MkdirTemp("", "fyr-isolate-bin-*")
		if err != nil {
			sharedFyrErr = err
			return
		}
		sharedFyrPath = filepath.Join(dir, "fyr")
		cmd := exec.Command("go", "build", "-o", sharedFyrPath, "./cmd/fyr")
		cmd.Dir = sharedFyrRoot
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOMODCACHE="+goModCache, "GOCACHE="+goBuildCache)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = os.RemoveAll(dir)
			sharedFyrErr = fmt.Errorf("go build ./cmd/fyr: %w\n%s", err, out)
		}
	})
	if sharedFyrErr != nil {
		t.Fatal(sharedFyrErr)
	}
	return sharedFyrPath, sharedFyrRoot
}

// TestScanCredsIgnoresFyrOwnPatterns covers BUG-538 and BUG-544: compiled fyr
// .rodata strings, post-Baseline() indexing/transcripts/WAL of the tracked repo
// and seeded brain.db trail records, and >maxScan ELF/Mach-O Go build-cache
// outputs pass, while novel credentials planted anywhere fail closed.
func TestScanCredsIgnoresFyrOwnPatterns(t *testing.T) {
	t.Parallel()
	bin, root := sharedFyrBin(t)
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
	if got := New(nil, Scan(strDir)); len(got) != 0 {
		t.Errorf("compiled fyr printable strings flagged: %v", got)
	}

	assertSelfHostingSandbox(t)
	assertSelfHostedRepoIndex(t, root, bin)
}

const testOAuthPath = ".gemini/antigravity-cli/" + "antigravity-" + "oauth-" + "tok" + "en"

func put(t *testing.T, p, s string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
}

func TestMaskedCredPatternsAndMatches(t *testing.T) {
	t.Parallel()
	ensureCredPatterns()

	allMasked := []string{maskedCredAWS, maskedCredExample, maskedCredRefreshVal}
	for _, spec := range maskedCredSpecs {
		allMasked = append(allMasked, spec.pattern)
	}
	for idx, entry := range allMasked {
		if len(entry) == 0 {
			t.Fatalf("masked entry %d is empty", idx)
		}
		for i := range len(entry) {
			if entry[i] < 0x80 {
				t.Fatalf("masked entry %d byte %d = 0x%02x (< 0x80)", idx, i, entry[i])
			}
		}
	}

	if credPrivBegin == nil || credAWS == nil || credRefreshVal == nil || string(credExample) != awsExample {
		t.Fatalf("initCredPatterns left nil/mismatched globals: priv=%v aws=%v refresh=%v example=%q",
			credPrivBegin != nil, credAWS != nil, credRefreshVal != nil, string(credExample))
	}
	assertCredTextPatternMatches(t)
}

func assertCredTextPatternMatches(t *testing.T) {
	t.Helper()
	cases := []struct {
		kind string
		hit  string
		miss string
	}{
		{kind: "github-token", hit: ghSample, miss: "gh" + "o_short"},
		{kind: "github-pat", hit: "git" + "hub_pat_" + strings.Repeat("A", 32), miss: "git" + "hub_pat_short"},
		{kind: "private-key", hit: "-----" + "BEGIN RSA " + "PRIVATE " + "KEY-----", miss: "BEGIN PUBLIC KEY"},
		{kind: "anthropic-key", hit: "sk-" + "ant-" + strings.Repeat("a", 24), miss: "sk-" + "ant-short"},
		{kind: "openai-key", hit: "sk-" + "proj-" + strings.Repeat("b", 24), miss: "sk-" + "proj-short"},
		{kind: "refresh-token", hit: `"` + "ref" + "resh_" + "tok" + `en"`, miss: `"access_label"`},
		{kind: "google-access-token", hit: "ya" + "29." + strings.Repeat("c", 24), miss: "ya" + "29.short"},
		{kind: "google-api-key", hit: "AI" + "za" + strings.Repeat("d", 35), miss: "AI" + "za" + strings.Repeat("d", 10)},
		{kind: "gitlab-token", hit: "gl" + "pat-" + strings.Repeat("e", 24), miss: "gl" + "pat-short"},
	}
	if len(credText) != len(cases) {
		t.Fatalf("credText len = %d, want %d", len(credText), len(cases))
	}
	for i, tc := range cases {
		p := credText[i]
		if p.kind != tc.kind {
			t.Fatalf("credText[%d].kind = %q, want %q", i, p.kind, tc.kind)
		}
		if !p.re.MatchString(tc.hit) || p.re.MatchString(tc.miss) {
			t.Errorf("%s match mismatch: hit=%v miss=%v", p.kind, p.re.MatchString(tc.hit), p.re.MatchString(tc.miss))
		}
	}
	if !credAWS.MatchString(awsKey) || credAWS.MatchString("AK"+"IA"+"SHORT") {
		t.Errorf("credAWS match mismatch")
	}
	if !credRefreshVal.MatchString(`: "opaque-value"`) || credRefreshVal.MatchString("no-colon") {
		t.Errorf("credRefreshVal match mismatch")
	}
}
