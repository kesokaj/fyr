package dream

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeManager puts a recording systemctl or launchctl first on PATH; code is
// its exit status. The real user manager is never reached (R6).
func fakeManager(t *testing.T, name string, code int) (calls string) {
	t.Helper()
	bin := t.TempDir()
	calls = filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\necho \"$*\" >> \"" + calls + "\"\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	return calls
}

// fakeManagerOff is fakeManager whose probe call (systemctl is-enabled,
// launchctl print) prints answer and exits 1, and every other call succeeds.
func fakeManagerOff(t *testing.T, name, probe, answer string) (calls string) {
	t.Helper()
	bin := t.TempDir()
	calls = filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\necho \"$*\" >> \"" + calls + "\"\ncase \"$*\" in *" + probe + "*) echo '" + answer + "'; exit 1 ;; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	return calls
}

func timerHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	// The test's home counts as the real one, so the bare unit names apply.
	prev := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = prev })
	write(t, filepath.Join(home, ".local", "bin", "fyr"), "#!/bin/sh\n", fresh)
	return home
}

func callsOf(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRenderSystemdUnits(t *testing.T) {
	service, timer := renderSystemd("/h/.local/bin/fyr", "/h", "/h/.local/bin:/usr/bin")
	for _, want := range []string{"Type=oneshot", `Environment="PATH=/h/.local/bin:/usr/bin"`, `ExecStart="/h/.local/bin/fyr" dream --all --home "/h"`} {
		if !strings.Contains(service, want) {
			t.Errorf("service lacks %q:\n%s", want, service)
		}
	}
	for _, want := range []string{"OnCalendar=*-*-* 03:00", "Persistent=true", "WantedBy=timers.target"} {
		if !strings.Contains(timer, want) {
			t.Errorf("timer lacks %q:\n%s", want, timer)
		}
	}
}

func TestRenderLaunchdPlist(t *testing.T) {
	plist := renderLaunchd("com.fyr.dream", "/Users/o & p/.local/bin/fyr", "/Users/o & p", "/usr/bin")
	for _, want := range []string{"<string>com.fyr.dream</string>", "<string>/Users/o &amp; p/.local/bin/fyr</string>", "<string>dream</string>", "<string>--all</string>", "<string>--home</string><string>/Users/o &amp; p</string>", "<key>Hour</key><integer>3</integer>", "<key>PATH</key><string>/usr/bin</string>"} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q:\n%s", want, plist)
		}
	}
}

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

// A home other than the real one gets its own unit name and label, so a scratch
// install or removal never reaches the operator's timer through the shared user
// manager (v2.0.0 gate, security code slice; R6).
func TestUnitNamesPerHome(t *testing.T) {
	real := timerHome(t)
	if unit, plist := UnitFor(real); unit != "fyr-dream" || plist != "com.fyr.dream" {
		t.Fatalf("real home: %s %s", unit, plist)
	}
	other := t.TempDir()
	unit, plist := UnitFor(other)
	if !strings.HasPrefix(unit, "fyr-dream-") || len(unit) != len("fyr-dream-")+8 || plist != plistName+strings.TrimPrefix(unit, unitName) {
		t.Fatalf("other home: %s %s", unit, plist)
	}
	if again, _ := UnitFor(other); again != unit {
		t.Fatalf("not deterministic: %s then %s", unit, again)
	}
	if u2, _ := UnitFor(t.TempDir()); u2 == unit {
		t.Fatal("two homes share a unit name")
	}
	write(t, filepath.Join(other, ".local", "bin", "fyr"), "#!/bin/sh\n", fresh)
	calls := fakeManager(t, "systemctl", 0)
	if err := InstallTimer(other, "/p", "linux"); err != nil {
		t.Fatal(err)
	}
	if got := callsOf(t, calls); got != "--user daemon-reload\n--user enable --now "+unit+".timer\n" {
		t.Errorf("systemctl calls:\n%s", got)
	}
	service := read(t, filepath.Join(other, ".config", "systemd", "user", unit+".service"))
	if !strings.Contains(service, `dream --all --home "`+other+`"`) {
		t.Errorf("a scratch timer must dream over its own home:\n%s", service)
	}
	os.Remove(calls)
	if _, err := RemoveTimer(other, "linux"); err != nil {
		t.Fatal(err)
	}
	if got := callsOf(t, calls); got != "--user disable --now "+unit+".timer\n--user daemon-reload\n" {
		t.Errorf("remove side keeps the bare name:\n%s", got)
	}
	// macOS: the label and the plist path carry the same suffix, and bootout names that path.
	mcalls := fakeManager(t, "launchctl", 0)
	if err := InstallTimer(other, "/p", "darwin"); err != nil {
		t.Fatal(err)
	}
	pl := read(t, macPlistPath(other))
	if !strings.Contains(pl, "<string>"+plist+"</string>") || !strings.Contains(pl, "<string>--home</string><string>"+other+"</string>") || !strings.HasSuffix(macPlistPath(other), plist+".plist") {
		t.Errorf("darwin plist for a scratch home:\n%s", pl)
	}
	os.Remove(mcalls)
	if _, err := RemoveTimer(other, "darwin"); err != nil {
		t.Fatal(err)
	}
	if got := callsOf(t, mcalls); !strings.Contains(got, "bootout gui/") || !strings.Contains(got, plist+".plist") {
		t.Errorf("launchctl bootout:\n%s", got)
	}
	// A symlinked spelling of the real home is the real home.
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here")
	}
	if u, _ := UnitFor(link); u != "fyr-dream" {
		t.Errorf("symlinked real home got %s", u)
	}
}

