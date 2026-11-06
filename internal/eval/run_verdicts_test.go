package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestRunPass is the golden path, and every step a PASS goes through: the
// clone, the records listed before, the install through Self with the
// resolved source, the wrapper, the grants and trust, the linked token, the
// exact agy argv, environment and directory, the check's environment, and
// the line printed.
func TestRunPass(t *testing.T) {
	r := newRig(t)
	x := one(t, r.run(named("ok", "hello {{CLONE}} at {{STAMP}}", `grep -q "agy says ok" "$OUT" && "$SB/bin/fyr" memory show > "$SB/wrapped.txt" && env > "$SB/check.env"`)))
	wantVerdict(t, x, "PASS")
	dir := filepath.Join(ProjectSandboxRoot(r.home, r.root), "2026-10-26-080000", "ok", "1")
	app, sb := filepath.Join(dir, "app"), filepath.Join(dir, "home")
	if x.Dir != dir || x.AgyExit != 0 {
		t.Errorf("Dir %s, agy exit %d; want %s, 0", x.Dir, x.AgyExit, dir)
	}
	if x.Trial != 1 || x.Run != "2026-10-26-080000/ok/1" || x.Cause != "" || x.SessionSeconds < 0 || x.SessionSeconds > x.Seconds || x.Started.IsZero() {
		t.Errorf("trial %d, run %q, cause %q, session %ds of %ds, started %v", x.Trial, x.Run, x.Cause, x.SessionSeconds, x.Seconds, x.Started)
	}
	if !regexp.MustCompile(`(?m)^eval: ok#1  PASS  agy exit 0  \d+s \(\d+s session\)  ` + regexp.QuoteMeta(dir) + `$`).MatchString(r.out.String()) {
		t.Errorf("the row line is missing or misshapen:\n%s", r.out.String())
	}
	if _, err := os.Stat(filepath.Join(app, "README.md")); err != nil {
		t.Errorf("the fixture was not cloned: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(sb, "records-before.txt")); string(b) != "PLAN-1  plan  open  2026-10-26  a plan\n" {
		t.Errorf("records-before.txt = %q", b)
	}
	if calls := r.read("self.calls"); !strings.Contains(calls, "install --home "+sb+" --repo "+r.repo+"\n") || !strings.Contains(calls, "record list --home "+sb+"\n") {
		t.Errorf("Self was called as:\n%s", calls)
	}
	if got := strings.TrimSpace(r.read("install.cwd")); got != sb {
		t.Errorf("install ran in %s, want the sandbox home", got)
	}
	ienv := envMap(r.read("install.env"))
	if _, ok := ienv["FYR_REPO"]; ok {
		t.Error("FYR_REPO reached the install (D16)")
	}
	if _, ok := ienv["GIT_EVAL_MARK"]; ok {
		t.Error("a GIT_* variable reached the install")
	}
	if got, _ := os.ReadFile(filepath.Join(sb, "wrapped.txt")); string(got) != "fyr memory --home "+sb+" show\n" {
		t.Errorf("the wrapper does not pin --home: %q", got)
	}
	args := strings.Split(strings.TrimSuffix(r.read("agy.args.1"), "\n"), "\n")
	wantArgs := []string{"-p", "The project root is the current directory, " + app + "; ignore any other checkout. This is a print-mode run (`agy -p`): the turn's end is the run's end. hello " + app + " at 2026-10-26-0800", "--mode", "accept-edits", "--print-timeout", "20m"}
	if strings.Join(args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Errorf("agy argv:\n got %q\nwant %q", args, wantArgs)
	}
	if got := strings.TrimSpace(r.read("agy.cwd.1")); got != app {
		t.Errorf("agy ran in %s, want the clone", got)
	}
	aenv := envMap(r.read("agy.env.1"))
	if !strings.HasPrefix(aenv["PATH"], filepath.Join(sb, "bin")+":") || aenv["HOME"] != sb || aenv["STYRMAN_SKIP_CRED_PULL"] != "1" || aenv["SHELL"] != "/bin/bash" || aenv["GIT_AUTHOR_NAME"] != "agy-rel" {
		t.Errorf("agy env: %v", aenv)
	}
	allowed := map[string]bool{"PATH": true, "TERM": true, "LANG": true, "USER": true, "LOGNAME": true, "SHELL": true, "HOME": true, "STYRMAN_SKIP_CRED_PULL": true,
		"GIT_AUTHOR_NAME": true, "GIT_AUTHOR_EMAIL": true, "GIT_COMMITTER_NAME": true, "GIT_COMMITTER_EMAIL": true,
		"PWD": true, "SHLVL": true, "_": true, "OLDPWD": true} // the last four are bash's own
	for k := range aenv {
		if !allowed[k] {
			t.Errorf("agy got %s, which is not on the allowlist (D27)", k)
		}
	}
	if r.read("linked.1") != "yes\n" {
		t.Error("the token was not linked while agy ran")
	}
	var settings struct {
		Permissions struct{ Allow []string } `json:"permissions"`
		Trusted     []string                 `json:"trustedWorkspaces"`
	}
	raw := r.read("settings.1")
	if err := json.Unmarshal([]byte(raw), &settings); err != nil || strings.Contains(raw, "toolPermission") {
		t.Fatalf("settings: %v\n%s", err, raw)
	}
	if strings.Join(settings.Permissions.Allow, " ") != strings.Join(namedGrants, " ") || len(settings.Trusted) != 1 || settings.Trusted[0] != app {
		t.Errorf("settings: %+v", settings)
	}
	cenv := envMap(func() string { b, _ := os.ReadFile(filepath.Join(sb, "check.env")); return string(b) }())
	for k, v := range map[string]string{"HOME": sb, "SB": sb, "OUT": filepath.Join(dir, "out.txt"), "OUT2": filepath.Join(dir, "out2.txt"), "FIXDIR": r.root, "CLONE": app} {
		if cenv[k] != v {
			t.Errorf("check env %s = %q, want %q", k, cenv[k], v)
		}
	}
	if strings.Contains(cenv["PATH"], filepath.Join(sb, "bin")) || cenv["FYR_REPO"] != "" {
		t.Errorf("the check's PATH holds the wrapper, or eval's environment leaked in: %v", cenv)
	}
	if _, err := os.Stat(filepath.Join(r.root, ".git", "FETCH_HEAD")); err == nil {
		t.Error("the fixture was written")
	}
}

// TestRunVerdicts: each condition of step 16 fails the row with its fixed
// reason, and the agy exit alone does not.
func TestRunVerdicts(t *testing.T) {
	leak := "gh" + "o_" + strings.Repeat("B", 36)
	for name, c := range map[string]struct {
		mode, value string
		row         Row
		verdict     string
		reasons     []string
		agyExit     int
		cause       string
	}{
		"the check fails":               {"", "", named("r", "p", "false"), "FAIL", []string{ReasonCheck}, 0, "check"},
		"agy exits 3, the check holds":  {"exit", "3", named("r", "p", "true"), "PASS", nil, 3, ""},
		"the agent invoked":             {"invoke", "reviewer", Row{ID: "r", Prompt: "p", Check: "true", Agent: "reviewer", Grants: "named"}, "PASS", nil, 0, ""},
		"another agent invoked":         {"invoke", "bugs", Row{ID: "r", Prompt: "p", Check: "true", Agent: "reviewer", Grants: "named"}, "FAIL", []string{ReasonAgent("reviewer")}, 0, "agent"},
		"no agent at all":               {"", "", Row{ID: "r", Prompt: "p", Check: "true", Agent: "reviewer", Grants: "named"}, "FAIL", []string{ReasonAgent("reviewer")}, 0, "agent"},
		"an auto-denied call":           {"denied", "", named("r", "p", "true"), "FAIL", []string{ReasonDenied}, 0, "denied"},
		"a soft-denied call in cli.log": {"soft-denied", "", named("r", "p", "true"), "FAIL", []string{ReasonDenied}, 3, "denied"},
		"an invalid allow entry":        {"invalid", "", named("r", "p", "true"), "FAIL", []string{ReasonInvalid}, 0, "grant"},
		"a credential written":          {"leak", leak, named("r", "p", "true"), "FAIL", []string{ReasonCredentials}, 0, "creds"},
		"a credential printed":          {"print-leak", leak, named("r", "p", "true"), "FAIL", []string{ReasonCredentials}, 0, "creds"},
		"a credential in the clone":     {"clone-leak", leak, named("r", "p", "true"), "FAIL", []string{ReasonCredentials}, 0, "creds"},
		"the token replaced":            {"replace-token", "", named("r", "p", "true"), "PASS", nil, 0, ""},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			if c.mode != "" {
				r.on(c.mode, c.value)
			}
			x := one(t, r.run(c.row))
			wantVerdict(t, x, c.verdict, c.reasons...)
			if x.Cause != c.cause {
				t.Errorf("cause %q, want %q", x.Cause, c.cause)
			}
			if x.AgyExit != c.agyExit {
				t.Errorf("agy exit %d, want %d", x.AgyExit, c.agyExit)
			}
			if strings.Contains(r.out.String(), leak) {
				t.Errorf("the output carries the credential:\n%s", r.out.String())
			}
			if c.mode == "replace-token" && !strings.Contains(r.out.String(), "WARNING the token link was replaced") {
				t.Errorf("no warning for a replaced token link:\n%s", r.out.String())
			}
			if c.mode == "leak" && !strings.Contains(r.out.String(), filepath.Join(".gemini", "term.log")+" (github-token)") {
				t.Errorf("the leak's path is not named:\n%s", r.out.String())
			}
		})
	}
}

