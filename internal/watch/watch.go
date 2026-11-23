// Package watch provides out-of-band, zero-daemon hang, interactive-wait,
// stale-artifact, and subagent-budget inspection across Antigravity sessions
// and child processes.
package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

const (
	defaultIdleSec         = 90
	minActiveMetaWindowSec = 300
	budgetWarnPct          = 80
	discoveryWindow        = 24 * time.Hour
	watchTempPrefix        = "watch-agy-"
	kindInteractiveWait    = "INTERACTIVE_WAIT"
	kindStuckNoProgress    = "STUCK_NO_PROGRESS"
	kindBudgetNearLimit    = "BUDGET_NEAR_LIMIT"
)

// Options configures a single out-of-band watch inspection pass.
type Options struct {
	Home       string
	Dir        string
	ConvID     string
	IdleSec    int
	Now        func() time.Time
	ProcLister func() ([]ProcInfo, error)
}

// ProcInfo describes one OS process inspected for interactive or idle waits.
type ProcInfo struct {
	PID        int    `json:"pid"`
	PPID       int    `json:"ppid"`
	State      string `json:"state"`
	Wchan      string `json:"wchan"`
	Cmdline    string `json:"cmdline"`
	ElapsedSec int64  `json:"elapsedSec"`
}

// Finding describes one hang, interactive-stdin block, stale artifact, or
// near-limit subagent budget condition detected by Check.
type Finding struct {
	Kind           string `json:"kind"`
	Target         string `json:"target"`
	PID            int    `json:"pid,omitempty"`
	IdleSec        int64  `json:"idleSec,omitempty"`
	PromptHint     string `json:"promptHint,omitempty"`
	ArtifactStatus string `json:"artifactStatus,omitempty"`
	Action         string `json:"action,omitempty"`
}

// Report summarizes all inspections performed by Check.
type Report struct {
	OK       bool      `json:"ok"`
	Checked  int       `json:"checked"`
	Findings []Finding `json:"findings,omitempty"`
}

// Check runs a read-only hang and budget inspection pass, opening search.db
// and metrics.db read-only and falling back to copies under harness.StateDir
// only when required.
func Check(ctx context.Context, o Options) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	norm, customProcs := normalizeOptions(o)
	if norm.ConvID != "" && !validConvID(norm.ConvID) {
		return Report{}, fmt.Errorf("watch: invalid conversation id %q", norm.ConvID)
	}
	var rep Report
	err := withWatchTemp(norm.Home, func(tmp string) error {
		return runChecks(ctx, norm, customProcs, tmp, &rep)
	})
	if err != nil {
		return Report{}, err
	}
	sortFindings(rep.Findings)
	rep.OK = len(rep.Findings) == 0
	return rep, nil
}

func runChecks(ctx context.Context, o Options, customProcs bool, tmp string, rep *Report) error {
	now := o.Now()
	summaries, hasSum, targets, err := loadAndDiscoverTargets(o, tmp, now)
	if err != nil {
		return err
	}
	targetSet := make(map[string]bool, len(targets))
	for _, id := range targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		targetSet[id] = true
		rep.Checked++
		if f, ok := inspectConversationFiles(o.Home, id, now); ok {
			rep.Findings = append(rep.Findings, f)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	procs, err := o.ProcLister()
	if err != nil {
		return err
	}
	if o.Dir != "" {
		if err := inspectProjectDir(o, summaries, hasSum, targetSet, procs, tmp, now, rep); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	nProcs, procFindings := inspectProcsFrom(o, customProcs, procs)
	rep.Checked += nProcs
	rep.Findings = append(rep.Findings, procFindings...)
	return nil
}

func inspectProjectDir(o Options, summaries map[string]summaryRow, hasSum bool, targetSet map[string]bool, procs []ProcInfo, tmp string, now time.Time, rep *Report) error {
	if err := trail.CheckScratchNoSymlinks(o.Dir); err != nil {
		return fmt.Errorf("watch: scratch path: %w", err)
	}
	n, artFindings, err := inspectArtifactsChecked(o.Dir, o.IdleSec, now)
	if err != nil {
		return err
	}
	rep.Checked += n
	rep.Findings = append(rep.Findings, artFindings...)
	nMeta, metaFindings, err := inspectSessionMeta(metaCheckInput{
		home: o.Home, dir: o.Dir, convID: o.ConvID, tmp: tmp,
		idleSec: o.IdleSec, now: now, summaries: summaries,
		hasSum: hasSum, targetSet: targetSet, procs: procs,
	})
	if err != nil {
		return err
	}
	rep.Checked += nMeta
	rep.Findings = append(rep.Findings, metaFindings...)
	return nil
}

func normalizeOptions(o Options) (Options, bool) {
	customProcs := o.ProcLister != nil
	if o.IdleSec <= 0 {
		o.IdleSec = defaultIdleSec
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ProcLister == nil {
		o.ProcLister = listProcs
	}
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	return o, customProcs
}

func withWatchTemp(home string, fn func(tmp string) error) error {
	if home == "" {
		return fn("")
	}
	conf := filepath.Join(home, harness.StateDir)
	_, statErr := os.Stat(conf)
	created := errors.Is(statErr, os.ErrNotExist)
	if err := os.MkdirAll(conf, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(conf, watchTempPrefix)
	if err != nil {
		if created {
			removeDirIfEmpty(conf)
		}
		return err
	}
	defer func() {
		_ = os.RemoveAll(tmp)
		if created {
			removeDirIfEmpty(conf)
		}
	}()
	return fn(tmp)
}

func removeDirIfEmpty(dir string) {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
}

func sortFindings(findings []Finding) {
	slices.SortFunc(findings, func(a, b Finding) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		if c := strings.Compare(a.Target, b.Target); c != 0 {
			return c
		}
		switch {
		case a.PID < b.PID:
			return -1
		case a.PID > b.PID:
			return 1
		default:
			return 0
		}
	})
}
