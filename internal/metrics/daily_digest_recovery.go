package metrics

import (
	"database/sql"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

var (
	revertSHARe  = regexp.MustCompile(`revert:\s*git revert ([0-9a-f]{7,40}|HEAD)`)
	appliedSHARe = regexp.MustCompile(`Applied commit ` + "`?" + `([0-9a-f]{7,40})` + "`?")
	sigIDRe      = regexp.MustCompile(`\bsig:(?:global|proj-[0-9a-f]{8}):[a-z0-9_-]+\b`)
)

func collectDigestActions(home, repoRoot, day string, curSum *Summary, prev, today *DailyDigest, signals []DigestAction) []DigestAppliedAction {
	cutoffDay := priorDayString(day)
	raw := readBrainAppliedActions(home, repoRoot, cutoffDay)
	if today != nil {
		raw = append(raw, today.Actions...)
	}
	if prev != nil {
		raw = append(raw, prev.Actions...)
	}
	merged := dedupAppliedActions(raw)
	for i := range merged {
		postSess := sessionsAfterDay(curSum, merged[i].ReportDate)
		merged[i].Status = EvaluateActionRecovery(merged[i], curSum, prev, day, postSess, signals)
	}
	return merged
}

func readBrainAppliedActions(home, repoRoot, sinceDay string) []DigestAppliedAction {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	db, cleanup, ok := openCopySQLiteFile(home, home, harness.StateDir, "brain.db")
	if !ok {
		return nil
	}
	defer cleanup()
	q := `SELECT id, kind, status, date, title, body, COALESCE(project, '') FROM records WHERE date >= ? AND ((kind = 'review' AND (body LIKE '%Applied%' OR body LIKE '%revert: git revert %')) OR (kind = 'proposal' AND status IN ('queued', 'accepted'))) ORDER BY date DESC, id DESC`
	rows, err := db.Query(q, sinceDay)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanBrainActionRows(rows, repoRoot)
}

func scanBrainActionRows(rows *sql.Rows, repoRoot string) []DigestAppliedAction {
	var out []DigestAppliedAction
	cleanRoot := filepath.Clean(strings.TrimSpace(repoRoot))
	for rows.Next() {
		var id, kind, status, date, title, body, proj string
		if rows.Scan(&id, &kind, &status, &date, &title, &body, &proj) != nil {
			continue
		}
		if cleanRoot != "" && proj != "" && filepath.Clean(proj) != cleanRoot {
			continue
		}
		if act, ok := parseAppliedActionRecord(id, kind, status, date, title, body); ok {
			out = append(out, act)
		}
	}
	return out
}

func parseAppliedActionRecord(id, kind, status, date, title, body string) (DigestAppliedAction, bool) {
	if len(date) < 10 {
		return DigestAppliedAction{}, false
	}
	day := printable(date[:10])
	sigID := sigIDRe.FindString(title + "\n" + body)
	if sigID == "" {
		sigID = printable(id)
	}
	scope := scopeProposeOnly
	if strings.Contains(body, "skills/") || strings.Contains(title, "skills/") {
		scope = scopeAutoSkill
	}
	revertCmd, sha := extractAppliedCommitHints(body)
	if kind == "review" && revertCmd == "" && !strings.Contains(body, "Applied") {
		return DigestAppliedAction{}, false
	}
	summary := "auto commit recorded"
	if kind == "proposal" {
		summary = "proposal " + printable(status)
	}
	return DigestAppliedAction{
		SignalID: sigID, ReportDate: day, ActionScope: scope, Status: StatusPendingData,
		InstallPending: strings.Contains(body, "install pending"), RevertCmd: revertCmd,
		Summary: summary, AppliedSHA: sha,
	}, true
}

func extractAppliedCommitHints(body string) (revertCmd, sha string) {
	if m := revertSHARe.FindStringSubmatch(body); len(m) == 2 {
		revertCmd = "git revert " + m[1]
		if m[1] != "HEAD" {
			sha = m[1]
		}
	}
	if sha == "" {
		if m := appliedSHARe.FindStringSubmatch(body); len(m) == 2 {
			sha = m[1]
		}
	}
	return revertCmd, sha
}

func priorDayString(day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, -1).Format("2006-01-02")
}

