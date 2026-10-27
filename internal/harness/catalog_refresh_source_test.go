package harness

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// overrideShapes refuses a source override that is not an https URL or an absolute path.
func (r *refreshRig) overrideShapes() {
	t, script, tmp, gskills, pinA, pinG, fakeHome := r.t, r.script, r.tmp, r.gskills, r.pinA, r.pinG, r.fakeHome
	// The source overrides take an https URL or an absolute path, nothing
	// else: git has transports that run a command (`ext::`).
	for _, bad := range []string{"ext::sh -c id", "relative/path", "http://example.invalid/x", "file:///x", "https://user:token@example.invalid/x", "http://user:token@example.invalid/x", "https://example.invalid/x?access_token=abc"} {
		cmd := exec.Command("bash", script, "--dest", filepath.Join(tmp, "dest-o"), "--work", filepath.Join(tmp, "work-o"), pinA, pinG)
		cmd.Dir = tmp
		cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+bad, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "CATALOG_AGENTS_CLI_REPO must be an https:// URL or an absolute path") {
			t.Errorf("override %q: want a refusal naming the variable, got err %v\n%s", bad, err, out)
		}
		// No refused value is echoed: any of them may hold a credential
		// (an http:// URL with user information is refused for its scheme).
		if strings.Contains(string(out), bad) || strings.Contains(string(out), "token") || strings.Contains(string(out), "example.invalid") {
			t.Errorf("override %q: the refusal echoes the value:\n%s", bad, out)
		}
		if strings.HasPrefix(bad, "https://") && !strings.Contains(string(out), "without credentials") {
			t.Errorf("override with credentials: the refusal does not say why:\n%s", out)
		}
		// Refused before anything is cloned: nothing is on disk, and the
		// note about the work directory would be false.
		if strings.Contains(string(out), "delete that directory") {
			t.Errorf("override %q: a refusal before any clone speaks of a value on disk:\n%s", bad, out)
		}
	}
	if _, err := os.Stat(filepath.Join(tmp, "work-o")); err == nil {
		t.Error("a refused override still created the work directory")
	}
}

// cloneByRole names a source that cannot be cloned by its role.
func (r *refreshRig) cloneByRole() {
	t, script, tmp, gskills, pinA, pinG, fakeHome := r.t, r.script, r.tmp, r.gskills, r.pinA, r.pinG, r.fakeHome
	// A source that passes the check and then cannot be cloned is named by
	// its role, not by its value: a token can sit in a URL's path.
	{
		cmd := exec.Command("bash", script, "--dest", filepath.Join(tmp, "dest-c"), "--work", filepath.Join(tmp, "work-c"), pinA, pinG)
		cmd.Dir = tmp
		secretPath := filepath.Join(tmp, "no-such-SECRETPATH-repository")
		cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+secretPath, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "cannot clone the agents-cli source") || strings.Contains(string(out), "SECRETPATH") || strings.Contains(string(out), "fatal:") {
			t.Errorf("a source that cannot be cloned: want it named by role, and neither its value nor git's own message in the output, got err %v\n%s", err, out)
		}
	}
}

