package auto

import (
	"cmp"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
)

func isValidSignalID(raw string) bool {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "sig:") || len(s) < 5 || len(s) > 96 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == ':' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func isValidActionScope(raw string) bool {
	switch raw {
	case "", "auto_skill", "operator_code", "operator_rule", "propose_only":
		return true
	}
	return false
}

func isValidReportDate(raw string) bool {
	if len(raw) != 10 {
		return false
	}
	_, err := time.Parse("2006-01-02", raw)
	return err == nil
}

func hasProposalMetaFields(d draftLine) bool {
	return d.SignalID != "" || d.ReportDate != "" || d.ActionScope != "" || d.RecommendedAction != ""
}

func validateProposalDraftMeta(d draftLine) error {
	if d.SignalID != "" && !isValidSignalID(d.SignalID) {
		return fmt.Errorf("proposal signal_id must be sig:..., not %q", d.SignalID)
	}
	if d.ReportDate != "" && !isValidReportDate(d.ReportDate) {
		return fmt.Errorf("proposal report_date must be YYYY-MM-DD, not %q", d.ReportDate)
	}
	if !isValidActionScope(d.ActionScope) {
		return fmt.Errorf("proposal action_scope invalid: %q", d.ActionScope)
	}
	return nil
}

func formatProposalDraftMeta(d draftLine) string {
	var b strings.Builder
	for _, pair := range [][2]string{
		{"Signal", d.SignalID},
		{"ReportDate", d.ReportDate},
		{"ActionScope", d.ActionScope},
		{"RecommendedAction", d.RecommendedAction},
	} {
		if v := strings.TrimSpace(oneLine(pair[1])); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", pair[0], v)
		}
	}
	return b.String()
}

func extractProposalAttribution(rec brain.Record) (sigID, repDate string) {
	text := rec.Title + "\n" + rec.Body
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if v, ok := recordFieldVal(line, "signal:", "signal_id:"); ok && sigID == "" && isValidSignalID(v) {
			sigID = v
		}
		if v, ok := recordFieldVal(line, "reportdate:", "report_date:"); ok && repDate == "" {
			if fld := strings.Fields(v); len(fld) > 0 && isValidReportDate(fld[0]) {
				repDate = fld[0]
			}
		}
	}
	if sigID == "" {
		sigID = firstSignalID(text)
	}
	return sigID, repDate
}

func firstSignalID(text string) string {
	for _, fld := range strings.FieldsFunc(text, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ':' || r == '_' || r == '-')
	}) {
		if isValidSignalID(fld) {
			return fld
		}
	}
	return ""
}

func recordFieldVal(line string, prefixes ...string) (string, bool) {
	low := strings.ToLower(line)
	for _, pfx := range prefixes {
		if strings.HasPrefix(low, pfx) {
			return strings.TrimSpace(line[len(pfx):]), true
		}
	}
	return "", false
}

func formatTriggerProse(sigID, repDate string) string {
	sig := strings.TrimSpace(oneLine(sigID))
	day := strings.TrimSpace(oneLine(repDate))
	switch {
	case day != "" && sig != "":
		return fmt.Sprintf("Triggered by daily report %s (%s).", day, sig)
	case day != "":
		return fmt.Sprintf("Triggered by daily report %s.", day)
	case sig != "":
		return fmt.Sprintf("Triggered by daily signal (%s).", sig)
	}
	return ""
}

func (r *run) finishKeptGateApply(root string, t Task, v string, af []Finding) (string, []Finding) {
	// D19 item 3: auto never installs into a real home; the report says install pending.
	r.installPending = true
	warn := ""
	if t.ID != "" {
		status := "applied:" + r.appliedSHA
		if err := brain.Edit(brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}, brain.EditInput{ID: t.ID, Status: &status}); err != nil {
			warn = "; warning: proposal " + t.ID + " update failed: " + oneLine(err.Error())
		} else if r.st.OpenAsks > 0 {
			r.st.OpenAsks--
		}
	}
	r.recordAppliedDigestCheckpoint(root, t)
	suffix := "; install pending" + warn
	v += suffix
	for i := range af {
		if af[i].Check == "worker" {
			af[i].Text += suffix
		}
	}
	return v, af
}

func (r *run) recordAppliedDigestCheckpoint(root string, t Task) {
	if r.appliedSHA == "" || r.o.Apply != nil {
		return
	}
	dbPath := metrics.Path(r.o.Home)
	if _, err := os.Lstat(dbPath); err != nil {
		return
	}
	s, err := metrics.Open(dbPath)
	if err != nil {
		return
	}
	defer s.Close()
	now := r.o.now()
	sigID := cmp.Or(t.SignalID, t.ID)
	metric, baseVal := captureSignalBaseline(r.projectSummary(root), sigID)
	_ = metrics.RecordDigestAppliedAction(s, r.o.Home, root, now, metrics.DigestAppliedAction{
		SignalID: sigID, ReportDate: cmp.Or(t.ReportDate, now.UTC().Format("2006-01-02")),
		ActionScope: "auto_skill", Status: metrics.StatusPendingData, InstallPending: true,
		RevertCmd: "git revert " + r.appliedSHA, Summary: oneLine(t.Class + " applied " + r.appliedSHA),
		AppliedSHA: r.appliedSHA, Metric: metric, BaselineValue: baseVal, AppliedAt: now.UnixMilli(),
	})
}

