package metrics

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
)

func TestPopulateAdherenceVerbsCharacterization(t *testing.T) {
	t.Parallel()
	sum := &Summary{Sessions: map[string]int{"agy": 2}}
	all := []sess{
		{ID: "s1", Vendor: "agy", Label: ""},
		{ID: "s2", Vendor: "agy", Label: ""},
		{ID: "s-hist", Vendor: historical, Label: ""},
	}
	stepsByID := map[string][]stp{
		"s1": {
			// Unranged reads (including leading cd) count as skips.
			{Ext: "e1", Kind: "tool", Tool: "Bash", Verbs: []string{"cat"}},
			{Ext: "e2", Kind: "tool", Tool: "Bash", Verbs: []string{"cd", "nl"}},
			{Ext: "e3", Kind: "tool", Tool: "Bash", Verbs: []string{"bat"}},
			// Denied unranged read does NOT count as skip.
			{Ext: "e4", Kind: "tool", Tool: "Bash", Denied: true, Verbs: []string{"cat"}},
			// First ranged read allowed; immediate repeat with same hash counts as skip.
			{Ext: "e5", Kind: "tool", Tool: "Bash", Hash: "h-head", Verbs: []string{"head"}},
			{Ext: "e6", Kind: "tool", Tool: "Bash", Hash: "h-head", Verbs: []string{"head"}},
			// Successful Edit clears seenRanged; next same hash is allowed again.
			{Ext: "e7", Kind: "tool", Tool: "Edit"},
			{Ext: "e8", Kind: "tool", Tool: "Bash", Hash: "h-head", Verbs: []string{"head"}},
			// Compaction also clears seenRanged.
			{Ext: "e9", Kind: "tool", Tool: "Bash", Hash: "h-sed", Verbs: []string{"sed -n"}},
			{Ext: "e10", Kind: "compaction"},
			{Ext: "e11", Kind: "tool", Tool: "Bash", Hash: "h-sed", Verbs: []string{"sed -n"}},
			// In-place sed -i -n is not a ranged read; bat -r is ranged (first call ok).
			{Ext: "e12", Kind: "tool", Tool: "Bash", Hash: "h-sedi", Verbs: []string{"sed -i -n"}},
			{Ext: "e13", Kind: "tool", Tool: "Bash", Hash: "h-batr", Verbs: []string{"bat -r 1:20"}},
			// Additional verbs to test top-10 cap and deterministic tie-breaking.
			{Ext: "e14", Kind: "tool", Tool: "Bash", Verbs: []string{"git status", "go test", "go vet", "fyr search"}},
			{Ext: "e15", Kind: "tool", Tool: "Bash", Verbs: []string{"git status", "go test", "alpha", "beta", "gamma", "delta"}},
		},
		"s2": {
			// Duplicate (vendor, label, ext="e1") must be deduplicated across forked sessions.
			{Ext: "e1", Kind: "tool", Tool: "Bash", Verbs: []string{"cat"}},
			// Non-tool/assistant/generation step ignored.
			{Ext: "e20", Kind: "system", Tool: "Bash", Verbs: []string{"cat"}},
		},
		"s-hist": {
			// Historical vendor counts BashReadSkips but never enters TopBashVerbs.
			{Ext: "h1", Kind: "tool", Tool: "Bash", Verbs: []string{"cat"}},
			{Ext: "h2", Kind: "tool", Tool: "Bash", Verbs: []string{"hist-only-verb"}},
		},
	}

	populateAdherenceVerbs(sum, all, stepsByID)

	wantSkips := map[string]int{"agy": 4, historical: 1}
	if !reflect.DeepEqual(sum.C.BashReadSkips, wantSkips) {
		t.Fatalf("BashReadSkips = %v, want %v", sum.C.BashReadSkips, wantSkips)
	}
	if len(sum.C.TopBashVerbs) != 10 {
		t.Fatalf("len(TopBashVerbs) = %d, want 10 (%+v)", len(sum.C.TopBashVerbs), sum.C.TopBashVerbs)
	}
	for _, vc := range sum.C.TopBashVerbs {
		if vc.Verb == "hist-only-verb" {
			t.Errorf("historical verb leaked into TopBashVerbs: %+v", sum.C.TopBashVerbs)
		}
	}
	wantHead := []VerbCount{
		{Verb: "head", Count: 3},
		{Verb: "cat", Count: 2},
		{Verb: "git status", Count: 2},
		{Verb: "go test", Count: 2},
		{Verb: "sed -n", Count: 2},
	}
	if !reflect.DeepEqual(sum.C.TopBashVerbs[:len(wantHead)], wantHead) {
		t.Errorf("TopBashVerbs prefix = %+v, want %+v", sum.C.TopBashVerbs[:len(wantHead)], wantHead)
	}
}

