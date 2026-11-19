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
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// Default conversation staleness cutoff and max auto.json size.
const (
	defaultConversationIdle = "1h"

	// configLimit is far above any config: a larger file is not one.
	configLimit = 4 << 10
)

// Config is auto.json: a global binary on/off switch and conversation_idle.
type Config struct {
	Enabled          bool   `json:"enabled"`
	ConversationIdle string `json:"conversation_idle"`
}

func defaults() Config {
	return Config{ConversationIdle: defaultConversationIdle}
}

// ConfigPath is the operator's switch. It sits beside auto/ and not inside
// it: check c skips by top-level name, so inside it would go unwatched (D6).
func ConfigPath(home string) string {
	return filepath.Join(home, harness.StateDir, "auto.json")
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

// ValidConversationIdle is the range of --conversation-idle (5m to 7d).
func ValidConversationIdle(s string) error {
	return within("conversation idle", s, 5*time.Minute, 7*24*time.Hour, "5m to 7d")
}

// ConversationIdleDuration returns the configured conversation_idle duration,
// defaulting to 1h when absent or unreadable.
func ConversationIdleDuration(home string) time.Duration {
	if c, found, err := Load(home); err == nil && found {
		if d, err := span(c.ConversationIdle); err == nil && d > 0 {
			return d
		}
	}
	return time.Hour
}

func (c Config) validate() error {
	return ValidConversationIdle(c.ConversationIdle)
}

// readBounded reads a regular file through no link, at most limit bytes: a
// link, a fifo, a directory or anything longer is an error, and a missing
// file is fs.ErrNotExist. O_NONBLOCK keeps a fifo from hanging the timer.
func readBounded(path string, limit int64) ([]byte, error) {
	f, err := trail.OpenRegular(path)
	if errors.Is(err, trail.ErrStampSymlink) {
		return nil, fmt.Errorf("%s is a link", path)
	}
	if errors.Is(err, trail.ErrStampNotRegular) {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
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
	if raw.Enabled == nil {
		return Config{}, false, errors.New("a key is missing: want enabled")
	}
	idle := defaultConversationIdle
	if raw.ConversationIdle != nil {
		idle = *raw.ConversationIdle
	}
	c = Config{Enabled: *raw.Enabled, ConversationIdle: idle}
	if err := c.validate(); err != nil {
		return Config{}, false, err
	}
	return c, true, nil
}

// rawConfig holds pointers, so a null reads as missing or invalid.
type rawConfig struct {
	Enabled                                        *bool
	ConversationIdle, Level, ProjectIdle, UserIdle *string
}

func decodeConfigKey(dec *json.Decoder, raw *rawConfig, key string) error {
	var dst **string
	switch key {
	case "enabled":
		if err := dec.Decode(&raw.Enabled); err != nil || raw.Enabled == nil {
			return errors.New(`key "enabled" must be a bool`)
		}
		return nil
	case "conversation_idle":
		dst = &raw.ConversationIdle
	case "level":
		dst = &raw.Level
	case "project_idle":
		dst = &raw.ProjectIdle
	case "user_idle":
		dst = &raw.UserIdle
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	var v *string
	if err := dec.Decode(&v); err != nil || v == nil {
		return fmt.Errorf("key %q must be a string", key)
	}
	*dst = v
	return nil
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
		if err := decodeConfigKey(dec, &raw, key); err != nil {
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
