package dream

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// keepFailure keeps the CLI's output of a failed project, scrubbed, for the
// operator: never in the CLI's working directory (D27).
func (o Options) keepFailure(conf string, c Collected, t0 time.Time, stdout, stderr string) {
	dir := filepath.Join(conf, "dream-failed")
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	path, err := unique(dir, t0.Format("2006-01-02")+"-"+c.Name, ".txt")
	if err != nil {
		return
	}
	body := "stdout:\n" + o.clean(stdout) + "\nstderr:\n" + o.clean(stderr) + "\n"
	harness.WriteAtomic(path, []byte(body), 0o600)
}

// prune removes kept failures older than keepFailed.
func prune(dir string, t0 time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() && t0.Sub(info.ModTime()) > keepFailed {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// logger appends one timestamped line per call.
func logger(path string, now func() time.Time) func(string) {
	return func(line string) {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		fmt.Fprintf(f, "%s %s\n", now().UTC().Format(time.RFC3339), line)
		f.Close()
	}
}

// writeState writes the state file; a test replaces it with a failing one.
var writeState = func(path string, data []byte) error { return harness.WriteAtomic(path, data, 0o600) }

func saveState(home string, st state) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeState(statePath(home), append(data, '\n'))
}

// lock takes an exclusive flock on path without waiting: held is true when
// another run has it. The kernel releases it when that run ends, however it
// ends, so no stale lock can stop the timer's job (code review 2026-09-30).
func lock(path string) (unlock func(), held bool, err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
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
	f.Truncate(0)
	fmt.Fprint(f, os.Getpid())
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, false, nil
}
