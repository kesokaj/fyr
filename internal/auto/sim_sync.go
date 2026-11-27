package auto

import (
	"cmp"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/trail"
)

var (
	simHexSHARe      = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	revertedCommitRe = regexp.MustCompile(`(?i)\bThis reverts commit ([0-9a-f]{7,40})\b`)
)

// SyncSIMFieldOutcomes synchronizes Stage B online outcomes (git reverts,
// DailyDigest recovery statuses, observed metrics, and Stage 3 per-model
// stratified recovery) onto eligible SIM attempts in brain.db. Missing homes,
// stores, or harness checkouts fail open cleanly.
func SyncSIMFieldOutcomes(home, harnessRepo string, digest *metrics.DailyDigest, store *metrics.Store, now time.Time) (int, error) {
	if strings.TrimSpace(home) == "" || !brainDBExists(home) {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	attempts, err := brain.FindSIMAttemptsForFieldSync(home)
	if err != nil || len(attempts) == 0 {
		return 0, err
	}
	updated, revertedIDs, err := syncSIMGitReverts(home, harnessRepo, attempts, now)
	if err != nil {
		return updated, err
	}
	if digest == nil || len(digest.Actions) == 0 {
		return updated, nil
	}
	digestUpdated, err := syncSIMDigestActions(home, attempts, revertedIDs, digest, store, now)
	return updated + digestUpdated, err
}

func (r *run) syncSIMFromLatestDigest() {
	if r.dry || strings.TrimSpace(r.o.Home) == "" || !brainDBExists(r.o.Home) {
		return
	}
	dig, err := metrics.ReadLatestDailyDigest(r.o.Home)
	if err != nil {
		dig = nil
	}
	var store *metrics.Store
	if dig != nil {
		dbPath := metrics.Path(r.o.Home)
		if _, err := os.Lstat(dbPath); err == nil {
			if s, serr := metrics.Open(dbPath); serr == nil {
				store = s
				defer store.Close()
			}
		}
	}
	n, err := SyncSIMFieldOutcomes(r.o.Home, r.o.Repo, dig, store, r.o.now())
	if err != nil || n == 0 || r.say == nil {
		return
	}
	r.say(fmt.Sprintf("sim: synced %d field outcome(s)", n))
}

func syncSIMGitReverts(home, harnessRepo string, attempts []brain.SIMAttempt, now time.Time) (int, map[int64]bool, error) {
	revertedSHAs := readRevertedSHAs(harnessRepo)
	if len(revertedSHAs) == 0 {
		return 0, nil, nil
	}
	revertedIDs := make(map[int64]bool)
	updated := 0
	for _, att := range attempts {
		if !matchesAnySHAPrefix(att.CommitSHA, revertedSHAs) {
			continue
		}
		revertedIDs[att.ID] = true
		ok, err := brain.UpdateSIMAttemptFieldOutcome(home, brain.SIMFieldUpdate{
			AttemptID:   att.ID,
			FieldStatus: "reverted",
			Now:         now,
		})
		if err != nil {
			return updated, revertedIDs, err
		}
		if ok {
			updated++
		}
	}
	return updated, revertedIDs, nil
}

func readRevertedSHAs(harnessRepo string) []string {
	repo := strings.TrimSpace(harnessRepo)
	if repo == "" {
		return nil
	}
	if info, err := os.Stat(repo); err != nil || !info.IsDir() {
		return nil
	}
	cmd := trail.Command(repo, "log", "--since=90.days", "--grep=This reverts commit ", "--format=%B")
	cmd.WaitDelay = time.Second
	out := trail.NewCappedBuffer(toolLimit, errToolOutputLimit)
	cmd.Stdout = out
	if err := runBoundedCmd(cmd, toolWait); err != nil {
		return nil
	}
	return parseRevertedSHAs(out.String())
}

func runBoundedCmd(cmd *exec.Cmd, wait time.Duration) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	proc := cmd.Process
	timer := time.AfterFunc(wait, func() {
		if proc != nil {
			_ = proc.Kill()
		}
	})
	err := cmd.Wait()
	timer.Stop()
	return err
}

func parseRevertedSHAs(logBody string) []string {
	matches := revertedCommitRe.FindAllStringSubmatch(logBody, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		sha := strings.ToLower(strings.TrimSpace(m[1]))
		if sha != "" && !seen[sha] {
			seen[sha] = true
			out = append(out, sha)
		}
	}
	return out
}

func matchesAnySHAPrefix(commitSHA string, shas []string) bool {
	for _, sha := range shas {
		if simSHAPrefixMatch(commitSHA, sha) {
			return true
		}
	}
	return false
}

