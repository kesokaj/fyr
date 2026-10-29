package serve

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestDreamLog(t *testing.T) {
	f := newFix(t)
	r := f.run(t)
	log := filepath.Join(f.home, harness.StateDir, "dream.log")
	dreamPage := func() string { return get(t, r, "GET", "/dream").Body.String() }

	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dreamPage(), "no dream log yet") {
		t.Error("an absent log is not named")
	}

	var b strings.Builder
	for i := range 300 {
		fmt.Fprintf(&b, "line %03d\n", i)
	}
	write(t, log, b.String())
	page := dreamPage()
	if !strings.Contains(page, "line 299") || !strings.Contains(page, "line 100\n") || strings.Contains(page, "line 099") {
		t.Errorf("over 200 lines: not the last 200\n%s", page)
	}

	// Over 256 KiB: the window's partial first line is dropped, and only the
	// window is read.
	big := strings.Repeat("x", dreamWindow) + "\nHEAD-OF-WINDOW-PARTIAL\nlast line\n"
	write(t, log, "first line, outside the window\n"+big)
	if got := readDreamLog(log); !strings.HasSuffix(got, "HEAD-OF-WINDOW-PARTIAL\nlast line\n") || strings.Contains(got, "first line") || strings.Contains(got, "xxxx") {
		t.Errorf("over 256 KiB: %q", got[max(0, len(got)-80):])
	}

	write(t, log, "bell \x07 and escape \x1b[2J\n")
	if got := readDreamLog(log); strings.ContainsAny(got, "\x07\x1b") {
		t.Errorf("a control byte reached the page: %q", got)
	}
}
