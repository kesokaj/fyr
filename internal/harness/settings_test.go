package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	attributionFragment = `"attribution": {"commit": "", "pr": ""}`
	autoCompactFragment = `"autoCompactWindow": 300000`
)

func TestReadSettingsProblems(t *testing.T) {
	dir := t.TempDir()

	if _, problem := ReadSettings(filepath.Join(dir, "absent.json")); problem != "could not be read" {
		t.Errorf("missing file: %q", problem)
	}

	sub := filepath.Join(dir, "adirectory.json")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, problem := ReadSettings(sub); problem != "is not a regular file" {
		t.Errorf("directory: %q", problem)
	}

	if os.Geteuid() == 0 {
		t.Skip("root can open a mode-000 file, so the open-failure leg cannot run")
	}
	unreadable := filepath.Join(dir, "unreadable.json")
	secret := `{"attribution":{"commit":"SECRET"}}`
	if err := os.WriteFile(unreadable, []byte(secret), 0o000); err != nil {
		t.Fatal(err)
	}
	data, problem := ReadSettings(unreadable)
	if problem != "could not be read" {
		t.Errorf("unreadable regular file: %q", problem)
	}
	if len(data) != 0 {
		t.Errorf("no bytes may be returned when the open failed, got %q", data)
	}
	// The problem strings are a closed set of fixed phrases, so asserting one
	// does not contain the secret proves nothing; what is worth asserting is
	// that the set IS closed — a future reader returning the OS error verbatim
	// would leak a path, and returning file bytes would leak the file.
	for _, allowed := range []string{"could not be read", "is not a regular file", ""} {
		if problem == allowed {
			return
		}
	}
	t.Errorf("readSettings returned an unrecognised problem string %q; it must stay a closed set so nothing from the file or the OS error escapes", problem)
}