// TestUnitForIgnoresHomeOverride: a sandbox run sets HOME to its own
// directory and passes that directory as --home. The service manager is the
// account's, not the sandbox's, so that home is not the real one and must not
// get the bare unit names (security review of the v2.1.0 gate: with HOME as
// the yardstick, a sandbox --remove-timer reached the operator's real timer).
// No skip: where the account database has no entry for this uid, no home is
// the real one, and the sandbox still gets suffixed names.
func TestUnitForIgnoresHomeOverride(t *testing.T) {
	sandbox := t.TempDir()
	t.Setenv("HOME", sandbox)
	unit, plist := UnitFor(sandbox)
	if unit == unitName || plist == plistName {
		t.Errorf("a HOME override got the real home's unit names: %s, %s", unit, plist)
	}
}

// TestAccountHomeNeverFallsBackToHOME: an account the database does not
// list (LDAP, SSSD, systemd-homed, a container's bare uid — os/user without
// cgo reads only /etc/passwd on Linux) has no known home. HOME is not an
// answer there: it is what a sandbox overrides (code review of 7dd0645..f62513b).
func TestAccountHomeNeverFallsBackToHOME(t *testing.T) {
	sandbox := t.TempDir()
	t.Setenv("HOME", sandbox)
	prev := lookupAccount
	lookupAccount = func() (*user.User, error) { return nil, user.UnknownUserIdError(os.Getuid()) }
	t.Cleanup(func() { lookupAccount = prev })
	if home, err := accountHome(); err == nil {
		t.Fatalf("an unlisted account got a home: %q", home)
	}
	unit, plist := UnitFor(sandbox)
	if unit == unitName || plist == plistName {
		t.Errorf("with no account home, a home still got the bare unit names: %s, %s", unit, plist)
	}
	// An entry with an empty home directory is no answer either.
	lookupAccount = func() (*user.User, error) { return &user.User{Uid: "1"}, nil }
	if home, err := accountHome(); err == nil {
		t.Errorf("an account with no home directory got one: %q", home)
	}
}

// TestRenderSystemdEscapesDollar: systemd expands $VAR and ${VAR} in
// ExecStart, inside quotes too; $$ is a literal dollar sign.
func TestRenderSystemdEscapesDollar(t *testing.T) {
	service, _ := renderSystemd("/h/a$b/fyr", "/h/${X}", "/p")
	if !strings.Contains(service, `ExecStart="/h/a$$b/fyr" dream --all --home "/h/$${X}"`) {
		t.Errorf("a $ in ExecStart was not escaped:\n%s", service)
	}
}

// TestRemoveTimerSaysWhatItRemoved: the caller prints "removed" only when a
// unit of this home was on disk and is gone (the v2.1.0 gate: a removal that
// found nothing reported success).
func TestRemoveTimerSaysWhatItRemoved(t *testing.T) {
	fakeManager(t, "systemctl", 0)
	home := timerHome(t)
	if err := InstallTimer(home, "/usr/bin:/bin", "linux"); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveTimer(home, "linux"); err != nil || !removed {
		t.Errorf("an installed timer: removed=%v err=%v, want true", removed, err)
	}
	if removed, err := RemoveTimer(home, "linux"); err != nil || removed {
		t.Errorf("nothing installed: removed=%v err=%v, want false", removed, err)
	}
}

