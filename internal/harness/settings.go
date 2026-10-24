package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// The vendor settings the harness owns, in Claude Code's ~/.claude/settings.json
// (REQUIREMENTS § Non-goals, the one exception for install): the attribution
// key, switched off so no commit carries the trailer the context's Git rule
// forbids (ADR-025), and the auto-compact window, the one monitor-and-trigger
// the harness can own without a hook (ADR-060). Every function here is
// advisory: no state of that file fails an install or a status, and nothing
// from the file is echoed.

// Setting is one top-level key of the operator's settings.json that install
// writes when it is absent. A present value of any kind is the operator's;
// satisfied, when set, says whether that value does what the harness wants,
// and unmet is the hint's clause when the key is absent or does not.
type Setting struct {
	Key       string
	Fragment  string // the JSON member written when the key is absent: `"key": value`
	Why       string // install's line after the fragment, what the key does
	unmet     string // the hint after the path, what Claude Code does until the key is there
	satisfied func(raw json.RawMessage) bool
}

// AutoCompactWindow is the window install writes: the metric's context target
// (internal/metrics.DefaultContextTarget, pinned equal in cmd/sh-agent), so
// the report's over-target count and the CLI's compaction point are one line
// (plan session-sized-work D4, D27).
const AutoCompactWindow = 300000

// Attribution is Claude Code's commit and PR attribution, off (ADR-025).
var Attribution = Setting{
	Key:       "attribution",
	Fragment:  `"attribution": {"commit": "", "pr": ""}`,
	Why:       "Claude Code's commit and PR trailers are off",
	unmet:     `does not set "attribution": {"commit": ""}; Claude Code adds a Co-Authored-By trailer to every commit until it does (context § Git)`,
	satisfied: attributionOff,
}

// AutoCompact is Claude Code's auto-compact window for every model (ADR-060).
// A window the operator saved for one model with /autocompact lives under
// modelSettings and takes precedence by the CLI's own rules, as do the
// --autocompact flag and CLAUDE_CODE_AUTO_COMPACT_WINDOW; the harness never
// reads or writes those.
var AutoCompact = Setting{
	Key:      "autoCompactWindow",
	Fragment: fmt.Sprintf(`"autoCompactWindow": %d`, AutoCompactWindow),
	Why:      fmt.Sprintf("Claude Code compacts at %d tokens unless /autocompact, --autocompact or CLAUDE_CODE_AUTO_COMPACT_WINDOW says otherwise", AutoCompactWindow),
	unmet:    fmt.Sprintf("does not set autoCompactWindow; Claude Code compacts at its model's default window, not at %d tokens, until it does (install writes the key when it is absent; context § Checkpoints)", AutoCompactWindow),
}

// OwnedSettings are the keys install writes, in the order they are written; a
// third one is a requirements change (TestOwnedSettingsIsTheClosedSet).
var OwnedSettings = []Setting{Attribution, AutoCompact}

// SettingSet writes one owned setting into the operator's settings.json. Only
// an absent key is written — a present value of any kind is the operator's,
// and SettingsHint says when it is not what the harness wants. The write is a
// textual insertion before the object's closing brace, re-parsed before it
// lands, because encoding/json would reorder the operator's keys; every byte
// up to the object's last value stays, and the file keeps its own line
// endings. No state of the file fails the install: the deployment already
// succeeded and this file is advisory. dryRun prints what would be written and
// touches nothing.
func SettingSet(home string, s Setting, dryRun bool, out io.Writer) {
	rel := filepath.Join(ClaudeRoot, "settings.json")
	path := filepath.Join(home, rel)
	// A linked directory above the file (a dotfiles checkout's ~/.claude) is
	// never written through either: the same rule every installed path follows
	// (underLink, BUG-129).
	if underLink(home, rel) {
		fmt.Fprintf(out, "settings: a directory above %s is a symlink; add the key to its target by hand\n", path)
		return
	}
	// Lstat, not Stat: a settings.json that is a symlink (a dotfiles checkout)
	// is never written through — a rename would replace the link with a file.
	info, err := os.Lstat(path)
	switch {
	case err == nil && info.Mode()&os.ModeSymlink != 0:
		fmt.Fprintf(out, "settings: %s is a symlink; add the key to its target by hand\n", path)
		return
	case errors.Is(err, os.ErrNotExist):
		if dryRun {
			fmt.Fprintf(out, "[dry-run] would create %s with %s\n", path, s.Fragment)
			return
		}
		content := []byte("{\n  " + s.Fragment + "\n}\n")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			settingsUnwritable(out, path, err)
			return
		}
		// O_EXCL: the Lstat above is advisory; whatever appears at the path
		// between the two calls (a planted link, a FIFO) makes this fail
		// instead of being written through or blocked on.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			settingsUnwritable(out, path, err)
			return
		}
		if _, err := f.Write(content); err != nil {
			f.Close()
			settingsUnwritable(out, path, err)
			return
		}
		if err := f.Sync(); err != nil {
			f.Close()
			settingsUnwritable(out, path, err)
			return
		}
		if err := f.Close(); err != nil {
			settingsUnwritable(out, path, err)
			return
		}
		fmt.Fprintf(out, "settings: %s created with %s — %s\n", path, s.Fragment, s.Why)
		return
	case err != nil, !info.Mode().IsRegular():
		return // SettingsHint names it, and ReadSettings bounds the size
	}
	data, problem := ReadSettings(path)
	if problem != "" {
		return // SettingsHint names it
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil || top == nil {
		// malformed, not an object, or the bare value null (a nil map with no
		// error): SettingsHint names each with one line
		return
	}
	if _, present := top[s.Key]; present {
		return
	}
	// Insert after the last byte of the object's body, keeping the operator's
	// indentation style (two spaces, the file's own newline before the brace)
	// and whatever follows the closing brace.
	trimmed := bytes.TrimRight(data, " \t\r\n")
	closing := bytes.LastIndexByte(trimmed, '}')
	opening := bytes.IndexByte(trimmed, '{')
	if closing <= opening {
		fmt.Fprintf(out, "settings: %s could not be edited safely (the result would not parse); add the key by hand\n", path)
		return
	}
	nl := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		nl = "\r\n" // keep the file's own line endings
	}
	body := bytes.TrimRight(trimmed[:closing], " \t\r\n")
	insert := "," + nl + "  " + s.Fragment + nl
	if len(bytes.TrimSpace(trimmed[opening+1:closing])) == 0 {
		body = trimmed[:opening+1]
		insert = nl + "  " + s.Fragment + nl
	}
	result := make([]byte, 0, len(data)+len(insert))
	result = append(result, body...)
	result = append(result, insert...)
	result = append(result, trimmed[closing:]...)
	result = append(result, data[len(trimmed):]...)
	if !json.Valid(result) {
		fmt.Fprintf(out, "settings: %s could not be edited safely (the result would not parse); add the key by hand\n", path)
		return
	}
	if dryRun {
		fmt.Fprintf(out, "[dry-run] would add %s to %s\n", s.Fragment, path)
		return
	}
	if err := WriteAtomic(path, result, info.Mode().Perm()); err != nil {
		settingsUnwritable(out, path, err)
		return
	}
	fmt.Fprintf(out, "settings: %s — %s added; other keys untouched\n", path, s.Fragment)
}

