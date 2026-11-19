package dream

// The hourly timer: R3's single background runner `fyr auto run` runs through
// Timer, which `fyr auto on` installs and `fyr auto off` or `uninstall` removes
// (R4, ADR-522). The former standalone dream timer (`fyr-dream` / `com.fyr.dream`)
// is retained here only to detect and remove legacy units on upgrade/uninstall.
// A timer is not in the install manifest: the next `install` would delete a
// listed unit as stale (plan D12).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
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

// dreamTimer is the legacy standalone dream timer, kept only for unit naming,
// detection, and cleanup via RemoveTimer / TimerInstalledHere / TimerInstalled.
var dreamTimer = Timer{
	Unit:             unitName,
	Plist:            plistName,
	Args:             []string{"dream", "--all"},
	Description:      "fyr dream: the hourly background dreamer, while an agy session runs (R3)",
	TimerDescription: "Run fyr dream hourly",
	Timeout:          "2h",
	InstallVerb:      "fyr auto on",
	RemoveVerb:       "fyr auto off",
	RemoveHint:       "fyr auto off",
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

// StatusLine is `fyr status`'s dream line.
func StatusLine(home string) string {
	timer := "merged into fyr auto"
	if TimerInstalled(home) {
		// Named with --home under a home that is not the real one: the bare
		// commands act on the home they default to (BUG-149's class).
		flag := ""
		if unit, _ := UnitFor(home); unit != unitName {
			flag = " --home " + shellQuote(home)
		}
		timer = "legacy dream timer installed: run fyr auto on" + flag + " or fyr auto off" + flag
	}
	st, err := readState(home)
	if err != nil {
		return "state unreadable: " + err.Error() + "; " + timer
	}
	r := st.LastRun
	switch {
	case r.At.IsZero():
		return "never run; " + timer
	case r.Error != "":
		return fmt.Sprintf("last run %s failed: %s; %s", r.At.Format("2006-01-02"), r.Error, timer)
	}
	return fmt.Sprintf("last run %s: %d projects, %d proposals; %s", r.At.Format("2006-01-02"), r.Projects, r.Proposals, timer)
}

// TimerInstalled reports whether a dream timer unit is on disk in home.
func TimerInstalled(home string) bool {
	for _, p := range []string{linuxTimerPath(home), macPlistPath(home)} {
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

func linuxTimerPath(home string) string { return dreamTimer.timerPath(home) }

func macPlistPath(home string) string { return dreamTimer.plistPath(home) }

// shellQuote puts a path in single quotes for a command the operator may
// paste: a space, a `$` or a quote in a home path must not change the command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
