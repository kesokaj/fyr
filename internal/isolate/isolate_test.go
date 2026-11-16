package isolate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const outsideProbeScript = `#!/bin/bash
set +e
OUTSIDE=@OUTSIDE@
REALCFG=@REALCFG@
TOK=@TOK@
printf 'ok-home\n' > "$HOME/ok.txt"
printf 'ok-tmp\n' > "$TMPDIR/ok.txt"
printf 'ok-app\n' > "$PWD/ok.txt"
(printf 'escaped\n' > "$OUTSIDE/escape.txt") 2> "$HOME/outside.err"
echo "outside_rc=$?" >> "$HOME/status.txt"
(printf 'escaped\n' > "$REALCFG/tamper.md") 2> "$HOME/real.err"
echo "real_rc=$?" >> "$HOME/status.txt"
(printf 'tamper\n' > "$TOK") 2> "$HOME/tok.err"
echo "tok_rc=$?" >> "$HOME/status.txt"
exit 0
`

func writeOutsideProbe(t *testing.T, r *rig, name, outside string) string {
	t.Helper()
	realCfg := filepath.Join(r.real, ".gemini", "config")
	if err := os.MkdirAll(realCfg, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(r.bin, name)
	tok := filepath.Join(r.real, TokenRel)
	body := strings.NewReplacer(
		"@OUTSIDE@", ShellQuote(outside),
		"@REALCFG@", ShellQuote(realCfg),
		"@TOK@", ShellQuote(tok),
	).Replace(outsideProbeScript)
	put(t, p, body, 0o755)
	return p
}

func TestIsolateLandlockDeniesOutsideWrite(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Landlock enforcement test runs on Linux")
	}
	if err := CheckAvailable(); err != nil {
		t.Fatalf("Landlock unavailable on Linux test host: %v", err)
	}
	r := newRig(t)
	outside := filepath.Join(r.tmp, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	probe := writeOutsideProbe(t, r, "probe-agy", outside)

	c := r.config("landlock-ro")
	c.TestRW = nil
	c.TestRO = []string{r.bin}
	b := r.prepared(c)
	tok := filepath.Join(r.real, TokenRel)
	if rc := b.Session(context.Background(), probe, tok, "probe", "out.txt"); rc != 0 {
		t.Fatalf("session rc %d\n%s", rc, r.out.String())
	}

	for name, p := range map[string]string{
		"home": filepath.Join(b.Home(), "ok.txt"),
		"tmp":  filepath.Join(b.Dir(), "tmp", "ok.txt"),
		"app":  filepath.Join(b.App(), "ok.txt"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s write failed inside sandbox (%s): %v", name, p, err)
		}
	}
	for name, p := range map[string]string{
		"outside": filepath.Join(outside, "escape.txt"),
		"realcfg": filepath.Join(r.real, ".gemini", "config", "tamper.md"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s escape file unexpectedly exists or errored (%s): %v", name, p, err)
		}
	}
	if got, err := os.ReadFile(tok); err != nil || string(got) != "token" {
		t.Errorf("real token modified: %q %v", got, err)
	}
	for _, errName := range []string{"outside.err", "real.err", "tok.err"} {
		msg, _ := os.ReadFile(filepath.Join(b.Home(), errName))
		if !strings.Contains(string(msg), "Permission denied") {
			t.Errorf("%s = %q, want Permission denied (EACCES)", errName, strings.TrimSpace(string(msg)))
		}
	}
	if !b.After() {
		t.Errorf("After() = false, reasons %v\n%s", b.Reasons(), r.out.String())
	}
	b.ExitChecks()
	if !b.Intact() {
		t.Errorf("box not intact: %v", b.Reasons())
	}
}

func TestIsolateLandlockOffMustFail(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Landlock control test runs on Linux")
	}
	r := newRig(t)
	outside := filepath.Join(r.tmp, "outside-unconfined")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	probe := writeOutsideProbe(t, r, "probe-unconfined", outside)

	c := r.config("unconfined")
	c.TestRW = nil
	c.TestRO = nil
	c.TestUnconfined = true
	b := r.prepared(c)
	tok := filepath.Join(r.real, TokenRel)
	if rc := b.Session(context.Background(), probe, tok, "probe", "out.txt"); rc != 0 {
		t.Fatalf("unconfined session rc %d\n%s", rc, r.out.String())
	}
	got, err := os.ReadFile(filepath.Join(outside, "escape.txt"))
	if err != nil || string(got) != "escaped\n" {
		t.Fatalf("unconfined probe did not write outside file (%q, %v): confinement test would be vacuous", got, err)
	}
	b.After()
	b.ExitChecks()
	if !b.Has(ReasonRealConfig) {
		t.Errorf("unconfined realHome tamper should trip %s; got reasons %v", ReasonRealConfig, b.Reasons())
	}
}

