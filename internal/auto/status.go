package auto

import (
	"fmt"
	"slices"
	"time"
)

func (o Options) timerSaid(c Config) string {
	installed := unit.InstalledHere(o.Home, o.goos())
	switch {
	case installed && c.Enabled:
		return "timer installed"
	case installed:
		return "timer installed, and nothing runs while auto is off"
	case c.Enabled:
		return "timer not installed, so nothing runs: run fyr auto on"
	}
	return "timer not installed"
}

// Status is `fyr auto status`. A corrupt config is exit 1, and so is an
// unreadable state: neither is rendered as "never run".
func Status(o Options) error {
	c, found, err := Load(o.Home)
	if err != nil {
		fmt.Fprintln(o.Out, configLine(err))
		return &ExitError{Code: 1}
	}
	if !found {
		c = defaults()
	}
	sw := "off"
	if c.Enabled {
		sw = "on"
	}
	fmt.Fprintf(o.Out, "auto       %s; %s\n", sw, o.timerSaid(c))
	fmt.Fprintf(o.Out, "level      %s; project idle %s, user idle %s\n", c.Level, c.ProjectIdle, c.UserIdle)
	st, err := usableState(o.Home, o.now())
	if err != nil {
		fmt.Fprintln(o.Out, "state      unreadable: "+oneLine(err.Error()))
		return &ExitError{Code: 1}
	}
	if st.LastRun.IsZero() {
		fmt.Fprintln(o.Out, "last run   never")
	} else {
		fmt.Fprintf(o.Out, "last run   %s: %s\n", st.LastRun.Format(time.RFC3339), oneLine(st.Verdict))
	}
	roots := make([]string, 0, len(st.Projects))
	for r := range st.Projects {
		roots = append(roots, r)
	}
	slices.Sort(roots)
	for _, r := range roots {
		fmt.Fprintf(o.Out, "project    %s: %s\n", oneLine(r), oneLine(st.Projects[r].Verdict))
	}
	fmt.Fprintf(o.Out, "today      %d of %d model runs\n", st.modelRunsToday(o.now()), modelRunsPerDay)
	if st.Budget != "" {
		fmt.Fprintln(o.Out, "quota      "+oneLine(st.Budget))
	}
	fmt.Fprintf(o.Out, "asks       %d open\n", st.OpenAsks)
	return nil
}

// StatusLine is `fyr status`'s auto line: it reads, and never fails the
// verb, whose exit status is about drift.
func StatusLine(o Options) string {
	c, found, err := Load(o.Home)
	switch {
	case err != nil:
		return "config unreadable: " + oneLine(err.Error()) + "; run fyr auto off or on"
	case !found || !c.Enabled:
		if unit.InstalledHere(o.Home, o.goos()) {
			return "off; timer installed, and nothing runs while auto is off"
		}
		return "off"
	}
	line := "on, level " + c.Level + "; " + o.timerSaid(c)
	st, err := usableState(o.Home, o.now())
	switch {
	case err != nil:
		return line + "; state unreadable: " + oneLine(err.Error())
	case st.LastRun.IsZero():
		return line + "; never run"
	}
	return line + "; last run " + st.LastRun.Format("2006-01-02")
}
