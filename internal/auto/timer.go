package auto

import "github.com/kesokaj/fyr/internal/dream"

// unit is R3's second exception's timer, apart from the dream's: its own unit
// name, its own manager calls, and a lower priority than an operator's work
// (D14). launchd has no start timeout, so the 3 h limit holds on Linux only.
var unit = dream.Timer{
	Unit:             "fyr-auto",
	Plist:            "com.fyr.auto",
	Args:             []string{"auto", "run"},
	Description:      "fyr auto: the hourly idle-time run, while the operator has it on (R3)",
	TimerDescription: "Run fyr auto hourly",
	Timeout:          "3h",
	UnitLines:        []string{"ConditionACPower=true"},
	ServiceLines:     []string{"Nice=19", "IOSchedulingClass=idle"},
	TimerLines:       []string{"RandomizedDelaySec=10m"},
	PlistKeys: "  <key>ProcessType</key><string>Background</string>\n" +
		"  <key>LowPriorityIO</key><true/>\n" +
		"  <key>Nice</key><integer>15</integer>\n",
	InstallVerb: "fyr auto on",
	RemoveVerb:  "fyr auto off",
	RemoveHint:  "fyr auto off",
	StrictStop:  true,
}

// TimerInstalledHere reports whether this home holds auto's unit for goos.
func TimerInstalledHere(home, goos string) bool { return unit.InstalledHere(home, goos) }

// RemoveTimer removes auto's unit of this home and says whether there was one.
func RemoveTimer(home, goos string) (bool, error) { return unit.Remove(home, goos) }

// OtherSystemTimerFiles lists this home's units of the other operating
// system, which a removal here never touches.
func OtherSystemTimerFiles(home, goos string) []string { return unit.OtherSystemFiles(home, goos) }
