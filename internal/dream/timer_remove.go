package dream

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

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
	var reloadErr error
	if len(stop) > 1 {
		if _, err := exec.LookPath(manager); err == nil {
			reloadErr = u.call(stop[1])
		} else {
			reloadErr = err
		}
	}
	if stopErr != nil && u.strict {
		if reloadErr != nil {
			return &ExitError{2, fmt.Sprintf("%s: %s %s: %v (reload: %v); the unit files are gone, and the manager may still hold the timer: run %s %s from a login shell", u.verb, manager, strings.Join(stop[0], " "), stopErr, reloadErr, manager, strings.Join(stop[1], " "))}
		}
		return &ExitError{2, fmt.Sprintf("%s: %s %s: %v; the unit files are gone, and the manager may still hold the timer: run %s %s from a login shell", u.verb, manager, strings.Join(stop[0], " "), stopErr, manager, strings.Join(stop[1], " "))}
	}
	if reloadErr != nil && u.strict {
		return &ExitError{2, fmt.Sprintf("%s: %s %s: %v; the unit files are gone, and the manager may still hold the timer: run %s %s from a login shell", u.verb, manager, strings.Join(stop[1], " "), reloadErr, manager, strings.Join(stop[1], " "))}
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
