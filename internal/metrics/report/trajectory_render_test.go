package report

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrajectoriesRenderAndRedactHomePath(t *testing.T) {
	t.Parallel()
	var empty bytes.Buffer
	WriteTrajectoriesText(&empty, nil)
	if strings.TrimSpace(empty.String()) != "(no sessions)" {
		t.Errorf("empty text = %q", empty.String())
	}
	var js bytes.Buffer
	if err := WriteTrajectoriesJSON(&js, nil); err != nil || strings.TrimSpace(js.String()) != "[]" {
		t.Errorf("empty JSON = %q, err=%v", js.String(), err)
	}
	const knee = 180000
	tt := []int64{100000, 200000}
	list := []SessionTrajectory{{
		SessionID:      "abcdef1234567890",
		Project:        "/work/demo",
		Model:          "gemini-3",
		StartedAt:      "2026-11-28T10:00:00Z",
		DurationSec:    42,
		PeakContextTok: 200000,
		KneeTokens:     knee,
		CacheHitPct:    80,
		Steps:          5,
		TurnTokens:     tt,
		Anomalies:      []string{"KNEE_EXCEEDED"},
		Subagents:      []SessionTrajectory{{SessionID: "sub123456789", AgentType: "builder", Model: "gemini-3", PeakContextTok: 5000}},
	}}
	var txt bytes.Buffer
	WriteTrajectoriesText(&txt, list)
	for _, want := range []string{"abcdef12", "KNEE_EXCEEDED", "t1=100.0k -> t2=200.0k", "sub12345 (builder)"} {
		if !strings.Contains(txt.String(), want) {
			t.Errorf("trajectory text missing %q:\n%s", want, txt.String())
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		if got := RedactHomePath(filepath.Join(home, "src", "fyr")); got != filepath.Join("~", "src", "fyr") {
			t.Errorf("RedactHomePath = %q", got)
		}
	}
	if got := RedactHomePath(""); got != "" {
		t.Errorf("RedactHomePath empty = %q", got)
	}
}
