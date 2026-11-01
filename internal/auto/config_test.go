package auto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

const validConfig = `{"enabled": true, "level": "project", "project_idle": "6h", "user_idle": "15m"}`

func TestAutoConfigMissingIsNotFound(t *testing.T) {
	if _, found, err := Load(t.TempDir()); found || err != nil {
		t.Errorf("found %v, err %v", found, err)
	}
}

func TestAutoConfigRoundTrip(t *testing.T) {
	home := t.TempDir()
	want := Config{Enabled: true, Level: LevelHarness, ProjectIdle: "7d", UserIdle: "1m"}
	if err := save(home, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := Load(home)
	if err != nil || !found || got != want {
		t.Fatalf("%+v, %v, %v", got, found, err)
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
	for name, text := range map[string]string{
		"empty":           ``,
		"not json":        `on`,
		"truncated":       `{"enabled": true`,
		"unknown key":     `{"enabled": true, "level": "project", "project_idle": "6h", "user_idle": "15m", "x": 1}`,
		"no enabled":      `{"level": "project", "project_idle": "6h", "user_idle": "15m"}`,
		"no level":        `{"enabled": true, "project_idle": "6h", "user_idle": "15m"}`,
		"no project idle": `{"enabled": true, "level": "project", "user_idle": "15m"}`,
		"no user idle":    `{"enabled": true, "level": "project", "project_idle": "6h"}`,
		"null enabled":    `{"enabled": null, "level": "project", "project_idle": "6h", "user_idle": "15m"}`,
		"string enabled":  `{"enabled": "true", "level": "project", "project_idle": "6h", "user_idle": "15m"}`,
		"two values":      validConfig + validConfig,
		"bad level":       `{"enabled": true, "level": "god", "project_idle": "6h", "user_idle": "15m"}`,
		"idle too short":  `{"enabled": true, "level": "project", "project_idle": "59m", "user_idle": "15m"}`,
		"idle too long":   `{"enabled": true, "level": "project", "project_idle": "8d", "user_idle": "15m"}`,
		"user idle short": `{"enabled": true, "level": "project", "project_idle": "6h", "user_idle": "59s"}`,
		"user idle long":  `{"enabled": true, "level": "project", "project_idle": "6h", "user_idle": "25h"}`,
		"oversized":       `{"enabled": true, "level": "project", "project_idle": "6h", "user_idle": "15m"}` + strings.Repeat(" ", configLimit),
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, text)
			if c, found, err := Load(home); err == nil || found || c.Enabled {
				t.Errorf("%+v, found %v, err %v", c, found, err)
			}
		})
	}
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	if _, found, err := Load(home); !found || err != nil {
		t.Errorf("the valid control failed: %v, %v", found, err)
	}
}

func TestAutoConfigLinkRefused(t *testing.T) {
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
	home := t.TempDir()
	if err := os.MkdirAll(ConfigPath(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Load(home); err == nil || found {
		t.Errorf("a directory: found %v, err %v", found, err)
	}
}

func TestAutoSpans(t *testing.T) {
	good := map[string]func(string) error{"1h": ValidProjectIdle, "7d": ValidProjectIdle, "168h": ValidProjectIdle, "1d": ValidProjectIdle, "1m": ValidUserIdle, "24h": ValidUserIdle}
	for s, valid := range good {
		if err := valid(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for s, valid := range map[string]func(string) error{"": ValidProjectIdle, "59m": ValidProjectIdle, "8d": ValidProjectIdle, "1.5d": ValidProjectIdle, "-1d": ValidProjectIdle, "d": ValidProjectIdle, "x": ValidUserIdle, "59s": ValidUserIdle, "2d": ValidUserIdle} {
		if err := valid(s); err == nil {
			t.Errorf("%q passed", s)
		}
	}
	if err := ValidLevel("harness"); err != nil {
		t.Error(err)
	}
	if err := ValidLevel(""); err == nil {
		t.Error("an empty level passed")
	}
}

// encoding/json alone lets each of these through: a closing bracket after the
// value, a repeated key, a key in another case.
func TestAutoConfigStrictJSON(t *testing.T) {
	for name, text := range map[string]string{
		"stray brace":   validConfig + `}`,
		"stray bracket": validConfig + `]`,
		"stray many":    validConfig + "\n}}}]",
		"twice":         `{"enabled": false, "enabled": true, "level": "project", "project_idle": "6h", "user_idle": "15m"}`,
		"case folded":   `{"ENABLED": true, "level": "project", "project_idle": "6h", "user_idle": "15m"}`,
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
