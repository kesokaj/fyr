package dream

// The nightly timer: R3's exception runs `fyr dream --all` from an OS
// user timer that `--install-timer` installs and `--remove-timer` or
// `uninstall` removes (R4). The timer is not in the install manifest: the
// next `install` would delete a listed unit as stale (plan D12).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	unitName  = "fyr-dream"
	plistName = "com.fyr.dream"
	// The names before the rename (plan fyr D13): install replaces this
	// home's unit under them (ReplaceOldTimer), and their bare form under a
	// home that is not the real one is what v2.0.0 or earlier wrote there
	// (LegacyTimerFiles).
	oldUnitName  = "sh-agent-dream"
	oldPlistName = "com.styrman.sh-agent-dream"
)

// userHomeDir is the real home; a test seam, since the tests' homes are
// temporary directories that must count as the real one.
var userHomeDir = accountHome

// accountHome is the home of the account the process runs as, from the
// account database and never from HOME: the user service manager belongs to
// the account, and a sandbox run overrides HOME (the testing guide's kits
// do), so HOME as the yardstick let a sandbox --remove-timer or uninstall
// reach the operator's real timer (BUG-149). An account the database does
// not list has no known home, and then no home gets the bare names:
// user.Current would answer with HOME there without saying so (os/user
// without cgo reads only /etc/passwd on Linux, so LDAP, SSSD and
// systemd-homed accounts and a container's bare uid are not listed; on
// macOS it asks the system's own resolver, with or without cgo).
func accountHome() (string, error) {
	u, err := lookupAccount()
	if err != nil {
		return "", err
	}
	if u.HomeDir == "" {
		return "", errors.New("the account has no home directory")
	}
	return u.HomeDir, nil
}

// lookupAccount is the account database lookup by uid; a test seam.
// LookupId, not Current: Current falls back to HOME when the lookup fails.
var lookupAccount = func() (*user.User, error) { return user.LookupId(strconv.Itoa(os.Getuid())) }

// UnitFor names the systemd unit and the launchd label for a home. The real
// home gets the bare names; any other home gets them suffixed with a short
// hash of its path, so a scratch `--install-timer`, `--remove-timer` or
// `uninstall` never touches the operator's own timer through the shared user
// service manager (R6; security review of the v2.0.0 gate).
func UnitFor(home string) (unit, plist string) { return namesFor(home, unitName, plistName) }

// oldUnitFor is UnitFor under the names before the rename, with the same
// per-home suffix: what v2.0.1 to v2.7.0 installed for this home.
func oldUnitFor(home string) (unit, plist string) {
	return namesFor(home, oldUnitName, oldPlistName)
}

func namesFor(home, unit, plist string) (string, string) {
	if real, err := userHomeDir(); err == nil {
		if filepath.Clean(real) == filepath.Clean(home) {
			return unit, plist
		}
		// A symlinked spelling of the real home is the real home (review 2026-10-01).
		if ri, err := os.Stat(real); err == nil {
			if hi, err := os.Stat(home); err == nil && os.SameFile(ri, hi) {
				return unit, plist
			}
		}
	}
	sum := sha256.Sum256([]byte(filepath.Clean(home)))
	suffix := "-" + hex.EncodeToString(sum[:4])
	return unit + suffix, plist + suffix
}

func linuxServicePath(home string) string {
	unit, _ := UnitFor(home)
	return filepath.Join(home, ".config", "systemd", "user", unit+".service")
}

