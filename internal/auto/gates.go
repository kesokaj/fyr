package auto

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// gatePower is G3: why this machine is not on AC, or "". A supply it cannot
// read fails closed; a box with no supplies counts as AC. A supply of scope
// Device is a mouse's or a headset's battery, never the machine's.
func gatePower(env Env, goos string) string {
	switch goos {
	case "linux":
		supplies, err := env.Supplies()
		if err != nil {
			return "G3 power: unreadable: " + oneLine(err.Error())
		}
		battery, online := false, false
		for _, s := range supplies {
			switch {
			case s.Scope == "Device":
			case s.Type == "Battery":
				battery = true
			case s.Online == "1":
				online = true
			}
		}
		if battery && !online {
			return "G3 power: on battery"
		}
		return ""
	case "darwin":
		batt, err := env.Pmset("-g", "batt")
		if err != nil {
			return "G3 power: unreadable: " + oneLine(err.Error())
		}
		if !strings.Contains(batt, "'AC Power'") {
			return "G3 power: not on AC power"
		}
		all, err := env.Pmset("-g")
		if err != nil {
			return "G3 power: unreadable: " + oneLine(err.Error())
		}
		if lowPower.MatchString(all) {
			return "G3 power: low power mode"
		}
		return ""
	}
	return "G3 power: no reading on " + goos
}

var lowPower = regexp.MustCompile(`(?m)^\s*lowpowermode\s+1\s*$`)

// gatePresence is G4: stop says the operator is at the machine; unknown says
// no signal could be read at all, which row 5 lets through, logged. A reading
// that fails is no signal and never a stop (R3); a signal whose idle time
// cannot be parsed counts as present.
func gatePresence(env Env, goos string, uid int, userIdle time.Duration, now time.Time) (stop string, unknown bool) {
	switch goos {
	case "linux":
		return presenceLinux(env, uid, userIdle, now)
	case "darwin":
		return presenceDarwin(env, userIdle)
	}
	return "", true
}

func presenceLinux(env Env, uid int, userIdle time.Duration, now time.Time) (string, bool) {
	signal := false
	sessions, _ := env.Sessions(uid)
	for _, s := range sessions {
		if s["Type"] != "x11" && s["Type"] != "wayland" {
			continue
		}
		signal = true
		if s["Active"] == "yes" && s["LockedHint"] == "no" && !idleFor(s, userIdle, now) {
			return "G4 operator: a " + s["Type"] + " session is in use", false
		}
	}
	ptys, _ := env.PTYs(uid)
	for _, at := range ptys {
		signal = true
		if now.Sub(at) < userIdle {
			return "G4 operator: a terminal was used " + at.UTC().Format(time.RFC3339), false
		}
	}
	return "", !signal
}

// idleFor is a session that says it has been idle for at least d.
func idleFor(s map[string]string, d time.Duration, now time.Time) bool {
	if s["IdleHint"] != "yes" {
		return false
	}
	us, err := strconv.ParseInt(s["IdleSinceHint"], 10, 64)
	if err != nil || us <= 0 {
		return false
	}
	return now.Sub(time.UnixMicro(us)) >= d
}

var (
	hidIdle      = regexp.MustCompile(`"HIDIdleTime"\s*=\s*(\d+)`)
	screenLocked = regexp.MustCompile(`"CGSSessionScreenIsLocked"\s*=\s*Yes`)
)

func presenceDarwin(env Env, userIdle time.Duration) (string, bool) {
	hid, err := env.Ioreg("-c", "IOHIDSystem")
	if err != nil {
		return "", true
	}
	matches := hidIdle.FindAllStringSubmatch(hid, -1)
	if matches == nil {
		return "", true
	}
	idle := time.Duration(1<<63 - 1)
	for _, m := range matches {
		ns, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			ns = 0 // an idle time that cannot be parsed counts as present
		}
		idle = min(idle, time.Duration(ns))
	}
	if idle >= userIdle {
		return "", false
	}
	// An unreadable lock state is not a locked screen: toward not running.
	if root, err := env.Ioreg("-n", "Root", "-d1"); err == nil && screenLocked.MatchString(root) {
		return "", false
	}
	return fmt.Sprintf("G4 operator: input %s ago", idle.Round(time.Second)), false
}

// gateSession is G5. The dream's check errs toward no session, the unsafe
// direction here, so an error from it stops the run; what it cannot see at
// all, R3 says it can miss.
func gateSession(env Env, goos string, uid int) string {
	found, err := env.AgySession(goos, uid)
	switch {
	case err != nil:
		return "G5 agy session: unreadable: " + oneLine(err.Error())
	case found:
		return "G5 agy session: an interactive agy is running"
	}
	return ""
}

// machineStop runs G3, G4 and G5 in order. A pass with no presence signal
// says so through say.
func (r *run) machineStop() string {
	if stop := gatePower(r.env, r.o.goos()); stop != "" {
		return stop
	}
	idle, err := span(r.cfg.UserIdle)
	if err != nil {
		return "G4 operator: " + oneLine(err.Error())
	}
	stop, unknown := gatePresence(r.env, r.o.goos(), os.Getuid(), idle, r.o.now())
	if stop != "" {
		return stop
	}
	if unknown && !r.unknownSaid {
		r.unknownSaid = true
		r.say("user idle: unknown")
	}
	return gateSession(r.env, r.o.goos(), os.Getuid())
}
