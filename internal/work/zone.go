package work

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// zone is the lead's zone, the one its worker is given and its date check
// holds (workers D144): name is the worker's TZ, "" when the lead has none
// to pass, and loc the zone the window's days are read in.
type zone struct {
	name string
	loc  *time.Location
}

// zoneRe is an IANA zone name: no leading '/' (a path), no ':' (glibc's
// form), no '.' (so no '..'), no ',' (a POSIX rule string), space or '='.
var zoneRe = regexp.MustCompile(`^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+)*$`)

// maxZone caps a name before it is loaded or put in docker's argv.
const maxZone = 64

// localtimeLink is the machine's zone link; a test points it at its own.
var localtimeLink = "/etc/localtime"

// loadLocation is time.LoadLocation; a test replaces it to reach the
// checks made before it.
var loadLocation = time.LoadLocation

// leadZone reads the lead's zone, in § Interfaces § The worker's zone's
// order: TZ set and non-empty, which must be a zone name (refusal 22); TZ
// set and empty, UTC, as Go and glibc read it; else the name after the
// last zoneinfo/ in localtimeLink's target; else no name and time.Local,
// whose /etc/localtime a host worker reads too.
func leadZone() (zone, error) {
	if tz, ok := os.LookupEnv("TZ"); ok {
		if tz == "" {
			return zone{"UTC", time.UTC}, nil
		}
		if z, ok := asZone(tz); ok {
			return z, nil
		}
		return zone{}, fmt.Errorf("TZ=%.128q: not a zone name fyr work can pass to the worker; set TZ=<Area/City>, or unset it", tz)
	}
	if target, err := os.Readlink(localtimeLink); err == nil {
		if i := strings.LastIndex(target, "zoneinfo/"); i >= 0 {
			if z, ok := asZone(target[i+len("zoneinfo/"):]); ok {
				return z, nil
			}
		}
	}
	return zone{"", time.Local}, nil
}

// asZone is s as a zone when it is a name a worker's own tzdata could share:
// Local and localtime name whatever the host's /etc/localtime is, and a name
// Go cannot load the lead reads as UTC while the worker's tools may not.
func asZone(s string) (zone, bool) {
	if len(s) > maxZone || !zoneRe.MatchString(s) || s == "Local" || s == "localtime" {
		return zone{}, false
	}
	loc, err := loadLocation(s)
	if err != nil {
		return zone{}, false
	}
	return zone{s, loc}, true
}

// runZone is refusal 22: leadZone, and with --runner docker a name, since a
// container has no /etc/localtime of the lead's to read.
func runZone(runner string) (zone, error) {
	z, err := leadZone()
	if err == nil && z.name == "" && runner == RunnerDocker {
		err = errors.New("--runner docker: the worker needs a zone name, and TZ is unset and /etc/localtime names none; set TZ=<Area/City>")
	}
	return z, err
}