func TestIsolateLandlockAllowsPTY(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Landlock PTY test runs on Linux")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skipf("script command unavailable for PTY test: %v", err)
	}
	if err := CheckAvailable(); err != nil {
		t.Fatalf("Landlock unavailable on Linux test host: %v", err)
	}
	r := newRig(t)
	probe := filepath.Join(r.bin, "pty-agy")
	put(t, probe, "#!/bin/bash\nscript -qec 'printf \"pty-ok\\n\" > \"$HOME/pty.txt\"' /dev/null 2> \"$HOME/pty.err\"\nexit 0\n", 0o755)

	c := r.config("landlock-pty")
	c.TestRW = nil
	c.TestRO = []string{r.bin}
	b := r.prepared(c)
	tok := filepath.Join(r.real, TokenRel)
	if rc := b.Session(context.Background(), probe, tok, "probe", "out.txt"); rc != 0 {
		t.Fatalf("session rc %d\n%s", rc, r.out.String())
	}
	got, err := os.ReadFile(filepath.Join(b.Home(), "pty.txt"))
	if err != nil || string(got) != "pty-ok\n" {
		ptyErr, _ := os.ReadFile(filepath.Join(b.Home(), "pty.err"))
		t.Fatalf("pty probe output = (%q, %v), stderr = %q; want pty-ok", got, err, strings.TrimSpace(string(ptyErr)))
	}
	if !b.After() {
		t.Errorf("After() = false, reasons %v\n%s", b.Reasons(), r.out.String())
	}
	b.ExitChecks()
	if !b.Intact() {
		t.Errorf("box not intact: %v", b.Reasons())
	}
}

