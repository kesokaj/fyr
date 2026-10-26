package dream

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestRemoveTimerSaysWhatItRemoved: the caller prints "removed" only when a
// unit of this home was on disk and is gone (the v2.1.0 gate: a removal that
// found nothing reported success).
func TestRemoveTimerSaysWhatItRemoved(t *testing.T) {
	fakeManager(t, "systemctl", 0)
	home := timerHome(t)
	if err := InstallTimer(home, "/usr/bin:/bin", "linux"); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveTimer(home, "linux"); err != nil || !removed {
		t.Errorf("an installed timer: removed=%v err=%v, want true", removed, err)
	}
	if removed, err := RemoveTimer(home, "linux"); err != nil || removed {
		t.Errorf("nothing installed: removed=%v err=%v, want false", removed, err)
	}
}

// TestLegacyTimerFilesAreNamedNotRemoved: a unit that v2.0.0 or earlier wrote
// under the bare name, in a home that now gets a suffixed name, is not this
// home's unit any more. It is never stopped or deleted here (a stop by name
// ends whichever unit the manager loaded, possibly the real home's): it is
// named, with the steps, by remove, uninstall and status.
func TestLegacyTimerFilesAreNamedNotRemoved(t *testing.T) {
	calls := fakeManager(t, "systemctl", 0)
	real := timerHome(t)
	other := t.TempDir()
	timerFile := filepath.Join(other, ".config", "systemd", "user", oldUnitName+".timer")
	plistFile := filepath.Join(other, "Library", "LaunchAgents", oldPlistName+".plist")
	write(t, timerFile, "x", fresh)
	write(t, plistFile, "x", fresh)
	got := LegacyTimerFiles(other)
	if len(got) != 2 || got[0] != timerFile || got[1] != plistFile {
		t.Errorf("LegacyTimerFiles = %v, want the timer and the plist", got)
	}
	if removed, err := RemoveTimer(other, "linux"); err != nil || removed {
		t.Errorf("RemoveTimer on a home with only legacy files: removed=%v err=%v", removed, err)
	}
	for _, f := range []string{timerFile, plistFile} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("a legacy file was removed: %v", err)
		}
	}
	if c := callsOf(t, calls); c != "" {
		t.Errorf("the service manager was called for a legacy unit:\n%s", c)
	}
	linux := LegacyTimerNotice(other, "linux")
	for _, want := range []string{timerFile, "FragmentPath", "systemctl --user disable --now " + oldUnitName + ".timer", "daemon-reload", "v2.0.0", "fyr dream --install-timer --home '" + other + "'"} {
		if !strings.Contains(linux, want) {
			t.Errorf("the linux notice lacks %q:\n%s", want, linux)
		}
	}
	darwin := LegacyTimerNotice(other, "darwin")
	for _, want := range []string{plistFile, "launchctl print gui/" + strconv.Itoa(os.Getuid()) + "/" + oldPlistName, "launchctl bootout"} {
		if !strings.Contains(darwin, want) {
			t.Errorf("the darwin notice lacks %q:\n%s", want, darwin)
		}
	}
	// The advice is a dry run with this home named: the bare command would
	// remove the timer of whatever home it defaults to, the real one included
	// (code review of v2.1.0..a62eb9d).
	if got := StatusLine(other); !strings.HasSuffix(got, "; legacy timer files under "+other+": fyr dream --remove-timer --dry-run --home '"+other+"' prints the cleanup steps") {
		t.Errorf("StatusLine does not name the legacy files: %q", got)
	}
	// A scratch home that shares the real home's .config through a link sees
	// the real home's own unit there: that is not legacy, and the notice must
	// not tell the operator to delete it (security review of v2.1.0..a62eb9d).
	write(t, filepath.Join(real, ".config", "systemd", "user", oldUnitName+".service"), "x", fresh)
	linked := t.TempDir()
	if err := os.Symlink(filepath.Join(real, ".config"), filepath.Join(linked, ".config")); err == nil {
		if got := LegacyTimerFiles(linked); len(got) != 0 {
			t.Errorf("the real home's unit, seen through a linked .config, is called legacy: %v", got)
		}
	}
	// The real home's bare-named unit is its own, never legacy.
	write(t, filepath.Join(real, ".config", "systemd", "user", oldUnitName+".timer"), "x", fresh)
	if got := LegacyTimerFiles(real); len(got) != 0 || LegacyTimerNotice(real, "linux") != "" {
		t.Errorf("the real home's own unit is called legacy: %v", got)
	}
}

