package dream

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallTimerLinux(t *testing.T) {
	home := timerHome(t)
	calls := fakeManager(t, "systemctl", 0)
	if err := InstallTimer(home, "/p:/q", "linux"); err != nil {
		t.Fatal(err)
	}
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	service := read(t, filepath.Join(unitDir, "fyr-dream.service"))
	if !strings.Contains(service, `ExecStart="`+filepath.Join(home, ".local", "bin", "fyr")+`" dream --all`) || !strings.Contains(service, `PATH=/p:/q`) {
		t.Errorf("service:\n%s", service)
	}
	if got := callsOf(t, calls); got != "--user daemon-reload\n--user enable --now fyr-dream.timer\n" {
		t.Errorf("systemctl calls:\n%s", got)
	}
	if !TimerInstalled(home) {
		t.Error("TimerInstalled = false after install")
	}
	// Identical files: a rerun is fine.
	if err := InstallTimer(home, "/p:/q", "linux"); err != nil {
		t.Errorf("identical rerun: %v", err)
	}
	// Another PATH renders other text: the file on disk is kept and named.
	os.Remove(calls)
	err := InstallTimer(home, "/other", "linux")
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 2 || !strings.Contains(ee.Msg, "fyr-dream.service") || !strings.Contains(ee.Msg, "--remove-timer") {
		t.Errorf("a differing unit: %v", err)
	}
	if callsOf(t, calls) != "" {
		t.Error("systemctl ran although a unit was kept")
	}
	if !strings.Contains(read(t, filepath.Join(unitDir, "fyr-dream.service")), "PATH=/p:/q") {
		t.Error("the differing unit was overwritten")
	}
}

func TestInstallTimerRefusals(t *testing.T) {
	var ee *ExitError
	// No binary at BinPath.
	fakeManager(t, "systemctl", 0)
	if err := InstallTimer(t.TempDir(), "/p", "linux"); !errors.As(err, &ee) || ee.Code != 2 || !strings.Contains(ee.Msg, "fyr") {
		t.Errorf("no binary: %v", err)
	}
	// No systemctl on PATH.
	home := timerHome(t)
	t.Setenv("PATH", t.TempDir())
	if err := InstallTimer(home, "/p", "linux"); !errors.As(err, &ee) || ee.Code != 2 || !strings.Contains(ee.Msg, "systemctl") {
		t.Errorf("no systemctl: %v", err)
	}
	if TimerInstalled(home) {
		t.Error("files written although systemctl is missing")
	}
	// systemctl fails: the files stay and are named.
	home = timerHome(t)
	fakeManager(t, "systemctl", 1)
	if err := InstallTimer(home, "/p", "linux"); !errors.As(err, &ee) || ee.Code != 2 || !strings.Contains(ee.Msg, "left in place") {
		t.Errorf("failing systemctl: %v", err)
	}
	// An OS with no user timer.
	if err := InstallTimer(timerHome(t), "/p", "windows"); err == nil {
		t.Error("installed on an unsupported OS")
	}
}

func TestRemoveTimerLinux(t *testing.T) {
	home := timerHome(t)
	calls := fakeManager(t, "systemctl", 0)
	if _, err := RemoveTimer(home, "linux"); err != nil {
		t.Fatalf("absent is fine: %v", err)
	}
	if callsOf(t, calls) != "" {
		t.Error("systemctl ran with no unit installed")
	}
	if err := InstallTimer(home, "/p", "linux"); err != nil {
		t.Fatal(err)
	}
	os.Remove(calls)
	if _, err := RemoveTimer(home, "linux"); err != nil {
		t.Fatal(err)
	}
	if got := callsOf(t, calls); got != "--user disable --now fyr-dream.timer\n--user daemon-reload\n" {
		t.Errorf("systemctl calls:\n%s", got)
	}
	if TimerInstalled(home) {
		t.Error("unit files left after remove")
	}
}

func TestTimerMacOS(t *testing.T) {
	home := timerHome(t)
	calls := fakeManager(t, "launchctl", 0)
	if err := InstallTimer(home, "/p", "darwin"); err != nil {
		t.Fatal(err)
	}
	plist := macPlistPath(home)
	if _, err := os.Stat(plist); err != nil {
		t.Fatal(err)
	}
	if got := callsOf(t, calls); !strings.HasPrefix(got, "bootstrap gui/") || !strings.Contains(got, plist) {
		t.Errorf("launchctl calls:\n%s", got)
	}
	os.Remove(calls)
	if _, err := RemoveTimer(home, "darwin"); err != nil {
		t.Fatal(err)
	}
	if got := callsOf(t, calls); !strings.HasPrefix(got, "bootout gui/") {
		t.Errorf("launchctl calls:\n%s", got)
	}
	if TimerInstalled(home) {
		t.Error("plist left after remove")
	}
}