func TestIsolateLandlockBlocksHostProcAndDevPts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Landlock proc/pts isolation test runs on Linux")
	}
	if err := CheckAvailable(); err != nil {
		t.Fatalf("Landlock unavailable on Linux test host: %v", err)
	}
	r := newRig(t)
	probe := filepath.Join(r.bin, "proc-pts-agy")
	script := strings.ReplaceAll(`#!/bin/bash
set +e
(cat "/proc/@PPID@/environ") >/dev/null 2> "$HOME/proc_parent.err"
echo "parent_rc=$?" >> "$HOME/status.txt"
ls -1 /proc > "$HOME/proc_ls.txt" 2> "$HOME/proc_ls.err"
(ls /dev) >/dev/null 2> "$HOME/dev_ls.err"
echo "dev_ls_rc=$?" >> "$HOME/status.txt"
cat /proc/self/status > "$HOME/self_status.txt" 2> "$HOME/self_status.err"
echo "self_rc=$?" >> "$HOME/status.txt"
ls -1 /dev/pts | sort > "$HOME/pts.txt" 2> "$HOME/pts.err"
(cd / && exec setsid sleep 300 </dev/null >/dev/null 2>&1) &
exit 0
`, "@PPID@", strconv.Itoa(os.Getpid()))
	put(t, probe, script, 0o755)

	c := r.config("landlock-proc-pts")
	c.TestRW = nil
	c.TestRO = []string{r.bin}
	b := r.prepared(c)
	tok := filepath.Join(r.real, TokenRel)
	if rc := b.Session(context.Background(), probe, tok, "probe", "out.txt"); rc != 0 {
		t.Fatalf("session rc %d\n%s", rc, r.out.String())
	}
	procParentErr, _ := os.ReadFile(filepath.Join(b.Home(), "proc_parent.err"))
	if !strings.Contains(string(procParentErr), "No such file or directory") {
		t.Errorf("proc_parent.err = %q, want No such file or directory (host PID hidden in private /proc)", strings.TrimSpace(string(procParentErr)))
	}
	procList, _ := os.ReadFile(filepath.Join(b.Home(), "proc_ls.txt"))
	if slices.Contains(strings.Fields(string(procList)), strconv.Itoa(os.Getpid())) {
		t.Errorf("/proc listing exposed host pid %d: %q", os.Getpid(), strings.TrimSpace(string(procList)))
	}
	devLsErr, _ := os.ReadFile(filepath.Join(b.Home(), "dev_ls.err"))
	if !strings.Contains(string(devLsErr), "Permission denied") {
		t.Errorf("dev_ls.err = %q, want Permission denied (EACCES)", strings.TrimSpace(string(devLsErr)))
	}
	selfStatus, err := os.ReadFile(filepath.Join(b.Home(), "self_status.txt"))
	if err != nil || !strings.Contains(string(selfStatus), "Pid:") {
		selfErr, _ := os.ReadFile(filepath.Join(b.Home(), "self_status.err"))
		t.Errorf("/proc/self/status unreadable (%q, %v, stderr=%q)", string(selfStatus), err, strings.TrimSpace(string(selfErr)))
	}
	ptsOut, err := os.ReadFile(filepath.Join(b.Home(), "pts.txt"))
	if err != nil || strings.TrimSpace(string(ptsOut)) != "ptmx" {
		ptsErr, _ := os.ReadFile(filepath.Join(b.Home(), "pts.err"))
		t.Errorf("/dev/pts listing = (%q, %v, err=%q), want only ptmx in private devpts", strings.TrimSpace(string(ptsOut)), err, strings.TrimSpace(string(ptsErr)))
	}
	if !b.After() {
		t.Errorf("After() = false, reasons %v\n%s", b.Reasons(), r.out.String())
	}
	b.ExitChecks()
	if !b.Intact() {
		t.Errorf("box not intact (PID namespace should reap leftover children): %v", b.Reasons())
	}
}

func TestAppendSafePATHDirsSymlink(t *testing.T) {
	base := t.TempDir()
	osTmp := filepath.Join(base, "ostmp")
	t.Setenv("TMPDIR", osTmp)

	realHome := filepath.Join(base, "realhome")
	homeBin := filepath.Join(realHome, ".local", "bin")
	tmpTarget := filepath.Join(osTmp, "tbin")
	sysDir := filepath.Join(base, "sysbin")
	for _, d := range []string{homeBin, tmpTarget, sysDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	linkToHome := filepath.Join(base, "ext-home-bin")
	linkToTmp := filepath.Join(base, "ext-tmp-bin")
	if err := os.Symlink(homeBin, linkToHome); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tmpTarget, linkToTmp); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.Join([]string{linkToHome, linkToTmp, sysDir}, string(os.PathListSeparator)))

	got := appendSafePATHDirs(nil, realHome)
	if !slices.Contains(got, sysDir) {
		t.Fatalf("appendSafePATHDirs missing safe dir %s: got %v", sysDir, got)
	}
	for _, blocked := range []string{linkToHome, homeBin, linkToTmp, tmpTarget} {
		if slices.Contains(got, blocked) {
			t.Errorf("appendSafePATHDirs leaked symlinked/excluded dir %s: got %v", blocked, got)
		}
	}
}

