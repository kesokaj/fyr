package auto

import (
	"regexp"
	"strings"
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

// machineStop runs G3 (AC power). G4 (operator presence) and G5 (active agy
// session) are retired (ADR-524).
func (r *run) machineStop() string {
	return gatePower(r.env, r.o.goos())
}
