package dream

// The hourly timers: R3's first exception runs `fyr dream --all` from an OS
// user timer that `--install-timer` installs and `--remove-timer` or
// `uninstall` removes (R4); its second, `fyr auto run`, has its own unit
// through the same code (Timer). A timer is not in the install manifest: the
// next `install` would delete a listed unit as stale (plan D12).

import (
	"context"
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
	"time"
)

const (
	unitName  = "fyr-dream"
	plistName = "com.fyr.dream"
)

// Timer is one hourly job of `fyr`: what its unit runs and how it is told
// apart. The extras are appended to their section, so a timer with none
// renders the dream's text byte for byte. Every field is a constant of the
// code; none comes from the operator or a file.
type Timer struct {
	Unit, Plist      string   // the real home's unit and launchd label
	Args             []string // fyr's arguments, before --home
	Description      string   // the service's
	TimerDescription string
	Timeout          string   // TimeoutStartSec
	UnitLines        []string // extra lines of the service's [Unit]
	ServiceLines     []string // extra lines of [Service], before Environment=
	TimerLines       []string // extra lines of [Timer]
	PlistKeys        string   // extra plist keys, one line each, already XML
	InstallVerb      string   // the verb a refusal to install names
	RemoveVerb       string   // the verb a failed removal names
	RemoveHint       string   // what to run when a unit on disk has other text
	StrictStop       bool     // systemd: a failed stop call is an error, after the files go
}

// dreamTimer is R3's first exception.
var dreamTimer = Timer{
	Unit:             unitName,
	Plist:            plistName,
	Args:             []string{"dream", "--all"},
	Description:      "fyr dream: the hourly background dreamer, while an agy session runs (R3)",
	TimerDescription: "Run fyr dream hourly",
	Timeout:          "2h",
	InstallVerb:      "fyr dream --install-timer",
	RemoveVerb:       "fyr dream --remove-timer",
	RemoveHint:       "fyr dream --remove-timer",
}

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
func UnitFor(home string) (unit, plist string) { return dreamTimer.Names(home) }

// Names is the timer's unit and label for a home, by UnitFor's rule.
func (t Timer) Names(home string) (unit, plist string) { return namesFor(home, t.Unit, t.Plist) }

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

func linuxServicePath(home string) string { return dreamTimer.servicePath(home) }

func (t Timer) servicePath(home string) string {
	unit, _ := t.Names(home)
	return filepath.Join(home, ".config", "systemd", "user", unit+".service")
}

func (t Timer) timerPath(home string) string {
	unit, _ := t.Names(home)
	return filepath.Join(home, ".config", "systemd", "user", unit+".timer")
}

func (t Timer) plistPath(home string) string {
	_, plist := t.Names(home)
	return filepath.Join(home, "Library", "LaunchAgents", plist+".plist")
}

// unitLines is extra unit lines, each with its line break.
func unitLines(extra []string) string {
	if len(extra) == 0 {
		return ""
	}
	return strings.Join(extra, "\n") + "\n"
}

// renderSystemd returns the service and timer unit texts. PATH is the one of
// the shell that ran --install-timer: the user manager's own PATH lacks
// ~/.local/bin, where agy lives (cold read r1). A oneshot has no start
// timeout by default, and a Persistent= timer does not start a unit that is
// still active, so TimeoutStartSec ends a run hung past the per-project kill
// and a later hour runs (SD36; plan 2026-10-27-ledger D2). The run itself
// does nothing unless an agy session runs (ADR-073).
func renderSystemd(bin, home, path string) (string, string) {
	return dreamTimer.renderSystemd(bin, home, path)
}

func (t Timer) renderSystemd(bin, home, path string) (string, string) {
	// systemd expands %-specifiers inside quotes too; %% is a literal %.
	bin, home, path = strings.ReplaceAll(bin, "%", "%%"), strings.ReplaceAll(home, "%", "%%"), strings.ReplaceAll(path, "%", "%%")
	// ExecStart also expands $VAR and ${VAR}, inside quotes too; $$ is a
	// literal dollar sign. Environment= expands nothing, so path keeps its own.
	bin, home = strings.ReplaceAll(bin, "$", "$$"), strings.ReplaceAll(home, "$", "$$")
	service := fmt.Sprintf(`[Unit]
Description=%s
%s
[Service]
Type=oneshot
TimeoutStartSec=%s
%sEnvironment="PATH=%s"
ExecStart="%s" %s --home "%s"
`, t.Description, unitLines(t.UnitLines), t.Timeout, unitLines(t.ServiceLines), path, bin, strings.Join(t.Args, " "), home)
	timer := fmt.Sprintf(`[Unit]
Description=%s

[Timer]
OnCalendar=hourly
Persistent=true
%s
[Install]
WantedBy=timers.target
`, t.TimerDescription, unitLines(t.TimerLines))
	return service, timer
}

// renderLaunchd returns the launchd agent's plist.
func renderLaunchd(label, bin, home, path string) string {
	return dreamTimer.renderLaunchd(label, bin, home, path)
}

