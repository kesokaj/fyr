package relay

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var validModelIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func isValidModelID(raw string) bool {
	return validModelIDRe.MatchString(strings.TrimSpace(raw))
}

// verifyPlanIntegrity verifies that after preserves Title, Status, and every
// line of before.Body verbatim except allowing `[ ]` -> `[x]`/`[X]` transitions
// strictly on target.OpenLines with identical task text.
func verifyPlanIntegrity(before, after ParsedPlan, target Phase) error {
	if after.Title != before.Title {
		return fmt.Errorf("plan %s title changed during relay", before.ID)
	}
	if after.Status != before.Status {
		return fmt.Errorf("plan %s status changed during relay (%q -> %q)", before.ID, before.Status, after.Status)
	}
	bLines := strings.Split(strings.ReplaceAll(before.Body, "\r\n", "\n"), "\n")
	aLines := strings.Split(strings.ReplaceAll(after.Body, "\r\n", "\n"), "\n")
	if len(bLines) != len(aLines) {
		return fmt.Errorf("plan %s line count changed during relay (%d -> %d)", before.ID, len(bLines), len(aLines))
	}
	allowed := make(map[int]bool, len(target.OpenLines))
	for _, ln := range target.OpenLines {
		allowed[ln] = true
	}
	for i := range bLines {
		if bLines[i] == aLines[i] {
			continue
		}
		if !allowed[i] || !isValidCheckboxTick(bLines[i], aLines[i]) {
			return fmt.Errorf("plan %s line %d modified outside allowed target checkbox ticks", before.ID, i+1)
		}
	}
	return nil
}

func isValidCheckboxTick(beforeLine, afterLine string) bool {
	for _, pair := range [][2]string{
		{"- [ ] ", "- [x] "}, {"- [ ] ", "- [X] "},
		{"* [ ] ", "* [x] "}, {"* [ ] ", "* [X] "},
	} {
		if pos := strings.Index(beforeLine, pair[0]); pos >= 0 && strings.TrimSpace(beforeLine[:pos]) == "" {
			want := beforeLine[:pos] + pair[1] + beforeLine[pos+len(pair[0]):]
			if afterLine == want {
				return true
			}
		}
	}
	return false
}

func checkLiveSessionHome(home, realHome string) error {
	h := strings.TrimSpace(home)
	if h == "" {
		return nil
	}
	rh := strings.TrimSpace(realHome)
	if rh == "" {
		var err error
		rh, err = os.UserHomeDir()
		if err != nil || strings.TrimSpace(rh) == "" {
			return refusef("cannot resolve host HOME to verify --home %q for live agy session", h)
		}
	}
	if sameCleanPath(h, rh) {
		return nil
	}
	return refusef("refusing live agy session with mismatched --home %q (host HOME is %q); use --dry-run for scratch homes or run installed fyr without --home", h, rh)
}

func sameCleanPath(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	if ra, err := filepath.EvalSymlinks(ca); err == nil {
		ca = filepath.Clean(ra)
	}
	if rb, err := filepath.EvalSymlinks(cb); err == nil {
		cb = filepath.Clean(rb)
	}
	return ca == cb
}
