package auto

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

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
