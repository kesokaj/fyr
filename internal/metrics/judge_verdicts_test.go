package metrics

import (
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"
)

func TestV910InferWorkerAttribution(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	s := openT(t, Path(home))
	now := time.Now().UTC()
	projA := filepath.Join(t.TempDir(), "proj-a")
	projB := filepath.Join(t.TempDir(), "proj-b")
	staleProj := filepath.Join(t.TempDir(), "proj-stale")

	for _, row := range []struct {
		id, root, model, agent string
		ended                  int64
	}{
		{"S-RECENT", projA, "gemini-4-neon-high", "", now.Add(-10 * time.Minute).UnixMilli()},
		{"S-REV", projA, "claude-opus-5-5-high", "reviewer", now.Add(-2 * time.Minute).UnixMilli()},
		{"S-OTHER", projB, "claude-sonnet-5-5", "", now.Add(-5 * time.Minute).UnixMilli()},
		{"S-STALE", staleProj, "old-model", "", now.Add(-72 * time.Hour).UnixMilli()},
	} {
		if _, err := s.db.Exec(
			`INSERT INTO sessions(id, vendor, project_root, model, agent_type, started_at, ended_at) VALUES (?, 'agy', ?, ?, ?, ?, ?)`,
			row.id, row.root, row.model, row.agent, row.ended-60_000, row.ended,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(
		`INSERT INTO work_waves(stamp, worker, project_root, state, model, updated_at) VALUES ('2026-11-23-0500', 'slice-wave', ?, 'PASS', 'gemini-4-carbon-high', ?)`,
		projA, now.Add(-5*time.Minute).Unix(),
	); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Branch target must resolve via project_root + recent window, ignoring judge subagent.
	mod, sid := InferWorkerAttribution(home, projA, "work/v910-p9-slice")
	if mod != "gemini-4-neon-high" || sid != "S-RECENT" {
		t.Fatalf("InferWorkerAttribution(branch target) = (%q, %q), want (gemini-4-neon-high, S-RECENT)", mod, sid)
	}

	// Empty target still resolves via project_root + recent window.
	mod, sid = InferWorkerAttribution(home, projA, "")
	if mod != "gemini-4-neon-high" || sid != "S-RECENT" {
		t.Fatalf("InferWorkerAttribution(empty target) = (%q, %q), want (gemini-4-neon-high, S-RECENT)", mod, sid)
	}

	// Explicit work_waves worker match takes precedence for model.
	mod, _ = InferWorkerAttribution(home, projA, "work/slice-wave")
	if mod != "gemini-4-carbon-high" {
		t.Fatalf("InferWorkerAttribution(wave worker) = %q, want gemini-4-carbon-high", mod)
	}

	// Must-fail: stale session outside recent window is NOT attributed.
	if mod, sid := InferWorkerAttribution(home, staleProj, "work/old-slice"); mod != "" || sid != "" {
		t.Errorf("stale session was attributed: (%q, %q), want empty", mod, sid)
	}

	// Must-fail: unrelated project with no sessions returns empty.
	if mod, sid := InferWorkerAttribution(home, filepath.Join(t.TempDir(), "unknown"), "work/x"); mod != "" || sid != "" {
		t.Errorf("unrelated project attributed: (%q, %q), want empty", mod, sid)
	}
}

func TestClipPrintableUTF8Bounds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"  αβγ  ", 4, "αβ"},
		{"αβγ", 3, "α"},
		{"αβγ", 0, ""},
		{"abc", -1, ""},
		{"ok", 10, "ok"},
	}
	for _, tc := range cases {
		got := clipPrintable(tc.in, tc.maxLen)
		if got != tc.want || !utf8.ValidString(got) {
			t.Errorf("clipPrintable(%q, %d) = %q (valid=%v), want %q", tc.in, tc.maxLen, got, utf8.ValidString(got), tc.want)
		}
	}
}