// TestSettingsWriteUsesHarnessWriteAtomic pins the settings editor to the one
// atomic-write helper: a second temp-write-sync-rename sequence is a fix to
// port by hand (project review 2026-09-30, simplify finding 1).
func TestSettingsWriteUsesHarnessWriteAtomic(t *testing.T) {
	src, err := os.ReadFile("settings.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "os.CreateTemp(") {
		t.Error("settings.go writes through its own temp file; use WriteAtomic")
	}
	if !strings.Contains(string(src), "WriteAtomic(") {
		t.Error("settings.go does not call WriteAtomic")
	}
}

// TestOwnedSettingsIsTheClosedSet pins the vendor keys the harness writes to
// the two REQUIREMENTS § Non-goals names for install (ADR-025, ADR-060), in the
// order install writes them, and the backstop window to the one number the
// metric's context target shares (plan session-sized-work D27; the equality
// with metrics.DefaultContextTarget is pinned in cmd/sh-agent, which imports
// both packages). A third key here is a requirements change, never a drop-in.
func TestOwnedSettingsIsTheClosedSet(t *testing.T) {
	var keys []string
	for _, s := range OwnedSettings {
		keys = append(keys, s.Key)
	}
	if strings.Join(keys, ",") != "attribution,autoCompactWindow" {
		t.Fatalf("OwnedSettings = %v; REQUIREMENTS § Non-goals allows exactly attribution and autoCompactWindow", keys)
	}
	if AutoCompactWindow != 300000 {
		t.Errorf("AutoCompactWindow = %d, want 300000 (D4, D27)", AutoCompactWindow)
	}
	if Attribution.Fragment != attributionFragment || AutoCompact.Fragment != autoCompactFragment {
		t.Errorf("fragments %q / %q", Attribution.Fragment, AutoCompact.Fragment)
	}
}

// TestSettingSetReportsAnUnwritableSettingsFile drives the error branch
// behind harness.WriteAtomic: a settings directory that cannot take a temp
// file makes the edit fail, and the failure is reported with its cause and
// the by-hand remedy, the file left as it was (v1.13.1 gate review).
func TestSettingSetReportsAnUnwritableSettingsFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permissions this test relies on")
	}
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	var out bytes.Buffer
	SettingSet(home, Attribution, false, &out)
	got := out.String()
	if !strings.Contains(got, "could not be written") || !strings.Contains(got, "add the key by hand") {
		t.Errorf("the failure should be reported with the remedy, got %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{}\n" {
		t.Errorf("the settings file should be untouched, got %q %v", data, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("no temp file should be left behind, got %d entries", len(entries))
	}
}

func settingsPath(home string) string { return filepath.Join(home, ClaudeRoot, "settings.json") }

func writeSettings(t *testing.T, home, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, ClaudeRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath(home), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readSettings(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(settingsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestSettingSetAttributionAndHint drives the settings editor in package
// harness (S1) with the attribution key: the file is created when absent, the
// key inserted into an existing object with every other byte kept, a present
// key left alone, a dry run touches nothing, a symlink is never written
// through, and the hint names each state of the file in one line.
func TestSettingSetAttributionAndHint(t *testing.T) {
	const fragment = attributionFragment

	t.Run("absent file is created, then the second key joins it", func(t *testing.T) {
		home := t.TempDir()
		var out bytes.Buffer
		SettingSet(home, Attribution, false, &out)
		if got := readSettings(t, home); got != "{\n  "+fragment+"\n}\n" {
			t.Errorf("created file: %q", got)
		}
		if !strings.Contains(out.String(), "created with") {
			t.Errorf("no creation line: %q", out.String())
		}
		SettingSet(home, AutoCompact, false, &out)
		if got := readSettings(t, home); got != "{\n  "+fragment+",\n  "+autoCompactFragment+"\n}\n" {
			t.Errorf("after both keys: %q", got)
		}
		out.Reset()
		SettingsHint(home, "note: ", &out)
		if out.Len() != 0 {
			t.Errorf("hint after both keys are set: %q", out.String())
		}
	})
	t.Run("dry run writes nothing", func(t *testing.T) {
		home := t.TempDir()
		var out bytes.Buffer
		SettingSet(home, Attribution, true, &out)
		if _, err := os.Stat(settingsPath(home)); err == nil {
			t.Error("dry run created the file")
		}
		if !strings.Contains(out.String(), "[dry-run] would create") {
			t.Errorf("dry run line: %q", out.String())
		}
		writeSettings(t, home, "{\n  \"theme\": \"dark\"\n}\n")
		out.Reset()
		SettingSet(home, Attribution, true, &out)
		if !strings.Contains(out.String(), "[dry-run] would add") || readSettings(t, home) != "{\n  \"theme\": \"dark\"\n}\n" {
			t.Errorf("dry run on an existing file: %q / %q", out.String(), readSettings(t, home))
		}
	})
	t.Run("key inserted, other bytes kept, CRLF kept", func(t *testing.T) {
		home := t.TempDir()
		writeSettings(t, home, "{\r\n  \"theme\": \"dark\"\r\n}\r\n")
		var out bytes.Buffer
		SettingSet(home, Attribution, false, &out)
		got := readSettings(t, home)
		if !strings.Contains(got, "\"theme\": \"dark\",\r\n  "+fragment+"\r\n}") {
			t.Errorf("insertion: %q", got)
		}
		if !strings.Contains(out.String(), "added; other keys untouched") {
			t.Errorf("line: %q", out.String())
		}
	})
	t.Run("empty object", func(t *testing.T) {
		home := t.TempDir()
		writeSettings(t, home, "{}\n")
		SettingSet(home, Attribution, false, &bytes.Buffer{})
		if got := readSettings(t, home); got != "{\n  "+fragment+"\n}\n" {
			t.Errorf("empty object: %q", got)
		}
	})
	t.Run("present key of any value is the operator's", func(t *testing.T) {
		home := t.TempDir()
		const custom = "{\"attribution\": {\"commit\": \"Co-Authored-By: me\"}}\n"
		writeSettings(t, home, custom)
		var out bytes.Buffer
		SettingSet(home, Attribution, false, &out)
		if readSettings(t, home) != custom || out.Len() != 0 {
			t.Errorf("present key: file %q, out %q", readSettings(t, home), out.String())
		}
		SettingsHint(home, "note: ", &out)
		if !strings.Contains(out.String(), "does not set \"attribution\": {\"commit\": \"\"}") {
			t.Errorf("hint for a custom trailer: %q", out.String())
		}
	})
	t.Run("off with false, hint silent", func(t *testing.T) {
		home := t.TempDir()
		writeSettings(t, home, "{\"attribution\": {\"commit\": false}, "+autoCompactFragment+"}\n")
		var out bytes.Buffer
		SettingsHint(home, "note: ", &out)
		if out.Len() != 0 {
			t.Errorf("hint: %q", out.String())
		}
	})
	t.Run("malformed and non-object files are advisory", func(t *testing.T) {
		// a bare null is a nil map for SettingSet (left alone) and holds no key
		// for the hint, which then reports each key as unset
		for content, want := range map[string]string{"{\"attribution\": \n": "is not valid JSON", "[1, 2]\n": "is not a JSON object", "null\n": "does not set \"attribution\""} {
			home := t.TempDir()
			writeSettings(t, home, content)
			var out bytes.Buffer
			SettingSet(home, Attribution, false, &out)
			if readSettings(t, home) != content {
				t.Errorf("%q: file changed to %q", content, readSettings(t, home))
			}
			SettingsHint(home, "note: ", &out)
			if !strings.Contains(out.String(), want) {
				t.Errorf("%q: hint %q, want %q", content, out.String(), want)
			}
			if strings.Contains(out.String(), "invalid character") {
				t.Errorf("%q: the JSON error is echoed: %q", content, out.String())
			}
		}
	})
	t.Run("symlink is never written through", func(t *testing.T) {
		home := t.TempDir()
		target := filepath.Join(t.TempDir(), "real.json")
		if err := os.WriteFile(target, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(home, ClaudeRoot), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, settingsPath(home)); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		SettingSet(home, Attribution, false, &out)
		if data, _ := os.ReadFile(target); string(data) != "{}\n" {
			t.Errorf("target written through the link: %q", data)
		}
		if !strings.Contains(out.String(), "is a symlink") {
			t.Errorf("line: %q", out.String())
		}
	})
	t.Run("oversized file", func(t *testing.T) {
		home := t.TempDir()
		big := append([]byte("{"), bytes.Repeat([]byte(" "), 1<<20)...)
		writeSettings(t, home, string(append(big, '}')))
		var out bytes.Buffer
		SettingsHint(home, "note: ", &out)
		if !strings.Contains(out.String(), "larger than 1 MiB") {
			t.Errorf("oversized: %q", out.String())
		}
	})
}

// TestSettingSetAutoCompact is the auto-compact backstop's contract (ADR-060,
// plan session-sized-work Phase 2b): the absent top-level key is written as
// `"autoCompactWindow": 300000` with every other byte kept; a present key of
// any value is the operator's; a window saved under modelSettings (what
// /autocompact writes) is neither read nor touched, and the top-level key is
// still written beside it; a symlinked file is refused with the by-hand hint;
// a dry run touches nothing.
func TestSettingSetAutoCompact(t *testing.T) {
	t.Run("absent key written, every other byte kept", func(t *testing.T) {
		home := t.TempDir()
		const before = "{\n  \"theme\": \"dark\",\n  " + attributionFragment + "\n}\n"
		writeSettings(t, home, before)
		var out bytes.Buffer
		SettingSet(home, AutoCompact, false, &out)
		want := "{\n  \"theme\": \"dark\",\n  " + attributionFragment + ",\n  " + autoCompactFragment + "\n}\n"
		if got := readSettings(t, home); got != want {
			t.Errorf("file:\n%q\nwant\n%q", got, want)
		}
		if !strings.Contains(out.String(), autoCompactFragment+" added; other keys untouched") {
			t.Errorf("line: %q", out.String())
		}
		out.Reset()
		SettingSet(home, AutoCompact, false, &out)
		if out.Len() != 0 || readSettings(t, home) != want {
			t.Errorf("second write changed something: %q / %q", out.String(), readSettings(t, home))
		}
	})
	t.Run("present key of any value is the operator's", func(t *testing.T) {
		for _, value := range []string{"150000", `"auto"`, `"500k"`, "null", "0"} {
			home := t.TempDir()
			content := "{\"autoCompactWindow\": " + value + "}\n"
			writeSettings(t, home, content)
			var out bytes.Buffer
			SettingSet(home, AutoCompact, false, &out)
			if readSettings(t, home) != content || out.Len() != 0 {
				t.Errorf("%s: file %q, out %q", value, readSettings(t, home), out.String())
			}
			SettingsHint(home, "note: ", &out)
			if strings.Contains(strings.ReplaceAll(out.String(), settingsPath(home), ""), "autoCompactWindow") {
				t.Errorf("%s: the hint names a key that is present: %q", value, out.String())
			}
		}
	})
	t.Run("a modelSettings window is left alone and the top-level key still written", func(t *testing.T) {
		home := t.TempDir()
		const model = "{\n  \"modelSettings\": {\"claude-opus-5-5\": {\"autoCompactWindow\": 500000}}\n}\n"
		writeSettings(t, home, model)
		SettingSet(home, AutoCompact, false, &bytes.Buffer{})
		want := "{\n  \"modelSettings\": {\"claude-opus-5-5\": {\"autoCompactWindow\": 500000}},\n  " + autoCompactFragment + "\n}\n"
		if got := readSettings(t, home); got != want {
			t.Errorf("file:\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("symlinked file is refused with the hint", func(t *testing.T) {
		home := t.TempDir()
		target := filepath.Join(t.TempDir(), "real.json")
		if err := os.WriteFile(target, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(home, ClaudeRoot), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, settingsPath(home)); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		SettingSet(home, AutoCompact, false, &out)
		if data, _ := os.ReadFile(target); string(data) != "{}\n" {
			t.Errorf("target written through the link: %q", data)
		}
		if !strings.Contains(out.String(), "is a symlink; add the key to its target by hand") {
			t.Errorf("line: %q", out.String())
		}
	})
	t.Run("dry run touches nothing", func(t *testing.T) {
		home := t.TempDir()
		var out bytes.Buffer
		SettingSet(home, AutoCompact, true, &out)
		if _, err := os.Stat(settingsPath(home)); err == nil {
			t.Error("dry run created the file")
		}
		if !strings.Contains(out.String(), "[dry-run] would create "+settingsPath(home)+" with "+autoCompactFragment) {
			t.Errorf("dry run line: %q", out.String())
		}
		writeSettings(t, home, "{}\n")
		out.Reset()
		SettingSet(home, AutoCompact, true, &out)
		if !strings.Contains(out.String(), "[dry-run] would add "+autoCompactFragment) || readSettings(t, home) != "{}\n" {
			t.Errorf("dry run on an existing file: %q / %q", out.String(), readSettings(t, home))
		}
	})
}

// TestSettingsHintNamesEachAbsentKey: one line per key the harness owns that
// the file does not set, none when both are there, and one shared line when
// the file itself cannot be judged (the shape of the attribution hint, which
// install and status print).
func TestSettingsHintNamesEachAbsentKey(t *testing.T) {
	lines := func(home string) []string {
		var out bytes.Buffer
		SettingsHint(home, "note: ", &out)
		return strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	}
	t.Run("missing file names both", func(t *testing.T) {
		got := lines(t.TempDir())
		if len(got) != 2 || !strings.Contains(got[0], "does not set \"attribution\"") || !strings.Contains(got[1], "does not set autoCompactWindow") {
			t.Errorf("lines: %q", got)
		}
	})
	t.Run("attribution only", func(t *testing.T) {
		home := t.TempDir()
		writeSettings(t, home, "{"+attributionFragment+"}\n")
		got := lines(home)
		if len(got) != 1 || !strings.Contains(got[0], "does not set autoCompactWindow; Claude Code compacts at its model's default window") || strings.Contains(got[0], "Co-Authored-By") {
			t.Errorf("lines: %q", got)
		}
		if !strings.HasPrefix(got[0], "note: "+settingsPath(home)+" ") {
			t.Errorf("the line does not open with the prefix and the file: %q", got[0])
		}
	})
	t.Run("autoCompactWindow only", func(t *testing.T) {
		home := t.TempDir()
		writeSettings(t, home, "{"+autoCompactFragment+"}\n")
		got := lines(home)
		// the clause after the path: a temp dir's name carries the test's name
		clause := strings.TrimPrefix(got[0], "note: "+settingsPath(home))
		if len(got) != 1 || !strings.Contains(clause, "does not set \"attribution\": {\"commit\": \"\"}") || strings.Contains(clause, "autoCompactWindow") {
			t.Errorf("lines: %q", got)
		}
	})
	t.Run("both present, silent", func(t *testing.T) {
		home := t.TempDir()
		writeSettings(t, home, "{"+attributionFragment+", "+autoCompactFragment+"}\n")
		var out bytes.Buffer
		SettingsHint(home, "note: ", &out)
		if out.Len() != 0 {
			t.Errorf("hint: %q", out.String())
		}
	})
	t.Run("an unreadable file is one line naming both keys", func(t *testing.T) {
		home := t.TempDir()
		writeSettings(t, home, "[1]\n")
		got := lines(home)
		if len(got) != 1 || !strings.Contains(got[0], "is not a JSON object, so the settings the harness owns could not be checked") || !strings.Contains(got[0], "autoCompactWindow") || !strings.Contains(got[0], "Co-Authored-By") {
			t.Errorf("lines: %q", got)
		}
	})
}

// A linked ~/.claude (a dotfiles checkout) is never written through: the
// installer refuses every other path under a linked directory (underLink,
// BUG-129's rule), and the settings file is no exception, for either key.
func TestSettingSetRefusesALinkedClaudeDir(t *testing.T) {
	for _, s := range OwnedSettings {
		t.Run(s.Key, func(t *testing.T) {
			home := t.TempDir()
			target := t.TempDir()
			if err := os.Symlink(target, filepath.Join(home, ClaudeRoot)); err != nil {
				t.Skip("symlinks unavailable:", err)
			}
			var out bytes.Buffer
			SettingSet(home, s, false, &out)
			if _, err := os.Lstat(filepath.Join(target, "settings.json")); err == nil {
				t.Fatalf("settings.json was written through the linked %s into %s", ClaudeRoot, target)
			}
			if !strings.Contains(out.String(), "symlink") {
				t.Fatalf("the refusal does not name the link: %q", out.String())
			}
			// The edit branch too: an existing file behind the link is left byte for byte.
			existing := []byte("{\n  \"theme\": \"dark\"\n}\n")
			if err := os.WriteFile(filepath.Join(target, "settings.json"), existing, 0o644); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			SettingSet(home, s, false, &out)
			got, _ := os.ReadFile(filepath.Join(target, "settings.json"))
			if string(got) != string(existing) {
				t.Fatalf("settings.json behind the link was edited: %q", got)
			}
			if !strings.Contains(out.String(), "symlink") {
				t.Fatalf("the refusal does not name the link on the edit path: %q", out.String())
			}
		})
	}
}
