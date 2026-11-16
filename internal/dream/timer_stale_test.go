package dream

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func installed(t *testing.T, home, goos string) map[string]string {
	t.Helper()
	files := map[string]string{}
	switch goos {
	case "linux":
		svc, tmr := renderSystemd("/p/fyr", home, "/p")
		files[linuxServicePath(home)] = svc
		files[linuxTimerPath(home)] = tmr
	case "darwin":
		_, plist := UnitFor(home)
		files[macPlistPath(home)] = renderLaunchd(plist, "/p/fyr", home, "/p")
	}
	for p, text := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func TestStatusLineLegacyDreamTimer(t *testing.T) {
	home := t.TempDir()
	if got := StatusLine(home); got != "never run; merged into fyr auto" {
		t.Fatalf("without unit: %q", got)
	}
	installed(t, home, runtime.GOOS)
	q := shellQuote(home)
	wantScratch := "never run; legacy dream timer installed: run fyr auto on --home " + q + " or fyr auto off --home " + q
	if got := StatusLine(home); got != wantScratch {
		t.Errorf("scratch unit:\ngot  %q\nwant %q", got, wantScratch)
	}

	real := t.TempDir()
	old := userHomeDir
	userHomeDir = func() (string, error) { return real, nil }
	t.Cleanup(func() { userHomeDir = old })

	if got := StatusLine(real); got != "never run; merged into fyr auto" {
		t.Fatalf("real without unit: %q", got)
	}
	installed(t, real, runtime.GOOS)
	wantReal := "never run; legacy dream timer installed: run fyr auto on or fyr auto off"
	if got := StatusLine(real); got != wantReal {
		t.Errorf("real unit:\ngot  %q\nwant %q", got, wantReal)
	}
}
