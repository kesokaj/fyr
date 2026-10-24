package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State is the install manifest. It is the only thing uninstall trusts: a
// path not listed here is not sh-agent's to delete, whatever directory it
// sits in.
type State struct {
	Repo        string `json:"repo"`
	Commit      string `json:"commit"`
	InstalledAt string `json:"installedAt"`
	// Vendor is always "antigravity" when written. A value an install from
	// before ADR-062 recorded (such as "all") is read and ignored: the
	// install it describes is replaced by the next one.
	Vendor string   `json:"vendor,omitempty"`
	Files  []string `json:"files"` // relative to $HOME, sorted
}

func statePath(home string) string {
	return filepath.Join(home, ".config", "sh-agent", "state.json")
}

// LoadState reads the manifest. A missing file is not an error: it means
// nothing has been installed into this home yet.
func LoadState(home string) (State, bool, error) {
	data, err := os.ReadFile(statePath(home))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, false, fmt.Errorf("parsing %s: %w", statePath(home), err)
	}
	// The manifest is a file under the user's home: its entries are paths
	// every reader (install's resync, uninstall, status) joins under the
	// install root and may Lstat or remove, so a non-local entry (`../x`, an
	// absolute path) is dropped HERE, once, never followed by any caller
	// (security review 2026-09-14).
	kept := s.Files[:0]
	for _, rel := range s.Files {
		if filepath.IsLocal(rel) {
			kept = append(kept, rel)
		}
	}
	s.Files = kept
	return s, true, nil
}

func SaveState(home string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if _, err := ConfigDir(home); err != nil {
		return err
	}
	return WriteAtomic(statePath(home), append(data, '\n'), 0o644)
}

// ConfigDir returns ~/.config/sh-agent, created 0700 when absent: the
// feedback log, the retro proposals, the global search index (search.db) and
// the metrics store live there, and none of it leaves the machine (ADR-062).
// An existing directory is left as it is
// here — every install used to create it 0755 on the way to state.json, and
// TightenConfigDir, run by install alone, is what corrects that (security
// review of the 2026-09-18 audit, rounds 2 and 3; the operator's option 1).
func ConfigDir(home string) (string, error) {
	dir := filepath.Join(home, ".config", "sh-agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// TightenConfigDir corrects the runtime directory's mode to 0700 at the start
// of an install, the way install corrects a deployed file's mode: it names
// the change with a `mode` line, a dry run names it and touches nothing, and
// a chmod that fails is a note, never a failed install — the first version
// aborted after every file was written, leaving no manifest. A symlinked
// directory (a dotfiles arrangement) and one the caller does not own are
// left alone: the first is not sh-agent's to reach through, the second is
// not its to change.
func TightenConfigDir(home string, dryRun bool, out io.Writer) {
	dir := filepath.Join(home, ".config", "sh-agent")
	info, err := os.Lstat(dir)
	if err != nil {
		return
	}
	// Names what it declines to touch, as install's `keep` line does
	// (security review of the audit, round 3).
	if info.Mode()&os.ModeSymlink != 0 {
		fmt.Fprintf(out, "note: ~/.config/sh-agent is a symlink; its target's mode is yours to set (0700 keeps the feedback log private)\n")
		return
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 == 0 {
		return
	}
	if !ownedByCaller(info) {
		fmt.Fprintf(out, "note: ~/.config/sh-agent is not yours to tighten (mode %04o); ask its owner for 0700 — the feedback log lives there\n", info.Mode().Perm())
		return
	}
	fmt.Fprintf(out, "mode    ~/.config/sh-agent (%04o -> 0700)\n", info.Mode().Perm())
	if dryRun {
		return
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		fmt.Fprintf(out, "note: could not correct the mode of ~/.config/sh-agent (%v)\n", err)
	}
}

func RemoveState(home string) error {
	p := statePath(home)
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// The directory is sh-agent's own; drop it when empty.
	_ = os.Remove(filepath.Dir(p))
	return nil
}

// LastRetro returns when the learning loop last ran: the newest of the
// harness proposal files under ~/.config/sh-agent/proposals/ and the
// cross-project index ~/.config/sh-agent/retros.jsonl, which a project-level
// /retro or /distill appends to even when it proposes nothing for the harness.
func LastRetro(home string) (time.Time, bool) {
	// Listed by name, never through a pattern built from the home path: a
	// `[` in it would be read as a character class (the golang rule).
	dir := filepath.Join(home, ".config", "sh-agent", "proposals")
	var paths []string
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				paths = append(paths, filepath.Join(dir, e.Name()))
			}
		}
	}
	paths = append(paths, filepath.Join(home, ".config", "sh-agent", "retros.jsonl"))
	var newest time.Time
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, !newest.IsZero()
}