// renderSystemd returns the service and timer unit texts. PATH is the one of
// the shell that ran --install-timer: the user manager's own PATH lacks
// ~/.local/bin, where agy lives (cold read r1).
func renderSystemd(bin, home, path string) (string, string) {
	// systemd expands %-specifiers inside quotes too; %% is a literal %.
	bin, home, path = strings.ReplaceAll(bin, "%", "%%"), strings.ReplaceAll(home, "%", "%%"), strings.ReplaceAll(path, "%", "%%")
	// ExecStart also expands $VAR and ${VAR}, inside quotes too; $$ is a
	// literal dollar sign. Environment= expands nothing, so path keeps its own.
	bin, home = strings.ReplaceAll(bin, "$", "$$"), strings.ReplaceAll(home, "$", "$$")
	service := fmt.Sprintf(`[Unit]
Description=fyr dream: the nightly background dreamer (R3)

[Service]
Type=oneshot
Environment="PATH=%s"
ExecStart="%s" dream --all --home "%s"
`, path, bin, home)
	timer := `[Unit]
Description=Run fyr dream nightly

[Timer]
OnCalendar=*-*-* 03:00
Persistent=true

[Install]
WantedBy=timers.target
`
	return service, timer
}

// renderLaunchd returns the launchd agent's plist.
func renderLaunchd(label, bin, home, path string) string {
	x := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			switch r {
			case '&':
				b.WriteString("&amp;")
			case '<':
				b.WriteString("&lt;")
			case '>':
				b.WriteString("&gt;")
			default:
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string><string>dream</string><string>--all</string><string>--home</string><string>%s</string></array>
  <key>EnvironmentVariables</key>
  <dict><key>PATH</key><string>%s</string></dict>
  <key>StartCalendarInterval</key>
  <dict><key>Hour</key><integer>3</integer><key>Minute</key><integer>0</integer></dict>
</dict>
</plist>
`, label, x(bin), x(home), x(path))
}

// absolutePath keeps PATH's absolute entries: a relative one (".", "bin", an
// empty one) would resolve against the unit's working directory at 03:00,
// not where the installing shell was (security review round 2).
func absolutePath(path string) string {
	var keep []string
	for _, e := range filepath.SplitList(path) {
		if filepath.IsAbs(e) {
			keep = append(keep, e)
		}
	}
	return strings.Join(keep, string(filepath.ListSeparator))
}

// RemoveTimer stops and removes the dream timer of this home, for goos, under
// the fyr name and under the name before the rename (OldTimerHere: a home
// that never ran install since keeps that one, and uninstall or
// --remove-timer must not leave it running; TR9), and reports whether there
// was one: absent is fine, but the caller says which it was (a removal that
// found nothing once printed "removed").
func RemoveTimer(home, goos string) (bool, error) {
	removed := false
	for _, namesOf := range []func(string) (string, string){UnitFor, oldUnitFor} {
		unit, plist := namesOf(home)
		u, ok := unitOn(home, goos, unit, plist)
		if !ok || !u.present() {
			continue
		}
		if err := u.remove(); err != nil {
			return removed, err
		}
		removed = true
	}
	return removed, nil
}

// OldTimerHere names this home's unit under the name before the rename
// (oldUnitFor) when its files are on disk for goos, and "" otherwise: what
// RemoveTimer removes beside the fyr unit. The bare old name under a home
// that is not the real one is LegacyTimerFiles', never this one's.
func OldTimerHere(home, goos string) string {
	unit, plist := oldUnitFor(home)
	u, ok := unitOn(home, goos, unit, plist)
	if !ok || !u.present() {
		return ""
	}
	if goos == "darwin" {
		return plist
	}
	return unit
}

// timerUnit is one home's unit on one system under one name: its files, its
// service manager, the calls that stop it, the read-only call that asks the
// manager about it, and the answers to that call that mean it is turned off.
type timerUnit struct {
	files   []string
	manager string
	stop    [][]string
	probe   []string
	off     []string
}

func unitOn(home, goos, unit, plist string) (timerUnit, bool) {
	switch goos {
	case "linux":
		dir := filepath.Join(home, ".config", "systemd", "user")
		return timerUnit{
			files:   []string{filepath.Join(dir, unit+".timer"), filepath.Join(dir, unit+".service")},
			manager: "systemctl",
			stop:    [][]string{{"--user", "disable", "--now", unit + ".timer"}, {"--user", "daemon-reload"}},
			probe:   []string{"--user", "is-enabled", unit + ".timer"},
			off:     []string{"disabled", "masked"},
		}, true
	case "darwin":
		p := filepath.Join(home, "Library", "LaunchAgents", plist+".plist")
		uid := "gui/" + strconv.Itoa(os.Getuid())
		return timerUnit{files: []string{p}, manager: "launchctl", stop: [][]string{{"bootout", uid, p}}, probe: []string{"print", uid + "/" + plist}, off: []string{"Could not find service"}}, true
	}
	return timerUnit{}, false
}

func (u timerUnit) present() bool {
	for _, p := range u.files {
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

// on reports whether the unit may be turned on: false only when the manager
// says it is off. A manager that is missing, or a probe that fails any other
// way (no user bus over SSH, a container), cannot tell, and the unit counts
// as on: the replacement then runs as it did before the probe, and a refusal
// keeps the old unit as it was (security review round 2, 2026-10-25).
func (u timerUnit) on() bool {
	if _, err := exec.LookPath(u.manager); err != nil {
		return true
	}
	out, err := exec.Command(u.manager, u.probe...).CombinedOutput()
	if err == nil {
		return true
	}
	for _, off := range u.off {
		if strings.Contains(string(out), off) {
			return false
		}
	}
	return true
}

func (u timerUnit) remove() error {
	manager, files, stop := u.manager, u.files, u.stop
	if _, err := exec.LookPath(manager); err == nil {
		// The first stop call may fail when the unit was never loaded; the
		// files are still removed, and the reload runs after.
		exec.Command(manager, stop[0]...).Run()
	}
	for _, p := range files {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return &ExitError{2, "fyr dream --remove-timer: " + err.Error()}
		}
	}
	if len(stop) > 1 {
		if _, err := exec.LookPath(manager); err == nil {
			exec.Command(manager, stop[1]...).Run()
		}
	}
	return nil
}

// ReplaceOldTimer is install's half of the rename for the dream timer: when
// this home has its unit under the old name, the fyr unit is installed with
// path first, and only then is the old one stopped and removed, so a refused
// install keeps the old timer as it was. An old unit the operator turned off
// (files left, not enabled or not loaded) only has its files removed: no fyr
// timer is turned on in its place, since the nightly run sends redacted
// project content to the model provider (security review 2026-10-25). It
// returns the old unit's name, "" when there is none, and whether a fyr
// timer replaced it; a dry run finds it, asks the manager, and changes
// nothing. A bare old name under a home that is not the real one is not this
// home's (LegacyTimerFiles) and is never touched here.
func ReplaceOldTimer(home, path, goos string, dryRun bool) (name string, replaced bool, err error) {
	unit, plist := oldUnitFor(home)
	old, ok := unitOn(home, goos, unit, plist)
	if !ok || !old.present() {
		return "", false, nil
	}
	name = unit
	if goos == "darwin" {
		name = plist
	}
	if !old.on() {
		if dryRun {
			return name, false, nil
		}
		return name, false, old.remove()
	}
	if dryRun {
		return name, true, nil
	}
	if err := InstallTimer(home, path, goos); err != nil {
		return name, false, fmt.Errorf("the dream timer %s was kept, because its fyr replacement was refused: %w", name, err)
	}
	return name, true, old.remove()
}

// OtherSystemTimerFiles lists this home's unit files that belong to the
// operating system the binary is not running on: a home copied between a Mac
// and a Linux box holds them, TimerInstalled counts them, and RemoveTimer for
// goos never touches them. The caller names them, so "timer installed" in
// status and a removal that removed nothing do not contradict each other in
// silence.
func OtherSystemTimerFiles(home, goos string) []string {
	var candidates []string
	switch goos {
	case "linux":
		candidates = []string{macPlistPath(home)}
	case "darwin":
		candidates = []string{linuxServicePath(home), linuxTimerPath(home)}
	}
	var found []string
	for _, p := range candidates {
		if _, err := os.Lstat(p); err == nil {
			found = append(found, p)
		}
	}
	return found
}

// TimerInstalledHere reports whether a unit of this home, for goos, is on
// disk: what RemoveTimer(home, goos) would remove. TimerInstalled counts the
// other system's files too, which a removal here never touches.
func TimerInstalledHere(home, goos string) bool {
	var files []string
	switch goos {
	case "linux":
		files = []string{linuxTimerPath(home), linuxServicePath(home)}
	case "darwin":
		files = []string{macPlistPath(home)}
	}
	for _, p := range files {
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

// shellQuote puts a path in single quotes for a command the operator may
// paste: a space, a `$` or a quote in a home path must not change the command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// LegacyTimerFiles lists the bare-named unit files under a home whose own
// unit name is suffixed: what v2.0.0 or earlier wrote there before names were
// per home (BUG-144), or what a home of an account the database does not
// list holds since BUG-149. They are not this home's unit any more, so
// install, remove and the installed check never see them; this is how the
// operator still learns of them. The real home's bare-named unit is its own.
func LegacyTimerFiles(home string) []string {
	if unit, _ := UnitFor(home); unit == unitName {
		return nil
	}
	// The bare names v2.0.0 or earlier wrote: no release has written a bare
	// fyr name under a home that is not the real one.
	real, realErr := userHomeDir()
	var found []string
	for _, rel := range []string{
		filepath.Join(".config", "systemd", "user", oldUnitName+".service"),
		filepath.Join(".config", "systemd", "user", oldUnitName+".timer"),
		filepath.Join("Library", "LaunchAgents", oldPlistName+".plist"),
	} {
		p := filepath.Join(home, rel)
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		// A home that shares a directory with the real one through a link
		// (a scratch home whose .config points at the real .config) sees the
		// real home's own unit here: that file is not legacy, and the notice
		// must never tell the operator to delete it.
		if realErr == nil {
			if ri, err := os.Stat(filepath.Join(real, rel)); err == nil {
				if hi, err := os.Stat(p); err == nil && os.SameFile(ri, hi) {
					continue
				}
			}
		}
		found = append(found, p)
	}
	return found
}

// LegacyTimerNotice is what remove, uninstall and status tell the operator
// about LegacyTimerFiles: the paths and the steps. Nothing here stops or
// deletes them, because the service manager is shared by every home of the
// account and a stop by name ends whichever unit it loaded, which may be
// the real home's.
func LegacyTimerNotice(home, goos string) string {
	files := LegacyTimerFiles(home)
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "legacy dream timer files under %s, written by v2.0.0 or earlier under the name every home shared:\n", home)
	for _, f := range files {
		fmt.Fprintf(&b, "  %s\n", f)
	}
	b.WriteString("They are not removed or stopped here: a stop by name ends whichever unit the service manager loaded, which may be another home's.\n")
	if goos == "darwin" {
		uid := strconv.Itoa(os.Getuid())
		fmt.Fprintf(&b, "Check which file is loaded: launchctl print gui/%s/%s (its path line)\n", uid, oldPlistName)
		fmt.Fprintf(&b, "If it is one of the files above: launchctl bootout gui/%s <that plist>\n", uid)
		b.WriteString("Then delete the files above.\n")
	} else {
		fmt.Fprintf(&b, "Check which file is loaded: systemctl --user show -p FragmentPath %s.timer\n", oldUnitName)
		fmt.Fprintf(&b, "If it is one of the files above: systemctl --user disable --now %s.timer\n", oldUnitName)
		b.WriteString("Then delete the files above and run: systemctl --user daemon-reload\n")
	}
	fmt.Fprintf(&b, "If this home should keep a nightly timer, install it again afterwards: fyr dream --install-timer --home %s", shellQuote(home))
	return b.String()
}