func captureSignalBaseline(sum *metrics.Summary, sigID string) (string, *float64) {
	if sum == nil || sigID == "" {
		return "", nil
	}
	if sum.Diagnostics != nil {
		for _, sig := range sum.Diagnostics.Signals {
			if sig.SignalID == sigID {
				v := sig.Current
				return sig.Metric, &v
			}
		}
	}
	metric := ruleToMetricName(sigID)
	if metric == "" {
		return "", nil
	}
	if v, ok := lookupBaselineFromSummary(sum, metric); ok {
		return metric, &v
	}
	return metric, nil
}

func ruleToMetricName(sigID string) string {
	idx := strings.LastIndexByte(sigID, ':')
	if idx < 0 || idx+1 >= len(sigID) {
		return ""
	}
	switch sigID[idx+1:] {
	case "search_grep_low":
		return "C.search_grep_ratio.agy"
	case "redundant_reads_elevated":
		return "C.redundant_reads.agy"
	case "context_knee_rate":
		return "H.sessions_over_knee"
	case "tool_error_rate_high":
		return "D.tool_error_rate.agy"
	case "pre_invocation_timeouts":
		return "E.timeout_rate_pct"
	case "bash_read_bypass":
		return "C.bash_read_skips.agy"
	case "retry_loops_elevated":
		return "D.retry_loops.agy"
	case "low_session_outcome":
		return "A.mean_outcome_score"
	}
	return ""
}

func lookupBaselineFromSummary(sum *metrics.Summary, metric string) (float64, bool) {
	switch metric {
	case "E.timeout_rate_pct":
		if sum.E.TimeoutRatePct != nil {
			return *sum.E.TimeoutRatePct, true
		}
		return 0, false
	case "C.bash_read_skips.agy":
		return float64(sum.C.BashReadSkips["agy"]), true
	case "H.sessions_over_knee":
		sess := max(sum.Sessions["agy"], sum.Health.MainSessions)
		if sess > 0 {
			return float64(sum.Health.SessionsOverKnee) / float64(sess), true
		}
		return 0, false
	}
	v, ok := sum.Numbers()[metric]
	return v, ok
}

func (r *run) selectModelTask(root string, p *Project, findings []Finding) (*Task, string) {
	lastSlot := r.st.modelRunsToday(r.o.now()) == modelRunsPerDay-1
	if lastSlot && r.isHarnessCheckout(root) {
		if t := r.pickReservedHarnessEditTask(root, false); t != nil {
			return t, ""
		}
	}
	t := r.pickProjectTask(root, p, findings)
	if t == nil {
		return nil, "no model task due"
	}
	if lastSlot && !r.isHarnessCheckout(root) && t.Class != ClassHarnessEdit {
		if r.o.Repo != "" && r.pickReservedHarnessEditTask(r.o.Repo, false) != nil {
			return nil, "worker deferred: 4th daily slot reserved for high-severity harness-edit"
		}
	}
	return t, ""
}

func (r *run) pickReservedHarnessEditTask(root string, expireStale bool) *Task {
	cands := r.collectHarnessCandidates(root, expireStale)
	if len(cands) == 0 {
		return nil
	}
	dig, _ := metrics.ReadLatestDailyDigest(r.o.Home)
	for _, c := range cands {
		t := preparedTask(ClassHarnessEdit, c.entries, c.rec.ID, len(c.entries))
		if t == nil || len(t.Kept) == 0 {
			continue
		}
		if isHighSeverityAutoSkillProposal(dig, c.rec, t.Kept) {
			t.SignalID, t.ReportDate = extractProposalAttribution(c.rec)
			return t
		}
	}
	return nil
}

func isHighSeverityAutoSkillProposal(dig *metrics.DailyDigest, rec brain.Record, kept []string) bool {
	return isHighSeverityProposal(dig, rec) && isAutoSkillProposal(rec, kept)
}

func isHighSeverityProposal(dig *metrics.DailyDigest, rec brain.Record) bool {
	text := rec.Title + "\n" + rec.Body
	if strings.Contains(strings.ToLower(text), "[high]") {
		return true
	}
	for _, raw := range strings.Split(rec.Body, "\n") {
		if v, ok := recordFieldVal(strings.TrimSpace(raw), "severity:"); ok && strings.EqualFold(v, "high") {
			return true
		}
	}
	sigID, _ := extractProposalAttribution(rec)
	if sigID == "" || dig == nil {
		return false
	}
	for _, s := range dig.Signals {
		if s.SignalID == sigID && strings.EqualFold(s.Severity, "high") {
			return true
		}
	}
	return false
}

func isAutoSkillProposal(rec brain.Record, kept []string) bool {
	for _, raw := range strings.Split(rec.Body, "\n") {
		if v, ok := recordFieldVal(strings.TrimSpace(raw), "actionscope:", "action_scope:"); ok && v != "" {
			return strings.EqualFold(v, "auto_skill")
		}
	}
	if slices.ContainsFunc(kept, func(e string) bool { return strings.HasPrefix(e, "skills/") }) {
		return true
	}
	return strings.Contains(rec.Title+"\n"+rec.Body, "skills/")
}

func (r *run) redactedDailyDigest() string {
	d, err := metrics.ReadLatestDailyDigest(r.o.Home)
	if err != nil || d == nil {
		return ""
	}
	return marshalRedactedJSON(func() (any, error) { return d, nil })
}
