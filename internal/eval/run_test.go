package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// fakeAgy is the CLI a Run test drives: it records its arguments,
// environment, working directory, the settings it was given and whether the
// token was linked, then does what the control files in @CTL@ say. The
// fakeAgy of internal/dream is unexported and speaks stream-json; eval's
// agy is the -p form (plan § Verification).
const fakeAgy = `#!/bin/bash
CTL=@CTL@; REC=@REC@
n=$(( $(cat "$REC/agy.n" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$REC/agy.n"
printf '%s\n' "$@" > "$REC/agy.args.$n"
env > "$REC/agy.env.$n"; pwd > "$REC/agy.cwd.$n"
cp "$HOME/.gemini/antigravity-cli/settings.json" "$REC/settings.$n" 2>/dev/null
[ -L "$HOME/.gemini/antigravity-cli/antigravity-oauth-token" ] && echo yes > "$REC/linked.$n"
conv=$HOME/.gemini/antigravity-cli/conversations
if [ -e "$CTL/invoke" ]; then mkdir -p "$conv"; printf 'SQLite format 3\000{"TypeName":"%s"}\000' "$(cat "$CTL/invoke")" > "$conv/main-$n.db"; fi
if [ -e "$CTL/denied" ]; then echo "tool call auto-denied: command(rm)" >&2; fi
if [ -e "$CTL/invalid" ]; then mkdir -p "$HOME/.gemini/antigravity-cli/log"; echo "invalid allow entry: x" > "$HOME/.gemini/antigravity-cli/log/cli-1.log"; fi
if [ -e "$CTL/leak" ]; then printf 'GH_TOKEN=%s\n' "$(cat "$CTL/leak")" > "$HOME/.gemini/term.log"; fi
if [ -e "$CTL/realconfig" ]; then echo x >> "$(cat "$CTL/realconfig")"; fi
if [ -e "$CTL/replace-token" ]; then rm "$HOME/.gemini/antigravity-cli/antigravity-oauth-token"; echo copy > "$HOME/.gemini/antigravity-cli/antigravity-oauth-token"; fi
if [ -e "$CTL/child" ]; then sleep 300 </dev/null >/dev/null 2>&1 & echo $! >> "$REC/pids"; fi
if [ -e "$CTL/setsid" ]; then (cd / && exec setsid sleep 301 </dev/null >/dev/null 2>&1) & echo $! >> "$REC/pids"; fi
if [ -e "$CTL/hang" ]; then touch "$REC/started"; exec sleep 300; fi
sbdir=$(dirname "$HOME")
if [ -e "$CTL/print-leak" ]; then echo "GH_TOKEN=$(cat "$CTL/print-leak")"; fi
if [ -e "$CTL/clone-leak" ]; then echo "GH_TOKEN=$(cat "$CTL/clone-leak")" > leak.env; fi
if [ -e "$CTL/fifo" ]; then rm "$sbdir/err.txt"; mkfifo "$sbdir/err.txt" "$sbdir/check.log"; mkdir -p "$conv"; mkfifo "$conv/main.db"; fi
if [ -e "$CTL/link-cli" ]; then mv "$HOME/.gemini/antigravity-cli" "$HOME/.gemini/cli.moved"; ln -s "$(cat "$CTL/link-cli")" "$HOME/.gemini/antigravity-cli"; fi
if [ -e "$CTL/decoy" ]; then (cd "$(cat "$CTL/decoy")" && HOME=/nowhere exec setsid sleep 303 </dev/null >/dev/null 2>&1) & echo $! >> "$REC/decoy"; fi
if [ -e "$CTL/swap" ]; then for d in app home; do mv "$sbdir/$d" "$sbdir/$d.moved"; ln -s "$(cat "$CTL/swap")" "$sbdir/$d"; done; fi
echo "stderr $n" >&2
echo "agy says ok"; echo "session $n"
if [ -e "$CTL/exit" ]; then exit "$(cat "$CTL/exit")"; fi
exit 0
`

// fakeSelf stands in for the fyr binary: install makes the installed binary
// the wrapper execs, and record list prints one record.
const fakeSelf = `#!/bin/bash
CTL=@CTL@; REC=@REC@
printf '%s\n' "$*" >> "$REC/self.calls"
case "$1" in
install)
  if [ -e "$CTL/install-fail" ]; then echo "install broke" >&2; exit 1; fi
  pwd > "$REC/install.cwd"; env > "$REC/install.env"
  mkdir -p "$3/.local/bin"; printf '#!/bin/bash\necho "fyr $*"\n' > "$3/.local/bin/fyr"; chmod +x "$3/.local/bin/fyr" ;;
record)
  if [ -e "$CTL/records-fail" ]; then exit 1; fi
  echo "PLAN-1  plan  open  2026-10-26  a plan" ;;
esac
`