// gitChannels keeps an override's value out of git's trace files and askpass.
func (r *refreshRig) gitChannels() {
	t, script, tmp, gskills, pinA, pinG, fakeHome := r.t, r.script, r.tmp, r.gskills, r.pinA, r.pinG, r.fakeHome
	// git has channels that no redirect of its output covers: a trace file
	// named by the caller's environment records the clone's arguments. The
	// script turns tracing off for its own git calls (security review of
	// 7b3e969..a224329).
	{
		trace := filepath.Join(tmp, "git-trace.log")
		trace2 := filepath.Join(tmp, "git-trace2.log")
		cmd := exec.Command("bash", script, "--dest", filepath.Join(tmp, "dest-t"), "--work", filepath.Join(tmp, "work-t"), pinA, pinG)
		cmd.Dir = tmp
		secretPath := filepath.Join(tmp, "no-such-SECRETTRACE-repository")
		cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+secretPath, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=", "GIT_TRACE="+trace, "GIT_TRACE2_EVENT="+trace2)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "cannot clone the agents-cli source") {
			t.Errorf("a traced run with a source that cannot be cloned: want the role-named refusal, got err %v\n%s", err, out)
		}
		for _, f := range []string{trace, trace2} {
			if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), "SECRETTRACE") {
				t.Errorf("%s holds the override's value: the script's git calls ran with the caller's tracing on", filepath.Base(f))
			}
		}
		// A trace target can also come from git's configuration, where no
		// environment variable of the caller's names it: the script's own
		// GIT_TRACE2* settings must win over it.
		cfgHome := filepath.Join(tmp, "trace-home")
		cfgTraces := map[string]string{ // trace2 key: its file
			"eventTarget":  filepath.Join(tmp, "git-trace2-config-event.log"),
			"normalTarget": filepath.Join(tmp, "git-trace2-config-normal.log"),
			"perfTarget":   filepath.Join(tmp, "git-trace2-config-perf.log"),
		}
		if err := os.MkdirAll(cfgHome, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := "[trace2]\n"
		for key, f := range cfgTraces {
			cfg += "\t" + key + " = " + f + "\n"
		}
		if err := os.WriteFile(filepath.Join(cfgHome, ".gitconfig"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command("bash", script, "--dest", filepath.Join(tmp, "dest-t2"), "--work", filepath.Join(tmp, "work-t2"), pinA, pinG)
		cmd.Dir = tmp
		cmd.Env = fixtureEnv("HOME="+cfgHome, "CATALOG_AGENTS_CLI_REPO="+secretPath, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err = cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "cannot clone the agents-cli source") {
			t.Errorf("a run under a trace2 target in git's config: want the role-named refusal, got err %v\n%s", err, out)
		}
		for key, f := range cfgTraces {
			if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), "SECRETTRACE") {
				t.Errorf("%s holds the override's value: trace2.%s from git's config was left on", filepath.Base(f), key)
			}
		}
		// git asks for a credential through an askpass program before it
		// tries the terminal, and hands it a prompt that names the URL: an
		// https source that answers 401 must not reach one (security review
		// of a224329..01255bc).
		var asked atomic.Int32
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked.Add(1)
			w.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
			http.Error(w, "credentials required", http.StatusUnauthorized)
		}))
		defer srv.Close()
		askLog := filepath.Join(tmp, "askpass.log")
		askStub := filepath.Join(tmp, "askpass.sh")
		if err := os.WriteFile(askStub, []byte("#!/bin/sh\necho \"$*\" >> '"+askLog+"'\necho x\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command("bash", script, "--dest", filepath.Join(tmp, "dest-a"), "--work", filepath.Join(tmp, "work-a"), pinA, pinG)
		cmd.Dir = tmp
		cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+srv.URL+"/SECRETASK", "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=", "GIT_SSL_NO_VERIFY=1", "GIT_ASKPASS="+askStub, "SSH_ASKPASS="+askStub,
			"https_proxy=", "HTTPS_PROXY=", "ALL_PROXY=", "all_proxy=", "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1")
		out, err = cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "cannot clone the agents-cli source") || strings.Contains(string(out), "SECRETASK") {
			t.Errorf("an https source that wants credentials: want the role-named refusal and no value, got err %v\n%s", err, out)
		}
		if b, err := os.ReadFile(askLog); err == nil {
			t.Errorf("git ran the caller's askpass program for the script's clone:\n%s", b)
		}
		// The condition this case sets up: git did ask the server, and was
		// told 401. Without that the stub staying silent proves nothing.
		if asked.Load() == 0 {
			t.Errorf("git never reached the https fixture (no https transport, or a proxy in the way): the askpass case tested nothing\n%s", out)
		}
		// GIT_CURL_VERBOSE is on whenever it is set, whatever its value, and
		// what it prints goes where the calls' output goes (discarded), so
		// no run shows the difference: the script's text is pinned instead.
		// It removes the variable and never assigns it.
		text, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(text), "GIT_CURL_VERBOSE=") || !strings.Contains(string(text), "unset GIT_CURL_VERBOSE") {
			t.Error("the script assigns GIT_CURL_VERBOSE or does not unset it: git turns curl's trace on when the variable is set to anything, 0 included")
		}
	}
}

// fetchByRole names a source that cannot be fetched by its role.
func (r *refreshRig) fetchByRole() {
	t, repo, script, tmp, gskills, dest, work, pinA, pinG, good := r.t, r.repo, r.script, r.tmp, r.gskills, r.dest, r.work, r.pinA, r.pinG, r.good
	// The same on the path every later refresh takes: a work directory that
	// already holds the clones fetches, and a fetch that fails says which
	// source, not its value (the v2.2.0 gate's L3).
	{
		secretPath := filepath.Join(tmp, "no-such-SECRETFETCH-repository")
		cmd := exec.Command("bash", script, "--dest", dest, "--work", work, pinA, pinG)
		cmd.Dir = repo
		cmd.Env = fixtureEnv("CATALOG_AGENTS_CLI_REPO="+secretPath, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "cannot fetch the agents-cli source") || strings.Contains(string(out), "SECRETFETCH") || strings.Contains(string(out), "fatal:") {
			t.Errorf("a source that cannot be fetched: want it named by role, and neither its value nor git's own message in the output, got err %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "delete that directory if the value is sensitive") {
			t.Errorf("a failed fetch does not say an override's value stays in the work directory (remote set-url wrote it):\n%s", out)
		}
		if r.snapshot() != good {
			t.Error("a failed fetch changed the destination")
		}
		// Before the fetch the clone is pointed at the source, and git
		// repeats the value when it cannot write the clone's config (a
		// stale lock file): that message is not shown either (security
		// review of 4165f81..7b3e969).
		lock := filepath.Join(work, "agents-cli", ".git", "config.lock")
		if err := os.WriteFile(lock, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		lockPath := filepath.Join(tmp, "no-such-SECRETLOCK-repository")
		cmd = exec.Command("bash", script, "--dest", dest, "--work", work, pinA, pinG)
		cmd.Dir = repo
		cmd.Env = fixtureEnv("CATALOG_AGENTS_CLI_REPO="+lockPath, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err = cmd.CombinedOutput()
		os.Remove(lock)
		if err == nil || !strings.Contains(string(out), "cannot point the agents-cli clone at its source") || strings.Contains(string(out), "SECRETLOCK") || strings.Contains(string(out), "error:") {
			t.Errorf("a clone whose config cannot be written: want it named by role, and neither the source's value nor git's own message in the output, got err %v\n%s", err, out)
		}
		if r.snapshot() != good {
			t.Error("a failed set-url changed the destination")
		}
		if out, code := r.run(pinA, pinG); code != 0 || r.snapshot() != good {
			t.Fatalf("could not refresh again after the failed fetch (exit %d)\n%s", code, out)
		}
	}
}