// TestRunWide: a wide row runs with command(*) and says why.
func TestRunWide(t *testing.T) {
	r := newRig(t)
	wantVerdict(t, one(t, r.run(Row{ID: "w", Prompt: "p", Check: "true", Grants: "wide: the skill pipes"})), "PASS")
	if !strings.Contains(r.read("settings.1"), `"command(*)"`) || !strings.Contains(r.out.String(), "runs with command(*) in its throwaway clone: the skill pipes") {
		t.Errorf("settings %s\nout %s", r.read("settings.1"), r.out.String())
	}
}

// TestRunPrompt2: a second session on the same home after a clean first
// exit, its stdout to out2.txt and its stderr appended; none after a
// failed first one.
func TestRunPrompt2(t *testing.T) {
	r := newRig(t)
	row := Row{ID: "two", Prompt: "first", Prompt2: "second in {{SB}}", Check: `grep -q "session 2" "$OUT2"`, Grants: "named"}
	x := one(t, r.run(row))
	wantVerdict(t, x, "PASS")
	if !strings.HasSuffix(strings.Split(r.read("agy.args.2"), "\n")[1], "second in "+filepath.Join(x.Dir, "home")) {
		t.Errorf("the second prompt: %q", r.read("agy.args.2"))
	}
	if b, _ := os.ReadFile(filepath.Join(x.Dir, "err.txt")); string(b) != "stderr 1\nstderr 2\n" {
		t.Errorf("err.txt = %q, want both sessions' stderr", b)
	}
	r2 := newRig(t)
	r2.on("exit", "1")
	x = one(t, r2.run(row))
	wantVerdict(t, x, "FAIL", ReasonCheck)
	if r2.read("agy.n") != "1\n" {
		t.Errorf("prompt2 ran after a failed first session (%s sessions)", strings.TrimSpace(r2.read("agy.n")))
	}
}