var stampTime = time.Date(2026, 10, 26, 8, 0, 0, 0, time.Local)

type rig struct {
	t                                    *testing.T
	tmp, root, home, real, ctl, rec, bin string
	self, repo                           string
	out                                  bytes.Buffer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	for _, tool := range []string{"git", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s here", tool)
		}
	}
	tmp := outsideTemp(t)
	r := &rig{t: t, tmp: tmp, root: filepath.Join(tmp, "project"), home: filepath.Join(tmp, "fyr-home"),
		real: filepath.Join(tmp, "real"), ctl: filepath.Join(tmp, "ctl"), rec: filepath.Join(tmp, "rec"),
		bin: filepath.Join(tmp, "bin"), repo: filepath.Join(tmp, "harness")}
	for _, d := range []string{r.ctl, r.rec, r.bin, r.repo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitInit(t, r.root)
	put(t, filepath.Join(r.root, "README.md"), "demo\n", 0o644)
	put(t, filepath.Join(r.root, ".agents", "trail.jsonl"), "", 0o644)
	r.git("add", "-A")
	r.git("commit", "-qm", "fixture")
	put(t, filepath.Join(r.real, tokenRel), "token", 0o600)
	put(t, filepath.Join(r.real, harness.StateDir, "brain.db"), "store", 0o600)
	sub := strings.NewReplacer("@CTL@", shellQuote(r.ctl), "@REC@", shellQuote(r.rec))
	put(t, filepath.Join(r.bin, "agy"), sub.Replace(fakeAgy), 0o755)
	r.self = filepath.Join(tmp, "self")
	put(t, r.self, sub.Replace(fakeSelf), 0o755)
	t.Setenv("PATH", r.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", r.real)
	t.Setenv("FYR_REPO", "/must/not/reach/the/install")
	t.Setenv("GIT_EVAL_MARK", "must not reach git")
	t.Cleanup(func() {
		b, _ := os.ReadFile(filepath.Join(r.rec, "pids"))
		for _, f := range strings.Fields(string(b)) {
			if pid, err := strconv.Atoi(f); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	return r
}

func (r *rig) git(args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.root, "-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (r *rig) on(mode, value string) { put(r.t, filepath.Join(r.ctl, mode), value, 0o644) }

func (r *rig) opts() Options {
	return Options{Root: r.root, Home: r.home, RealHome: r.real, Self: r.self, Repo: r.repo, Out: &r.out, Now: func() time.Time { return stampTime }}
}

func (r *rig) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(r.rec, name))
	return string(b)
}

// run runs rows and asserts what every case owes: no error, and the token
// link and settings.json gone from every sandbox it made.
func (r *rig) run(rows ...Row) []Result {
	r.t.Helper()
	res, err := Run(context.Background(), r.opts(), rows)
	if err != nil {
		r.t.Fatalf("Run: %v", err)
	}
	r.clean(res)
	return res
}

func (r *rig) clean(res []Result) {
	r.t.Helper()
	for _, x := range res {
		for _, p := range []string{tokenRel, ".gemini/antigravity-cli/settings.json"} {
			if _, err := os.Lstat(filepath.Join(x.Dir, "home", p)); !os.IsNotExist(err) {
				r.t.Errorf("row %s: %s is still in the sandbox", x.Row, p)
			}
		}
	}
}

func one(t *testing.T, res []Result) Result {
	t.Helper()
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1: %+v", len(res), res)
	}
	return res[0]
}

func wantVerdict(t *testing.T, x Result, verdict string, reasons ...string) {
	t.Helper()
	if x.Verdict != verdict || strings.Join(x.Reasons, "; ") != strings.Join(reasons, "; ") {
		t.Errorf("row %s: %s %v, want %s %v", x.Row, x.Verdict, x.Reasons, verdict, reasons)
	}
}

func named(id, prompt, check string) Row {
	return Row{ID: id, Prompt: prompt, Check: check, Grants: "named"}
}

func envMap(s string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			m[k] = v
		}
	}
	return m
}

