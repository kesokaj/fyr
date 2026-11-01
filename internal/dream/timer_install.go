package dream

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/kesokaj/fyr/internal/harness"
)

// timerPlan is what InstallTimer writes and runs: the unit files by path,
// the service manager and its start commands.
type timerPlan struct {
	files   map[string]string
	manager string
	start   [][]string
	paths   []string
}

// CheckTimer runs every refusal InstallTimer makes and changes nothing, so
// `--install-timer --dry-run` refuses what the real run refuses (SD26 of
// the 2026-10-27 audit, plan D17).
func CheckTimer(home, path, goos string) error { return dreamTimer.Check(home, path, goos) }

// Check is CheckTimer for this timer.
func (t Timer) Check(home, path, goos string) error {
	_, err := t.plan(home, path, goos)
	return err
}

// InstallTimer installs and starts the hourly dream timer for goos, with
// ExecStart at home's BinPath, `--home home` so the timer's run is scoped to
// that home (a scratch timer never dreams over the real home, R6), and PATH
// as given.
func InstallTimer(home, path, goos string) error { return dreamTimer.Install(home, path, goos) }

// Install is InstallTimer for this timer.
func (t Timer) Install(home, path, goos string) error {
	p, err := t.plan(home, path, goos)
	if err != nil {
		return err
	}
	for f, text := range p.files {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			return &ExitError{2, t.InstallVerb + ": " + err.Error()}
		}
		if err := harness.WriteAtomic(f, []byte(text), 0o644); err != nil {
			return &ExitError{2, t.InstallVerb + ": " + err.Error()}
		}
	}
	for _, args := range p.start {
		if out, err := exec.Command(p.manager, args...).CombinedOutput(); err != nil {
			return &ExitError{2, fmt.Sprintf("%s: %s %s: %v %s; the unit files are left in place: %s",
				t.InstallVerb, p.manager, strings.Join(args, " "), err, strings.TrimSpace(string(out)), strings.Join(p.paths, ", "))}
		}
	}
	return nil
}

// planTimer is every check of an install, and the files and commands it
// would write and run; it writes and runs nothing.
func (t Timer) plan(home, path, goos string) (timerPlan, error) {
	var p timerPlan
	bin := filepath.Join(home, harness.BinPath)
	if info, err := os.Stat(bin); err != nil || !info.Mode().IsRegular() {
		return p, &ExitError{2, fmt.Sprintf("%s: no binary at %s: run fyr install first", t.InstallVerb, bin)}
	}
	path = absolutePath(path)
	// A quote, a backslash or a line break would end the unit's quoted value
	// and let the rest of the path become unit text (security review
	// 2026-09-30), and any other control character is one the unit or the
	// plist's XML cannot carry as written (project review 2026-10-09, row
	// 21); such a home or PATH is refused, never escaped by guess. The home
	// is checked as given, not only through bin: Join cleans bin, so a quote
	// in a component `..` cancels was gone from bin and still in --home
	// (the release gate's security review of v2.6.2..13c5bde).
	if raw := bin + home + path; strings.ContainsAny(raw, "\"\\") || strings.ContainsFunc(raw, unicode.IsControl) {
		return p, &ExitError{2, t.InstallVerb + ": the binary path or PATH holds a quote, a backslash or a control character; the timer's unit cannot carry it safely: run it from a shell whose PATH, and for a --home whose path, holds none of them"}
	}
	unit, label := t.Names(home)
	switch goos {
	case "linux":
		service, timer := t.renderSystemd(bin, home, path)
		p.files = map[string]string{t.servicePath(home): service, t.timerPath(home): timer}
		p.manager = "systemctl"
		p.start = [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", unit + ".timer"}}
	case "darwin":
		p.files = map[string]string{t.plistPath(home): t.renderLaunchd(label, bin, home, path)}
		p.manager = "launchctl"
		p.start = [][]string{{"bootstrap", "gui/" + strconv.Itoa(os.Getuid()), t.plistPath(home)}}
	default:
		return p, &ExitError{2, t.InstallVerb + ": no user timer on " + goos + " (systemd on Linux, launchd on macOS)"}
	}
	if _, err := exec.LookPath(p.manager); err != nil {
		return p, &ExitError{2, fmt.Sprintf("%s: no %s on PATH: the timer needs the user service manager", t.InstallVerb, p.manager)}
	}
	// A unit on disk with other text is the operator's or an older install's:
	// kept and named, never overwritten.
	for f, text := range p.files {
		p.paths = append(p.paths, f)
		same, err := t.unitMatches(f, text)
		if err != nil {
			return p, err
		}
		if !same {
			return p, &ExitError{2, fmt.Sprintf("%s: %s exists with other text (another PATH, an older fyr, or edited by hand); kept. Run %s first", t.InstallVerb, f, t.RemoveHint)}
		}
	}
	return p, nil
}

// unitMatches compares a unit on disk with its text; a missing one matches.
// Whatever cannot be read as a regular file is refused rather than taken for
// missing: the rename would replace a link, or a file it could not read, that
// this guard exists to keep, and reading a fifo would hang (the guard keeps
// the data when its check errors, review-standards § 8).
func (t Timer) unitMatches(f, text string) (bool, error) {
	info, err := os.Lstat(f)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, &ExitError{2, t.InstallVerb + ": " + err.Error()}
	}
	if !info.Mode().IsRegular() {
		return false, &ExitError{2, fmt.Sprintf("%s: %s is not a regular file; kept. Run %s first", t.InstallVerb, f, t.RemoveHint)}
	}
	data, err := os.ReadFile(f)
	if err != nil {
		return false, &ExitError{2, fmt.Sprintf("%s: %v; kept. Run %s first", t.InstallVerb, err, t.RemoveHint)}
	}
	return string(data) == text, nil
}
