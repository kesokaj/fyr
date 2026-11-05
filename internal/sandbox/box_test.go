package sandbox

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A Box run end to end, as a verb with no setup or check of its own runs it:
// agy gets the prompt with its prefix, the print timeout, the git identity
// and HOME of Config; the token is linked during the session only; a second
// session appends to err.txt; the after steps pass and remove the token link
// and settings.json. eval's tests (internal/eval) drive the same steps
// through eval.Run, its byte-identical output included.
func TestBoxSession(t *testing.T) {
	r := newRig(t)
	b := r.prepared(r.config("ok"))
	ctx := context.Background()
	if rc := b.Session(ctx, r.agy, filepath.Join(r.real, TokenRel), "hello", "out.txt"); rc != 0 {
		t.Fatalf("rc %d\n%s", rc, r.out.String())
	}
	if rc := b.Session(ctx, r.agy, filepath.Join(r.real, TokenRel), "again", "out2.txt"); rc != 0 {
		t.Fatalf("second rc %d", rc)
	}
	args := strings.Split(strings.TrimSpace(r.read("agy.args.1")), "\n")
	want := []string{"-p", "The project root is the current directory, " + b.App() + "; ignore any other checkout. This is a print-mode run (`agy -p`): the turn's end is the run's end. hello", "--mode", "accept-edits", "--print-timeout", "20m"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("agy args %q, want %q", args, want)
	}
	env := r.read("agy.env.1")
	for _, kv := range []string{"HOME=" + b.Home(), "GIT_AUTHOR_NAME=w", "GIT_COMMITTER_EMAIL=w@localhost", "PATH=" + filepath.Join(b.Home(), "bin") + ":"} {
		if !strings.Contains(env, "\n"+kv) && !strings.HasPrefix(env, kv) {
			t.Errorf("agy env lacks %q", kv)
		}
	}
	if strings.Contains(env, "GIT_AUTHOR_NAME=agy-rel") {
		t.Error("agy got eval's identity, not Config's")
	}
	if got := strings.TrimSpace(r.read("agy.cwd.1")); got != b.App() {
		t.Errorf("agy ran in %s, want the clone %s", got, b.App())
	}
	if r.read("linked.1") == "" || r.read("linked.2") == "" {
		t.Error("the token was not linked for both sessions")
	}
	if !strings.Contains(r.read("settings.1"), `"command(git)"`) || !strings.Contains(r.read("settings.1"), b.App()) {
		t.Errorf("settings: %s", r.read("settings.1"))
	}
	if errs, _ := os.ReadFile(filepath.Join(b.Dir(), "err.txt")); string(errs) != "stderr 1\nstderr 2\n" {
		t.Errorf("err.txt %q, want both sessions' stderr", errs)
	}
	if out, _ := os.ReadFile(filepath.Join(b.Dir(), "out2.txt")); string(out) != "session 2\n" {
		t.Errorf("out2.txt %q", out)
	}
	if !b.After() {
		t.Fatal("After: the sandbox is not intact")
	}
	b.ExitChecks()
	if len(b.Reasons()) != 0 || b.TimedOut() {
		t.Errorf("reasons %v, timed out %v\n%s", b.Reasons(), b.TimedOut(), r.out.String())
	}
	for _, p := range []string{TokenRel, ".gemini/antigravity-cli/settings.json"} {
		if _, err := os.Lstat(filepath.Join(b.Home(), p)); !os.IsNotExist(err) {
			t.Errorf("%s is still in the sandbox", p)
		}
	}
	if b, err := os.ReadFile(filepath.Join(r.real, TokenRel)); err != nil || string(b) != "token" {
		t.Errorf("the real token: %q %v", b, err)
	}
}

// A session past Timeout is a note and TimedOut, never a reason: each verb
// decides what a timeout means (eval keeps its verdicts, D37).
func TestBoxTimedOut(t *testing.T) {
	r := newRig(t)
	c := r.config("slow")
	c.Timeout = 300 * time.Millisecond
	b := r.prepared(c)
	put(t, filepath.Join(r.rec, "hang"), "", 0o644)
	if rc := b.Session(context.Background(), r.agy, filepath.Join(r.real, TokenRel), "p", "out.txt"); rc != -1 {
		t.Errorf("rc %d, want -1 for a killed agy", rc)
	}
	b.After()
	b.ExitChecks()
	if !b.TimedOut() || len(b.Reasons()) != 0 {
		t.Errorf("timed out %v, reasons %v", b.TimedOut(), b.Reasons())
	}
	if !strings.Contains(r.out.String(), "test: slow: agy timed out after 300ms; agy itself was killed\n") {
		t.Errorf("no timeout note:\n%s", r.out.String())
	}
}