// TestRunPass is the golden path, and every step a PASS goes through: the
// clone, the records listed before, the install through Self with the
// resolved source, the wrapper, the grants and trust, the linked token, the
// exact agy argv, environment and directory, the check's environment, and
// the line printed.
func TestRunPass(t *testing.T) {
	r := newRig(t)
	x := one(t, r.run(named("ok", "hello {{CLONE}} at {{STAMP}}", `grep -q "agy says ok" "$OUT" && "$SB/bin/fyr" memory show > "$SB/wrapped.txt" && env > "$SB/check.env"`)))
	wantVerdict(t, x, "PASS")
	dir := filepath.Join(r.home, harness.StateDir, "eval", "2026-10-26-080000", "ok")
	app, sb := filepath.Join(dir, "app"), filepath.Join(dir, "home")
	if x.Dir != dir || x.AgyExit != 0 {
		t.Errorf("Dir %s, agy exit %d; want %s, 0", x.Dir, x.AgyExit, dir)
	}
	if !regexp.MustCompile(`(?m)^eval: ok  PASS  agy exit 0  \d+s  ` + regexp.QuoteMeta(dir) + `$`).MatchString(r.out.String()) {
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
	wantArgs := []string{"-p", "The project root is the current directory, " + app + "; ignore any other checkout. hello " + app + " at 2026-10-26-0800", "--mode", "accept-edits", "--print-timeout", "20m"}
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
	}{
		"the check fails":              {"", "", named("r", "p", "false"), "FAIL", []string{ReasonCheck}, 0},
		"agy exits 3, the check holds": {"exit", "3", named("r", "p", "true"), "PASS", nil, 3},
		"the agent invoked":            {"invoke", "reviewer", Row{ID: "r", Prompt: "p", Check: "true", Agent: "reviewer", Grants: "named"}, "PASS", nil, 0},
		"another agent invoked":        {"invoke", "bugs", Row{ID: "r", Prompt: "p", Check: "true", Agent: "reviewer", Grants: "named"}, "FAIL", []string{ReasonAgent("reviewer")}, 0},
		"no agent at all":              {"", "", Row{ID: "r", Prompt: "p", Check: "true", Agent: "reviewer", Grants: "named"}, "FAIL", []string{ReasonAgent("reviewer")}, 0},
		"an auto-denied call":          {"denied", "", named("r", "p", "true"), "FAIL", []string{ReasonDenied}, 0},
		"an invalid allow entry":       {"invalid", "", named("r", "p", "true"), "FAIL", []string{ReasonInvalid}, 0},
		"a credential written":         {"leak", leak, named("r", "p", "true"), "FAIL", []string{ReasonCredentials}, 0},
		"a credential printed":         {"print-leak", leak, named("r", "p", "true"), "FAIL", []string{ReasonCredentials}, 0},
		"a credential in the clone":    {"clone-leak", leak, named("r", "p", "true"), "FAIL", []string{ReasonCredentials}, 0},
		"the token replaced":           {"replace-token", "", named("r", "p", "true"), "PASS", nil, 0},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			if c.mode != "" {
				r.on(c.mode, c.value)
			}
			x := one(t, r.run(c.row))
			wantVerdict(t, x, c.verdict, c.reasons...)
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
	row := Row{ID: "s", Setup: `git commit -q --allow-empty -m setup-commit && echo "$CLONE" > "$SB/setup.clone"`, Prompt: "p",
		Check: `[ "$(git log -1 --format=%an/%s)" = fixture/setup-commit ] && [ "$(cat "$SB/setup.clone")" = "$CLONE" ]`, Grants: "named"}
	wantVerdict(t, one(t, r.run(row)), "PASS")
	r2 := newRig(t)
	x := one(t, r2.run(Row{ID: "s", Setup: "exit 1", Prompt: "p", Check: "true", Grants: "named"}))
	wantVerdict(t, x, "FAIL", ReasonSetup)
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
		wantVerdict(t, res[0], "FAIL", ReasonInstall)
		if r.read("agy.n") != "" {
			t.Error("agy ran after a failed install")
		}
	})
	t.Run("records list", func(t *testing.T) {
		r := newRig(t)
		r.on("records-fail", "")
		wantVerdict(t, one(t, r.run(named("a", "p", "true"))), "FAIL", ReasonRecords)
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
		wantVerdict(t, one(t, res), "FAIL", ReasonClone)
	})
	t.Run("sandbox exists", func(t *testing.T) {
		r := newRig(t)
		first := one(t, r.run(named("a", "p", "true")))
		marker := filepath.Join(first.Dir, "home", "kept")
		put(t, marker, "x", 0o600)
		// The same clock second: the sandbox is there already.
		x := one(t, r.run(named("a", "p", "true")))
		wantVerdict(t, x, "FAIL", ReasonExists)
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("the existing sandbox was touched: %v", err)
		}
	})
}