// TestLegacyTimerFilesAreNamedNotRemoved: a unit that v2.0.0 or earlier wrote
// under the bare name, in a home that now gets a suffixed name, is not this
// home's unit any more. It is never stopped or deleted here (a stop by name
// ends whichever unit the manager loaded, possibly the real home's): it is
// named, with the steps, by remove, uninstall and status.
func TestLegacyTimerFilesAreNamedNotRemoved(t *testing.T) {
	calls := fakeManager(t, "systemctl", 0)
	real := timerHome(t)
	other := t.TempDir()
	timerFile := filepath.Join(other, ".config", "systemd", "user", oldUnitName+".timer")
	plistFile := filepath.Join(other, "Library", "LaunchAgents", oldPlistName+".plist")
	write(t, timerFile, "x", fresh)
	write(t, plistFile, "x", fresh)
	got := LegacyTimerFiles(other)
	if len(got) != 2 || got[0] != timerFile || got[1] != plistFile {
		t.Errorf("LegacyTimerFiles = %v, want the timer and the plist", got)
	}
	if removed, err := RemoveTimer(other, "linux"); err != nil || removed {
		t.Errorf("RemoveTimer on a home with only legacy files: removed=%v err=%v", removed, err)
	}
	for _, f := range []string{timerFile, plistFile} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("a legacy file was removed: %v", err)
		}
	}
	if c := callsOf(t, calls); c != "" {
		t.Errorf("the service manager was called for a legacy unit:\n%s", c)
	}
	linux := LegacyTimerNotice(other, "linux")
	for _, want := range []string{timerFile, "FragmentPath", "systemctl --user disable --now " + oldUnitName + ".timer", "daemon-reload", "v2.0.0", "fyr dream --install-timer --home '" + other + "'"} {
		if !strings.Contains(linux, want) {
			t.Errorf("the linux notice lacks %q:\n%s", want, linux)
		}
	}
	darwin := LegacyTimerNotice(other, "darwin")
	for _, want := range []string{plistFile, "launchctl print gui/" + strconv.Itoa(os.Getuid()) + "/" + oldPlistName, "launchctl bootout"} {
		if !strings.Contains(darwin, want) {
			t.Errorf("the darwin notice lacks %q:\n%s", want, darwin)
		}
	}
	// The advice is a dry run with this home named: the bare command would
	// remove the timer of whatever home it defaults to, the real one included
	// (code review of v2.1.0..a62eb9d).
	if got := StatusLine(other); !strings.HasSuffix(got, "; legacy timer files under "+other+": fyr dream --remove-timer --dry-run --home '"+other+"' prints the cleanup steps") {
		t.Errorf("StatusLine does not name the legacy files: %q", got)
	}
	// A scratch home that shares the real home's .config through a link sees
	// the real home's own unit there: that is not legacy, and the notice must
	// not tell the operator to delete it (security review of v2.1.0..a62eb9d).
	write(t, filepath.Join(real, ".config", "systemd", "user", oldUnitName+".service"), "x", fresh)
	linked := t.TempDir()
	if err := os.Symlink(filepath.Join(real, ".config"), filepath.Join(linked, ".config")); err == nil {
		if got := LegacyTimerFiles(linked); len(got) != 0 {
			t.Errorf("the real home's unit, seen through a linked .config, is called legacy: %v", got)
		}
	}
	// The real home's bare-named unit is its own, never legacy.
	write(t, filepath.Join(real, ".config", "systemd", "user", oldUnitName+".timer"), "x", fresh)
	if got := LegacyTimerFiles(real); len(got) != 0 || LegacyTimerNotice(real, "linux") != "" {
		t.Errorf("the real home's own unit is called legacy: %v", got)
	}
}