func TestSeatbeltProfileRender(t *testing.T) {
	prof, err := renderSeatbeltProfile(Policy{
		Argv:    []string{"/opt/agy/bin/agy"},
		Dir:     "/tmp/box/app",
		RWPaths: []string{"/tmp/box/app", "/tmp/box/home", "/tmp/box/tmp"},
		ROPaths: []string{"/usr", "/bin"},
		Network: true,
	})
	if err != nil {
		t.Fatalf("renderSeatbeltProfile: %v", err)
	}
	for _, want := range []string{
		"(version 1)",
		"(deny default)",
		"(allow signal (target same-sandbox))",
		"(allow pseudo-tty)",
		`(allow file-read* file-write* file-ioctl (literal "/dev/ptmx"))`,
		`(deny mach-lookup (global-name "com.apple.SecurityServer") (global-name "com.apple.coreservices.launchservicesd") (global-name "com.apple.pasteboard.1") (global-name "com.apple.lsd.mapdb"))`,
		"(allow network*)",
		`(allow file-read* file-write* file-ioctl (subpath "/tmp/box/app")`,
		`(allow file-read* file-write* file-ioctl (subpath "/tmp/box/home")`,
		`(allow file-read* (subpath "/usr")`,
	} {
		if !strings.Contains(prof, want) {
			t.Errorf("seatbelt profile missing %s\n%s", want, prof)
		}
	}
	for _, forbid := range []string{
		`(regex #"^/dev/ttys`,
		"(allow mach-lookup)\n",
		`file-write* (subpath "/usr")`,
		`/Users/real`,
	} {
		if strings.Contains(prof, forbid) {
			t.Errorf("seatbelt profile unexpectedly contains %s:\n%s", forbid, prof)
		}
	}
}

func TestChooseRunner(t *testing.T) {
	okFn := func() error { return nil }
	failFn := func(msg string) func() error { return func() error { return errors.New(msg) } }

	if r, _, err := ChooseRunner(RunnerIsolate, okFn); err != nil || r != RunnerIsolate {
		t.Errorf("explicit isolate: got (%q, %v)", r, err)
	}
	if _, _, err := ChooseRunner(RunnerIsolate, failFn("no landlock")); err == nil {
		t.Error("explicit isolate with failing check succeeded")
	}
	if r, why, err := ChooseRunner(RunnerAuto, okFn); err != nil || r != RunnerIsolate || why != RunnerIsolate {
		t.Errorf("auto primary isolate: got (%q, %q, %v)", r, why, err)
	}
	if r, _, err := ChooseRunner(RunnerAuto, failFn("no landlock")); err == nil || r != "" {
		t.Errorf("unavailable isolate under auto: got (%q, %v), want fail closed", r, err)
	}
	if _, _, err := ChooseRunner("docker", okFn); err == nil {
		t.Error("docker runner should be refused")
	}
	if _, _, err := ChooseRunner("host", okFn); err == nil {
		t.Error("host runner should be refused")
	}
	if err := CheckEvalRunner("auto"); err == nil {
		t.Error("CheckEvalRunner accepted auto")
	}
	if err := CheckEvalRunner("docker"); err == nil {
		t.Error("CheckEvalRunner accepted docker")
	}
	if err := CheckEvalRunner(RunnerIsolate); err != nil {
		t.Errorf("CheckEvalRunner(%s): %v", RunnerIsolate, err)
	}
}

func TestIsolatePackageDeps(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{join .Deps \"\\n\"}}", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		for _, forbidden := range []string{"modernc.org/sqlite", "internal/sandbox", "internal/work", "internal/brain", "internal/search", "internal/metrics"} {
			if strings.Contains(line, forbidden) {
				t.Errorf("internal/isolate imports forbidden dependency %q (%s)", forbidden, line)
			}
		}
	}
}