func TestInstallTimerRefusesUnsafePaths(t *testing.T) {
	fakeManager(t, "systemctl", 0)
	var ee *ExitError
	for _, path := range []string{"/p\"\nExecStartPre=/bin/evil", "/p\\x", "/p\r"} {
		home := timerHome(t)
		if err := InstallTimer(home, path, "linux"); !errors.As(err, &ee) || ee.Code != 2 || TimerInstalled(home) {
			t.Errorf("PATH %q: %v", path, err)
		}
	}
	// The refusal names what to do instead (guide § 3c; the verifier of
	// ee20d00): the shell's PATH is what the unit freezes.
	if !strings.Contains(ee.Msg, "run it from a shell whose PATH") {
		t.Errorf("the refusal names no alternative: %s", ee.Msg)
	}
	service, _ := renderSystemd("/h/100%/fyr", "/h/100%", "/p%n")
	if !strings.Contains(service, "/h/100%%/fyr") || !strings.Contains(service, "PATH=/p%%n") || !strings.Contains(service, `--home "/h/100%%"`) {
		t.Errorf("a %% was not escaped:\n%s", service)
	}
}

// TestInstallTimerRefusesEveryControlCharacter: the refusal named a quote, a
// backslash and two line breaks; any other control character reached the
// unit or the plist too (a plist's XML cannot carry most of them), in the
// home as well as in PATH (project review 2026-10-09, row 21). One case per
// character, in each place.
func TestInstallTimerRefusesEveryControlCharacter(t *testing.T) {
	fakeManager(t, "systemctl", 0)
	var ee *ExitError
	for name, c := range map[string]string{"quote": `"`, "backslash": `\`, "newline": "\n", "carriage return": "\r",
		"tab": "\t", "start of heading": "\x01", "escape": "\x1b", "delete": "\x7f", "next line (U+0085)": "\u0085"} {
		home := timerHome(t)
		if err := InstallTimer(home, "/p"+c+"q", "linux"); !errors.As(err, &ee) || ee.Code != 2 || TimerInstalled(home) {
			t.Errorf("PATH with a %s: %v", name, err)
		}
		// The home is in the binary's path and in --home.
		home = filepath.Join(t.TempDir(), "h"+c+"o")
		write(t, filepath.Join(home, ".local", "bin", "fyr"), "#!/bin/sh\n", fresh)
		if err := InstallTimer(home, "/p", "linux"); !errors.As(err, &ee) || ee.Code != 2 || TimerInstalled(home) {
			t.Errorf("home with a %s: %v", name, err)
		}
	}
}

// The home was checked only through the binary's path, which Join cleans:
// a home whose quote sits in a component that `..` cancels passed the check
// and reached `--home "…"` raw. main makes the home absolute (and so clean)
// first, which kept the CLI safe; InstallTimer no longer depends on that
// (the release gate's security review of v2.6.2..13c5bde, finding 3).
func TestInstallTimerChecksTheHomeItself(t *testing.T) {
	fakeManager(t, "systemctl", 0)
	base := t.TempDir()
	write(t, filepath.Join(base, ".local", "bin", "fyr"), "#!/bin/sh\n", fresh)
	home := base + string(filepath.Separator) + `"x` + string(filepath.Separator) + ".."
	var ee *ExitError
	if err := InstallTimer(home, "/p", "linux"); !errors.As(err, &ee) || ee.Code != 2 {
		t.Errorf("a home holding a quote that Join cleans away: %v", err)
	}
}

// TestInstallTimerKeepsOnlyAbsolutePath: a relative PATH entry (".", "bin",
// an empty one) would resolve against the unit's working directory at 03:00,
// not where the operator's shell was; only absolute entries are frozen in.
func TestInstallTimerKeepsOnlyAbsolutePath(t *testing.T) {
	home := timerHome(t)
	fakeManager(t, "systemctl", 0)
	if err := InstallTimer(home, "/a/bin::.:node_modules/.bin:/b", "linux"); err != nil {
		t.Fatal(err)
	}
	service := read(t, filepath.Join(home, ".config", "systemd", "user", "fyr-dream.service"))
	if !strings.Contains(service, `Environment="PATH=/a/bin:/b"`) {
		t.Errorf("service:\n%s", service)
	}
}

// TestInstallTimerOlderUnitNamed: a unit an older fyr wrote, without
// TimeoutStartSec, is kept and the refusal names that case and the way out;
// --remove-timer then --install-timer writes the current unit (SD36).
func TestInstallTimerOlderUnitNamed(t *testing.T) {
	home := timerHome(t)
	fakeManager(t, "systemctl", 0)
	if err := InstallTimer(home, "/p", "linux"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "systemd", "user", "fyr-dream.service")
	older := strings.Replace(read(t, path), "TimeoutStartSec=2h\n", "", 1)
	if err := os.WriteFile(path, []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	err := InstallTimer(home, "/p", "linux")
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 2 || !strings.Contains(ee.Msg, "an older fyr") || !strings.Contains(ee.Msg, "--remove-timer") {
		t.Errorf("an older unit: %v", err)
	}
	if read(t, path) != older {
		t.Error("the older unit was overwritten")
	}
	if _, err := RemoveTimer(home, "linux"); err != nil {
		t.Fatal(err)
	}
	if err := InstallTimer(home, "/p", "linux"); err != nil || !strings.Contains(read(t, path), "TimeoutStartSec=2h") {
		t.Errorf("reinstall: %v\n%s", err, read(t, path))
	}
}
