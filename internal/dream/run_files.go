package dream

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

var maxLogBytes int64 = 1 << 20

// safeFileName strips path separators, parent-traversal segments, and control
// characters so a project's display name can safely sit inside a filename.
func safeFileName(s string) string {
	s = oneLine(s)
	s = strings.NewReplacer("/", "-", "\\", "-").Replace(s)
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", "_")
	}
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if s == "" || s == "." {
		return "project"
	}
	return s
}

// keepFailure keeps the CLI's output of a failed project, scrubbed, for the
// operator: never in the CLI's working directory (D27).
func (o Options) keepFailure(conf string, c Collected, t0 time.Time, stdout, stderr string) {
	dir := filepath.Join(conf, "dream-failed")
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	path, err := unique(dir, t0.Format("2006-01-02")+"-"+safeFileName(c.Name), ".txt")
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

func openAppendLog(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, os.ErrInvalid
		}
		if maxLogBytes > 0 && info.Size() >= maxLogBytes {
			if err := os.Rename(path, path+".1"); err != nil {
				return nil, err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, os.ErrInvalid
	}
	return f, nil
}

// logger appends one timestamped line per call, rotating dream.log safely.
func logger(path string, now func() time.Time) func(string) {
	return func(line string) {
		f, err := openAppendLog(path)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = fmt.Fprintf(f, "%s %s\n", now().UTC().Format(time.RFC3339), line)
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
