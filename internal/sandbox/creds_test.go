package sandbox

import (
	"os"
	"os/exec"
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

// Gate review of v3.0.0: a directory link the baseline held unchanged is
// skipped only while it leads inside the scanned tree; one that leads out
// is never walked, so a credential written through it would go unseen, and
// it counts.
func TestCredsBaselineLinkOutsideCounts(t *testing.T) {
	h, elsewhere := t.TempDir(), t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(h, "out")); err != nil {
		t.Fatal(err)
	}
	base := scanCreds(h)
	got := newCreds(base, scanCreds(h))
	if len(got) != 1 || got[0] != "out (a link to a directory, not walked)" {
		t.Errorf("a baseline link out of the tree passes: %v", got)
	}
}

// TestCredsLockedModules: a node_modules beside a regular lockfile is a
// package manager's install, whose files are not read (BUG-185: prettier's
// flow plugin holds an AKIA-shaped string), while the name scan, links out of
// the tree and unreadable files still count inside it; a node_modules with
// no lockfile or a linked one, a sibling whose name only starts with
// node_modules, the lockfile itself, a plain file named node_modules and a
// scan root named node_modules are read as before.
func TestCredsLockedModules(t *testing.T) {
	h := t.TempDir()
	base := scanCreds(h)
	outside := filepath.Join(t.TempDir(), "token.txt")
	put(t, outside, ghToken, 0o600)
	web := filepath.Join(h, "app", "web")
	put(t, filepath.Join(web, "package-lock.json"), "{}", 0o644)
	put(t, filepath.Join(web, "node_modules", "prettier", "plugins", "flow.js"), "x="+awsKey, 0o644)
	put(t, filepath.Join(web, "node_modules", "a", "node_modules", "b", "i.js"), ghToken, 0o644)
	put(t, filepath.Join(web, "node_modules", "a", "id_rsa"), "", 0o600)
	if err := os.MkdirAll(filepath.Join(web, "node_modules", ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"inside": filepath.Join("..", "prettier", "plugins", "flow.js"), "outside": outside} {
		if err := os.Symlink(target, filepath.Join(web, "node_modules", ".bin", name)); err != nil {
			t.Fatal(err)
		}
	}
	put(t, filepath.Join(web, "node_modules.bak", "k.js"), awsKey, 0o644)
	for _, lock := range []string{"npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb"} {
		put(t, filepath.Join(h, "app", lock, lock), ghToken, 0o644)
		put(t, filepath.Join(h, "app", lock, "node_modules", "z.js"), awsKey, 0o644)
	}
	put(t, filepath.Join(h, "app", "loose", "node_modules", "y.js"), awsKey, 0o644)
	put(t, filepath.Join(h, "app", "linked", "real-lock"), "{}", 0o644)
	if err := os.Symlink("real-lock", filepath.Join(h, "app", "linked", "package-lock.json")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(h, "app", "linked", "node_modules", "y.js"), awsKey, 0o644)
	put(t, filepath.Join(h, "app", "file", "pnpm-lock.yaml"), "", 0o644)
	put(t, filepath.Join(h, "app", "file", "node_modules"), ghToken, 0o644)
	want := []string{
		filepath.Join("app", "file", "node_modules") + " (github-token)",
		filepath.Join("app", "linked", "node_modules", "y.js") + " (aws-key)",
		filepath.Join("app", "loose", "node_modules", "y.js") + " (aws-key)",
		filepath.Join("app", "web", "node_modules", ".bin", "outside") + " (github-token)",
		filepath.Join("app", "web", "node_modules", "a", "id_rsa") + " (name)",
		filepath.Join("app", "web", "node_modules.bak", "k.js") + " (aws-key)",
	}
	for _, lock := range []string{"bun.lock", "bun.lockb", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock"} {
		want = append(want, filepath.Join("app", lock, lock)+" (github-token)")
	}
	if os.Geteuid() != 0 {
		put(t, filepath.Join(web, "node_modules", "locked.js"), "", 0o000)
		want = append(want, filepath.Join("app", "web", "node_modules", "locked.js")+" (unreadable)")
	}
	slices.Sort(want)
	if got := newCreds(base, scanCreds(h)); !slices.Equal(got, want) {
		t.Errorf("findings\n got %q\nwant %q", got, want)
	}

	// The scan root is never an install, even named node_modules beside a
	// lockfile: the lockfile lies outside the tree.
	top := t.TempDir()
	put(t, filepath.Join(top, "package-lock.json"), "{}", 0o644)
	nm := filepath.Join(top, "node_modules")
	put(t, filepath.Join(nm, "x.js"), awsKey, 0o644)
	if got := newCreds(nil, scanCreds(nm)); !slices.Equal(got, []string{"x.js (aws-key)"}) {
		t.Errorf("a scan root named node_modules: %q", got)
	}
}

// TestScanCredsGoBuildCacheArchivesAndPlantedCreds is the BUG-520 regression
// pin: compiled Go build cache Unix ar archives ("!<arch>\n") under
// home/.cache/go-build/<xx>/<hex>-a|-d carrying redactor test patterns or
// exceeding maxScan pass, while plaintext secrets, credential-named files,
// symlinks and unreadable files planted inside home/.cache/go-build/ fail
// closed.
func TestScanCredsGoBuildCacheArchivesAndPlantedCreds(t *testing.T) {
	old := maxScan
	maxScan = 128
	t.Cleanup(func() { maxScan = old })

	h := t.TempDir()
	base := scanCreds(h)
	gb := filepath.Join(h, "home", ".cache", "go-build")
	put(t, filepath.Join(gb, "README"), "This directory holds cached build artifacts.\n", 0o644)
	put(t, filepath.Join(gb, "00", "3ca7d41d7f2166632c91f1865e8f4adbb12b55dffeb8302087315de6385734f0-d"),
		"!<arch>\n__.PKGDEF\n"+ghToken+"\n"+awsKey+"\n", 0o644)
	put(t, filepath.Join(gb, "00", "3e4b8790b7e608509787b1d693e3f5c9feaf6d4134151a531993b1e9831dcbe2-d"),
		"!<arch>\n__.PKGDEF\n"+strings.Repeat("x", 256)+ghToken, 0o644)
	if got := newCreds(base, scanCreds(h)); len(got) != 0 {
		t.Fatalf("go-build archives flagged as credentials: %v", got)
	}

	outside := filepath.Join(t.TempDir(), "secret.a")
	put(t, outside, "!<arch>\n"+ghToken, 0o600)
	put(t, filepath.Join(gb, "00", "deadbeef-d"), "plaintext "+ghToken, 0o644)
	put(t, filepath.Join(gb, "leak.txt"), "!<arch>\n"+awsKey, 0o644)
	put(t, filepath.Join(gb, "00", "id_rsa"), "!<arch>\n", 0o600)
	if err := os.Symlink(outside, filepath.Join(gb, "00", "feedface-d")); err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join("home", ".cache", "go-build", "00", "deadbeef-d") + " (github-token)",
		filepath.Join("home", ".cache", "go-build", "00", "feedface-d") + " (github-token)",
		filepath.Join("home", ".cache", "go-build", "00", "id_rsa") + " (name)",
		filepath.Join("home", ".cache", "go-build", "leak.txt") + " (aws-key)",
	}
	if os.Geteuid() != 0 {
		locked := filepath.Join(gb, "00", "badcafe0-d")
		put(t, locked, "!<arch>\n", 0o000)
		t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })
		want = append(want, filepath.Join("home", ".cache", "go-build", "00", "badcafe0-d")+" (unreadable)")
	}
	slices.Sort(want)
	if got := newCreds(base, scanCreds(h)); !slices.Equal(got, want) {
		t.Errorf("planted credentials under go-build:\n got %q\nwant %q", got, want)
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

// TestScanCredsIgnoresFyrOwnPatterns covers BUG-538: compiled fyr .rodata
// strings, post-Baseline() copies of baseline fixture tokens into search.db and
// agy transcripts, and >maxScan ELF/Mach-O Go build-cache outputs pass, while
// novel credentials planted anywhere still fail closed.
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
}

