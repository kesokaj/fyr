package auto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, home, text string) {
	t.Helper()
	p := ConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

const validConfig = `{"enabled": true, "conversation_idle": "1h"}`

func TestAutoConfigMissingIsNotFound(t *testing.T) {
	t.Parallel()
	if _, found, err := Load(t.TempDir()); found || err != nil {
		t.Errorf("found %v, err %v", found, err)
	}
}

func TestAutoConfigRoundTrip(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	want := Config{Enabled: true, ConversationIdle: "7d"}
	if err := save(home, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := Load(home)
	if err != nil || !found || got != want {
		t.Fatalf("%+v, %v, %v", got, found, err)
	}
	raw, err := os.ReadFile(ConfigPath(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"level", "project_idle", "user_idle"} {
		if strings.Contains(string(raw), legacy) {
			t.Errorf("saved config contains legacy key %q: %s", legacy, raw)
		}
	}
	info, err := os.Stat(ConfigPath(home))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode: %v, %v", info.Mode(), err)
	}
	if info, err := os.Stat(filepath.Dir(ConfigPath(home))); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("state dir made at %v, %v", info.Mode(), err)
	}
}

// Everything but a well-formed file fails closed: Load errs, found is false,
// and no caller can read a half-parsed "enabled".
func TestAutoConfigCorrupt(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{
		"empty":             ``,
		"not json":          `on`,
		"truncated":         `{"enabled": true`,
		"unknown key":       `{"enabled": true, "conversation_idle": "1h", "x": 1}`,
		"no enabled":        `{"conversation_idle": "1h"}`,
		"null enabled":      `{"enabled": null, "conversation_idle": "1h"}`,
		"string enabled":    `{"enabled": "true", "conversation_idle": "1h"}`,
		"null conv idle":    `{"enabled": true, "conversation_idle": null}`,
		"int conv idle":     `{"enabled": true, "conversation_idle": 60}`,
		"null legacy level": `{"enabled": true, "level": null}`,
		"int legacy idle":   `{"enabled": true, "project_idle": 6}`,
		"bool legacy user":  `{"enabled": true, "user_idle": false}`,
		"two values":        validConfig + validConfig,
		"idle too short":    `{"enabled": true, "conversation_idle": "4m"}`,
		"idle too long":     `{"enabled": true, "conversation_idle": "8d"}`,
		"idle invalid":      `{"enabled": true, "conversation_idle": "soon"}`,
		"oversized":         validConfig + strings.Repeat(" ", configLimit),
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, text)
			if c, found, err := Load(home); err == nil || found || c.Enabled {
				t.Errorf("%+v, found %v, err %v", c, found, err)
			}
		})
	}
	for name, text := range map[string]string{
		"valid":              validConfig,
		"omitted conv idle":  `{"enabled": true}`,
		"legacy keys compat": `{"enabled": true, "level": "harness", "project_idle": "6h", "user_idle": "15m"}`,
		"legacy plus idle":   `{"enabled": false, "level": "observe", "conversation_idle": "2h"}`,
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, text)
			if c, found, err := Load(home); !found || err != nil || c.ConversationIdle == "" {
				t.Errorf("%s failed: %+v, %v, %v", name, c, found, err)
			}
		})
	}
}

func TestAutoConfigLinkRefused(t *testing.T) {
	t.Parallel()
	home, other := t.TempDir(), t.TempDir()
	target := filepath.Join(other, "elsewhere.json")
	if err := os.WriteFile(target, []byte(validConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(ConfigPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, ConfigPath(home)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Load(home); err == nil || found || !strings.Contains(err.Error(), "link") {
		t.Errorf("found %v, err %v", found, err)
	}
}

func TestAutoConfigDirectoryRefused(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.MkdirAll(ConfigPath(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Load(home); err == nil || found {
		t.Errorf("a directory: found %v, err %v", found, err)
	}
}

func TestAutoSpans(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"5m", "1h", "7d", "168h", "1d", "24h"} {
		if err := ValidConversationIdle(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, s := range []string{"", "4m", "59s", "8d", "1.5d", "-1d", "d", "x"} {
		if err := ValidConversationIdle(s); err == nil {
			t.Errorf("%q passed", s)
		}
	}
}

// encoding/json alone lets each of these through: a closing bracket after the
// value, a repeated key, a key in another case.
func TestAutoConfigStrictJSON(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{
		"stray brace":   validConfig + `}`,
		"stray bracket": validConfig + `]`,
		"stray many":    validConfig + "\n}}}]",
		"twice":         `{"enabled": false, "enabled": true, "conversation_idle": "1h"}`,
		"case folded":   `{"ENABLED": true, "conversation_idle": "1h"}`,
		"an array":      `[` + validConfig + `]`,
		"null":          `null`,
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, text)
			if c, found, err := Load(home); err == nil || found || c.Enabled {
				t.Errorf("%+v, found %v, err %v", c, found, err)
			}
		})
	}
}

func TestConversationIdleDuration(t *testing.T) {
	t.Parallel()
	if got := ConversationIdleDuration(t.TempDir()); got != time.Hour {
		t.Errorf("missing config = %v, want 1h", got)
	}
	corruptHome := t.TempDir()
	writeConfig(t, corruptHome, "{bad json")
	if got := ConversationIdleDuration(corruptHome); got != time.Hour {
		t.Errorf("corrupt config = %v, want 1h", got)
	}
	validHome := t.TempDir()
	writeConfig(t, validHome, `{"enabled": true, "conversation_idle": "2h"}`)
	if got := ConversationIdleDuration(validHome); got != 2*time.Hour {
		t.Errorf("valid config = %v, want 2h", got)
	}
}
