package dream

import (
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/sim"
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
		TargetPaths: dreamSIMTargetPaths(installed, c),
		Terms:       dreamSIMTerms(c, signals),
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
	sim.ExtractSignalTokens(metricsJSON, push)
	collectDigestSIMSignals(home, push)
	collectInputSIMSignals(c.Inputs, push)
	return out
}

func collectDigestSIMSignals(home string, push func(string)) {
	d, err := metrics.ReadLatestDailyDigest(home)
	if err != nil || d == nil {
		return
	}
	for _, s := range d.Signals {
		push(s.SignalID)
	}
	for _, a := range d.Actions {
		if isUnlockedFieldStatus(a.Status) {
			push(a.SignalID)
		}
	}
}

func collectInputSIMSignals(inputs []Input, push func(string)) {
	for _, in := range inputs {
		if in.Kind == kindTrail || in.Kind == kindFeedback {
			sim.ExtractSignalTokens(in.Text, push)
		}
	}
}

func dreamSIMTargetPaths(installed []skill, c *Collected) []string {
	names := make([]string, 0, len(installed))
	for _, s := range installed {
		names = append(names, s.name)
	}
	var texts []string
	for _, in := range c.Inputs {
		if in.Kind == kindTrail || in.Kind == kindFeedback {
			texts = append(texts, in.Text)
		}
	}
	return sim.CollectTargetPaths(names, texts)
}

func dreamSIMTerms(c *Collected, signals []string) []string {
	var texts []string
	for _, in := range c.Inputs {
		if in.Kind == kindTrail && in.Label != "hidden-coupling" {
			texts = append(texts, in.Text)
		}
	}
	return sim.CollectTerms(c.Name, signals, texts, maxSIMTerms)
}