func assertSelfHostingSandbox(t *testing.T) {
	t.Helper()
	sb := t.TempDir()
	baseGH := "gh" + "p_" + "abcdefghijklmnopqrstuvwxyz0123456789ABCD"
	baseHdr := "-----" + "BEGIN OPENSSH " + "PRIVATE KEY-----"
	basePEM := "-----" + "BEGIN RSA " + "PRIVATE KEY-----\nMIIEpAIBAAKCAQEAsecretbody\n-----END RSA PRIVATE KEY-----"
	baseRef := `{"` + "refresh_" + `token": "x"}`
	fixture := strings.Join([]string{baseGH, baseHdr, basePEM, baseRef}, "\n") + "\n"
	put(t, filepath.Join(sb, "app", "internal", "fixture_test.go"), fixture, 0o644)
	base := scanCreds(sb)

	sdb := filepath.Join(sb, "app", ".agents", "scratch", "search.db")
	outTxt := filepath.Join(sb, "home", ".gemini", "antigravity-cli", "brain", "conv1", ".system_generated", "steps", "1", "output.txt")
	jsonl := filepath.Join(sb, "home", ".gemini", "antigravity-cli", "brain", "conv1", ".system_generated", "logs", "transcript.jsonl")
	put(t, sdb, "SQLite format 3\x00"+fixture, 0o600)
	put(t, outTxt, "1: "+baseGH+"\n2: "+baseHdr+"\n3: "+baseRef+"\n", 0o600)
	put(t, jsonl, `{"out":"`+strings.ReplaceAll(basePEM, "\n", `\n`)+`"}`+"\n", 0o600)
	hex64 := strings.Repeat("ab", 32)
	putSparse(t, filepath.Join(sb, "home", ".cache", "go-build", "ab", hex64+"-d"), "\x7fELF\x02\x01\x01\x00", maxScan+4096)
	putSparse(t, filepath.Join(sb, "home", ".cache", "go-build", "cd", hex64+"-d"), "\xcf\xfa\xed\xfe\x0c\x00\x00\x01", maxScan+4096)
	if got := newCreds(base, scanCreds(sb)); len(got) != 0 {
		t.Errorf("self-hosting sandbox post-baseline artifacts flagged: %v", got)
	}

	novelAnt := "sk-" + "ant-" + strings.Repeat("Q", 24)
	novelRef := `{"` + "refresh_" + `token": "1//0gNovelSecretRefreshToken"}`
	novelKey := "-----" + "BEGIN OPENSSH " + "PRIVATE KEY-----\nComment: leaked\n" + strings.Repeat("Z", 32) + "\n-----END OPENSSH PRIVATE KEY-----"
	for _, tc := range []struct{ path, payload, wantKind string }{
		{sdb, "SQLite format 3\x00" + fixture + novelAnt, "anthropic-key"},
		{outTxt, "1: " + baseRef + "\n2: " + novelRef + "\n", "refresh-token"},
		{filepath.Join(sb, "app", "leaked.pem"), novelKey, "private-key"},
		{filepath.Join(sb, "home", ".cache", "go-build", "ef", hex64+"-d"), "not-elf " + novelAnt, "anthropic-key"},
	} {
		prev, existed := readOptional(tc.path)
		put(t, tc.path, tc.payload, 0o600)
		got := newCreds(base, scanCreds(sb))
		restoreOptional(t, tc.path, prev, existed)
		if len(got) != 1 || !strings.Contains(got[0], tc.wantKind) {
			t.Errorf("novel %s in %s: got %v, want 1 hit containing %q", tc.wantKind, tc.path, got, tc.wantKind)
		}
	}

	outsideELF := filepath.Join(t.TempDir(), "outside-elf")
	putSparse(t, outsideELF, "\x7fELF\x02\x01\x01\x00", maxScan+4096)
	symPath := filepath.Join(sb, "home", ".cache", "go-build", "99", hex64+"-d")
	if err := os.MkdirAll(filepath.Dir(symPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideELF, symPath); err != nil {
		t.Fatal(err)
	}
	if got := newCreds(base, scanCreds(sb)); len(got) != 1 || !strings.Contains(got[0], "larger than") {
		t.Errorf("symlinked oversized ELF in go-build: got %v, want oversized refusal", got)
	}
}

func readOptional(p string) ([]byte, bool) {
	b, err := os.ReadFile(p)
	return b, err == nil
}

func restoreOptional(t *testing.T, p string, b []byte, existed bool) {
	t.Helper()
	if !existed {
		_ = os.Remove(p)
		return
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