// TestRunExitChecks: what the run leaves on the machine fails the row even
// when its check passed (step 15): a child in agy's own group and one that
// called setsid (D18, BUG-163), and a change to the real config.
func TestRunExitChecks(t *testing.T) {
	t.Run("leftover processes", func(t *testing.T) {
		if !ProcsChecked {
			t.Skip("no /proc here (O1)")
		}
		if _, err := exec.LookPath("setsid"); err != nil {
			t.Skip("no setsid here")
		}
		r := newRig(t)
		r.on("child", "")
		r.on("setsid", "")
		x := one(t, r.run(named("p", "p", "true")))
		wantVerdict(t, x, "FAIL", ReasonProcs)
		pids := strings.Fields(r.read("pids"))
		if len(pids) != 2 {
			t.Fatalf("the fake planted %v", pids)
		}
		for _, pid := range pids {
			if !strings.Contains(r.out.String(), " "+pid+" (") {
				t.Errorf("pid %s is not named:\n%s", pid, r.out.String())
			}
			if alive(pid) {
				t.Errorf("pid %s outlived the check", pid)
			}
		}
	})
	t.Run("real config", func(t *testing.T) {
		r := newRig(t)
		r.on("realconfig", filepath.Join(r.real, harness.StateDir, "brain.db"))
		x := one(t, r.run(named("c", "p", "true")))
		wantVerdict(t, x, "FAIL", ReasonRealConfig)
		if !strings.Contains(r.out.String(), filepath.Join("fyr", "brain.db")) || strings.Contains(r.out.String(), "store") {
			t.Errorf("want the changed path and no content:\n%s", r.out.String())
		}
	})
}