func sessionsAfterDay(sum *Summary, reportDate string) int {
	if sum == nil || reportDate == "" {
		return 0
	}
	n := 0
	for _, d := range sum.Daily {
		if d.Date > reportDate {
			n += d.Sessions["agy"]
		}
	}
	return n
}

func dedupAppliedActions(in []DigestAppliedAction) []DigestAppliedAction {
	byID := map[string]DigestAppliedAction{}
	for _, a := range in {
		id := a.SignalID + "|" + a.ReportDate + "|" + a.RevertCmd
		if prev, ok := byID[id]; ok {
			byID[id] = mergeAppliedAction(prev, a)
		} else {
			byID[id] = a
		}
	}
	out := make([]DigestAppliedAction, 0, len(byID))
	for _, a := range byID {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ReportDate != out[j].ReportDate {
			return out[i].ReportDate > out[j].ReportDate
		}
		return out[i].SignalID < out[j].SignalID
	})
	return out
}

func mergeAppliedAction(base, incoming DigestAppliedAction) DigestAppliedAction {
	if base.ActionScope == "" {
		base.ActionScope = incoming.ActionScope
	}
	if base.Status == "" || base.Status == StatusPendingData {
		if incoming.Status != "" {
			base.Status = incoming.Status
		}
	}
	base.InstallPending = base.InstallPending || incoming.InstallPending
	if base.RevertCmd == "" {
		base.RevertCmd = incoming.RevertCmd
	}
	if base.Summary == "" {
		base.Summary = incoming.Summary
	}
	if base.AppliedSHA == "" {
		base.AppliedSHA = incoming.AppliedSHA
	}
	if base.Metric == "" {
		base.Metric = incoming.Metric
	}
	if base.BaselineValue == nil && incoming.BaselineValue != nil {
		v := *incoming.BaselineValue
		base.BaselineValue = &v
	}
	if base.AppliedAt == 0 {
		base.AppliedAt = incoming.AppliedAt
	}
	if base.PolicyModel == "" {
		base.PolicyModel = incoming.PolicyModel
	}
	return base
}

func upsertDigestAppliedAction(existing []DigestAppliedAction, act DigestAppliedAction) []DigestAppliedAction {
	replaced := false
	out := make([]DigestAppliedAction, 0, len(existing)+1)
	for _, cur := range existing {
		sameKey := cur.SignalID == act.SignalID && cur.ReportDate == act.ReportDate &&
			(cur.RevertCmd == act.RevertCmd || (act.AppliedSHA != "" && cur.AppliedSHA == act.AppliedSHA) || cur.RevertCmd == "" || act.RevertCmd == "")
		if sameKey && !replaced {
			out = append(out, mergeAppliedAction(act, cur))
			replaced = true
			continue
		}
		out = append(out, cur)
	}
	if !replaced {
		out = append(out, act)
	}
	return dedupAppliedActions(out)
}

func normalizeAppliedAction(act DigestAppliedAction, day string, nowMS int64) DigestAppliedAction {
	act.SignalID = printable(strings.TrimSpace(act.SignalID))
	act.ReportDate = printable(strings.TrimSpace(act.ReportDate))
	if act.ReportDate == "" {
		act.ReportDate = day
	}
	act.ActionScope = printable(strings.TrimSpace(act.ActionScope))
	if act.ActionScope == "" {
		act.ActionScope = scopeAutoSkill
	}
	act.Status = printable(strings.TrimSpace(act.Status))
	if act.Status == "" {
		act.Status = StatusPendingData
	}
	act.RevertCmd = printable(strings.TrimSpace(act.RevertCmd))
	act.Summary = printable(strings.TrimSpace(act.Summary))
	act.AppliedSHA = printable(strings.TrimSpace(act.AppliedSHA))
	act.Metric = printable(strings.TrimSpace(act.Metric))
	act.PolicyModel = clipPrintable(act.PolicyModel, trialText)
	if act.AppliedAt <= 0 {
		act.AppliedAt = nowMS
	}
	return act
}

