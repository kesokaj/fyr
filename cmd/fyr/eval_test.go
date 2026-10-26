package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// evalAgy is the CLI the verb tests drive. Session n (counted across the
// test) copies the template $TPL/n over the sandbox's Antigravity directory,
// so the metrics ingest finds real conversation files; --version answers
// for the record. The internal/eval tests cover the CLI's every other shape.
const evalAgy = `#!/bin/bash
REC=@REC@; TPL=@TPL@; CTL=@CTL@
if [ "$1" = --version ]; then echo "agy-fake 9.9"; echo second line; exit 0; fi
n=$(( $(cat "$REC/agy.n" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$REC/agy.n"
if [ -e "$CTL/hang" ]; then touch "$REC/started"; exec sleep 300; fi
if [ -d "$TPL/$n" ]; then cp -r "$TPL/$n/." "$HOME/.gemini/antigravity-cli/"; fi
echo "agy says ok"
`

// evalSelfScript stands in for the fyr each sandbox installs: install makes
// the binary the wrapper execs, and record list prints one record.
const evalSelfScript = `#!/bin/bash
case "$1" in
install) mkdir -p "$3/.local/bin"; printf '#!/bin/bash\necho "fyr $*"\n' > "$3/.local/bin/fyr"; chmod +x "$3/.local/bin/fyr" ;;
record) echo "GUIDE-eval  guide  current  2026-10-26  suite" ;;
esac
`

const evalSuite = "The suite of the verb tests.\n\n" +
	"| id | setup | prompt | prompt2 | check | agent | grants |\n" +
	"|---|---|---|---|---|---|---|\n" +
	"| `ok` |  | say ok |  | `grep -q 'agy says ok' \"$OUT\"` |  | named |\n" +
	"| `two` |  | first |  | `grep -q 'agy says ok' \"$OUT2\"` |  | named |\n" +
	"| `bad` |  | say ok |  | `false` |  | named |\n"

// evalRig is a project with a committed GUIDE-eval, a real home with a
// login, a fyr home, a fake agy on PATH and a fake fyr as the sandbox's.
type evalRig struct {
	t                                  *testing.T
	project, home, real, rec, tpl, ctl string
	harness                            string
}

