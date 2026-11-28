package show

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

func TestParseFileHashRangeAndReadIntervalMustPassAndMustFail(t *testing.T) {
	t.Parallel()
	ranged, base, s, e := ParseFileHashRange("r:abc123:10-20")
	if !ranged || base != "abc123" || s != 10 || e != 20 {
		t.Fatalf("ParseFileHashRange(ranged) = (%v,%q,%d,%d)", ranged, base, s, e)
	}
	ranged, base, s, e = ParseFileHashRange("f:abc123")
	if ranged || base != "abc123" || s != 1 || e <= 1 {
		t.Fatalf("ParseFileHashRange(full) = (%v,%q,%d,%d)", ranged, base, s, e)
	}
	if r, b, _, _ := ParseFileHashRange("invalid"); r || b != "" {
		t.Fatalf("ParseFileHashRange(invalid) = (%v,%q)", r, b)
	}
	seen := map[string][][2]int{}
	if RecordReadInterval(seen, "a", 1, 20) {
		t.Fatal("first interval should not be redundant")
	}
	if RecordReadInterval(seen, "a", 21, 40) {
		t.Fatal("disjoint interval should not be redundant")
	}
	if !RecordReadInterval(seen, "a", 10, 29) {
		t.Fatal("overlapping interval >=50% should be redundant")
	}
	if RecordReadInterval(seen, "", 1, 10) || RecordReadInterval(seen, "a", 20, 10) {
		t.Fatal("invalid intervals must return false")
	}
}

func TestSummaryAddFinishNumbersAndWrite(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	now := since.Add(48 * time.Hour)
	sum := NewSummary(since, now)
	sum.B.ContextTarget = DefaultContextTarget
	x := Sess{ID: "s1", Vendor: "agy", Start: since.UnixMilli(), End: since.Add(10 * time.Minute).UnixMilli()}
	steps := []Stp{
		{Ext: "1", Kind: "user", TS: since.UnixMilli()},
		{Ext: "2", Kind: "tool", Tool: "Read", Hash: "r:f1:1-20", TS: since.Add(time.Minute).UnixMilli()},
		{Ext: "3", Kind: "tool", Tool: "Read", Hash: "r:f1:1-20", TS: since.Add(2 * time.Minute).UnixMilli()},
		{Ext: "4", Kind: "tool", Tool: "Bash", Hash: "h1", Verbs: []string{"fyr search foo"}, VerbNs: []int{0}, TS: since.Add(3 * time.Minute).UnixMilli()},
		{Ext: "5", Kind: "tool", Tool: "Grep", TS: since.Add(4 * time.Minute).UnixMilli()},
		{Ext: "6", Kind: "generation", In: 100, Out: 50, Ctx: 150, TS: since.Add(5 * time.Minute).UnixMilli()},
	}
	sum.Add(x, steps)
	sum.Finish([]Sess{x}, nil, nil, Estimators{})
	if sum.Sessions["agy"] != 1 || sum.C.SQLiteVerbs["agy"] != 1 || sum.C.GrepCalls["agy"] != 1 || sum.C.RedundantReads["agy"] != 1 {
		t.Fatalf("unexpected summary counters: sessions=%v sqlite=%v grep=%v red=%v", sum.Sessions, sum.C.SQLiteVerbs, sum.C.GrepCalls, sum.C.RedundantReads)
	}
	nums := sum.Numbers()
	if nums["sessions.agy"] != 1 || nums["C.redundant_reads.agy"] != 1 {
		t.Fatalf("unexpected Numbers(): %v", nums)
	}
	var txt, js bytes.Buffer
	sum.WriteText(&txt)
	if !strings.Contains(txt.String(), "metrics 2026-11-01 .. 2026-11-03") {
		t.Fatalf("unexpected text output: %q", txt.String())
	}
	if err := sum.WriteJSON(&js); err != nil || !strings.Contains(js.String(), `"sessions"`) {
		t.Fatalf("WriteJSON err=%v out=%q", err, js.String())
	}
}

func TestSubagentCapAndRoleCohortAccumulation(t *testing.T) {
	t.Parallel()
	if got := SubagentCapForRole("reviewer"); got != 40 {
		t.Errorf("SubagentCapForRole(reviewer) = %d, want 40", got)
	}
	if got := SubagentCapForRole("builder"); got != 120 {
		t.Errorf("SubagentCapForRole(builder) = %d, want 120", got)
	}
	all := []Sess{{ID: "s1", Vendor: "agy", Agent: "builder"}}
	steps := map[string][]Stp{
		"s1": {
			{Ext: "1", Kind: "tool", Tool: "Bash", Verbs: []string{"cd dir", "rg foo"}, VerbNs: []int{0, 1}},
			{Ext: "1", Kind: "tool", Tool: "Bash", Verbs: []string{"cd dir", "rg foo"}, VerbNs: []int{0, 1}}, // duplicate skipped
			{Ext: "2", Kind: "tool", Tool: "Bash", Verbs: []string{"cat foo.go"}, VerbNs: []int{0}},
		},
	}
	roles := BuildAgentRoles(all, steps, map[string][2]int{"s1": {2, 1}})
	if len(roles) != 1 || roles[0].ToolCalls != 2 || roles[0].GrepCalls != 1 || roles[0].TimedOutTurns != 1 {
		t.Fatalf("unexpected roles: %+v", roles)
	}
}

func TestSD137AutoStateInWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -7)
	if autoStateInWindow(time.Time{}, "", since, now) {
		t.Fatal("empty lastRun and empty date must return false")
	}
	if !autoStateInWindow(now.Add(-time.Hour), "", since, now) || autoStateInWindow(since.Add(-time.Hour), "", since, now) {
		t.Fatal("lastRun window check wrong")
	}
	if !autoStateInWindow(time.Time{}, "2026-11-18", since, now) || autoStateInWindow(time.Time{}, "bad-date", since, now) {
		t.Fatal("date fallback check wrong")
	}
}

func TestStructuralAccSplitCoupling(t *testing.T) {
	t.Parallel()
	var acc structuralAcc
	acc.add(search.StructuralSignals{HiddenCouplingPairs: 2, HiddenCouplingEditPairs: 1, HiddenCouplingReadPairs: 1, TotalCodeFiles: 4})
	acc.add(search.StructuralSignals{HiddenCouplingPairs: 3, HiddenCouplingEditPairs: 2, HiddenCouplingReadPairs: 1, TotalCodeFiles: 4})
	got := acc.finish()
	if got.HiddenCouplingPairs != 5 || got.HiddenCouplingEditPairs != 3 || got.HiddenCouplingReadPairs != 2 {
		t.Fatalf("structuralAcc split mismatch: %+v", got)
	}
}