func TestJoinRecordsCharacterization(t *testing.T) {
	t.Parallel()
	ts, ok := recordTime("HANDOFF-2026-11-27-1430-wave3")
	if !ok {
		t.Fatalf("recordTime failed on valid stamped ID")
	}
	wantBugTS := time.Date(2026, 11, 27, 0, 0, 0, 0, time.Local).UnixMilli()

	ws := []window{
		{id: "win-before", start: ts - 120_000, end: ts - 1},         // ends 1ms before minute -> excluded
		{id: "win-edge-end", start: ts - 60_000, end: ts},            // ends exactly at ts -> included
		{id: "win-edge-start", start: ts + 59_999, end: ts + 90_000}, // starts 1ms before ts+60s -> included
		{id: "win-after", start: ts + 60_000, end: ts + 120_000},     // starts at ts+60s -> excluded
	}
	sessionOf := map[string]string{
		"abcdef1234567890": "sess-bug-owner",
		"fedcba9876543210": "sess-other",
	}
	recs := []brain.Record{
		{ID: "HANDOFF-2026-11-27-1430-wave3", Kind: "handoff", Title: "Wave 3 handoff"},
		{ID: "HANDOFF-2026-11-27-nostamp", Kind: "handoff", Title: "No minute stamp"},
		{ID: "TEST-2026-11-27-1430-verify", Kind: "testing", Title: "Verification: Wave 3", Body: "## Status: PASS\nAll green.\n"},
		{ID: "TEST-2026-11-27-1430-notes", Kind: "testing", Title: "Unit notes", Body: "## Status: PASS\n"},
		{ID: "TEST-2026-11-27-1430-nostatus", Kind: "testing", Title: "Verify run", Body: "missing status line"},
		{ID: "REVIEW-2026-11-27-1430-code", Kind: "review", Body: "**[high]** a\n**[high]** b\n**[medium]** c\n**[low]** d\n"},
		{ID: "BUG-777", Kind: "bug", Date: "2026-11-27", Body: "unmatched 1111111 then abcdef1 and fedcba9"},
		{ID: "BUG-778", Kind: "bug", Date: "2026-11-27", Body: "no matching commit 9999999"},
		{ID: "ADR-537", Kind: "adr", Title: "Ignored kind"},
	}

	type emitted struct {
		sid, kind, ref, val string
		ts                  int64
	}
	var got []emitted
	err := joinRecords(recs, ws, sessionOf, func(sid, kind, ref, value string, evTS int64) error {
		got = append(got, emitted{sid: sid, kind: kind, ref: ref, val: value, ts: evTS})
		return nil
	})
	if err != nil {
		t.Fatalf("joinRecords: %v", err)
	}

	want := []emitted{
		{sid: "win-edge-end", kind: "handoff", ref: "HANDOFF-2026-11-27-1430-wave3", val: "", ts: ts},
		{sid: "win-edge-start", kind: "handoff", ref: "HANDOFF-2026-11-27-1430-wave3", val: "", ts: ts},
		{sid: "win-edge-end", kind: "verifier", ref: "TEST-2026-11-27-1430-verify", val: "PASS", ts: ts},
		{sid: "win-edge-start", kind: "verifier", ref: "TEST-2026-11-27-1430-verify", val: "PASS", ts: ts},
		{sid: "win-edge-end", kind: "review", ref: "REVIEW-2026-11-27-1430-code", val: "high=2,medium=1,low=1", ts: ts},
		{sid: "win-edge-start", kind: "review", ref: "REVIEW-2026-11-27-1430-code", val: "high=2,medium=1,low=1", ts: ts},
		{sid: "sess-bug-owner", kind: "bug", ref: "BUG-777", val: "abcdef1234567890", ts: wantBugTS},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("joinRecords events:\ngot:  %+v\nwant: %+v", got, want)
	}

	wantErr := errors.New("put failed")
	if err := joinRecords(recs[:1], ws, sessionOf, func(string, string, string, string, int64) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("joinRecords error propagation = %v, want %v", err, wantErr)
	}
}

func TestRenderReportCharacterization(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 11, 27, 15, 4, 0, 0, time.UTC)

	t.Run("populated summary renders deterministic offline HTML sections", func(t *testing.T) {
		sum := reportFixture(t)
		var b1, b2 bytes.Buffer
		if err := RenderReport(&b1, sum, nil, fixedNow); err != nil {
			t.Fatalf("RenderReport(1): %v", err)
		}
		if err := RenderReport(&b2, sum, nil, fixedNow); err != nil {
			t.Fatalf("RenderReport(2): %v", err)
		}
		if !bytes.Equal(b1.Bytes(), b2.Bytes()) {
			t.Fatalf("RenderReport is non-deterministic across repeated calls")
		}
		html := b1.String()
		for _, want := range []string{
			"<html", "</html>", "<svg",
			"2026-11-27 15:04 UTC",
			"main sessions",
			"tool calls",
			"bash read bypasses",
			"fyr search : grep",
			"Tool calls per day",
			"Tool calls by tool",
			"Subagent tool calls by type",
			"Trigger coverage",
		} {
			if !strings.Contains(html, want) {
				t.Errorf("RenderReport HTML missing %q", want)
			}
		}
		for _, forbidden := range []string{"<script", "http://", "https://"} {
			if strings.Contains(strings.ToLower(html), forbidden) {
				t.Errorf("RenderReport HTML contains forbidden offline token %q", forbidden)
			}
		}
	})

	t.Run("minimal empty summary renders deterministically without panic", func(t *testing.T) {
		empty := &Summary{
			Window:  map[string]string{"since": "2026-11-01", "now": "2026-11-27"},
			Columns: []string{"agy"},
		}
		var b1, b2 bytes.Buffer
		if err := RenderReport(&b1, empty, nil, fixedNow); err != nil {
			t.Fatalf("RenderReport(empty 1): %v", err)
		}
		if err := RenderReport(&b2, empty, nil, fixedNow); err != nil {
			t.Fatalf("RenderReport(empty 2): %v", err)
		}
		if !bytes.Equal(b1.Bytes(), b2.Bytes()) || b1.Len() == 0 {
			t.Fatalf("RenderReport(empty) non-deterministic or empty (len=%d)", b1.Len())
		}
	})
}
