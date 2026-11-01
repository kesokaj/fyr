package auto

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/check"
)

// ExitError is a verb's exit status with the text it prints on stdout, as
// eval's refusals do.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// Options is what every verb runs with; the zero Now, GOOS, Path and Env are
// the machine's own. Vocabulary, Install and Metrics are the checks only the
// binary can answer; `cmd/fyr` gives them, and a nil one is a check not run.
type Options struct {
	Home       string
	Out        io.Writer
	Now        func() time.Time
	GOOS       string
	Path       string
	Env        Env
	Vocabulary check.Vocabulary
	Install    func() []Finding
	Metrics    func() ([]Finding, error)
	// ReadLimit replaces every read's time limit; a test shortens it.
	ReadLimit time.Duration
}

func (o Options) limit(d time.Duration) time.Duration {
	if o.ReadLimit > 0 {
		return o.ReadLimit
	}
	return d
}

func (o Options) env() Env {
	if o.Env != nil {
		return o.Env
	}
	return machine{}
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o Options) goos() string {
	if o.GOOS != "" {
		return o.GOOS
	}
	return runtime.GOOS
}

func (o Options) path() string {
	if o.Path != "" {
		return o.Path
	}
	return os.Getenv("PATH")
}

// Request is `on`'s flags; an empty field was not given. The verb has
// refused a flag given empty, and a value out of range, before this.
type Request struct {
	Level, ProjectIdle, UserIdle string
	// CheckHarness answers whether level harness can run here: a harness
	// checkout has somewhere to commit, an embedded source or a plain copy
	// has not.
	CheckHarness func() error
}

// On turns auto on. The config says on last, so no run sees "on" before the
// timer exists, and any refusal leaves it off (R3).
func On(o Options, r Request) error {
	prev, _, err := Load(o.Home)
	if fi, lerr := os.Lstat(ConfigPath(o.Home)); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		return &ExitError{2, "fyr auto on: " + ConfigPath(o.Home) + " is a link; refused: run fyr auto off, which replaces it"}
	}
	c := defaults()
	if err == nil && prev.Enabled {
		// Only a running config carries over: a level raised once never comes
		// back by itself after an off or a self-disable (D21).
		c = prev
	}
	if r.Level != "" {
		c.Level = r.Level
	}
	if r.ProjectIdle != "" {
		c.ProjectIdle = r.ProjectIdle
	}
	if r.UserIdle != "" {
		c.UserIdle = r.UserIdle
	}
	if c.Level == LevelHarness {
		if err := r.CheckHarness(); err != nil {
			return &ExitError{2, "fyr auto on: level harness needs a harness checkout: " + oneLine(err.Error())}
		}
	}
	if err := unit.Check(o.Home, o.path(), o.goos()); err != nil {
		return refuseOn(o, c, err)
	}
	if err := unit.Install(o.Home, o.path(), o.goos()); err != nil {
		return refuseOn(o, c, err)
	}
	c.Enabled = true
	if err := save(o.Home, c); err != nil {
		return &ExitError{1, "fyr auto on: " + err.Error() + "; the timer is installed and the config did not change"}
	}
	fmt.Fprintf(o.Out, "auto on: level %s, project idle %s, user idle %s; fyr auto run runs hourly\n", c.Level, c.ProjectIdle, c.UserIdle)
	return nil
}

// refuseOn leaves auto off and says why: exit 2 whether or not the off could
// be written, with both reasons when it could not.
func refuseOn(o Options, c Config, cause error) error {
	c.Enabled = false
	msg := cause.Error()
	if err := save(o.Home, c); err != nil {
		msg += "; " + ConfigPath(o.Home) + " could not be turned off either: " + err.Error()
	}
	return &ExitError{2, msg}
}

// Off is the kill switch: no lock, a corrupt file or a link replaced, and
// each of its two steps tried whatever the other did. Exit 1 when one failed:
// a switch that reports off with `enabled: true` on disk is the worse lie.
func Off(o Options) error {
	c, found, err := Load(o.Home)
	_, lerr := os.Lstat(ConfigPath(o.Home))
	present := err != nil || found || lerr == nil
	timer := unit.InstalledHere(o.Home, o.goos())
	if !timer && (!present || (found && !c.Enabled)) {
		fmt.Fprintln(o.Out, "auto: already off")
		o.otherSystemNote()
		return nil
	}
	if err != nil || !found {
		c = defaults()
	}
	c.Enabled = false
	var failed []string
	if err := save(o.Home, c); err != nil {
		failed = append(failed, "could not write the config: "+err.Error())
	}
	if _, err := unit.Remove(o.Home, o.goos()); err != nil {
		failed = append(failed, err.Error())
	}
	o.otherSystemNote()
	if len(failed) > 0 {
		return &ExitError{1, "fyr auto off: " + strings.Join(failed, "; ")}
	}
	fmt.Fprintln(o.Out, "auto off")
	return nil
}

func (o Options) otherSystemNote() {
	if files := unit.OtherSystemFiles(o.Home, o.goos()); len(files) > 0 {
		fmt.Fprintln(o.Out, "an auto timer unit for another system is in this home and stays: "+strings.Join(files, ", "))
	}
}

func stateLine(home string, err error) string {
	return "auto: state: " + err.Error() + "; the file is kept: remove " + dir(home) + "/runs.json to start the day's count over"
}

func configLine(err error) string {
	return "auto: config: " + oneLine(err.Error()) + "; run fyr auto off or on"
}