// TestRunSandboxReplaced: the agent replaces the clone and the sandbox home
// with links to the real home. Nothing is removed, scanned or signalled
// through them: the real token stays, a credential-shaped file of the real
// home is not reported, a process whose cwd is the real home is not sent
// SIGTERM, and the row FAILs sandbox replaced.
func TestRunSandboxReplaced(t *testing.T) {
	r := newRig(t)
	put(t, filepath.Join(r.real, ".netrc"), "machine x login y password z\n", 0o600)
	r.on("swap", r.real)
	r.on("decoy", r.real)
	t.Cleanup(func() {
		b, _ := os.ReadFile(filepath.Join(r.rec, "decoy"))
		for _, f := range strings.Fields(string(b)) {
			if pid, err := strconv.Atoi(f); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	res, err := Run(context.Background(), r.opts(), []Row{named("s", "p", "true")})
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, one(t, res), "FAIL", ReasonReplaced)
	if b, err := os.ReadFile(filepath.Join(r.real, tokenRel)); err != nil || string(b) != "token" {
		t.Errorf("the real token: %q, %v", b, err)
	}
	if strings.Contains(r.out.String(), ".netrc") {
		t.Errorf("the real home was scanned:\n%s", r.out.String())
	}
	if ProcsChecked {
		decoy := strings.TrimSpace(r.read("decoy"))
		if decoy == "" || !alive(decoy) {
			t.Errorf("the process in the real home (%q) was signalled", decoy)
		}
	}
}

// TestRunCLIDirLinked: the agent links the sandbox's CLI directory to the
// real one. Removing the token link and settings.json never follows it.
func TestRunCLIDirLinked(t *testing.T) {
	r := newRig(t)
	realCLI := filepath.Dir(filepath.Join(r.real, tokenRel))
	put(t, filepath.Join(realCLI, "settings.json"), "{}", 0o600)
	r.on("link-cli", realCLI)
	res, err := Run(context.Background(), r.opts(), []Row{named("l", "p", "true")})
	if err != nil {
		t.Fatal(err)
	}
	x := one(t, res)
	if x.Verdict != "FAIL" || !slices.Contains(x.Reasons, ReasonReplaced) {
		t.Errorf("row %s: %s %v, want a FAIL naming %q", x.Row, x.Verdict, x.Reasons, ReasonReplaced)
	}
	for _, name := range []string{"antigravity-oauth-token", "settings.json"} {
		if _, err := os.Stat(filepath.Join(realCLI, name)); err != nil {
			t.Errorf("the real %s was removed: %v", name, err)
		}
	}
}

// TestRunFIFOs: FIFOs the agent left where eval reads err.txt, writes
// check.log and reads a conversation database never block the run.
func TestRunFIFOs(t *testing.T) {
	r := newRig(t)
	r.on("fifo", "")
	done := make(chan []Result, 1)
	go func() {
		res, _ := Run(context.Background(), r.opts(), []Row{{ID: "f", Prompt: "p", Check: "true", Agent: "reviewer", Grants: "named"}})
		done <- res
	}()
	select {
	case res := <-done:
		wantVerdict(t, one(t, res), "FAIL", ReasonCheck, ReasonAgent("reviewer"))
	case <-time.After(60 * time.Second):
		t.Fatal("Run blocked on a FIFO")
	}
}

// TestRunWrapperPinsHome: the sandbox's fyr refuses a --home of the
// agent's own in every spelling, and pins SB otherwise.
func TestRunWrapperPinsHome(t *testing.T) {
	r := newRig(t)
	x := one(t, r.run(named("w", "p", "true")))
	sb := filepath.Join(x.Dir, "home")
	wrapper := filepath.Join(sb, "bin", "fyr")
	out, err := exec.Command(wrapper, "search", "x").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "fyr search --home "+sb+" x" {
		t.Errorf("the wrapper without --home: %q, %v", out, err)
	}
	for _, args := range [][]string{{"search", "x", "--home", r.real}, {"search", "--home=" + r.real, "x"}, {"record", "list", "-home", r.real}, {"record", "-home=" + r.real}} {
		out, err := exec.Command(wrapper, args...).CombinedOutput()
		if err == nil || strings.Contains(string(out), "fyr "+args[0]) || !strings.Contains(string(out), "this sandbox pins --home") {
			t.Errorf("the wrapper with %v: %q, %v; want a refusal", args, out, err)
		}
	}
}

// alive reports whether pid is running and not a zombie, waiting up to a
// few seconds for it to go.
func alive(pid string) bool {
	for i := 0; i < 100; i++ {
		b, err := os.ReadFile("/proc/" + pid + "/status")
		if err != nil || strings.Contains(string(b), "State:\tZ") {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}

// TestRunTimeout: at the timeout only agy is killed, the check still runs,
// and the agy exit is printed but is not a verdict condition.
func TestRunTimeout(t *testing.T) {
	r := newRig(t)
	old := agyTimeout
	agyTimeout = time.Second
	t.Cleanup(func() { agyTimeout = old })
	r.on("hang", "")
	x := one(t, r.run(named("slow", "p", "true")))
	wantVerdict(t, x, "PASS")
	if x.AgyExit != -1 || !strings.Contains(r.out.String(), "agy timed out after 1s") {
		t.Errorf("agy exit %d\n%s", x.AgyExit, r.out.String())
	}
}

// TestRunInterrupted: on ctx's end the row FAILs interrupted, the rows after
// it never run, and the token link and settings are still removed.
func TestRunInterrupted(t *testing.T) {
	r := newRig(t)
	r.on("hang", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for i := 0; i < 600; i++ {
			if _, err := os.Stat(filepath.Join(r.rec, "started")); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	res, err := Run(ctx, r.opts(), []Row{named("a", "p", "true"), named("b", "p", "true")})
	if err != nil {
		t.Fatal(err)
	}
	r.clean(res)
	x := one(t, res)
	wantVerdict(t, x, "FAIL", ReasonInterrupted)
	if time.Since(start) > 30*time.Second {
		t.Errorf("the interrupt took %s", time.Since(start))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(x.Dir), "b")); !os.IsNotExist(err) {
		t.Error("the row after the interrupt ran")
	}
}

// TestRunRefusals: step 0 refuses before anything is created.
func TestRunRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		change func(r *rig, o *Options)
		rows   []Row
		want   string
	}{
		"not a checkout":       {func(r *rig, o *Options) { o.Root = r.real }, nil, "is not a git checkout"},
		"a home in a checkout": {func(r *rig, o *Options) { o.Home = filepath.Join(r.root, ".agents", "scratch", "home") }, nil, "(BUG-106)"},
		"no login":             {func(r *rig, o *Options) { os.Remove(filepath.Join(r.real, tokenRel)) }, nil, "no Antigravity login at"},
		"a login that is a link": {func(r *rig, o *Options) {
			p := filepath.Join(r.real, tokenRel)
			os.Rename(p, p+".real")
			os.Symlink(p+".real", p)
		}, nil, "no Antigravity login at"},
		"no agy on PATH": {func(r *rig, o *Options) {
			only := filepath.Join(r.tmp, "only-git")
			os.MkdirAll(only, 0o755)
			g, _ := exec.LookPath("git")
			os.Symlink(g, filepath.Join(only, "git"))
			r.t.Setenv("PATH", only)
		}, nil, "agy not found on PATH"},
		"a row named twice": {nil, []Row{named("a", "p", "true"), named("a", "p", "true")}, "row a is named twice"},
		"no rows":           {nil, []Row{}, "no row to run"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			o := r.opts()
			if c.change != nil {
				c.change(r, &o)
			}
			rows := c.rows
			if rows == nil {
				rows = []Row{named("a", "p", "true")}
			}
			res, err := Run(context.Background(), o, rows)
			if err == nil || !strings.Contains(err.Error(), c.want) || res != nil {
				t.Errorf("want a refusal holding %q, got %v, %+v", c.want, err, res)
			}
			if _, err := os.Stat(SandboxRoot(o.Home)); !os.IsNotExist(err) {
				t.Error("a refusal created the sandbox root")
			}
			if r.read("agy.n") != "" || r.read("self.calls") != "" {
				t.Error("a refusal ran the CLI or the install")
			}
		})
	}
}

// TestRunRealFyrBaseline is the A5 case with the real binary: a fixture
// whose trail quotes a private-key header is loaded into the sandbox's
// brain.db by `record list` (step 4), and the baseline of step 8b absorbs it
// (D17), so the row passes; the same fixture with a token the run writes
// fails. Self is a build of this checkout, and the install source is this
// checkout, as `fyr eval` resolves it here (D16).
func TestRunRealFyrBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and installs fyr")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	// The go tool keeps its caches under the real HOME, which the rig moves.
	for _, k := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		out, err := exec.Command("go", "env", k).Output()
		if err != nil {
			t.Skipf("go env %s: %v", k, err)
		}
		t.Setenv(k, strings.TrimSpace(string(out)))
	}
	t.Setenv("GOPROXY", "off")
	r := newRig(t)
	self := filepath.Join(r.tmp, "fyr")
	if out, err := exec.Command("go", "build", "-o", self, filepath.Join(root, "cmd", "fyr")).CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	fixture := filepath.Join(r.tmp, "fixture")
	gitInit(t, fixture)
	add := exec.Command(self, "record", "add", "guide", "--title", "Key shapes", "--home", filepath.Join(r.tmp, "import-home"))
	add.Dir = fixture
	add.Stdin = strings.NewReader("The redactor's test quotes a header: -----BEGIN OPENSSH PRIVATE KEY-----\n")
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("record add: %v\n%s", err, out)
	}
	for _, args := range [][]string{{"add", ".agents/trail.jsonl"}, {"commit", "-qm", "trail"}} {
		cmd := exec.Command("git", append([]string{"-C", fixture, "-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	o := r.opts()
	o.Self, o.Fixture, o.Repo = self, fixture, root
	res, err := Run(context.Background(), o, []Row{named("a5", "p", `"$SB/bin/fyr" record list | grep -q "Key shapes"`)})
	if err != nil {
		t.Fatal(err)
	}
	r.clean(res)
	x := one(t, res)
	wantVerdict(t, x, "PASS")
	if x.Verdict != "PASS" {
		b, _ := os.ReadFile(filepath.Join(x.Dir, "install.log"))
		t.Fatalf("output:\n%s\ninstall.log:\n%s", r.out.String(), b)
	}
	if b, _ := os.ReadFile(filepath.Join(x.Dir, "install.log")); !strings.Contains(string(b), root) {
		t.Errorf("the install did not name the checkout as its source:\n%s", b)
	}
	// The positive control: the store does hold the header the baseline absorbed.
	if f := scanCreds(filepath.Join(x.Dir, "home"))[filepath.Join(harness.StateDir, "brain.db")]; f.kinds["private-key"] == 0 {
		t.Fatalf("the sandbox's brain.db holds no key header, so the case proves nothing: %+v", f)
	}
	r.on("leak", "gh"+"o_"+strings.Repeat("C", 36))
	res, err = Run(context.Background(), o, []Row{named("a5-leak", "p", "true")})
	if err != nil {
		t.Fatal(err)
	}
	r.clean(res)
	wantVerdict(t, one(t, res), "FAIL", ReasonCredentials)
}
