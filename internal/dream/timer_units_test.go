package dream

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

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
