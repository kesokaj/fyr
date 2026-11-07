package auto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/kesokaj/fyr/internal/harness"
)

const (
	// modelRunsPerDay is the budget's day limit (D1, row 7): a constant, not a
	// setting.
	modelRunsPerDay = 4
	// stateLimit bounds runs.json; logLimit bounds the prune's read.
	stateLimit = 1 << 20
	logLimit   = 16 << 20
	logKeep    = 30 * 24 * time.Hour
)

// State is runs.json. Only a run holding the lock writes it.
type State struct {
	Date      string             `json:"date"`
	ModelRuns int                `json:"model_runs"`
	LastRun   time.Time          `json:"last_run"`
	Verdict   string             `json:"verdict"`
	OpenAsks  int                `json:"open_asks"`
	Budget    string             `json:"budget,omitempty"` // the last `/usage` reading's line
	Machine   Mark               `json:"machine"`
	Projects  map[string]Project `json:"projects,omitempty"`
}

// Mark is the last report of one scope: an unchanged set of findings writes no
// second record, so a finding left open does not file one an hour.
type Mark struct {
	Hash   string `json:"hash,omitempty"`
	Report string `json:"report,omitempty"`
}

// Project is one project's last run, for the gates and `status`.
type Project struct {
	LastRun                 time.Time `json:"last_run"`
	LastClass               string    `json:"last_class,omitempty"`
	LastMemoryConsolidation time.Time `json:"last_memory_consolidation,omitempty"`
	LastRetroLite           time.Time `json:"last_retro_lite,omitempty"`
	LastMemoryCandidates    time.Time `json:"last_memory_candidates,omitempty"`
	LastEvalRerun           time.Time `json:"last_eval_rerun,omitempty"`
	LastEvalCLI             string    `json:"last_eval_cli,omitempty"`
	LastMiniAudit           time.Time `json:"last_mini_audit,omitempty"`
	PendingMemoryCandidates int       `json:"pending_memory_candidates,omitempty"`
	Verdict                 string    `json:"verdict"`
	Mark                    Mark      `json:"mark"`
}

func dir(home string) string { return filepath.Join(home, harness.StateDir, "auto") }

// ensureDir makes auto/ at 0700 and refuses it as a link: the files below it
// are refused as links too, and a linked parent would walk round that.
func ensureDir(home string) error {
	if err := os.MkdirAll(filepath.Join(home, harness.StateDir), 0o700); err != nil {
		return err
	}
	d := dir(home)
	if err := os.Mkdir(d, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return checkDir(d)
}

func checkDir(d string) error {
	info, err := os.Lstat(d)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", d)
	}
	return nil
}

