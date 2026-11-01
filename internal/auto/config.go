// Package auto is `fyr auto`, R3's second timer exception: a switch the
// operator turns, an hourly run that does nothing unless it is on, and from
// later phases the gates, checks and workers `SPEC-fyr-auto` describes.
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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// The levels, lowest first, and the defaults `on` starts from.
const (
	LevelObserve = "observe"
	LevelProject = "project"
	LevelHarness = "harness"

	defaultProjectIdle = "6h"
	defaultUserIdle    = "15m"

	// configLimit is far above any config: a larger file is not one.
	configLimit = 4 << 10
)

// Config is auto.json, every key required.
type Config struct {
	Enabled     bool   `json:"enabled"`
	Level       string `json:"level"`
	ProjectIdle string `json:"project_idle"`
	UserIdle    string `json:"user_idle"`
}

func defaults() Config {
	return Config{Level: LevelObserve, ProjectIdle: defaultProjectIdle, UserIdle: defaultUserIdle}
}

// ConfigPath is the operator's switch. It sits beside auto/ and not inside
// it: check c skips by top-level name, so inside it would go unwatched (D6).
func ConfigPath(home string) string {
	return filepath.Join(home, harness.StateDir, "auto.json")
}

// ValidLevel refuses a level that is not one of the three.
func ValidLevel(s string) error {
	switch s {
	case LevelObserve, LevelProject, LevelHarness:
		return nil
	}
	return fmt.Errorf("level %q: want observe, project or harness", s)
}

// span parses a duration in Go's form, or whole days as `7d`, which Go's
// parser refuses and the flags' range names.
func span(s string) (time.Duration, error) {
	if digits, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(digits)
		if err != nil || n < 0 || n > 1000 {
			return 0, fmt.Errorf("duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func within(name, s string, low, high time.Duration, say string) error {
	d, err := span(s)
	if err != nil || d < low || d > high {
		return fmt.Errorf("%s %q: want %s", name, s, say)
	}
	return nil
}

// ValidProjectIdle is the range of --project-idle.
func ValidProjectIdle(s string) error {
	return within("project idle", s, time.Hour, 7*24*time.Hour, "1h to 7d")
}

// ValidUserIdle is the range of --user-idle.
func ValidUserIdle(s string) error {
	return within("user idle", s, time.Minute, 24*time.Hour, "1m to 24h")
}

func (c Config) validate() error {
	if err := ValidLevel(c.Level); err != nil {
		return err
	}
	if err := ValidProjectIdle(c.ProjectIdle); err != nil {
		return err
	}
	return ValidUserIdle(c.UserIdle)
}

// readBounded reads a regular file through no link, at most limit bytes: a
// link, a fifo, a directory or anything longer is an error, and a missing
// file is fs.ErrNotExist. O_NONBLOCK keeps a fifo from hanging the timer.
func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ELOOP) {
		return nil, fmt.Errorf("%s is a link", path)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is over %d bytes", path, limit)
	}
	return data, nil
}

// Load reads auto.json. found is false when there is none; any other
// problem is an error, and every caller fails closed on it. Hand-edited
// values get no more trust than the flags: an unknown key, a missing key, a
// second value or an out-of-range threshold is corrupt.
func Load(home string) (c Config, found bool, err error) {
	data, err := readBounded(ConfigPath(home), configLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, err
	}
	raw, err := decodeConfig(data)
	if err != nil {
		return Config{}, false, err
	}
	if raw.Enabled == nil || raw.Level == nil || raw.ProjectIdle == nil || raw.UserIdle == nil {
		return Config{}, false, errors.New("a key is missing: want enabled, level, project_idle, user_idle")
	}
	c = Config{*raw.Enabled, *raw.Level, *raw.ProjectIdle, *raw.UserIdle}
	if err := c.validate(); err != nil {
		return Config{}, false, err
	}
	return c, true, nil
}

// rawConfig holds pointers, so a null reads as missing.
type rawConfig struct {
	Enabled                      *bool
	Level, ProjectIdle, UserIdle *string
}

// decodeConfig reads one object key by key: encoding/json alone folds a key's
// case, lets a repeated key win, and leaves a stray closing bracket after the
// value for nobody to see, and each of those would be an `enabled` a reader of
// the file does not see.
func decodeConfig(data []byte) (rawConfig, error) {
	var raw rawConfig
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return raw, errors.New("not a JSON object")
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return raw, err
		}
		key, _ := t.(string)
		if seen[key] {
			return raw, fmt.Errorf("key %q twice", key)
		}
		seen[key] = true
		switch key {
		case "enabled":
			err = dec.Decode(&raw.Enabled)
		case "level":
			err = dec.Decode(&raw.Level)
		case "project_idle":
			err = dec.Decode(&raw.ProjectIdle)
		case "user_idle":
			err = dec.Decode(&raw.UserIdle)
		default:
			return raw, fmt.Errorf("unknown key %q", key)
		}
		if err != nil {
			return raw, err
		}
	}
	if _, err := dec.Token(); err != nil {
		return raw, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return raw, errors.New("more after the JSON value")
	}
	return raw, nil
}

// save writes auto.json atomically at 0600. The rename replaces a link
// rather than following it, which is what lets `off` take one out.
func save(home string, c Config) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(home, harness.StateDir), 0o700); err != nil {
		return err
	}
	return harness.WriteAtomic(ConfigPath(home), append(data, '\n'), 0o600)
}