// settingsUnwritable is the one sentence for every way the settings file
// could not be written.
func settingsUnwritable(out io.Writer, path string, err error) {
	fmt.Fprintf(out, "settings: %s could not be written (%v); add the key by hand\n", path, err)
}

// ReadSettings reads the operator's settings file, bounded to a regular file
// of at most one megabyte; problem names why it could not be read, without a
// byte of its contents.
func ReadSettings(path string) (data []byte, problem string) {
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return nil, "could not be read"
	case !info.Mode().IsRegular():
		return nil, "is not a regular file"
	case info.Size() > 1<<20:
		// One bound, here, so install and status say the same thing about
		// a file this tool will not rewrite.
		return nil, "is larger than 1 MiB, more than this tool will read or rewrite"
	}
	data, err = ReadCapped(path, 1<<20) // the one capped reader (S7)
	if err != nil {
		return nil, "could not be read"
	}
	return data, ""
}

// SettingsHint prints one line per owned setting the file does not set the
// way the harness wants: the context's rules alone do not hold in a headless
// session (BUG-039: the trailer), and the model cannot see its own context
// (ADR-060); the CLI's own settings are what act, and settings.json is the
// operator's file, so install and status only say so. A file that cannot be
// judged gets one line naming both keys. prefix is each command's note style
// ("note: " for install, "note       " for status). Advisory only: no state of
// that file may fail install or status, and nothing from it is echoed.
func SettingsHint(home, prefix string, out io.Writer) {
	path := filepath.Join(home, ClaudeRoot, "settings.json")
	top, problem := readSettingsObject(path)
	if problem != "" {
		fmt.Fprintf(out, "%s%s %s, so the settings the harness owns could not be checked; Claude Code adds a Co-Authored-By trailer to every commit unless it sets \"attribution\": {\"commit\": \"\"}, and compacts at its model's default window unless it sets autoCompactWindow (context § Git, § Checkpoints)\n", prefix, path, problem)
		return
	}
	for _, s := range OwnedSettings {
		raw, present := top[s.Key]
		if present && (s.satisfied == nil || s.satisfied(raw)) {
			continue
		}
		fmt.Fprintf(out, "%s%s %s\n", prefix, path, s.unmet)
	}
}

// readSettingsObject reads the operator's settings file as a JSON object,
// bounded to a regular file of at most one megabyte; a missing file is an
// empty object. problem names why the file could not be judged, without a
// byte of its contents (a JSON error string carries the input it choked on).
func readSettingsObject(path string) (top map[string]json.RawMessage, problem string) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, ""
	}
	data, problem := ReadSettings(path)
	if problem != "" {
		return nil, problem
	}
	if err := json.Unmarshal(data, &top); err != nil {
		if json.Valid(data) {
			return nil, "is not a JSON object"
		}
		return nil, "is not valid JSON"
	}
	if top == nil {
		top = map[string]json.RawMessage{} // the bare value null: no key is set
	}
	return top, ""
}

// attributionOff is true when attribution.commit is "" or false — the two
// values that switch the trailer off.
func attributionOff(raw json.RawMessage) bool {
	var attribution struct {
		Commit any `json:"commit"`
	}
	if err := json.Unmarshal(raw, &attribution); err != nil {
		return false
	}
	switch v := attribution.Commit.(type) {
	case string:
		return v == ""
	case bool:
		return !v
	}
	return false
}
