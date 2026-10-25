package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// State is the install manifest. It is the only thing uninstall trusts: a
// path not listed here is not fyr's to delete, whatever directory it
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

// StateDir is the state directory under a home: the manifest, the feedback
// and retro logs, the proposals, the search index, the metrics store and the
// dream's state. Every path into it is built from this one constant (plan
// fyr D22): a literal missed would split a machine's state across two
// directories (TestStateDirHasOneHome).
const StateDir = ".config/fyr"

func statePath(home string) string {
	return filepath.Join(home, StateDir, "state.json")
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
	// install root and may Lstat or remove, so an entry that is not local
	// (`../x`, an absolute path) or lies outside the roots fyr manages
	// (`.ssh/authorized_keys`, `.`) is dropped HERE, once, never followed by
	// any caller (security review 2026-09-14; IN26).
	kept := s.Files[:0]
	for _, rel := range s.Files {
		if filepath.IsLocal(rel) && managedEntry(rel) {
			kept = append(kept, rel)
		}
	}
	s.Files = kept
	return s, true, nil
}

// managedBinaries are the binaries an install of fyr, or of it under an
// earlier name, wrote: BinPath now, and the names before the renames, which a
// resync removes (plan fyr Interfaces § Migration).
var managedBinaries = []string{BinPath, ".local/bin/sh-agent", ".local/bin/styrman"}

// managedEntry reports whether a manifest entry lies under a root fyr
// manages: a file below ~/.gemini/config, one of managedBinaries, or a file
// below ~/.claude that an install before ADR-062 wrote and the resync keeps
// (claudeFile's rule, first element without case). It is judged as the path
// it removes, cleaned, so `.gemini/../.ssh/x` is `.ssh/x`.
func managedEntry(rel string) bool {
	clean := filepath.ToSlash(filepath.Clean(rel))
	if slices.Contains(managedBinaries, clean) {
		return true
	}
	if strings.HasPrefix(clean, AgyRoot+"/") {
		return true
	}
	first, _, nested := strings.Cut(clean, "/")
	return nested && strings.EqualFold(first, ".claude")
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

// ConfigDir returns ~/.config/fyr, created 0700 when absent: the
// feedback log, the retro proposals, the global search index (search.db) and
// the metrics store live there, and none of it leaves the machine (ADR-062).
// An existing directory is left as it is
// here — every install used to create it 0755 on the way to state.json, and
// tightenConfigDir, run by install alone, is what corrects that (security
// review of the 2026-09-18 audit, rounds 2 and 3; the operator's option 1).
func ConfigDir(home string) (string, error) {
	dir := filepath.Join(home, StateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// tightenConfigDir corrects the runtime directory's mode to 0700 at the start
// of an install, the way install corrects a deployed file's mode: it names
// the change with a `mode` line, a dry run names it and touches nothing, and
// a chmod that fails is a note, never a failed install — the first version
// aborted after every file was written, leaving no manifest. A symlinked
// directory (a dotfiles arrangement) and one the caller does not own are
// left alone: the first is not fyr's to reach through, the second is
// not its to change.
func tightenConfigDir(home string, dryRun bool, out io.Writer) {
	dir := filepath.Join(home, StateDir)
	info, err := os.Lstat(dir)
	if err != nil {
		return
	}
	// Names what it declines to touch, as install's `keep` line does
	// (security review of the audit, round 3).
	if info.Mode()&os.ModeSymlink != 0 {
		fmt.Fprintf(out, "note: ~/.config/fyr is a symlink; its target's mode is yours to set (0700 keeps the feedback log private)\n")
		return
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 == 0 {
		return
	}
	if !ownedByCaller(info) {
		fmt.Fprintf(out, "note: ~/.config/fyr is not yours to tighten (mode %04o); ask its owner for 0700 — the feedback log lives there\n", info.Mode().Perm())
		return
	}
	fmt.Fprintf(out, "mode    ~/.config/fyr (%04o -> 0700)\n", info.Mode().Perm())
	if dryRun {
		return
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		fmt.Fprintf(out, "note: could not correct the mode of ~/.config/fyr (%v)\n", err)
	}
}

func removeState(home string) error {
	p := statePath(home)
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// The directory is fyr's own; drop it when empty.
	_ = os.Remove(filepath.Dir(p))
	return nil
}

// LastRetro returns when the learning loop last ran: the newest of the
// harness proposal files under ~/.config/fyr/proposals/ and the
// cross-project index ~/.config/fyr/retros.jsonl, which a project-level
// /retro or /distill appends to even when it proposes nothing for the harness.
func LastRetro(home string) (time.Time, bool) {
	// Listed by name, never through a pattern built from the home path: a
	// `[` in it would be read as a character class (the golang rule).
	dir := filepath.Join(home, StateDir, "proposals")
	var paths []string
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				paths = append(paths, filepath.Join(dir, e.Name()))
			}
		}
	}
	paths = append(paths, filepath.Join(home, StateDir, "retros.jsonl"))
	var newest time.Time
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, !newest.IsZero()
}