// Make never reuses a directory, and every note carries the prefix and is
// made printable.
func TestBoxMakeAndNote(t *testing.T) {
	r := newRig(t)
	c := r.config("twice")
	if b := r.box(c); !b.Make() {
		t.Fatalf("first Make: %v", b.Reasons())
	}
	b := r.box(c)
	if b.Make() || strings.Join(b.Reasons(), "; ") != ReasonExists {
		t.Errorf("a second Make: reasons %v, want [%s]", b.Reasons(), ReasonExists)
	}
	var out bytes.Buffer
	c.Prefix, c.Out = "work: fix-a", &out
	New(c).Note("line %s", "one\ntwo")
	if out.String() != "work: fix-a: line one?two\n" {
		t.Errorf("note %q", out.String())
	}
}

// TestBoxMakeTrialPath: Make builds a trial's sandbox <stamp>/<row>/<i>
// under parents that do not exist yet, a sibling trial beside it, and never
// reuses either (fyr eval --runs, the eval-metrics plan D4).
func TestBoxMakeTrialPath(t *testing.T) {
	r := newRig(t)
	c1, c2 := r.config(filepath.Join("stamp", "row", "1")), r.config(filepath.Join("stamp", "row", "2"))
	for _, c := range []Config{c1, c2} {
		if b := r.box(c); !b.Make() {
			t.Fatalf("Make %s: %v", c.Dir, b.Reasons())
		}
		if st, err := os.Stat(c.Dir); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v, want a 0700 directory", c.Dir, st, err)
		}
	}
	if b := r.box(c1); b.Make() || strings.Join(b.Reasons(), "; ") != ReasonExists {
		t.Errorf("trial 1 again: reasons %v, want [%s]", b.Reasons(), ReasonExists)
	}
}

// A token link that cannot be made fails the box token link, and the after
// steps still check the real token, never an empty path (security review of
// Phase 1, finding 2).
func TestBoxTokenLinkRefused(t *testing.T) {
	r := newRig(t)
	b := r.prepared(r.config("tok"))
	put(t, filepath.Join(b.Home(), TokenRel), "in the way", 0o600)
	if rc := b.Session(context.Background(), r.agy, filepath.Join(r.real, TokenRel), "p", "out.txt"); rc != -1 {
		t.Errorf("rc %d, want -1", rc)
	}
	b.After()
	b.ExitChecks()
	if strings.Join(b.Reasons(), "; ") != ReasonToken || r.read("agy.n") != "" {
		t.Errorf("reasons %v, agy runs %q", b.Reasons(), r.read("agy.n"))
	}
	if strings.Contains(r.out.String(), "is no longer a regular file") {
		t.Errorf("a false warning about the real token:\n%s", r.out.String())
	}
	if err := os.Remove(filepath.Join(r.real, TokenRel)); err != nil {
		t.Fatal(err)
	}
	b2 := r.prepared(r.config("tok2"))
	put(t, filepath.Join(b2.Home(), TokenRel), "in the way", 0o600)
	b2.Session(context.Background(), r.agy, filepath.Join(r.real, TokenRel), "p", "out.txt")
	b2.After()
	if !strings.Contains(r.out.String(), "WARNING "+filepath.Join(r.real, TokenRel)+" is no longer a regular file") {
		t.Errorf("no warning for a real token that is gone:\n%s", r.out.String())
	}
}

