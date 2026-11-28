package report

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestComputeSignalTransitionsAndInfoFiltering(t *testing.T) {
	t.Parallel()
	prev := &DailyDigest{
		Day: "2026-11-27",
		Signals: []DigestAction{
			{SignalID: "sig:global:a", Severity: "high"},
			{SignalID: "sig:global:b", Severity: "medium"},
			{SignalID: "sig:global:info_old", Severity: SeverityInfo},
		},
	}
	cur := []DigestAction{
		{SignalID: "sig:global:a", Severity: "high"},
		{SignalID: "sig:global:c", Severity: "medium"},
		{SignalID: "sig:global:info_new", Severity: SeverityInfo},
	}
	newReg, resolved, persistHigh := ComputeSignalTransitions(cur, prev)
	if !reflect.DeepEqual(newReg, []string{"sig:global:c"}) {
		t.Errorf("newReg = %v, want [sig:global:c]", newReg)
	}
	if !reflect.DeepEqual(resolved, []string{"sig:global:b"}) {
		t.Errorf("resolved = %v, want [sig:global:b]", resolved)
	}
	if !reflect.DeepEqual(persistHigh, []string{"sig:global:a"}) {
		t.Errorf("persistHigh = %v, want [sig:global:a]", persistHigh)
	}
	n, r, p := ComputeSignalTransitions(cur, nil)
	if n != nil || r != nil || p != nil {
		t.Errorf("nil prev should return nil slices, got %v %v %v", n, r, p)
	}
	if got := ClipPrintable("  gemini-4-neon-xhigh \n ", TrialTextLimit); got != "gemini-4-neon-xhigh" {
		t.Errorf("ClipPrintable = %q, want gemini-4-neon-xhigh", got)
	}
}

func TestDailyDigestFailOpenIOAndRender(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	d := DailyDigest{
		Day:     "2026-11-28",
		Verdict: VerdictSteady,
		Signals: []DigestAction{{SignalID: "sig:global:info_1", Severity: SeverityInfo, Rule: "model_shift", ActionScope: ScopeProposeOnly, Summary: "info note"}},
	}
	if err := WriteLatestDailyDigest("", d); err == nil {
		t.Fatal("WriteLatestDailyDigest empty home succeeded, want error")
	}
	if err := WriteLatestDailyDigest(home, d); err != nil {
		t.Fatalf("WriteLatestDailyDigest: %v", err)
	}
	got, err := ReadLatestDailyDigest(home)
	if err != nil || got == nil || got.Day != d.Day {
		t.Fatalf("ReadLatestDailyDigest = %+v, %v", got, err)
	}
	txt := RenderDailyDigestText(*got)
	if !strings.Contains(txt, "Daily Health Digest (2026-11-28): steady") || !strings.Contains(txt, "Info:") {
		t.Errorf("unexpected digest text:\n%s", txt)
	}
	var js bytes.Buffer
	if err := RenderDailyDigestJSON(&js, *got); err != nil || !strings.Contains(js.String(), `"day": "2026-11-28"`) {
		t.Errorf("RenderDailyDigestJSON = %v, %q", err, js.String())
	}
	p := LatestDailyDigestPath(home)
	target := filepath.Join(home, "outside.json")
	if err := os.WriteFile(target, []byte(`{"day":"2026-11-28"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(p)
	if err := os.Symlink(target, p); err != nil {
		t.Skip(err)
	}
	if sym, err := ReadLatestDailyDigest(home); err != nil || sym != nil {
		t.Errorf("symlink digest = %+v, %v; want nil, nil", sym, err)
	}
}
