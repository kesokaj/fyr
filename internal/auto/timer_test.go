package auto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
)

// fakeManagers puts recording systemctl and launchctl first on PATH, exit
// code: the real user manager is never reached (R6).
func fakeManagers(t *testing.T, code string) (calls string) {
	t.Helper()
	bin := t.TempDir()
	calls = filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\necho \"$0 $*\" >> \"" + calls + "\"\nexit " + code + "\n"
	for _, m := range []string{"systemctl", "launchctl"} {
		if err := os.WriteFile(filepath.Join(bin, m), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	return calls
}

// homeWithBinary is a scratch home a timer can point at.
func homeWithBinary(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, harness.BinPath)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The systemd text whole, on any host: R10 asks for both systems, and goos
// is a parameter, so neither text needs its own machine.
func TestAutoTimerSystemdText(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	if err := unit.Install(home, "/p:/q", "linux"); err != nil {
		t.Fatal(err)
	}
	name, _ := unit.Names(home)
	if !strings.HasPrefix(name, "fyr-auto-") {
		t.Errorf("a scratch home's unit is %q; it must not be the bare name (R6)", name)
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	bin := filepath.Join(home, harness.BinPath)
	want := `[Unit]
Description=fyr auto: the hourly idle-time run, while the operator has it on (R3)
ConditionACPower=true

[Service]
Type=oneshot
TimeoutStartSec=3h
Nice=19
IOSchedulingClass=idle
Environment="PATH=/p:/q"
ExecStart="` + bin + `" auto run --home "` + home + `"
`
	if got := readFile(t, filepath.Join(dir, name+".service")); got != want {
		t.Errorf("service:\n%s\nwant:\n%s", got, want)
	}
	wantTimer := `[Unit]
Description=Run fyr auto hourly

[Timer]
OnCalendar=hourly
Persistent=true
RandomizedDelaySec=10m

[Install]
WantedBy=timers.target
`
	if got := readFile(t, filepath.Join(dir, name+".timer")); got != wantTimer {
		t.Errorf("timer:\n%s\nwant:\n%s", got, wantTimer)
	}
}

func TestAutoTimerPlistText(t *testing.T) {
	home := homeWithBinary(t)
	calls := fakeManagers(t, "0")
	if err := unit.Install(home, "/p:/q", "darwin"); err != nil {
		t.Fatal(err)
	}
	_, label := unit.Names(home)
	if !strings.HasPrefix(label, "com.fyr.auto-") {
		t.Errorf("a scratch home's label is %q", label)
	}
	got := readFile(t, filepath.Join(home, "Library", "LaunchAgents", label+".plist"))
	for _, want := range []string{
		"<string>auto</string><string>run</string><string>--home</string><string>" + home + "</string>",
		"<key>ProcessType</key><string>Background</string>",
		"<key>LowPriorityIO</key><true/>",
		"<key>Nice</key><integer>15</integer>",
		"<dict><key>Minute</key><integer>0</integer></dict>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the plist lacks %q:\n%s", want, got)
		}
	}
	if !strings.Contains(readFile(t, calls), "bootstrap") {
		t.Error("launchctl was not called")
	}
}

// A refusal names auto's own verbs: the dream's would send the operator to a
// verb that cannot touch this unit.
func TestAutoTimerRefusalNamesAuto(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	name, _ := unit.Names(home)
	service := filepath.Join(home, ".config", "systemd", "user", name+".service")
	if err := os.MkdirAll(filepath.Dir(service), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(service, []byte("edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := unit.Check(home, "/p", "linux")
	if err == nil || !strings.Contains(err.Error(), "fyr auto on:") || !strings.Contains(err.Error(), "fyr auto off") || strings.Contains(err.Error(), "dream") {
		t.Errorf("refusal: %v", err)
	}
	if err := unit.Check(t.TempDir(), "/p", "linux"); err == nil || !strings.Contains(err.Error(), "fyr auto on: no binary at") {
		t.Errorf("no binary: %v", err)
	}
}

// The two timers of one home are two units: removing one leaves the other.
func TestAutoTimerApartFromTheDream(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	if err := dream.InstallTimer(home, "/p", "linux"); err != nil {
		t.Fatal(err)
	}
	if err := unit.Install(home, "/p", "linux"); err != nil {
		t.Fatal(err)
	}
	if removed, err := unit.Remove(home, "linux"); err != nil || !removed {
		t.Fatalf("remove auto: %v, %v", removed, err)
	}
	if !dream.TimerInstalledHere(home, "linux") {
		t.Error("removing auto's timer took the dream's with it")
	}
	if unit.InstalledHere(home, "linux") {
		t.Error("auto's timer is still there")
	}
}