// A later session runs only on an intact sandbox: one the first session
// replaced fails sandbox replaced and no agy starts (security review of
// Phase 1, finding 3).
func TestBoxLaterSessionNeedsIntact(t *testing.T) {
	r := newRig(t)
	b := r.prepared(r.config("swap"))
	tok := filepath.Join(r.real, TokenRel)
	if rc := b.Session(context.Background(), r.agy, tok, "p", "out.txt"); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	if err := os.Rename(b.App(), b.App()+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(r.tmp, b.App()); err != nil {
		t.Fatal(err)
	}
	if rc := b.Session(context.Background(), r.agy, tok, "p", "out2.txt"); rc != -1 || !b.Has(ReasonReplaced) {
		t.Errorf("rc %d, reasons %v", rc, b.Reasons())
	}
	if n := strings.TrimSpace(r.read("agy.n")); n != "1" {
		t.Errorf("agy ran %s times, want 1", n)
	}
	b.After()
	b.ExitChecks()
}

// On darwin, CheckLogin accepts $HOME/Library/Keychains in host mode when
// antigravity-oauth-token is absent (BUG-166), still requires the token file
// when tokenOnly is set, and refuses a symlinked token on every OS.
func TestCheckLoginMacKeychain(t *testing.T) {
	real := t.TempDir()
	tokPath := filepath.Join(real, TokenRel)
	kcPath := filepath.Join(real, KeychainsRel)
	put(t, filepath.Join(kcPath, "login.keychain-db"), "keychain", 0o600)

	if _, err := CheckLogin(real, "linux", false); err == nil || !strings.Contains(err.Error(), "no Antigravity login at "+tokPath) {
		t.Fatalf("linux without token file: %v", err)
	}
	if _, err := CheckLogin(real, "darwin", true); err == nil || !strings.Contains(err.Error(), "no Antigravity login at "+tokPath) {
		t.Fatalf("darwin tokenOnly without token file: %v", err)
	}
	if tok, err := CheckLogin(real, "darwin", false); err != nil || tok != "" {
		t.Fatalf("darwin Keychain-only: tok=%q err=%v", tok, err)
	}
	os.MkdirAll(filepath.Dir(tokPath), 0o700)
	os.Symlink(filepath.Join(kcPath, "login.keychain-db"), tokPath)
	if _, err := CheckLogin(real, "darwin", false); err == nil || !strings.Contains(err.Error(), "no Antigravity login at "+tokPath) {
		t.Errorf("symlinked token on darwin: %v", err)
	}
	os.Remove(tokPath)
	os.RemoveAll(filepath.Join(real, "Library"))
	if _, err := CheckLogin(real, "darwin", false); err == nil || !strings.Contains(err.Error(), "Keychain") || !strings.Contains(err.Error(), "antigravity-oauth-token") {
		t.Errorf("darwin with neither login: %v", err)
	}
}

// On darwin, Session links $HOME/Library/Keychains into the sandbox home (alone
// or beside the token file) and unlinks it before scanCreds runs.
func TestBoxSessionMacKeychain(t *testing.T) {
	old := hostOS
	hostOS = "darwin"
	t.Cleanup(func() { hostOS = old })

	r := newRig(t)
	tokPath, kcPath := filepath.Join(r.real, TokenRel), filepath.Join(r.real, KeychainsRel)
	if err := os.Remove(tokPath); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(kcPath, "login.keychain-db"), "keychain", 0o600)

	b := r.prepared(r.config("mac-kc"))
	if rc := b.Session(context.Background(), r.agy, "", "hello", "out.txt"); rc != 0 {
		t.Fatalf("rc %d\n%s", rc, r.out.String())
	}
	if got := strings.TrimSpace(r.read("keychains.1")); got != kcPath || r.read("linked.1") != "" {
		t.Errorf("keychains=%q token=%q", got, r.read("linked.1"))
	}
	if !b.After() {
		t.Fatal("After: not intact")
	}
	b.ExitChecks()
	if len(b.Reasons()) != 0 {
		t.Errorf("reasons %v\n%s", b.Reasons(), r.out.String())
	}
	if _, err := os.Lstat(filepath.Join(b.Home(), KeychainsRel)); !os.IsNotExist(err) {
		t.Errorf("Keychains link still in sandbox: %v", err)
	}
	put(t, tokPath, "token", 0o600)
	b2 := r.prepared(r.config("mac-both"))
	if rc := b2.Session(context.Background(), r.agy, tokPath, "hello", "out.txt"); rc != 0 {
		t.Fatalf("both rc %d", rc)
	}
	if r.read("linked.2") == "" || strings.TrimSpace(r.read("keychains.2")) != kcPath {
		t.Errorf("want both linked, got token=%q keychains=%q", r.read("linked.2"), r.read("keychains.2"))
	}
	b2.After()
	b2.ExitChecks()
	if len(b2.Reasons()) != 0 {
		t.Errorf("both reasons %v", b2.Reasons())
	}
}

// On linux, Session never links Library/Keychains even when tok is empty; on
// darwin, After warns when the Keychains link was replaced or a baseline
// keychain file in RealHome/Library/Keychains is no longer a regular file.
func TestBoxSessionKeychainGuards(t *testing.T) {
	r := newRig(t)
	kcPath := filepath.Join(r.real, KeychainsRel)
	kcFile := filepath.Join(kcPath, "login.keychain-db")
	put(t, kcFile, "keychain", 0o600)

	old := hostOS
	hostOS = "linux"
	t.Cleanup(func() { hostOS = old })
	bLinux := r.prepared(r.config("linux-no-kc"))
	bLinux.Session(context.Background(), r.agy, "", "hello", "out.txt")
	if _, err := os.Lstat(filepath.Join(bLinux.Home(), KeychainsRel)); !os.IsNotExist(err) {
		t.Errorf("linux linked Library/Keychains when tok was empty: %v", err)
	}
	bLinux.ExitChecks()

	hostOS = "darwin"
	r.out.Reset()
	bWarn := r.prepared(r.config("mac-kc-warn"))
	if rc := bWarn.Session(context.Background(), r.agy, "", "hello", "out.txt"); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	os.Remove(filepath.Join(bWarn.Home(), KeychainsRel))
	os.Remove(kcFile)
	bWarn.After()
	bWarn.ExitChecks()
	out := r.out.String()
	if !strings.Contains(out, "WARNING the Keychains link was replaced; the real login may be stale") {
		t.Errorf("missing Keychains link warning in:\n%s", out)
	}
	if !strings.Contains(out, "WARNING "+kcFile+" is no longer a regular file") {
		t.Errorf("missing keychain file warning in:\n%s", out)
	}
}
