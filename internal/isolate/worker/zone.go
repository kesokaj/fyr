package worker

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// zone is the lead's zone, the one its worker is given and its date check
// holds (workers D144).
type zone struct {
	name string
	loc  *time.Location
}

var zoneRe = regexp.MustCompile(`^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+)*$`)

const maxZone = 64

var (
	localtimeLink = "/etc/localtime"
	loadLocation  = time.LoadLocation
)

func leadZone() (zone, error) {
	if tz, ok := os.LookupEnv("TZ"); ok {
		if tz == "" {
			return zone{"UTC", time.UTC}, nil
		}
		if z, ok := asZone(tz); ok {
			return z, nil
		}
		return zone{}, fmt.Errorf("TZ=%.128q: not a zone name fyr can pass to the worker; set TZ=<Area/City>, or unset it", tz)
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

func runZone(_ string) (zone, error) {
	return leadZone()
}