// TestReplaceOldTimer: install replaces this home's timer under the name
// from before the rename — sh-agent-dream, com.styrman.sh-agent-dream, with
// the per-home suffix of UnitFor — with fyr-dream: the new unit first, so a
// refusal keeps the old one running, then the old one stopped and removed
// (plan fyr D13). A bare old name under a home that is not the real one is
// a legacy file, named elsewhere and never touched here.
func TestReplaceOldTimer(t *testing.T) {
	oldFiles := func(home string) []string {
		unit, _ := oldUnitFor(home)
		dir := filepath.Join(home, ".config", "systemd", "user")
		return []string{filepath.Join(dir, unit+".service"), filepath.Join(dir, unit+".timer")}
	}
	plant := func(t *testing.T, home string) {
		for _, p := range oldFiles(home) {
			write(t, p, "[Unit]\nDescription=sh-agent dream\n", fresh)
		}
	}
	gone := func(t *testing.T, paths ...string) {
		t.Helper()
		for _, p := range paths {
			if _, err := os.Lstat(p); !os.IsNotExist(err) {
				t.Errorf("%s still there: %v", p, err)
			}
		}
	}

	t.Run("real home, linux", func(t *testing.T) {
		home := timerHome(t)
		calls := fakeManager(t, "systemctl", 0)
		plant(t, home)
		old, replaced, err := ReplaceOldTimer(home, "/p", "linux", false)
		if err != nil || old != "sh-agent-dream" || !replaced {
			t.Fatalf("old %q, replaced %v, err %v", old, replaced, err)
		}
		gone(t, oldFiles(home)...)
		if !TimerInstalledHere(home, "linux") || !strings.Contains(read(t, linuxServicePath(home)), `/.local/bin/fyr" dream --all`) {
			t.Error("the fyr-dream unit is not installed")
		}
		want := "--user is-enabled sh-agent-dream.timer\n--user daemon-reload\n--user enable --now fyr-dream.timer\n--user disable --now sh-agent-dream.timer\n--user daemon-reload\n"
		if got := callsOf(t, calls); got != want {
			t.Errorf("systemctl calls:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("scratch home, suffixed names", func(t *testing.T) {
		timerHome(t) // the real home is another directory
		other := t.TempDir()
		write(t, filepath.Join(other, ".local", "bin", "fyr"), "#!/bin/sh\n", fresh)
		calls := fakeManager(t, "systemctl", 0)
		plant(t, other)
		oldUnit, _ := oldUnitFor(other)
		newUnit, _ := UnitFor(other)
		if !strings.HasPrefix(oldUnit, "sh-agent-dream-") || strings.TrimPrefix(oldUnit, oldUnitName) != strings.TrimPrefix(newUnit, unitName) {
			t.Fatalf("old %s and new %s do not share the per-home suffix", oldUnit, newUnit)
		}
		old, _, err := ReplaceOldTimer(other, "/p", "linux", false)
		if err != nil || old != oldUnit {
			t.Fatalf("old %q, err %v", old, err)
		}
		gone(t, oldFiles(other)...)
		if got := callsOf(t, calls); !strings.Contains(got, "enable --now "+newUnit+".timer") || !strings.Contains(got, "disable --now "+oldUnit+".timer") {
			t.Errorf("systemctl calls:\n%s", got)
		}
	})

	t.Run("dry run changes nothing", func(t *testing.T) {
		home := timerHome(t)
		calls := fakeManager(t, "systemctl", 0)
		plant(t, home)
		old, replaced, err := ReplaceOldTimer(home, "/p", "linux", true)
		if err != nil || old != "sh-agent-dream" || !replaced {
			t.Fatalf("old %q, err %v", old, err)
		}
		for _, p := range oldFiles(home) {
			if _, err := os.Lstat(p); err != nil {
				t.Errorf("a dry run removed %s", p)
			}
		}
		// The one call a dry run makes is the read-only probe.
		if got := callsOf(t, calls); TimerInstalledHere(home, "linux") || got != "--user is-enabled sh-agent-dream.timer\n" {
			t.Errorf("a dry run installed or changed the manager's state; calls:\n%s", got)
		}
	})

	t.Run("no old unit: nothing", func(t *testing.T) {
		home := timerHome(t)
		calls := fakeManager(t, "systemctl", 0)
		if old, _, err := ReplaceOldTimer(home, "/p", "linux", false); err != nil || old != "" {
			t.Fatalf("old %q, err %v", old, err)
		}
		if TimerInstalledHere(home, "linux") || callsOf(t, calls) != "" {
			t.Error("a home with no old unit got a timer")
		}
	})

	t.Run("new install refused: the old unit stays", func(t *testing.T) {
		home := timerHome(t)
		calls := fakeManager(t, "systemctl", 0)
		plant(t, home)
		write(t, linuxServicePath(home), "edited by hand\n", fresh)
		if _, _, err := ReplaceOldTimer(home, "/p", "linux", false); err == nil || !strings.Contains(err.Error(), "sh-agent-dream") {
			t.Fatalf("err %v, want a refusal naming the old unit it kept", err)
		}
		for _, p := range oldFiles(home) {
			if _, err := os.Lstat(p); err != nil {
				t.Errorf("the old unit was removed although the new one was refused: %s", p)
			}
		}
		if strings.Contains(callsOf(t, calls), "disable") {
			t.Error("the old unit was stopped although the new one was refused")
		}
	})

	t.Run("legacy bare name under a scratch home: untouched", func(t *testing.T) {
		timerHome(t)
		other := t.TempDir()
		write(t, filepath.Join(other, ".local", "bin", "fyr"), "#!/bin/sh\n", fresh)
		calls := fakeManager(t, "systemctl", 0)
		legacy := filepath.Join(other, ".config", "systemd", "user", oldUnitName+".timer")
		write(t, legacy, "x", fresh)
		if old, _, err := ReplaceOldTimer(other, "/p", "linux", false); err != nil || old != "" {
			t.Fatalf("old %q, err %v", old, err)
		}
		if _, err := os.Lstat(legacy); err != nil || callsOf(t, calls) != "" {
			t.Errorf("a legacy file was touched (%v) or the manager called", err)
		}
	})

	// The operator turned the old timer off and left its files: install
	// must not turn the nightly prompt to the model provider back on
	// (security review of v2.7.0..881389a, 2026-10-25).
	t.Run("old unit turned off: its files go, no fyr timer", func(t *testing.T) {
		for _, c := range []struct{ goos, manager, probe, answer string }{
			{"linux", "systemctl", "is-enabled", "disabled"},
			{"linux", "systemctl", "is-enabled", "masked"},
			{"darwin", "launchctl", "print", `Could not find service "com.styrman.sh-agent-dream" in domain for user gui: 501`},
		} {
			home := timerHome(t)
			calls := fakeManagerOff(t, c.manager, c.probe, c.answer)
			unit, plist := oldUnitFor(home)
			paths := oldFiles(home)
			if c.goos == "darwin" {
				paths = []string{filepath.Join(home, "Library", "LaunchAgents", plist+".plist")}
				unit = plist
			}
			for _, p := range paths {
				write(t, p, "x\n", fresh)
			}
			old, replaced, err := ReplaceOldTimer(home, "/p", c.goos, true)
			if err != nil || old != unit || replaced {
				t.Fatalf("%s dry run: old %q, replaced %v, err %v", c.goos, old, replaced, err)
			}
			for _, p := range paths {
				if _, err := os.Lstat(p); err != nil {
					t.Errorf("%s: a dry run removed %s", c.goos, p)
				}
			}
			old, replaced, err = ReplaceOldTimer(home, "/p", c.goos, false)
			if err != nil || old != unit || replaced {
				t.Fatalf("%s: old %q, replaced %v, err %v", c.goos, old, replaced, err)
			}
			gone(t, paths...)
			got := callsOf(t, calls)
			if TimerInstalledHere(home, c.goos) || strings.Contains(got, "enable --now") || strings.Contains(got, "bootstrap") {
				t.Errorf("%s: a turned-off timer was replaced by a running one; calls:\n%s", c.goos, got)
			}
		}
	})

	// A probe that fails without saying "off" (no user bus over SSH, a
	// container) cannot tell: the old unit is treated as on and replaced as
	// before the probe existed, never deleted as turned off (security
	// review round 2, 2026-10-25).
	t.Run("probe cannot tell: replaced as on", func(t *testing.T) {
		home := timerHome(t)
		fakeManagerOff(t, "systemctl", "is-enabled", "Failed to connect to bus: No medium found")
		plant(t, home)
		old, replaced, err := ReplaceOldTimer(home, "/p", "linux", false)
		if err != nil || old != "sh-agent-dream" || !replaced {
			t.Fatalf("old %q, replaced %v, err %v; want it replaced as on", old, replaced, err)
		}
		if !TimerInstalledHere(home, "linux") {
			t.Error("the fyr-dream unit is not installed")
		}
	})

	t.Run("darwin", func(t *testing.T) {
		home := timerHome(t)
		calls := fakeManager(t, "launchctl", 0)
		_, oldPlist := oldUnitFor(home)
		oldPath := filepath.Join(home, "Library", "LaunchAgents", oldPlist+".plist")
		write(t, oldPath, "<plist/>\n", fresh)
		old, _, err := ReplaceOldTimer(home, "/p", "darwin", false)
		if err != nil || old != "com.styrman.sh-agent-dream" {
			t.Fatalf("old %q, err %v", old, err)
		}
		gone(t, oldPath)
		if !TimerInstalledHere(home, "darwin") {
			t.Error("com.fyr.dream not installed")
		}
		got := callsOf(t, calls)
		if b, o := strings.Index(got, "bootstrap"), strings.Index(got, "bootout"); b < 0 || o < b || !strings.Contains(got[o:], oldPath) {
			t.Errorf("launchctl calls (bootstrap the new, then bootout the old):\n%s", got)
		}
	})
}