// TestShellQuote: a path printed inside a command the operator may paste.
func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/home/u":      "'/home/u'",
		"/h/a b":       "'/h/a b'",
		"/h/$(id)":     "'/h/$(id)'",
		"/h/it's here": `'/h/it'\''s here'`,
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestReplaceOldTimer: install replaces this home's timer under the name
// from before the rename — sh-agent-dream, com.styrman.sh-agent-dream, with
// the per-home suffix of UnitFor — with fyr-dream: the new unit first, so a
// refusal keeps the old one running, then the old one stopped and removed
// (plan fyr D13). A bare old name under a home that is not the real one is
// a legacy file, named elsewhere and never touched here.
func TestReplaceOldTimer(t *testing.T) {
	oldFiles := func(home string) []string {
		unit, _ := oldUnitFor(home)
		dir := filepath.Join(home, ".config", "systemd", "user")
		return []string{filepath.Join(dir, unit+".service"), filepath.Join(dir, unit+".timer")}
	}
	plant := func(t *testing.T, home string) {
		for _, p := range oldFiles(home) {
			write(t, p, "[Unit]\nDescription=sh-agent dream\n", fresh)
		}
	}
	gone := func(t *testing.T, paths ...string) {
		t.Helper()
		for _, p := range paths {
			if _, err := os.Lstat(p); !os.IsNotExist(err) {
				t.Errorf("%s still there: %v", p, err)
			}
		}
	}

	t.Run("real home, linux", func(t *testing.T) {
		home := timerHome(t)
		calls := fakeManager(t, "systemctl", 0)
		plant(t, home)
		old, replaced, err := ReplaceOldTimer(home, "/p", "linux", false)
		if err != nil || old != "sh-agent-dream" || !replaced {
			t.Fatalf("old %q, replaced %v, err %v", old, replaced, err)
		}
		gone(t, oldFiles(home)...)
		if !TimerInstalledHere(home, "linux") || !strings.Contains(read(t, linuxServicePath(home)), `/.local/bin/fyr" dream --all`) {
			t.Error("the fyr-dream unit is not installed")
		}
		want := "--user is-enabled sh-agent-dream.timer\n--user daemon-reload\n--user enable --now fyr-dream.timer\n--user disable --now sh-agent-dream.timer\n--user daemon-reload\n"
		if got := callsOf(t, calls); got != want {
			t.Errorf("systemctl calls:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("scratch home, suffixed names", func(t *testing.T) {
		timerHome(t) // the real home is another directory
		other := t.TempDir()
		write(t, filepath.Join(other, ".local", "bin", "fyr"), "#!/bin/sh\n", fresh)
		calls := fakeManager(t, "systemctl", 0)
		plant(t, other)
		oldUnit, _ := oldUnitFor(other)
		newUnit, _ := UnitFor(other)
		if !strings.HasPrefix(oldUnit, "sh-agent-dream-") || strings.TrimPrefix(oldUnit, oldUnitName) != strings.TrimPrefix(newUnit, unitName) {
			t.Fatalf("old %s and new %s do not share the per-home suffix", oldUnit, newUnit)
		}
		old, _, err := ReplaceOldTimer(other, "/p", "linux", false)
		if err != nil || old != oldUnit {
			t.Fatalf("old %q, err %v", old, err)
		}
		gone(t, oldFiles(other)...)
		if got := callsOf(t, calls); !strings.Contains(got, "enable --now "+newUnit+".timer") || !strings.Contains(got, "disable --now "+oldUnit+".timer") {
			t.Errorf("systemctl calls:\n%s", got)
		}
	})

	t.Run("dry run changes nothing", func(t *testing.T) {
		home := timerHome(t)
		calls := fakeManager(t, "systemctl", 0)
		plant(t, home)
		old, replaced, err := ReplaceOldTimer(home, "/p", "linux", true)
		if err != nil || old != "sh-agent-dream" || !replaced {
			t.Fatalf("old %q, err %v", old, err)
		}
		for _, p := range oldFiles(home) {
			if _, err := os.Lstat(p); err != nil {
				t.Errorf("a dry run removed %s", p)
			}
		}
		// The one call a dry run makes is the read-only probe.
		if got := callsOf(t, calls); TimerInstalledHere(home, "linux") || got != "--user is-enabled sh-agent-dream.timer\n" {
			t.Errorf("a dry run installed or changed the manager's state; calls:\n%s", got)
		}
	})

	t.Run("no old unit: nothing", func(t *testing.T) {
		home := timerHome(t)
		calls := fakeManager(t, "systemctl", 0)
		if old, _, err := ReplaceOldTimer(home, "/p", "linux", false); err != nil || old != "" {
			t.Fatalf("old %q, err %v", old, err)
		}
		if TimerInstalledHere(home, "linux") || callsOf(t, calls) != "" {
			t.Error("a home with no old unit got a timer")
		}
	})

	t.Run("new install refused: the old unit stays", func(t *testing.T) {
		home := timerHome(t)
		calls := fakeManager(t, "systemctl", 0)
		plant(t, home)
		write(t, linuxServicePath(home), "edited by hand\n", fresh)
		if _, _, err := ReplaceOldTimer(home, "/p", "linux", false); err == nil || !strings.Contains(err.Error(), "sh-agent-dream") {
			t.Fatalf("err %v, want a refusal naming the old unit it kept", err)
		}
		for _, p := range oldFiles(home) {
			if _, err := os.Lstat(p); err != nil {
				t.Errorf("the old unit was removed although the new one was refused: %s", p)
			}
		}
		if strings.Contains(callsOf(t, calls), "disable") {
			t.Error("the old unit was stopped although the new one was refused")
		}
	})

	t.Run("legacy bare name under a scratch home: untouched", func(t *testing.T) {
		timerHome(t)
		other := t.TempDir()
		write(t, filepath.Join(other, ".local", "bin", "fyr"), "#!/bin/sh\n", fresh)
		calls := fakeManager(t, "systemctl", 0)
		legacy := filepath.Join(other, ".config", "systemd", "user", oldUnitName+".timer")
		write(t, legacy, "x", fresh)
		if old, _, err := ReplaceOldTimer(other, "/p", "linux", false); err != nil || old != "" {
			t.Fatalf("old %q, err %v", old, err)
		}
		if _, err := os.Lstat(legacy); err != nil || callsOf(t, calls) != "" {
			t.Errorf("a legacy file was touched (%v) or the manager called", err)
		}
	})

	// The operator turned the old timer off and left its files: install
	// must not turn the nightly prompt to the model provider back on
	// (security review of v2.7.0..881389a, 2026-10-25).
	t.Run("old unit turned off: its files go, no fyr timer", func(t *testing.T) {
		for _, c := range []struct{ goos, manager, probe, answer string }{
			{"linux", "systemctl", "is-enabled", "disabled"},
			{"linux", "systemctl", "is-enabled", "masked"},
			{"darwin", "launchctl", "print", `Could not find service "com.styrman.sh-agent-dream" in domain for user gui: 501`},
		} {
			home := timerHome(t)
			calls := fakeManagerOff(t, c.manager, c.probe, c.answer)
			unit, plist := oldUnitFor(home)
			paths := oldFiles(home)
			if c.goos == "darwin" {
				paths = []string{filepath.Join(home, "Library", "LaunchAgents", plist+".plist")}
				unit = plist
			}
			for _, p := range paths {
				write(t, p, "x\n", fresh)
			}
			old, replaced, err := ReplaceOldTimer(home, "/p", c.goos, true)
			if err != nil || old != unit || replaced {
				t.Fatalf("%s dry run: old %q, replaced %v, err %v", c.goos, old, replaced, err)
			}
			for _, p := range paths {
				if _, err := os.Lstat(p); err != nil {
					t.Errorf("%s: a dry run removed %s", c.goos, p)
				}
			}
			old, replaced, err = ReplaceOldTimer(home, "/p", c.goos, false)
			if err != nil || old != unit || replaced {
				t.Fatalf("%s: old %q, replaced %v, err %v", c.goos, old, replaced, err)
			}
			gone(t, paths...)
			got := callsOf(t, calls)
			if TimerInstalledHere(home, c.goos) || strings.Contains(got, "enable --now") || strings.Contains(got, "bootstrap") {
				t.Errorf("%s: a turned-off timer was replaced by a running one; calls:\n%s", c.goos, got)
			}
		}
	})

	// A probe that fails without saying "off" (no user bus over SSH, a
	// container) cannot tell: the old unit is treated as on and replaced as
	// before the probe existed, never deleted as turned off (security
	// review round 2, 2026-10-25).
	t.Run("probe cannot tell: replaced as on", func(t *testing.T) {
		home := timerHome(t)
		fakeManagerOff(t, "systemctl", "is-enabled", "Failed to connect to bus: No medium found")
		plant(t, home)
		old, replaced, err := ReplaceOldTimer(home, "/p", "linux", false)
		if err != nil || old != "sh-agent-dream" || !replaced {
			t.Fatalf("old %q, replaced %v, err %v; want it replaced as on", old, replaced, err)
		}
		if !TimerInstalledHere(home, "linux") {
			t.Error("the fyr-dream unit is not installed")
		}
	})

	t.Run("darwin", func(t *testing.T) {
		home := timerHome(t)
		calls := fakeManager(t, "launchctl", 0)
		_, oldPlist := oldUnitFor(home)
		oldPath := filepath.Join(home, "Library", "LaunchAgents", oldPlist+".plist")
		write(t, oldPath, "<plist/>\n", fresh)
		old, _, err := ReplaceOldTimer(home, "/p", "darwin", false)
		if err != nil || old != "com.styrman.sh-agent-dream" {
			t.Fatalf("old %q, err %v", old, err)
		}
		gone(t, oldPath)
		if !TimerInstalledHere(home, "darwin") {
			t.Error("com.fyr.dream not installed")
		}
		got := callsOf(t, calls)
		if b, o := strings.Index(got, "bootstrap"), strings.Index(got, "bootout"); b < 0 || o < b || !strings.Contains(got[o:], oldPath) {
			t.Errorf("launchctl calls (bootstrap the new, then bootout the old):\n%s", got)
		}
	})
}
