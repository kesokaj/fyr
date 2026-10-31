package dream

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// TestRunAgyPrivateHomeAndStdin: agy gets the prompt as one stream-json
// message on stdin, never on the command line, and runs from a private HOME
// whose settings grant nothing, whatever the operator's own settings allow.
func TestRunAgyPrivateHomeAndStdin(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	real := agyLogin(t)
	rec := fakeCLI(t, "agy", agyResult(`{"proposals":[],"skills":[]}`, false), 0)
	var out bytes.Buffer
	if err := Run(runOpts(f, &out)); err != nil {
		t.Fatal(err)
	}
	args := read(t, filepath.Join(rec, "args"))
	if strings.Contains(args, "You are the dreamer") || !strings.Contains(args, "stream-json") {
		t.Errorf("agy args carry the prompt or lack stream-json:\n%s", args)
	}
	var msg struct {
		Event   string `json:"event"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(rec, "stdin"))), &msg); err != nil || msg.Event != "user" || !strings.HasPrefix(msg.Message.Content, "dreaming: "+filepath.Base(f.root)+"\n\nIMPORTANT: every tool call") {
		t.Errorf("stdin is not one user message with the prompt: %v %+v", err, msg)
	}
	childHome := read(t, filepath.Join(rec, "home"))
	if childHome == real || !strings.Contains(childHome, "dream-agy-home-") {
		t.Errorf("child HOME = %s, want a private one", childHome)
	}
	if got := read(t, filepath.Join(rec, "settings")); strings.Contains(got, "always-proceed") || !strings.Contains(got, `"allow":[]`) || !strings.Contains(got, `"toolPermission":"request-review"`) {
		t.Errorf("child settings grant something: %s", got)
	}
	if !strings.Contains(read(t, filepath.Join(rec, "agyfiles")), "antigravity-oauth-token") {
		t.Error("the login was not copied")
	}
	if _, err := os.Stat(childHome); !os.IsNotExist(err) {
		t.Error("the private HOME was left behind")
	}
	// Zero proposals: no record, no file, watermark advanced, logged.
	if _, err := os.Stat(filepath.Join(f.home, harness.StateDir, "proposals")); !os.IsNotExist(err) {
		t.Error("zero proposals wrote a file")
	}
	if recs := dreamProposals(t, f.home); len(recs) != 0 {
		t.Errorf("zero proposals stored %d records", len(recs))
	}
	if st, _ := readState(f.home); !st.Projects[f.root].Equal(now) {
		t.Errorf("watermark not advanced: %+v", st)
	}
	if !strings.Contains(read(t, filepath.Join(f.home, harness.StateDir, "dream.log")), "ok 0 proposals") {
		t.Error("zero proposals not logged")
	}
}

func TestRunAgyDeniedToolIsNoAnswer(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	agyLogin(t)
	fakeCLI(t, "agy", agyResult(`{"proposals":[],"skills":[]}`, true), 0)
	var ee *ExitError
	if err := Run(runOpts(f, &bytes.Buffer{})); !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if st, _ := readState(f.home); !st.Projects[f.root].IsZero() {
		t.Error("watermark advanced on a denied tool call")
	}
}

// TestRunAgyWithoutLoginFailsTheProject: with no login under $HOME the
// private home cannot be built, which fails the project that needed it,
// exit 1, never the run (plan D12), and no call is made.
func TestRunAgyWithoutLoginFailsTheProject(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	t.Setenv("HOME", t.TempDir())
	rec := fakeCLI(t, "agy", agyResult(`{"proposals":[],"skills":[]}`, false), 0)
	var ee *ExitError
	if err := Run(runOpts(f, &bytes.Buffer{})); !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if log := read(t, filepath.Join(f.home, harness.StateDir, "dream.log")); !strings.Contains(log, f.root+" fail no Antigravity login") {
		t.Errorf("dream.log does not fail the project on the login:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(rec, "stdin")); !os.IsNotExist(err) {
		t.Error("agy was called without a home to run in")
	}
	if st, _ := readState(f.home); !st.Attempts[f.root].IsZero() {
		t.Error("a project that never reached the call counts as attempted")
	}
}

// TestAgyHomeMacKeychain: on macOS agy keeps its login in the Keychain, not
// in antigravity-oauth-token, and finds it only through $HOME/Library/Keychains
// (operator probe 2026-10-09, BUG-166). The private HOME links that folder;
// the token file is copied only when there is one. Linux keeps requiring it.
func TestAgyHomeMacKeychain(t *testing.T) {
	conf := t.TempDir()
	real := t.TempDir()
	t.Setenv("HOME", real)
	keychains := filepath.Join(real, "Library", "Keychains")
	write(t, filepath.Join(keychains, "login.keychain-db"), "keychain", fresh)
	write(t, filepath.Join(real, ".gemini", "antigravity-cli", "installation_id"), "id", fresh)

	if _, _, err := agyHome(conf, "linux"); err == nil || !strings.Contains(err.Error(), "antigravity-oauth-token") {
		t.Fatalf("linux without the token file: err = %v, want the missing-login error", err)
	}

	home, cleanup, err := agyHome(conf, "darwin")
	if err != nil {
		t.Fatalf("darwin with a Keychain and no token file: %v", err)
	}
	link := filepath.Join(home, "Library", "Keychains")
	if target, err := os.Readlink(link); err != nil || target != keychains {
		t.Errorf("Library/Keychains = %q, %v; want a link to %s", target, err, keychains)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")); !os.IsNotExist(err) {
		t.Errorf("a token file appeared in the private HOME: %v", err)
	}
	if !strings.Contains(read(t, filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")), `"allow":[]`) {
		t.Error("the private settings grant something")
	}
	cleanup()
	if _, err := os.Stat(filepath.Join(keychains, "login.keychain-db")); err != nil {
		t.Fatalf("cleanup reached through the link into the real Keychain folder: %v", err)
	}

	// Both: the file is copied and the folder linked.
	write(t, filepath.Join(real, ".gemini", "antigravity-cli", "antigravity-oauth-token"), "token", fresh)
	home, cleanup, err = agyHome(conf, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")) != "token" {
		t.Error("the token file was not copied")
	}
	if _, err := os.Readlink(filepath.Join(home, "Library", "Keychains")); err != nil {
		t.Errorf("Keychains not linked when a token file exists too: %v", err)
	}
	cleanup()

	// A HOME that is not absolute is refused: the check and the link would
	// resolve against different directories.
	t.Setenv("HOME", "relative/home")
	if _, _, err := agyHome(conf, "darwin"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("relative HOME: err = %v, want a refusal", err)
	}
	t.Setenv("HOME", real)

	// Neither: the error names both places. A Keychains path that is a file
	// is no Keychain folder.
	os.RemoveAll(filepath.Join(real, "Library"))
	write(t, keychains, "not a folder", fresh)
	os.Remove(filepath.Join(real, ".gemini", "antigravity-cli", "antigravity-oauth-token"))
	if _, _, err := agyHome(conf, "darwin"); err == nil || !strings.Contains(err.Error(), "Keychain") || !strings.Contains(err.Error(), "antigravity-oauth-token") {
		t.Errorf("darwin with no login at all: err = %v, want both places named", err)
	}
}

// TestAgyAuthHint: an agy that asks for a browser login in the branch's
// home (a locked macOS Keychain, an expired login) says so in the failure
// reason, naming that home, not just "invalid output".
func TestAgyAuthHint(t *testing.T) {
	if h := agyAuthHint("", "Authentication required. Please visit the URL to log in:", privateWhere); !strings.Contains(h, "not logged in inside "+privateWhere) || !strings.Contains(h, "Keychain") {
		t.Errorf("hint = %q", h)
	}
	if h := agyAuthHint("", "Authentication required.", visibleWhere); !strings.Contains(h, "not logged in inside the operator's Antigravity home") {
		t.Errorf("visible hint = %q", h)
	}
	if h := agyAuthHint(`{"event":"result"}`, "authenticated fine", privateWhere); h != "" {
		t.Errorf("hint on a normal run = %q", h)
	}
	if h := agyAuthHint(`{"event":"result","result":{"response":"the log said Authentication required"}}`+"\n", "", privateWhere); h != "" {
		t.Errorf("hint from model text quoting the phrase = %q", h)
	}
	if h := agyAuthHint("Authentication required. Please visit the URL to log in:\n{\"event\":\"init\"}\n", "", privateWhere); h == "" {
		t.Error("no hint when agy prints the prompt on stdout")
	}
}

// TestRunAgyAuthFailureNamed: through Run, both failure paths (agy exits
// non-zero; agy exits 0 with no answer) log the login hint.
func TestRunAgyAuthFailureNamed(t *testing.T) {
	for _, code := range []int{0, 1} {
		f := newFixture(t)
		scaffold(t, f)
		agyLogin(t)
		fakeCLI(t, "agy", "Authentication required. Please visit the URL to log in:\n", code)
		var out bytes.Buffer
		if err := Run(runOpts(f, &out)); err == nil {
			t.Fatalf("exit %d: a run with no login succeeded", code)
		}
		if log := read(t, filepath.Join(f.home, harness.StateDir, "dream.log")); !strings.Contains(log, "not logged in inside the dream job's private HOME") {
			t.Errorf("exit %d: dream.log lacks the login hint:\n%s", code, log)
		}
	}
}
