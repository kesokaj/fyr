package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

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
