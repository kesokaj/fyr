package dream

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// nightlyTimer is the timer unit fyr rendered before the visible dream.
const nightlyTimer = `[Unit]
Description=Run fyr dream nightly

[Timer]
OnCalendar=*-*-* 03:00
Persistent=true

[Install]
WantedBy=timers.target
`

// installed writes this fyr's units for goos under home, with a PATH that
// needs unescaping, and returns them by path.
func installed(t *testing.T, home, goos string) map[string]string {
	t.Helper()
	bin, path := filepath.Join(home, harness.BinPath), "/usr/bin:/o&p/%x"
	files := map[string]string{}
	if goos == "darwin" {
		_, label := UnitFor(home)
		files[macPlistPath(home)] = renderLaunchd(label, bin, home, path)
	} else {
		service, timer := renderSystemd(bin, home, path)
		files[linuxServicePath(home)], files[linuxTimerPath(home)] = service, timer
	}
	for p, text := range files {
		write(t, p, text, fresh)
	}
	return files
}

func TestTimerStale(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		home := t.TempDir()
		if timerStale(home, goos) {
			t.Errorf("%s: no unit is stale", goos)
		}
		installed(t, home, goos)
		if timerStale(home, goos) {
			t.Errorf("%s: today's render is stale", goos)
		}
	}
	home := t.TempDir()
	installed(t, home, "linux")
	write(t, linuxTimerPath(home), nightlyTimer, fresh)
	if !timerStale(home, "linux") {
		t.Error("the nightly timer unit is not stale")
	}
	home = t.TempDir()
	files := installed(t, home, "linux")
	write(t, linuxServicePath(home), strings.Replace(files[linuxServicePath(home)], "the hourly background dreamer, while an agy session runs", "the nightly background dreamer", 1), fresh)
	if !timerStale(home, "linux") {
		t.Error("the nightly service unit is not stale")
	}
	home = t.TempDir()
	files = installed(t, home, "darwin")
	nightly := strings.Replace(files[macPlistPath(home)], "<dict><key>Minute</key>", "<dict><key>Hour</key><integer>3</integer><key>Minute</key>", 1)
	write(t, macPlistPath(home), nightly, fresh)
	if !timerStale(home, "darwin") {
		t.Error("the nightly plist is not stale")
	}
}

func TestStatusLineOlderSchedule(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no user timer on " + runtime.GOOS)
	}
	home := t.TempDir()
	files := installed(t, home, runtime.GOOS)
	if got := StatusLine(home); got != "never run; timer installed" {
		t.Errorf("today's unit: %q", got)
	}
	for p, text := range files {
		write(t, p, text+"# edited\n", fresh)
	}
	want := "never run; timer installed, older schedule: fyr dream --remove-timer --home '" + home + "', then fyr dream --install-timer --home '" + home + "'"
	if got := StatusLine(home); got != want {
		t.Errorf("an older unit under a scratch home:\n got %q\nwant %q", got, want)
	}
	real, old := t.TempDir(), userHomeDir
	userHomeDir = func() (string, error) { return real, nil }
	t.Cleanup(func() { userHomeDir = old })
	for p, text := range installed(t, real, runtime.GOOS) {
		write(t, p, text+"# edited\n", fresh)
	}
	if got := StatusLine(real); got != "never run; timer installed, older schedule: fyr dream --remove-timer, then fyr dream --install-timer" {
		t.Errorf("an older unit under the real home: %q", got)
	}
}
