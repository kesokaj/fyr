package dream

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

// timerStale reports whether this home's unit for goos is on disk with text
// that is not this fyr's render for the home and the unit's own PATH: a
// unit an older fyr installed, the nightly one among them, which
// --install-timer keeps as it is (visible-dream plan D4, D13). A file that
// cannot be read says nothing.
func timerStale(home, goos string) bool {
	bin := filepath.Join(home, harness.BinPath)
	switch goos {
	case "linux":
		svc, svcErr := os.ReadFile(linuxServicePath(home))
		tmr, tmrErr := os.ReadFile(linuxTimerPath(home))
		service, timer := renderSystemd(bin, home, servicePATH(string(svc)))
		return (svcErr == nil && string(svc) != service) || (tmrErr == nil && string(tmr) != timer)
	case "darwin":
		data, err := os.ReadFile(macPlistPath(home))
		if err != nil {
			return false
		}
		_, label := UnitFor(home)
		return string(data) != renderLaunchd(label, bin, home, plistPATH(string(data)))
	}
	return false
}

// servicePATH is the PATH a rendered service unit froze, unescaped.
func servicePATH(unit string) string {
	for _, line := range strings.Split(unit, "\n") {
		if v, ok := strings.CutPrefix(line, `Environment="PATH=`); ok {
			return strings.ReplaceAll(strings.TrimSuffix(v, `"`), "%%", "%")
		}
	}
	return ""
}

// plistPATH is the PATH a rendered plist froze, unescaped.
func plistPATH(plist string) string {
	_, rest, ok := strings.Cut(plist, "<key>PATH</key><string>")
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(rest, "</string>")
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(v)
}
