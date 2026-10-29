package metrics

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The ingest line carries no skipped-lines count: it was never incremented
// (SD50 of the 2026-10-27 audit).
func TestIngestLine(t *testing.T) {
	got := Result{Sessions: 1, Steps: 2, Events: 3, Failed: []string{"x"}}.Line()
	if want := "metrics: 1 sessions, 2 steps, 3 events added; 1 sources failed"; got != want {
		t.Errorf("Line = %q, want %q", got, want)
	}
}

// A pass^k label is printed through the filter, as every other key: fyr eval
// takes it from the suite file (SD55 of the 2026-10-27 audit).
func TestWriteTextFiltersALabel(t *testing.T) {
	sum := Summary{A: Outcome{PassK: map[string]PassK{"eval=a\x1b[2Jb": {K: 1, Passed: 1, PassK: 1}}}}
	var text bytes.Buffer
	sum.WriteText(&text)
	if strings.Contains(text.String(), "\x1b") || !strings.Contains(text.String(), "pass^k eval=a?[2Jb") {
		t.Errorf("label not filtered:\n%q", text.String())
	}
}

// A tab-led terminator ends only a <<- here-doc, as in bash; for << the body
// goes on, and what follows it is never read as commands (SD56 of the
// 2026-10-27 audit).
func TestHeredocTabsOnlyForDash(t *testing.T) {
	plain := "cat <<EOF\n\tEOF\nrm -rf body-line\nEOF\necho after"
	if got := stripHeredocs(plain); strings.Contains(got, "rm -rf body-line") || !strings.Contains(got, "echo after") {
		t.Errorf("<< with a tab-led EOF:\n%s", got)
	}
	dash := "cat <<-EOF\n\tbody\n\tEOF\necho after"
	if got := stripHeredocs(dash); strings.Contains(got, "body") || !strings.Contains(got, "echo after") {
		t.Errorf("<<- with a tab-led EOF:\n%s", got)
	}
}

// A render that fails leaves no file, so the rerun is not refused as an
// overwrite (SD57 of the 2026-10-27 audit).
func TestWriteReportLeavesNoFileOnAFailedRender(t *testing.T) {
	saved := renderReport
	t.Cleanup(func() { renderReport = saved })
	renderReport = func(w io.Writer, sum *Summary, now time.Time) error {
		io.WriteString(w, "<html>half")
		return errors.New("render failed")
	}
	f := filepath.Join(t.TempDir(), "r", "metrics.html")
	if err := WriteReport(&Summary{}, f, time.Now()); err == nil {
		t.Fatal("a failed render reported success")
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Errorf("a failed render left %s: %v", f, err)
	}
}
