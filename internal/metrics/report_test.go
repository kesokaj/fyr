package metrics

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func reportFixture(t *testing.T) *Summary {
	t.Helper()
	chome, _ := claudeFixture(t)
	ahome, _ := agyFixture(t)
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Home: chome, HomeGiven: true, Vendor: "claude"})
	ingestT(t, s, Options{Home: ahome, HomeGiven: true, Vendor: "agy"})
	ingestT(t, s, Options{Source: sandboxCopy(t), Label: "arm=lean", Outcome: "pass", Vendor: "claude"})
	sum, err := s.Show(ShowOptions{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), AllLabels: true})
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

// The report is self-contained (no script, no host) and its numbers are
// show's: the summary rows and the per-day tables add up to the summary.
func TestReportSelfContainedAndConsistent(t *testing.T) {
	sum := reportFixture(t)
	var buf bytes.Buffer
	if err := RenderReport(&buf, sum, time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, bad := range []string{"<script", "http://", "https://", "@import", "url("} {
		if strings.Contains(strings.ToLower(html), bad) {
			t.Errorf("report contains %q", bad)
		}
	}
	for _, want := range []string{"Tool calls per day", "Tool calls by tool", "Subagent tool calls by type", "Sandbox runs", "<title>Claude Code, ", "prefers-color-scheme:dark", "n/a"} {
		if !strings.Contains(html, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	// The summary table's tool-call row is show's B.tool_calls.
	row := regexp.MustCompile(`<td>tool calls</td><td>(\d+)</td><td>(\d+)</td>`).FindStringSubmatch(html)
	if row == nil || row[1] != strconv.Itoa(sum.B.ToolCalls["claude"]) || row[2] != strconv.Itoa(sum.B.ToolCalls["agy"]) {
		t.Errorf("summary tool-call row %v, want %d and %d", row, sum.B.ToolCalls["claude"], sum.B.ToolCalls["agy"])
	}
	// The per-day table sums to the same totals.
	for _, v := range []string{"claude", "agy"} {
		n := 0
		for _, d := range sum.Daily {
			n += d.ToolCalls[v]
		}
		if n != sum.B.ToolCalls[v] {
			t.Errorf("%s: daily tool calls sum to %d, summary says %d", v, n, sum.B.ToolCalls[v])
		}
	}
	if !strings.Contains(html, "1 of 1 · pass^1 = 1") {
		t.Error("the pass^k chart does not show the label's run")
	}
}

// A label or tool name is text, never markup.
func TestReportEscapesNames(t *testing.T) {
	sum := reportFixture(t)
	sum.B.ToolCallsByTool["agy"][`agy:<img src=x onerror=alert(1)>`] = 99
	var buf bytes.Buffer
	if err := RenderReport(&buf, sum, time.Now()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "<img") {
		t.Fatal("a tool name reached the page unescaped")
	}
}

func TestWriteReportRefusesOverwrite(t *testing.T) {
	sum := reportFixture(t)
	f := filepath.Join(t.TempDir(), "r", "m.html")
	if err := WriteReport(sum, f, time.Now()); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	if err := WriteReport(sum, f, time.Now()); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second write: %v", err)
	}
}

// What a CLI cannot produce is not drawn as 0: Antigravity has no cost, so
// the cost chart has no Antigravity points; a day with no grep call has no
// search:grep point. Nothing the template sanitised away is left in the page.
func TestReportDrawsNoMissingValues(t *testing.T) {
	sum := reportFixture(t)
	var buf bytes.Buffer
	if err := RenderReport(&buf, sum, time.Now()); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if strings.Contains(html, "ZgotmplZ") {
		t.Error("the template sanitised a value away (ZgotmplZ)")
	}
	section := func(title string) string {
		for _, s := range strings.Split(html, "<section>") {
			if strings.Contains(s, "<h2>"+title) {
				return s
			}
		}
		t.Fatalf("no section %q", title)
		return ""
	}
	if strings.Contains(section("Cost per day"), "<title>Antigravity") {
		t.Error("the cost chart draws Antigravity, which exposes no cost")
	}
	if strings.Contains(section("sh-agent search : grep"), "<title>Antigravity") {
		t.Error("the ratio chart draws Antigravity days that have no grep call")
	}
}

// The summary table carries the over-target row as "k of n" per CLI, from the
// same Summary as show (plan 2026-10-23-session-sized-work, Phase 2).
func TestReportContextTargetRow(t *testing.T) {
	sum := reportFixture(t)
	var buf bytes.Buffer
	if err := RenderReport(&buf, sum, time.Now()); err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`<td>sessions over context target \(300000\)</td><td>([^<]*)</td><td>([^<]*)</td>`).FindStringSubmatch(buf.String())
	if row == nil {
		t.Fatalf("the summary table has no over-target row:\n%s", buf.String())
	}
	for i, v := range []string{"claude", "agy"} {
		want := fmt.Sprintf("%d of %d", *sum.B.SessionsOverContextTarget[v], len(sum.B.SessionPeaks[v]))
		if row[i+1] != want {
			t.Errorf("%s over-target cell %q, want %q", v, row[i+1], want)
		}
	}
	sum.B.SessionsOverContextTarget["agy"] = nil
	buf.Reset()
	RenderReport(&buf, sum, time.Now())
	if !regexp.MustCompile(`<td>sessions over context target \(300000\)</td><td>[^<]*</td><td>n/a</td>`).MatchString(buf.String()) {
		t.Error("a vendor with no context value is not drawn as n/a")
	}
}
