package dream

import (
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
)

const (
	maxSIMSliceBytes = 6 << 10
	maxSIMTerms      = 16
)

func addSIMContext(home, brainHome string, installed []skill, metricsJSON string, c *Collected, upper time.Time, add func(kind, label, text string, t time.Time)) {
	if !brainDBExists(brainHome) {
		return
	}
	q := buildSIMQuery(home, installed, metricsJSON, c, upper)
	slice, err := brain.RetrieveSIMSlice(brainHome, q)
	if err != nil || strings.TrimSpace(slice.Text) == "" {
		return
	}
	add(kindSIM, "optimizer-memory", slice.Text, time.Time{})
}

func buildSIMQuery(home string, installed []skill, metricsJSON string, c *Collected, upper time.Time) brain.SIMQuery {
	signals := collectSIMSignals(home, metricsJSON, c)
	return brain.SIMQuery{
		Signals:     signals,
		TargetPaths: collectSIMTargetPaths(installed, c),
		Terms:       collectSIMTerms(c, signals),
		MaxBytes:    maxSIMSliceBytes,
		Now:         upper,
	}
}

func collectSIMSignals(home, metricsJSON string, c *Collected) []string {
	seen := map[string]bool{}
	var out []string
	push := func(sig string) {
		if sig = cleanSignalID(sig); sig != "" && !seen[sig] {
			seen[sig] = true
			out = append(out, sig)
		}
	}
	extractSignalTokens(metricsJSON, push)
	if d, err := metrics.ReadLatestDailyDigest(home); err == nil && d != nil {
		for _, s := range d.Signals {
			push(s.SignalID)
		}
		for _, a := range d.Actions {
			if isUnlockedFieldStatus(a.Status) {
				push(a.SignalID)
			}
		}
	}
	for _, in := range c.Inputs {
		if in.Kind == kindTrail || in.Kind == kindFeedback {
			extractSignalTokens(in.Text, push)
		}
	}
	return out
}

func extractSignalTokens(text string, push func(string)) {
	if text == "" {
		return
	}
	for _, tok := range strings.FieldsFunc(text, isNotSignalRune) {
		if strings.HasPrefix(tok, "sig:") {
			push(tok)
		}
	}
}

func collectSIMTargetPaths(installed []skill, c *Collected) []string {
	seen := map[string]bool{}
	var out []string
	push := func(p string) {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, s := range installed {
		if name := strings.TrimSpace(s.name); name != "" {
			push("skills/" + name + "/")
		}
	}
	for _, in := range c.Inputs {
		if in.Kind != kindTrail && in.Kind != kindFeedback {
			continue
		}
		for _, f := range strings.Fields(in.Text) {
			tok := strings.Trim(f, "`\"'.,;:()[]")
			if strings.HasPrefix(tok, "skills/") && len(tok) > len("skills/") {
				push(tok)
			}
		}
	}
	return out
}

func collectSIMTerms(c *Collected, signals []string) []string {
	seen := map[string]bool{}
	var out []string
	push := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" || seen[term] || len(out) >= maxSIMTerms {
			return
		}
		seen[term] = true
		out = append(out, term)
	}
	push(c.Name)
	for _, sig := range signals {
		if i := strings.LastIndexByte(sig, ':'); i >= 0 && i+1 < len(sig) {
			push(strings.NewReplacer("_", " ", "-", " ").Replace(sig[i+1:]))
		}
	}
	for _, in := range c.Inputs {
		if in.Kind != kindTrail || in.Label == "hidden-coupling" {
			continue
		}
		first, _, _ := strings.Cut(strings.TrimSpace(in.Text), "\n")
		push(sliceUTF8Prefix(first, 80))
	}
	return out
}
