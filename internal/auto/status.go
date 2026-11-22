package auto

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
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
	fmt.Fprintf(o.Out, "mode       stale after %s; scope all conversations, projects & harness\n", c.ConversationIdle)
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
	fmt.Fprintf(o.Out, "asks       %d open\n", reconcileOpenAsks(o.Home, o.Now, st.OpenAsks))
	if banner := installPendingBanner(o, st); banner != "" {
		fmt.Fprintln(o.Out, banner)
	}
	return nil
}

func installPendingBanner(o Options, st State) string {
	pending, sha := st.InstallPending, strings.TrimSpace(st.InstallPendingSHA)
	if !pending {
		if dig, err := metrics.ReadLatestDailyDigest(o.Home); err == nil && dig != nil {
			for _, a := range dig.Actions {
				if a.InstallPending {
					pending = true
					if sha == "" {
						sha = strings.TrimSpace(a.AppliedSHA)
					}
					break
				}
			}
		}
	}
	if !pending {
		return ""
	}
	if sha != "" {
		if hst, found, err := harness.LoadState(o.Home); err == nil && found && hst.Commit != "" &&
			(hst.Commit == sha || strings.HasPrefix(hst.Commit, sha) || strings.HasPrefix(sha, hst.Commit)) {
			return ""
		}
		return fmt.Sprintf("install    pending (skills/ updated at %s; run 'fyr install' to deploy)", oneLine(sha))
	}
	return "install    pending (skills/ updated; run 'fyr install' to deploy)"
}

func isAutoAskTitle(title string) bool {
	return strings.HasPrefix(title, "auto ask:") || strings.HasPrefix(title, "Auto, ")
}

func reconcileOpenAsks(home string, now func() time.Time, current int) int {
	if _, err := os.Lstat(filepath.Join(home, harness.StateDir, "brain.db")); err != nil {
		return current
	}
	opts := brain.Options{Home: home, Global: true, Now: now}
	all, err := brain.List(opts, brain.ListFilter{Kind: "proposal"})
	if err != nil {
		return current
	}
	hasAuto := false
	for _, rec := range all {
		if isAutoAskTitle(rec.Title) {
			hasAuto = true
			break
		}
	}
	if !hasAuto {
		return current
	}
	open, err := brain.List(opts, brain.ListFilter{Kind: "proposal", Open: true})
	if err != nil {
		return current
	}
	n := 0
	for _, rec := range open {
		if isAutoAskTitle(rec.Title) {
			n++
		}
	}
	return min(current, n)
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
	line := "on, stale after " + c.ConversationIdle + "; " + o.timerSaid(c)
	st, err := usableState(o.Home, o.now())
	switch {
	case err != nil:
		return line + "; state unreadable: " + oneLine(err.Error())
	case st.LastRun.IsZero():
		return line + "; never run"
	}
	return line + "; last run " + st.LastRun.Format("2006-01-02")
}
