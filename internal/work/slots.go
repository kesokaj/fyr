package work

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// The worker limit (D30): machine-wide, counted from the pid file of every
// running fyr work under the work root.
const (
	DefaultMaxWorkers = 4
	MaxMaxWorkers     = 8
)

// slotPoll is how often a run waiting for a slot recounts the other runs,
// a variable so a test waits less.
var slotPoll = 2 * time.Second

// maxPid bounds a pid file's pid: Linux's highest pid_max, above macOS's. A
// larger number would wrap in kill(2) and could reach -1, every process of
// the operator's (the Phase 4 security review, finding 1).
const maxPid = 1 << 22

// stampRe is a stamp as Run makes it: %F-%H%M%S-<pid> (D33), the pid at
// most seven digits (maxPid).
var stampRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}-([0-9]{1,7})$`)

const (
	pidName   = "pid"
	slotsLock = "slots.lock"
)

// run is one invocation's share of the limit: its pid file under
// <work>/<stamp>/, held under an exclusive flock until the run ends, so a
// lock another process can take marks a stale file (D33). The file is one
// line, "<pid> <running>", rewritten in place at a fixed length: running is
// at most MaxMaxWorkers, one digit, so a reader never sees half a line.
type run struct {
	work, stamp string
	max         int
	pid         *os.File
	running     int
}

// openRun makes <work>/<stamp>/ and its pid file, locked; the work root is
// the one CheckRoot approved.
func openRun(work, stamp string, max int) (*run, error) {
	if err := os.MkdirAll(work, 0o700); err != nil {
		return nil, err
	}
	dir := filepath.Join(work, stamp)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	// Locked and written under a name no count opens, then renamed: the
	// lock never waits, and no reader sees the file unlocked or empty
	// (the Phase 4 security review, round 2).
	tmp := filepath.Join(dir, pidName+".new")
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	r := &run{work: work, stamp: stamp, max: max, pid: f}
	err = flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		err = r.write()
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, pidName))
	}
	if err != nil {
		f.Close()
		os.Remove(tmp)
		os.Remove(dir)
		return nil, fmt.Errorf("%s: %v", tmp, err)
	}
	return r, nil
}

func (r *run) write() error {
	_, err := r.pid.WriteAt([]byte(fmt.Sprintf("%d %d\n", os.Getpid(), r.running)), 0)
	return err
}

// take claims one slot when the live workers of every other run and this
// run's own leave one under the limit. The count and the claim hold the
// work root's slots.lock, so two runs never both take the last slot. The
// lock is never waited for: while another process holds it no slot is
// free, and the run tries again at its next poll, so its end and --kill
// still reach it (the Phase 4 security review, finding 3).
func (r *run) take() (bool, error) {
	unlock, busy, err := r.lockSlots()
	if err != nil || busy {
		return false, err
	}
	defer unlock()
	others, _, err := liveRuns(r.work, r.stamp)
	if err != nil {
		return false, err
	}
	n := 0
	for _, c := range others {
		n += c
	}
	if n+r.running >= r.max {
		return false, nil
	}
	r.running++
	if err := r.write(); err != nil {
		r.running--
		return false, err
	}
	return true, nil
}

// give returns a slot. It needs no slots.lock: a lower count only frees a
// slot, and the one write is whole for a reader.
func (r *run) give() {
	r.running--
	r.write()
}

// lockSlots takes slots.lock without waiting; busy is true when another
// process holds it.
func (r *run) lockSlots() (unlock func(), busy bool, err error) {
	f, err := os.OpenFile(filepath.Join(r.work, slotsLock), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
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
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, false, nil
}

// close removes the pid file, then the stamp directory when nothing is kept
// in it, and drops the lock.
func (r *run) close() {
	dir := filepath.Join(r.work, r.stamp)
	os.Remove(filepath.Join(dir, pidName))
	os.Remove(dir)
	r.pid.Close()
}

// liveRuns is every run under work but except whose pid file is locked,
// with its running workers. A file that cannot be opened (but still exists)
// or whose lock cannot be tested, and a live one whose line cannot be read,
// count as the whole limit: the run may be alive, its share unknown.
func liveRuns(work, except string) (live map[string]int, bad []string, err error) {
	// ReadDir, not a glob: the home's path is not a pattern.
	entries, err := os.ReadDir(work)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	live = map[string]int{}
	for _, e := range entries {
		stamp := e.Name()
		if stamp == except || !stampRe.MatchString(stamp) {
			continue
		}
		p := filepath.Join(work, stamp, pidName)
		f, err := sandbox.OpenRegular(p, os.O_RDONLY)
		if errors.Is(err, fs.ErrNotExist) {
			continue // no run, or it ended since the listing
		} else if err != nil {
			live[stamp] = MaxMaxWorkers
			bad = append(bad, fmt.Sprintf("%s (%v)", p, err))
			continue
		}
		if h, err := held(f); err != nil {
			live[stamp] = MaxMaxWorkers
			bad = append(bad, fmt.Sprintf("%s (%v)", p, err))
		} else if h {
			_, n, err := readPid(f, stamp)
			if err != nil {
				n = MaxMaxWorkers
			}
			live[stamp] = n
		}
		f.Close()
	}
	return live, bad, nil
}

// flock is syscall.Flock, a variable so a test can make it fail.
var flock = syscall.Flock

// held reports whether another open file holds f's lock: a shared lock f
// cannot take without waiting. One f can take is released at once; any
// other error is the caller's to fail closed on.
func held(f *os.File) (bool, error) {
	err := flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	switch {
	case err == nil:
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false, nil
	case errors.Is(err, syscall.EWOULDBLOCK):
		return true, nil
	}
	return false, err
}

// readPid parses a pid file's line; the pid must be the one in its stamp.
func readPid(f *os.File, stamp string) (pid, running int, err error) {
	b, err := io.ReadAll(io.LimitReader(f, 64))
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(string(b))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("%s: not one line \"<pid> <running>\"", f.Name())
	}
	if pid, err = strconv.Atoi(fields[0]); err != nil || pid <= 1 || pid > maxPid {
		return 0, 0, fmt.Errorf("%s: bad pid %q", f.Name(), fields[0])
	}
	if m := stampRe.FindStringSubmatch(stamp); m == nil || m[1] != fields[0] {
		return 0, 0, fmt.Errorf("%s: pid %d is not the stamp's", f.Name(), pid)
	}
	if running, err = strconv.Atoi(fields[1]); err != nil || running < 0 || running > MaxMaxWorkers {
		return 0, 0, fmt.Errorf("%s: bad worker count %q", f.Name(), fields[1])
	}
	return pid, running, nil
}

// atLimit is refusal 5: the machine already runs max workers (D30).
func atLimit(work string, max int) error {
	live, bad, err := liveRuns(work, "")
	if err != nil {
		return err
	}
	n := 0
	var stamps []string
	for s, c := range live {
		n += c
		stamps = append(stamps, s)
	}
	if n < max {
		return nil
	}
	sort.Strings(stamps)
	msg := fmt.Sprintf("the machine already runs %d workers, at --max-workers %d: %s; wait, or fyr work --kill <stamp>", n, max, strings.Join(stamps, ", "))
	if len(bad) > 0 {
		// Counted as the whole limit; --kill cannot clear these.
		sort.Strings(bad)
		msg += "; counted as full, unreadable: " + strings.Join(bad, ", ")
	}
	return errors.New(msg)
}

// Kill sends SIGTERM to the run stamp names, which ends its context: every
// worker fails killed and the run prints its result contract (D26, D33).
// A pid file no live run holds is stale and refused with its path. It
// returns the pid signalled.
func Kill(home, stamp string) (int, error) {
	if !stampRe.MatchString(stamp) {
		return 0, fmt.Errorf("--kill %q: not a stamp; give the one the run printed on its first line", stamp)
	}
	p := filepath.Join(sandbox.Root(home, "work"), stamp, pidName)
	f, err := sandbox.OpenRegular(p, os.O_RDONLY)
	if err != nil {
		return 0, fmt.Errorf("no run %s: %v", stamp, err)
	}
	defer f.Close()
	if h, err := held(f); err != nil {
		return 0, fmt.Errorf("%s: %v", p, err)
	} else if !h {
		return 0, fmt.Errorf("%s is stale: no run holds it", p)
	}
	pid, _, err := readPid(f, stamp)
	if err != nil {
		return 0, err
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return 0, fmt.Errorf("SIGTERM to %d: %v", pid, err)
	}
	return pid, nil
}