// EvaluateActionRecovery determines post-fix recovery status for one applied action.
func EvaluateActionRecovery(act DigestAppliedAction, curSum *Summary, prev *DailyDigest, day string, postSessions int, activeSignals []DigestAction) string {
	if act.ReportDate == "" || day <= act.ReportDate || postSessions < minRecoverySessions || curSum == nil {
		return StatusPendingData
	}
	rule := extractRuleFromSignalID(act.SignalID)
	if rule != "" && hasRuleSignal(activeSignals, rule) {
		return StatusStillRegressing
	}
	if act.BaselineValue != nil && strings.TrimSpace(act.Metric) != "" {
		if curVal, ok := lookupSummaryMetric(curSum, act.Metric); ok {
			if curVal == 0 && !isHigherBetterMetric(act.Metric) && !hasRuleSignal(activeSignals, rule) {
				return StatusRecovered
			}
			return pickSev(isMetricBetter(act.Metric, curVal, *act.BaselineValue), StatusRecovered, StatusStillRegressing)
		}
	}
	if rule == "" {
		return pickSev(hasHighSignal(activeSignals), StatusStillRegressing, StatusRecovered)
	}
	return pickSev(isMetricImprovingForRule(rule, curSum, prev), StatusRecovered, StatusStillRegressing)
}

func hasRuleSignal(signals []DigestAction, rule string) bool {
	for _, s := range signals {
		if s.Rule == rule {
			return true
		}
	}
	return false
}

func hasHighSignal(signals []DigestAction) bool {
	for _, s := range signals {
		if s.Severity == "high" {
			return true
		}
	}
	return false
}

func lookupSummaryMetric(sum *Summary, metric string) (float64, bool) {
	switch metric {
	case "E.timeout_rate_pct":
		if sum.E.TimeoutRatePct != nil {
			return *sum.E.TimeoutRatePct, true
		}
		return 0, false
	case "C.bash_read_bypasses.agy", "C.bash_read_skips.agy":
		return float64(sum.C.BashReadSkips["agy"]), true
	case "H.sessions_over_knee":
		sessions := max(sum.Sessions["agy"], sum.Health.MainSessions)
		if sessions > 0 {
			return float64(sum.Health.SessionsOverKnee) / float64(sessions), true
		}
	}
	if v, ok := sum.Numbers()[metric]; ok {
		return v, true
	}
	return 0, false
}

func isHigherBetterMetric(metric string) bool {
	switch metric {
	case "C.search_grep_ratio.agy", "C.ranged_read_pct.agy", "A.mean_outcome_score",
		"A.mean_token_efficiency", "E.mean_vet_score", "eval.pass_rate",
		"H.clamped_saves", "E.ledger_saved":
		return true
	}
	return false
}

func isMetricBetter(metric string, cur, base float64) bool {
	if isHigherBetterMetric(metric) {
		return cur > base
	}
	return cur < base || cur == 0
}

func extractRuleFromSignalID(sigID string) string {
	if !strings.HasPrefix(sigID, "sig:") {
		return ""
	}
	idx := strings.LastIndexByte(sigID, ':')
	if idx < 0 || idx+1 >= len(sigID) {
		return ""
	}
	return sigID[idx+1:]
}

func isMetricImprovingForRule(rule string, cur *Summary, prev *DailyDigest) bool {
	if prev == nil || cur == nil {
		return true
	}
	switch rule {
	case "pre_invocation_timeouts":
		return cur.E.TimeoutRatePct == nil || *cur.E.TimeoutRatePct < minTimeoutRatePct ||
			(prev.PreInvocation.TimeoutRatePct != nil && *cur.E.TimeoutRatePct < *prev.PreInvocation.TimeoutRatePct)
	case ruleDirectShellRead:
		return cur.C.BashReadSkips["agy"] == 0 || float64(cur.C.BashReadSkips["agy"]) < prev.Rolling7d.BashReadSkips.Current
	case "structural_regression", "prod_pkg_cycles", "hidden_coupling":
		st, p := cur.Health.Structural, prev.ASTSnapshot
		return (st.ProdPkgCycles == 0 && st.HiddenCouplingEditPairs < minHiddenCouplingPairs) ||
			st.ProdPkgCycles < p.ProdPkgCycles || st.HiddenCouplingEditPairs < p.HiddenCouplingEditPairs
	case "search_grep_low":
		r := cur.C.SearchGrepRatio["agy"]
		return r == nil || *r >= 1.0 || (prev.Rolling7d.SearchGrepRatio != nil && *r > prev.Rolling7d.SearchGrepRatio.Current)
	default:
		return true
	}
}
