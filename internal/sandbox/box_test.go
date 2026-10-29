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
	want := []string{"-p", "The project root is the current directory, " + b.App() + "; ignore any other checkout. hello", "--mode", "accept-edits", "--print-timeout", "20m"}
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