func ensureSubdir(home, sub string) error {
	if err := ensureDir(home); err != nil {
		return err
	}
	p := filepath.Join(dir(home), sub)
	if err := os.Mkdir(p, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return checkDir(p)
}

// loadState reads runs.json; a missing one is a zero State, found false.
func loadState(home string) (st State, found bool, err error) {
	d := dir(home)
	if _, lerr := os.Lstat(d); errors.Is(lerr, fs.ErrNotExist) {
		return State{}, false, nil
	}
	if err := checkDir(d); err != nil {
		return State{}, false, err
	}
	data, err := readBounded(filepath.Join(d, "runs.json"), stateLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	return parseState(data)
}

// parseState refuses a null, an unknown key and a date that is no date, each
// of which would otherwise reach the day's reset and hand the budget back. It
// is not as strict as auto.json's parse: this file is a run's own, and a
// repeated or case-folded key here changes a count, never the switch.
func parseState(data []byte) (State, bool, error) {
	var p *State
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return State{}, false, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return State{}, false, errors.New("more after the JSON value")
	}
	if p == nil {
		return State{}, false, errors.New("null")
	}
	if p.ModelRuns < 0 || p.OpenAsks < 0 {
		return State{}, false, errors.New("a negative count")
	}
	if _, err := time.Parse("2006-01-02", p.Date); err != nil {
		return State{}, false, fmt.Errorf("date %q", p.Date)
	}
	return *p, true, nil
}

// usableState is the state a run may go on from: what the real run and a dry
// run both refuse. A date past tomorrow is refused; one day of grace covers a
// move across time zones. A missing file is a zero state with no date.
func usableState(home string, now time.Time) (State, error) {
	st, _, err := loadState(home)
	if err != nil {
		return State{}, err
	}
	if st.Date > now.AddDate(0, 0, 1).Format("2006-01-02") {
		return State{}, fmt.Errorf("date %s is ahead of the clock", st.Date)
	}
	return st, nil
}

func saveState(home string, st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return harness.WriteAtomic(filepath.Join(dir(home), "runs.json"), append(data, '\n'), 0o600)
}

// modelRunsToday is the count that belongs to the day of now.
func (st State) modelRunsToday(now time.Time) int {
	if st.Date < now.Format("2006-01-02") {
		return 0
	}
	return st.ModelRuns
}

// oneLine keeps a log or status line one line: an error can quote a file's
// bytes, and a config is not trusted to be printable.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// lineLimit bounds a quoted line in the log, the state and a report: a tool's
// output or a project's file is not trusted to be short. maxBeforeBranch caps
// any error/reason prefix before "; branch <b> kept" so the kept branch never
// falls past lineLimit (BUG-506).
const (
	lineLimit       = 500
	maxBeforeBranch = 200
)

// short is oneLine cut to lineLimit runes, which says it was cut.
func short(s string) string {
	s = capBeforeBranch(oneLine(s))
	if r := []rune(s); len(r) > lineLimit {
		return string(r[:lineLimit]) + "..."
	}
	return s
}

func capBeforeBranch(line string) string {
	head, tail, ok := strings.Cut(line, "; branch ")
	if !ok {
		return line
	}
	prop := ""
	if h, p, ok := strings.Cut(head, "; proposal "); ok {
		head, prop = h, "; proposal "+p
	}
	if r := []rune(head); len(r) > maxBeforeBranch {
		suffix := "..."
		if strings.HasSuffix(head, ")") && strings.Contains(head, "(") {
			suffix = "...)"
		}
		head = string(r[:maxBeforeBranch]) + suffix
	}
	return head + prop + "; branch " + tail
}

// openRegular creates or opens a file through no link, and refuses anything
// but a regular file; O_NONBLOCK keeps a fifo from hanging the timer here
// too, on Linux for 3 h and on a Mac for good.
func openRegular(path string, flags int) (*os.File, error) {
	f, err := os.OpenFile(path, flags|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return f, nil
}

// appendLog adds one timestamped line to auto.log, through no link.
func appendLog(home string, now time.Time, line string) error {
	if err := ensureDir(home); err != nil {
		return err
	}
	f, err := openRegular(filepath.Join(dir(home), "auto.log"), os.O_WRONLY|os.O_APPEND)
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintf(f, "%s %s\n", now.UTC().Format(time.RFC3339), oneLine(line))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// pruneLog drops lines over 30 days old. A line with no readable time is
// kept: the prune removes only what it can date. Called under the lock; a
// stop at G1 or G2 can still append during the rewrite and lose that one
// line, which is a line of a run that did nothing.
func pruneLog(home string, now time.Time) error {
	path := filepath.Join(dir(home), "auto.log")
	data, err := readBounded(path, logLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	lines := strings.SplitAfter(string(data), "\n")
	kept := lines[:0]
	for _, l := range lines {
		stamp, _, _ := strings.Cut(l, " ")
		if t, err := time.Parse(time.RFC3339, stamp); err == nil && now.Sub(t) > logKeep {
			continue
		}
		kept = append(kept, l)
	}
	text := strings.Join(kept, "")
	if len(text) == len(data) {
		return nil
	}
	return harness.WriteAtomic(path, []byte(text), 0o600)
}

// lock takes auto.lock without waiting, through no link: held is true when
// another run has it. The kernel lets go when that run ends, however it ends,
// so no stale lock can stop the timer's job.
func lock(home string) (unlock func(), held bool, err error) {
	f, err := openRegular(filepath.Join(dir(home), "auto.lock"), os.O_RDWR)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, false, nil
}