func simSHAPrefixMatch(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if !simHexSHARe.MatchString(a) || !simHexSHARe.MatchString(b) {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

type simObservedCache map[string]*float64

func syncSIMDigestActions(home string, attempts []brain.SIMAttempt, revertedIDs map[int64]bool, digest *metrics.DailyDigest, store *metrics.Store, now time.Time) (int, error) {
	probSigs := make(map[string]string)
	obsCache := make(simObservedCache)
	updated := 0
	for _, att := range attempts {
		if revertedIDs[att.ID] {
			continue
		}
		act, matched := matchDigestActionForAttempt(home, probSigs, att, digest.Actions)
		if !matched {
			continue
		}
		status := validDigestFieldStatus(act.Status)
		if status == "" {
			continue
		}
		ok, err := applyDigestSyncToAttempt(home, att, act, status, store, obsCache, now)
		if err != nil {
			return updated, err
		}
		if ok {
			updated++
		}
	}
	return updated, nil
}

func applyDigestSyncToAttempt(home string, att brain.SIMAttempt, act metrics.DigestAppliedAction, status string, store *metrics.Store, obsCache simObservedCache, now time.Time) (bool, error) {
	metric := cmp.Or(strings.TrimSpace(att.FieldMetric), strings.TrimSpace(act.Metric))
	baseline := att.FieldBaseline
	if baseline == nil {
		baseline = act.BaselineValue
	}
	observed := lookupCachedObservedMetric(store, home, metric, now, obsCache)
	models := computeStratifiedSIMModels(store, metric, baseline, simSyncSinceMS(att, act), now)
	if !simFieldUpdateNeeded(att, status, metric, observed, models) {
		return false, nil
	}
	return brain.UpdateSIMAttemptFieldOutcome(home, brain.SIMFieldUpdate{
		AttemptID:     att.ID,
		FieldStatus:   status,
		FieldMetric:   metric,
		FieldObserved: observed,
		FieldModels:   models,
		Now:           now,
	})
}

func lookupCachedObservedMetric(store *metrics.Store, home, metric string, now time.Time, cache simObservedCache) *float64 {
	if store == nil || metric == "" {
		return nil
	}
	if cached, seen := cache[metric]; seen {
		return cached
	}
	var out *float64
	if val, ok := metrics.LookupObservedMetric(store, home, metric, now); ok {
		v := val
		out = &v
	}
	cache[metric] = out
	return out
}

func computeStratifiedSIMModels(store *metrics.Store, metric string, baseline *float64, sinceMS int64, now time.Time) []brain.SIMModelStatus {
	if store == nil {
		return nil
	}
	raw, err := metrics.StratifiedFieldRecovery(store, metric, baseline, sinceMS, now)
	if err != nil || len(raw) == 0 {
		return nil
	}
	out := make([]brain.SIMModelStatus, len(raw))
	for i, m := range raw {
		out[i] = brain.SIMModelStatus{Model: m.Model, Status: m.Status, Sessions: m.Sessions}
	}
	return out
}

func matchDigestActionForAttempt(home string, probSigs map[string]string, att brain.SIMAttempt, actions []metrics.DigestAppliedAction) (metrics.DigestAppliedAction, bool) {
	for _, act := range actions {
		if strings.TrimSpace(act.AppliedSHA) != "" && simSHAPrefixMatch(att.CommitSHA, act.AppliedSHA) {
			return act, true
		}
	}
	attDay := attemptReportDate(att)
	if attDay == "" {
		return metrics.DigestAppliedAction{}, false
	}
	for _, act := range actions {
		if strings.TrimSpace(act.AppliedSHA) != "" {
			continue
		}
		sigID := strings.TrimSpace(act.SignalID)
		if sigID == "" || strings.TrimSpace(act.ReportDate) != attDay {
			continue
		}
		if attemptMatchesSignalID(home, probSigs, att, sigID) {
			return act, true
		}
	}
	return metrics.DigestAppliedAction{}, false
}

func attemptMatchesSignalID(home string, probSigs map[string]string, att brain.SIMAttempt, sigID string) bool {
	if att.ProposalID != "" && att.ProposalID == sigID {
		return true
	}
	if slices.Contains(att.PredictedSignals, sigID) {
		return true
	}
	if probSig, seen := probSigs[att.ProblemID]; seen {
		return probSig == sigID
	}
	var probSig string
	if det, err := brain.GetSIMProblem(home, att.ProblemID); err == nil {
		probSig = strings.TrimSpace(det.Problem.SignalID)
	}
	probSigs[att.ProblemID] = probSig
	return probSig == sigID
}

func attemptReportDate(att brain.SIMAttempt) string {
	raw := strings.TrimSpace(att.CreatedAt)
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts.UTC().Format("2006-01-02")
	}
	if len(raw) >= 10 && isValidReportDate(raw[:10]) {
		return raw[:10]
	}
	return ""
}

func simSyncSinceMS(att brain.SIMAttempt, act metrics.DigestAppliedAction) int64 {
	if act.AppliedAt > 0 {
		return act.AppliedAt
	}
	if ts, err := time.Parse(time.RFC3339, strings.TrimSpace(att.CreatedAt)); err == nil {
		return ts.UnixMilli()
	}
	if day, err := time.Parse("2006-01-02", strings.TrimSpace(act.ReportDate)); err == nil {
		return day.UTC().UnixMilli()
	}
	return 0
}

func validDigestFieldStatus(raw string) string {
	switch strings.TrimSpace(raw) {
	case metrics.StatusPendingData, metrics.StatusRecovered, metrics.StatusStillRegressing:
		return strings.TrimSpace(raw)
	}
	return ""
}

func simFieldUpdateNeeded(att brain.SIMAttempt, status, metric string, observed *float64, models []brain.SIMModelStatus) bool {
	if att.FieldStatus != status {
		return true
	}
	if metric != "" && att.FieldMetric != metric {
		return true
	}
	if observed != nil && (att.FieldObserved == nil || *att.FieldObserved != *observed) {
		return true
	}
	if models != nil && !slices.Equal(att.FieldModels, models) {
		return true
	}
	return false
}