func newEvalRig(t *testing.T, suite string, commit bool) *evalRig {
	t.Helper()
	for _, tool := range []string{"git", "bash", "cp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s here", tool)
		}
	}
	checkout, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if r, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = r
	}
	if out, err := exec.Command("git", "-C", tmp, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
		t.Skipf("the test's temporary directory is inside a git checkout (%s): eval refuses that by design", strings.TrimSpace(string(out)))
	}
	r := &evalRig{t: t, project: filepath.Join(tmp, "project"), home: filepath.Join(tmp, "fyr-home"), real: filepath.Join(tmp, "real"),
		rec: filepath.Join(tmp, "rec"), tpl: filepath.Join(tmp, "tpl"), ctl: filepath.Join(tmp, "ctl"), harness: checkout}
	bin := filepath.Join(tmp, "bin")
	for _, d := range []string{r.project, r.home, r.rec, r.tpl, r.ctl, bin, filepath.Join(r.real, ".gemini", "antigravity-cli")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(r.real, ".gemini", "antigravity-cli", "antigravity-oauth-token"), []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := strings.NewReplacer("@REC@", r.rec, "@TPL@", r.tpl, "@CTL@", r.ctl)
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(sub.Replace(evalAgy)), 0o755); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(tmp, "self")
	if err := os.WriteFile(self, []byte(evalSelfScript), 0o755); err != nil {
		t.Fatal(err)
	}
	old := evalSelf
	evalSelf = func() (string, error) { return self, nil }
	t.Cleanup(func() { evalSelf = old })
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", r.real)
	t.Setenv("FYR_REPO", "")

	r.git("init", "-q")
	if err := os.WriteFile(filepath.Join(r.project, "README.md"), []byte("demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(r.project)
	if suite != "" {
		r.mustRun(strings.NewReader(suite), "record", "add", "guide", "--id", "GUIDE-eval", "--title", "suite", "--home", r.home)
	} else {
		r.mustRun(strings.NewReader("not the suite"), "record", "add", "guide", "--id", "GUIDE-other", "--title", "other", "--home", r.home)
	}
	if commit {
		r.git("add", "-A")
		r.git("commit", "-qm", "fixture")
	} else {
		r.git("add", "README.md")
		r.git("commit", "-qm", "fixture")
	}
	return r
}

func (r *evalRig) git(args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.project, "-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// mustRun runs a verb with stdin from in and fails the test on any error.
func (r *evalRig) mustRun(in *strings.Reader, args ...string) string {
	r.t.Helper()
	if in != nil {
		stdin = in
		defer func() { stdin = os.Stdin }()
	}
	var out bytes.Buffer
	if err := run(args, &out); err != nil {
		r.t.Fatalf("%v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

// eval runs `fyr eval` with the rig's home and harness and returns the
// output and the exit status.
func (r *evalRig) eval(args ...string) (string, int) {
	r.t.Helper()
	var out bytes.Buffer
	err := run(append([]string{"eval", "--repo", r.harness, "--home", r.home}, args...), &out)
	var code exitCode
	switch {
	case err == nil:
		return out.String(), 0
	case errors.As(err, &code):
		return out.String(), int(code)
	}
	r.t.Fatalf("eval %v: %v\n%s", args, err, out.String())
	return "", -1
}

// template makes session n's conversations: every id in ids, in one home.
func (r *evalRig) template(n string, ids ...string) {
	for _, id := range ids {
		agyHome(r.t, filepath.Join(r.tpl, n), id)
	}
	// agyHome writes under <home>/.gemini/antigravity-cli; the fake copies
	// the template's contents over the sandbox's directory of that name.
	src := filepath.Join(r.tpl, n, ".gemini", "antigravity-cli")
	tmp := filepath.Join(r.tpl, n+".tmp")
	if err := os.Rename(src, tmp); err != nil {
		r.t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(r.tpl, n)); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(r.tpl, n)); err != nil {
		r.t.Fatal(err)
	}
}

var recordIDLine = regexp.MustCompile(`(?m)^eval: (\d+) of (\d+) PASS; record (TEST-\S+)$`)

func (r *evalRig) passK(label string) (k, passed int) {
	r.t.Helper()
	out := r.mustRun(nil, "metrics", "show", "--label", label, "--since", "2026-10-01", "--json", "--home", r.home)
	var sum struct {
		A struct {
			PassK map[string]struct {
				K      int `json:"k"`
				Passed int `json:"passed"`
			} `json:"pass_k"`
		} `json:"A"`
	}
	if err := json.Unmarshal([]byte(out), &sum); err != nil {
		r.t.Fatalf("metrics show --json: %v\n%s", err, out)
	}
	p := sum.A.PassK[label]
	return p.K, p.Passed
}

// TestEvalVerb is the golden path: the row's line, one testing record with
// the body the spec names, and the sandbox ingested with its label and
// outcome.
func TestEvalVerb(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	r.template("1", "C1")
	out, code := r.eval("ok")
	if code != 0 {
		t.Fatalf("eval ok: exit %d\n%s", code, out)
	}
	if !regexp.MustCompile(`(?m)^eval: ok  PASS  agy exit 0  \d+s  ` + regexp.QuoteMeta(filepath.Join(r.home, harness.StateDir, "eval")) + `/\d{4}-\d\d-\d\d-\d{6}/ok$`).MatchString(out) {
		t.Errorf("the row line is missing or misshapen:\n%s", out)
	}
	m := recordIDLine.FindStringSubmatch(out)
	if m == nil || m[1] != "1" || m[2] != "1" {
		t.Fatalf("no summary line naming the record:\n%s", out)
	}
	got := r.mustRun(nil, "record", "get", m[3], "--home", r.home)
	for _, want := range []string{"Eval — 1 of 1 PASS at ", "testing  closed", "- fyr: ", "- project: " + r.project + " at ", "- fixture: " + r.project + " at ",
		"- install source: " + r.harness, "- agy: agy-fake 9.9\n", "| row | verdict | reasons | agy exit | seconds | sandbox |", "| `ok` | PASS | - | 0 | "} {
		if !strings.Contains(got, want) {
			t.Errorf("the record lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "agy says ok") {
		t.Errorf("the CLI's output reached the record:\n%s", got)
	}
	if k, passed := r.passK("eval=ok"); k != 1 || passed != 1 {
		t.Errorf("pass^k of eval=ok: k %d passed %d, want 1 and 1", k, passed)
	}
}

// TestEvalVerbPrompt2 runs a two-session row: both sessions are ingested
// under the row's label with its outcome, so pass^k counts two (D12).
func TestEvalVerbPrompt2(t *testing.T) {
	suite := strings.Replace(evalSuite, "| `two` |  | first |  |", "| `two` |  | first | second |", 1)
	r := newEvalRig(t, suite, true)
	r.template("1", "C1")
	r.template("2", "C1", "C2")
	out, code := r.eval("two")
	if code != 0 {
		t.Fatalf("eval two: exit %d\n%s", code, out)
	}
	if n := strings.TrimSpace(readFile(t, filepath.Join(r.rec, "agy.n"))); n != "2" {
		t.Fatalf("agy ran %s times, want 2", n)
	}
	if k, passed := r.passK("eval=two"); k != 2 || passed != 2 {
		t.Errorf("pass^k of eval=two: k %d passed %d, want 2 and 2 (both sessions labelled)", k, passed)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A failing row exits 1, is recorded as such, and is ingested as fail, never
// skipped (D6).
func TestEvalVerbFail(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	r.template("1", "C1")
	r.template("2", "C2")
	out, code := r.eval("ok", "bad")
	if code != 1 {
		t.Fatalf("eval ok bad: exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "eval: bad  FAIL (check failed)  agy exit 0") {
		t.Errorf("no FAIL line for bad:\n%s", out)
	}
	m := recordIDLine.FindStringSubmatch(out)
	if m == nil || m[1] != "1" || m[2] != "2" {
		t.Fatalf("summary: want 1 of 2 PASS:\n%s", out)
	}
	got := r.mustRun(nil, "record", "get", m[3], "--home", r.home)
	if !strings.Contains(got, "| `bad` | FAIL | check failed | 0 | ") || !strings.Contains(got, "| `ok` | PASS | - | 0 | ") {
		t.Errorf("the record's table:\n%s", got)
	}
	if k, passed := r.passK("eval=bad"); k != 1 || passed != 0 {
		t.Errorf("pass^k of eval=bad: k %d passed %d, want 1 and 0", k, passed)
	}
}

// An ingest that fails is named and makes the invocation exit 1; the
// verdict stands and the record is still written.
func TestEvalVerbIngestFails(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	if err := os.MkdirAll(filepath.Join(r.home, harness.StateDir, "metrics.db"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, code := r.eval("ok")
	if code != 1 || !strings.Contains(out, "eval: ok  PASS") || !strings.Contains(out, "eval: ok ingest: ") || recordIDLine.FindString(out) == "" {
		t.Fatalf("exit %d, want 1 with the PASS line, the ingest error and the record:\n%s", code, out)
	}
}

// A record that cannot be written: every row line, then the error, exit 1.
func TestEvalVerbRecordFails(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	// The rig's record add made the store; a directory in its place cannot
	// be opened.
	stores, _ := filepath.Glob(filepath.Join(r.home, harness.StateDir, "brain.db*"))
	for _, p := range stores {
		os.Remove(p)
	}
	if err := os.MkdirAll(filepath.Join(r.home, harness.StateDir, "brain.db"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, code := r.eval("ok")
	if code != 1 || !strings.Contains(out, "eval: ok  PASS") || !strings.Contains(out, "eval: record: ") {
		t.Fatalf("exit %d, want 1 with the row line and the record error:\n%s", code, out)
	}
}

// SIGINT during a row: the row FAILs interrupted, the rows after it do not
// run, and the started one is still ingested and recorded (D22).
func TestEvalVerbInterrupted(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	if err := os.WriteFile(filepath.Join(r.ctl, "hang"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	go func() {
		for i := 0; i < 600; i++ {
			if _, err := os.Stat(filepath.Join(r.rec, "started")); err == nil {
				syscall.Kill(os.Getpid(), syscall.SIGINT)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	out, code := r.eval("ok", "bad")
	if code != 1 || !strings.Contains(out, "eval: ok  FAIL (interrupted)") || strings.Contains(out, "eval: bad ") {
		t.Fatalf("exit %d, want 1 with ok interrupted and bad not run:\n%s", code, out)
	}
	m := recordIDLine.FindStringSubmatch(out)
	if m == nil || m[1] != "0" || m[2] != "1" {
		t.Fatalf("summary: want 0 of 1 PASS:\n%s", out)
	}
	if k, passed := r.passK("eval=ok"); k != 0 || passed != 0 {
		// No conversation was written before the hang: nothing to count,
		// and the ingest itself must not have failed (it would print).
		t.Errorf("pass^k of eval=ok: k %d passed %d, want none", k, passed)
	}
	if strings.Contains(out, "ingest:") {
		t.Errorf("the interrupted row's ingest failed:\n%s", out)
	}
}

// Every refusal exits 2 before anything is created: no sandbox root and no
// record (the export unchanged).
func TestEvalVerbRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		suite  string
		commit bool
		setup  func(r *evalRig) []string // the arguments after eval's own --repo and --home
		want   string
	}{
		"no row":            {evalSuite, true, func(*evalRig) []string { return nil }, "usage: fyr eval"},
		"--list with a row": {evalSuite, true, func(*evalRig) []string { return []string{"--list", "ok"} }, "--list takes no row"},
		"unknown flag":      {evalSuite, true, func(*evalRig) []string { return []string{"--parallel", "2", "ok"} }, "flag provided but not defined: -parallel"},
		"unknown row":       {evalSuite, true, func(*evalRig) []string { return []string{"ok", "nope"} }, `eval: no row "nope" in GUIDE-eval`},
		"row named twice":   {evalSuite, true, func(*evalRig) []string { return []string{"ok", "ok"} }, "eval: row ok is named twice"},
		"not a checkout": {evalSuite, true, func(r *evalRig) []string {
			r.t.Chdir(r.t.TempDir())
			return []string{"ok"}
		}, "is not a git checkout"},
		"no install source": {evalSuite, true, func(r *evalRig) []string { r.harness = filepath.Join(r.real, "no-such-tree"); return []string{"ok"} }, "eval: "},
		"home inside a checkout": {evalSuite, true, func(r *evalRig) []string {
			r.home = filepath.Join(r.project, ".agents", "scratch", "home")
			return []string{"ok"}
		}, "is inside a git checkout (BUG-106); use a --home outside every checkout"},
		"no suite":       {"", true, func(*evalRig) []string { return []string{"ok"} }, "eval: no GUIDE-eval record in "},
		"not committed":  {evalSuite, false, func(*evalRig) []string { return []string{"ok"} }, "eval: GUIDE-eval is not committed; commit it first"},
		"does not parse": {"| id | setup | prompt | prompt2 | check | agent | grants |\n|---|---|---|---|---|---|---|\n| `x` |  |  |  | `true` |  | named |\n", true, func(*evalRig) []string { return []string{"x"} }, "eval: GUIDE-eval line 3"},
		"uncommitted change": {evalSuite, true, func(r *evalRig) []string {
			r.mustRun(strings.NewReader(evalSuite+"\nmore prose\n"), "record", "edit", "GUIDE-eval", "--body", "--home", r.home)
			return []string{"ok"}
		}, "eval: GUIDE-eval has uncommitted changes; commit them first"},
		"no login": {evalSuite, true, func(r *evalRig) []string {
			os.Remove(filepath.Join(r.real, ".gemini", "antigravity-cli", "antigravity-oauth-token"))
			return []string{"ok"}
		}, "eval: no Antigravity login at "},
		"login is a link": {evalSuite, true, func(r *evalRig) []string {
			tok := filepath.Join(r.real, ".gemini", "antigravity-cli", "antigravity-oauth-token")
			os.Rename(tok, tok+".real")
			os.Symlink(tok+".real", tok)
			return []string{"ok"}
		}, "eval: no Antigravity login at "},
		"no agy": {evalSuite, true, func(r *evalRig) []string {
			r.t.Setenv("PATH", "/usr/bin:/bin")
			if _, err := exec.LookPath("agy"); err == nil {
				r.t.Skip("an agy on /usr/bin or /bin")
			}
			return []string{"ok"}
		}, "eval: agy not found on PATH"},
		"fixture not a repository": {evalSuite, true, func(r *evalRig) []string { return []string{"--fixture", r.t.TempDir(), "ok"} }, "is not a git repository"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newEvalRig(t, tc.suite, tc.commit)
			args := tc.setup(r)
			trailBefore, _ := os.ReadFile(filepath.Join(r.project, ".agents", "trail.jsonl"))
			out, code := r.eval(args...)
			if code != 2 || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d, want 2 naming %q:\n%s", code, tc.want, out)
			}
			if _, err := os.Stat(filepath.Join(r.home, harness.StateDir, "eval")); !os.IsNotExist(err) {
				t.Errorf("the sandbox root was created: %v", err)
			}
			if trailAfter, _ := os.ReadFile(filepath.Join(r.project, ".agents", "trail.jsonl")); !bytes.Equal(trailBefore, trailAfter) {
				t.Error("a refusal wrote the trail export")
			}
			if n, err := os.ReadFile(filepath.Join(r.rec, "agy.n")); err == nil {
				t.Errorf("agy ran %s times", n)
			}
		})
	}
}

// --list prints one line per row from the working export, even when it
// differs from HEAD's, and runs nothing (D19).
func TestEvalVerbList(t *testing.T) {
	suite := strings.Replace(evalSuite, "| `bad` |  | say ok |  | `false` |  | named |", "| `bad` |  | say ok |  | `false` | reviewer | wide: needs bash |", 1)
	r := newEvalRig(t, suite, false)
	out, code := r.eval("--list")
	if code != 0 || out != "ok  -  named\ntwo  -  named\nbad  reviewer  wide\n" {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(r.home, harness.StateDir, "eval")); !os.IsNotExist(err) {
		t.Errorf("--list created the sandbox root: %v", err)
	}
}
