package brain

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

const (
	maxResumeDigestBytes = 64 << 10
	maxResumeDigestOut   = 1500
)

type resumeDigestView struct {
	Day     string               `json:"day"`
	Verdict string               `json:"verdict"`
	Actions []resumeDigestAction `json:"actions"`
	Signals []resumeDigestSignal `json:"signals"`
}

type resumeDigestAction struct {
	SignalID       string `json:"signal_id"`
	ActionScope    string `json:"action_scope"`
	Status         string `json:"status"`
	InstallPending bool   `json:"install_pending"`
	RevertCmd      string `json:"revert_cmd"`
}

type resumeDigestSignal struct {
	SignalID          string `json:"signal_id"`
	Severity          string `json:"severity"`
	Rule              string `json:"rule"`
	ActionScope       string `json:"action_scope"`
	RecommendedAction string `json:"recommended_action"`
	Summary           string `json:"summary"`
}

func readResumeDigest(home string) (resumeDigestView, bool) {
	if strings.TrimSpace(home) == "" {
		return resumeDigestView{}, false
	}
	f, err := trail.OpenRegular(filepath.Join(home, harness.StateDir, "reports", "latest-daily-digest.json"))
	if err != nil {
		return resumeDigestView{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxResumeDigestBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxResumeDigestBytes {
		return resumeDigestView{}, false
	}
	var d resumeDigestView
	if json.Unmarshal(data, &d) != nil || strings.TrimSpace(d.Day) == "" || strings.TrimSpace(d.Verdict) == "" {
		return resumeDigestView{}, false
	}
	return d, true
}

func writeResumeDailyDigest(b *strings.Builder, home string) {
	d, ok := readResumeDigest(home)
	if !ok {
		return
	}
	var sec strings.Builder
	fmt.Fprintf(&sec, "\n## Daily Health & Auto Actions\nDirection: %s (%s)\n",
		cleanResumeField(d.Verdict), cleanResumeField(d.Day))
	appendResumeDigestActions(&sec, d.Actions)
	appendResumeDigestSignals(&sec, d.Signals)
	b.WriteString(truncateResumeDigest(sec.String(), maxResumeDigestOut))
}

func appendResumeDigestActions(sec *strings.Builder, actions []resumeDigestAction) {
	if len(actions) == 0 {
		sec.WriteString("I did: none\n")
		return
	}
	for i, a := range actions {
		if i >= 4 {
			break
		}
		line := fmt.Sprintf("I did: %s [%s/%s]",
			cleanResumeField(a.SignalID), cleanResumeField(a.ActionScope), cleanResumeField(a.Status))
		if a.InstallPending {
			line += " [install_pending]"
		}
		if a.RevertCmd != "" {
			line += " (" + cleanResumeField(a.RevertCmd) + ")"
		}
		sec.WriteString(line + "\n")
	}
}

func appendResumeDigestSignals(sec *strings.Builder, signals []resumeDigestSignal) {
	if len(signals) == 0 {
		sec.WriteString("We need to do: none\n")
		return
	}
	for i, s := range signals {
		if i >= 5 {
			break
		}
		act := cleanResumeField(s.RecommendedAction)
		if act == "" {
			act = cleanResumeField(s.Summary)
		}
		fmt.Fprintf(sec, "We need to do: [%s/%s] %s (%s) — %s\n",
			cleanResumeField(s.Severity), cleanResumeField(s.ActionScope),
			cleanResumeField(s.Rule), cleanResumeField(s.SignalID), act)
	}
}

func truncateResumeDigest(out string, maxBytes int) string {
	if len(out) <= maxBytes && strings.HasSuffix(out, "\n") {
		return out
	}
	limit := maxBytes - 1
	if limit < 0 {
		return ""
	}
	for len(out) > limit {
		_, sz := utf8.DecodeLastRuneInString(out)
		if sz <= 0 {
			break
		}
		out = out[:len(out)-sz]
	}
	return out + "\n"
}

func cleanResumeField(s string) string {
	return strings.Join(strings.Fields(search.Printable(s)), " ")
}