// TestRunSetup: setup runs in the clone before the CLI, with the git
// identity of run.sh's gi() and the row's paths; a failed setup fails the
// row before any CLI runs.
func TestRunSetup(t *testing.T) {
	r := newRig(t)
	row := Row{ID: "s", Setup: `git commit -q --allow-empty -m setup-commit && echo "$CLONE" > "$SB/setup.clone" && echo "$FYR" > "$SB/setup.fyr"`, Prompt: "p",
		Check: `[ "$(git log -1 --format=%an/%s)" = fixture/setup-commit ] && [ "$(cat "$SB/setup.clone")" = "$CLONE" ] && [ "$(cat "$SB/setup.fyr")" = "` + r.self + `" ]`, Grants: "named"}
	wantVerdict(t, one(t, r.run(row)), "PASS")
	r2 := newRig(t)
	x := one(t, r2.run(Row{ID: "s", Setup: "exit 1", Prompt: "p", Check: "true", Grants: "named"}))
	wantVerdict(t, x, "ERROR", ReasonSetup)
	if x.Cause != "setup" || x.SessionSeconds != -1 {
		t.Errorf("cause %q, session %ds; want setup and -1 (no session)", x.Cause, x.SessionSeconds)
	}
	if r2.read("agy.n") != "" || strings.Contains(r2.read("self.calls"), "install") {
		t.Error("the CLI or the install ran after a failed setup")
	}
}

// TestRunEarlyFailures: a failure in steps 1-8 fails the row with the
// step's reason, runs no CLI and leaves no token behind; the next row runs.
func TestRunEarlyFailures(t *testing.T) {
	t.Run("install", func(t *testing.T) {
		r := newRig(t)
		r.on("install-fail", "")
		res := r.run(named("a", "p", "true"), named("b", "p", "true"))
		if len(res) != 2 {
			t.Fatalf("the next row did not run: %+v", res)
		}
		wantVerdict(t, res[0], "ERROR", ReasonInstall)
		if r.read("agy.n") != "" {
			t.Error("agy ran after a failed install")
		}
	})
	t.Run("records list", func(t *testing.T) {
		r := newRig(t)
		r.on("records-fail", "")
		wantVerdict(t, one(t, r.run(named("a", "p", "true"))), "ERROR", ReasonRecords)
	})
	t.Run("clone", func(t *testing.T) {
		r := newRig(t)
		o := r.opts()
		o.Fixture = filepath.Join(r.tmp, "not-a-repo")
		if err := os.MkdirAll(o.Fixture, 0o755); err != nil {
			t.Fatal(err)
		}
		res, err := Run(context.Background(), o, []Row{named("a", "p", "true")})
		if err != nil {
			t.Fatal(err)
		}
		wantVerdict(t, one(t, res), "ERROR", ReasonClone)
	})
	t.Run("sandbox exists", func(t *testing.T) {
		r := newRig(t)
		first := one(t, r.run(named("a", "p", "true")))
		marker := filepath.Join(first.Dir, "home", "kept")
		put(t, marker, "x", 0o600)
		// The same clock second: the sandbox is there already.
		x := one(t, r.run(named("a", "p", "true")))
		wantVerdict(t, x, "ERROR", ReasonExists)
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("the existing sandbox was touched: %v", err)
		}
	})
}