func (t Timer) renderLaunchd(label, bin, home, path string) string {
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
	var args string
	for _, a := range t.Args {
		args += "<string>" + x(a) + "</string>"
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string>%s<string>--home</string><string>%s</string></array>
  <key>EnvironmentVariables</key>
  <dict><key>PATH</key><string>%s</string></dict>
  <key>StartCalendarInterval</key>
  <dict><key>Minute</key><integer>0</integer></dict>
%s</dict>
</plist>
`, label, x(bin), args, x(home), x(path), t.PlistKeys)
}

// absolutePath keeps PATH's absolute entries: a relative one (".", "bin", an
// empty one) would resolve against the unit's working directory when the
// timer fires, not where the installing shell was (security review round 2).
func absolutePath(path string) string {
	var keep []string
	for _, e := range filepath.SplitList(path) {
		if filepath.IsAbs(e) {
			keep = append(keep, e)
		}
	}
	return strings.Join(keep, string(filepath.ListSeparator))
}

// RemoveTimer stops and removes the dream timer of this home, for goos, and
// reports whether there was one: absent is fine, but the caller says which it
// was (a removal that found nothing once printed "removed").
func RemoveTimer(home, goos string) (bool, error) { return dreamTimer.Remove(home, goos) }

// Remove is RemoveTimer for this timer.
func (t Timer) Remove(home, goos string) (bool, error) {
	unit, plist := t.Names(home)
	u, ok := unitOn(home, goos, unit, plist)
	// launchd's bootout fails on a job that is merely not loaded, so only
	// systemctl's failure says the manager kept the timer.
	u.verb, u.strict = t.RemoveVerb, t.StrictStop && u.manager == "systemctl"
	if !ok || !u.present() {
		return false, nil
	}
	if err := u.remove(); err != nil {
		return false, err
	}
	return true, nil
}

// timerUnit is one home's unit on one system: its files, its service
// manager, and the calls that stop it.
type timerUnit struct {
	files   []string
	manager string
	stop    [][]string
	verb    string // named by a failed removal
	strict  bool   // Timer.StrictStop
}

// managerLimit bounds one service manager call: a wedged user bus must not
// hang a removal, which for auto is its kill switch.
var managerLimit = 30 * time.Second

func (u timerUnit) call(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), managerLimit)
	defer cancel()
	return exec.CommandContext(ctx, u.manager, args...).Run()
}

func unitOn(home, goos, unit, plist string) (timerUnit, bool) {
	switch goos {
	case "linux":
		dir := filepath.Join(home, ".config", "systemd", "user")
		return timerUnit{
			files:   []string{filepath.Join(dir, unit+".timer"), filepath.Join(dir, unit+".service")},
			manager: "systemctl",
			stop:    [][]string{{"--user", "disable", "--now", unit + ".timer"}, {"--user", "daemon-reload"}},
		}, true
	case "darwin":
		p := filepath.Join(home, "Library", "LaunchAgents", plist+".plist")
		uid := "gui/" + strconv.Itoa(os.Getuid())
		return timerUnit{files: []string{p}, manager: "launchctl", stop: [][]string{{"bootout", uid, p}}}, true
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

func (u timerUnit) remove() error {
	manager, files, stop := u.manager, u.files, u.stop
	var stopErr error
	if _, err := exec.LookPath(manager); err == nil {
		// The first stop call may fail when the unit was never loaded; the
		// files are still removed, and the reload runs after.
		stopErr = u.call(stop[0])
	} else {
		stopErr = err
	}
	for _, p := range files {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return &ExitError{2, u.verb + ": " + err.Error()}
		}
	}
	if len(stop) > 1 {
		if _, err := exec.LookPath(manager); err == nil {
			u.call(stop[1])
		}
	}
	if stopErr != nil && u.strict {
		return &ExitError{2, fmt.Sprintf("%s: %s %s: %v; the unit files are gone, and the manager may still hold the timer: run %s %s from a login shell", u.verb, manager, strings.Join(stop[0], " "), stopErr, manager, strings.Join(stop[1], " "))}
	}
	return nil
}

// OtherSystemTimerFiles lists this home's unit files that belong to the
// operating system the binary is not running on: a home copied between a Mac
// and a Linux box holds them, TimerInstalled counts them, and RemoveTimer for
// goos never touches them. The caller names them, so "timer installed" in
// status and a removal that removed nothing do not contradict each other in
// silence.
func OtherSystemTimerFiles(home, goos string) []string {
	return dreamTimer.OtherSystemFiles(home, goos)
}

// OtherSystemFiles is OtherSystemTimerFiles for this timer.
func (t Timer) OtherSystemFiles(home, goos string) []string {
	var candidates []string
	switch goos {
	case "linux":
		candidates = []string{t.plistPath(home)}
	case "darwin":
		candidates = []string{t.servicePath(home), t.timerPath(home)}
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
func TimerInstalledHere(home, goos string) bool { return dreamTimer.InstalledHere(home, goos) }

// InstalledHere is TimerInstalledHere for this timer.
func (t Timer) InstalledHere(home, goos string) bool {
	var files []string
	switch goos {
	case "linux":
		files = []string{t.timerPath(home), t.servicePath(home)}
	case "darwin":
		files = []string{t.plistPath(home)}
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
