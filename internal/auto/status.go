package auto

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/isolate"
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
	pending, sha := resolveInstallPending(o.Home, st)
	if !pending {
		return ""
	}
	if sha == "" {
		return "install    pending (skills/ updated; run 'fyr install' to deploy)"
	}
	if installedMatchesSHA(o, sha) {
		return ""
	}
	return fmt.Sprintf("install    pending (skills/ updated at %s; run 'fyr install' to deploy)", oneLine(sha))
}

func resolveInstallPending(home string, st State) (bool, string) {
	sha := strings.TrimSpace(st.InstallPendingSHA)
	if st.InstallPending {
		return true, sha
	}
	dig, err := metrics.ReadLatestDailyDigest(home)
	if err != nil || dig == nil {
		return false, ""
	}
	for _, a := range dig.Actions {
		if !a.InstallPending {
			continue
		}
		if sha == "" {
			sha = strings.TrimSpace(a.AppliedSHA)
		}
		return true, sha
	}
	return false, ""
}

func installedMatchesSHA(o Options, sha string) bool {
	commit := o.resolvedPorts().installedCommit(o.Home)
	if commit == "" {
		return false
	}
	return commit == sha || strings.HasPrefix(commit, sha) || strings.HasPrefix(sha, commit)
}

func isAutoAskTitle(title string) bool {
	return strings.HasPrefix(title, "auto ask:") || strings.HasPrefix(title, "Auto, ")
}

func reconcileOpenAsks(home string, now func() time.Time, current int) int {
	if _, err := os.Lstat(isolate.Root(home, "brain.db")); err != nil {
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

const defaultLogLines = 50

// Log reads <home>/.config/fyr/auto/auto.log through O_NOFOLLOW (refusing
// symlinks) and writes the requested tail (default 50 lines, lines when > 0,
// or all lines when all is true) to o.Out. When auto.log does not exist, it
// prints "no auto log yet\n" and exits 0 (ADR-528, D5).
func Log(o Options, lines int, all bool) error {
	if lines < 0 {
		return &ExitError{2, "fyr auto log: --lines must be above 0"}
	}
	if all && lines > 0 {
		return &ExitError{2, "fyr auto log: --lines and --all exclude each other"}
	}
	data, found, err := readAutoLogNoFollow(o.Home)
	if err != nil {
		return err
	}
	if !found {
		fmt.Fprint(o.Out, "no auto log yet\n")
		return nil
	}
	if len(data) == 0 {
		return nil
	}
	entries := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if !all {
		n := defaultLogLines
		if lines > 0 {
			n = lines
		}
		if len(entries) > n {
			entries = entries[len(entries)-n:]
		}
	}
	for _, entry := range entries {
		fmt.Fprintln(o.Out, oneLine(entry))
	}
	return nil
}

func readAutoLogNoFollow(home string) ([]byte, bool, error) {
	d := dir(home)
	if _, lerr := os.Lstat(d); errors.Is(lerr, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err := checkDir(d); err != nil {
		return nil, false, &ExitError{1, "fyr auto log: " + err.Error()}
	}
	path := filepath.Join(d, "auto.log")
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK) {
		return nil, false, &ExitError{1, fmt.Sprintf("fyr auto log: %s is a link", path)}
	}
	if err != nil {
		return nil, false, &ExitError{1, "fyr auto log: " + err.Error()}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, false, &ExitError{1, fmt.Sprintf("fyr auto log: %s is not a regular file", path)}
	}
	data, err := io.ReadAll(io.LimitReader(f, logLimit+1))
	if err != nil {
		return nil, false, &ExitError{1, "fyr auto log: " + err.Error()}
	}
	if int64(len(data)) > logLimit {
		return nil, false, &ExitError{1, fmt.Sprintf("fyr auto log: %s is over %d bytes", path, logLimit)}
	}
	return data, true, nil
}
