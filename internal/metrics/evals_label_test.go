package metrics

import (
	"strings"
	"testing"
)

// TestEvalTableFiltersTheRowName: a label stored through `metrics ingest
// --label` is no suite id, so it may hold control bytes (the v5.5.0 gate).
func TestEvalTableFiltersTheRowName(t *testing.T) {
	e := &EvalRows{Rows: []EvalRow{{Row: "a\x1b[2Jb\tc"}}}
	cell := e.Table()[0][0]
	if strings.ContainsAny(cell, "\x1b\t") {
		t.Errorf("row cell %q holds a control byte", cell)
	}
}
