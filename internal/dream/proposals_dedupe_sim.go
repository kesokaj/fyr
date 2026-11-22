package dream

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
)

const appliedSkillCooldown = 7 * 24 * time.Hour

// dedupeNow returns the current time for applied-proposal cooldown and 7-day
// dominant-model checks; tests override it for deterministic time control.
var dedupeNow = time.Now

var policyEffortSuffixes = []string{
	"-xhigh", "-high", "-medium", "-low", "-minimal", "-max", "-tiered", "-fast",
}

type appliedDedupeContext struct {
	home           string
	now            time.Time
	digestLoaded   bool
	digest         *metrics.DailyDigest
	dominantLoaded bool
	dominantModel  string
}

func newAppliedDedupeContext(home string, now time.Time) *appliedDedupeContext {
	return &appliedDedupeContext{home: home, now: now}
}

func isAppliedStatus(status string) bool {
	return status == "applied" || strings.HasPrefix(status, "applied:")
}

func appliedSHA(status string) string {
	rest, ok := strings.CutPrefix(status, "applied:")
	if !ok {
		return ""
	}
	return strings.TrimSpace(rest)
}

func (idx *proposalIndex) addApplied(rec brain.Record, m proposalMeta, ctx *appliedDedupeContext) {
	origSignal := m.signalID
	if origSignal == "" {
		origSignal = firstSignalInText(rec.Title + "\n" + rec.Body)
	}
	m.signalID = ""
	if m.sessionID != "" {
		idx.sessions[m.sessionID] = true
	}
	if m.target != "harness" {
		idx.projectTitles[m.normTitle] = append(idx.projectTitles[m.normTitle], rec.Project)
		return
	}
	if !ctx.shouldSuppressApplied(rec, appliedSHA(rec.Status), origSignal) {
		return
	}
	idx.add(rec.Project, m)
}

func (c *appliedDedupeContext) shouldSuppressApplied(rec brain.Record, sha, origSignal string) bool {
	if !withinAppliedCooldown(rec, c.now) {
		return false
	}
	appliedModel, unlocked := c.checkAppliedOutcome(rec.ID, sha, origSignal)
	if unlocked {
		return false
	}
	if appliedModel == "" {
		return true
	}
	return !policyGenerationShifted(appliedModel, c.dominantModel7d())
}

func withinAppliedCooldown(rec brain.Record, now time.Time) bool {
	appliedAt, ok := parseAppliedTime(rec)
	if !ok {
		return false
	}
	return now.Sub(appliedAt) < appliedSkillCooldown
}

func parseAppliedTime(rec brain.Record) (time.Time, bool) {
	if s := strings.TrimSpace(rec.Updated); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t, true
		}
	}
	if s := strings.TrimSpace(rec.Date); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (c *appliedDedupeContext) checkAppliedOutcome(proposalID, sha, origSignal string) (string, bool) {
	appliedModel := ""
	if att, err := brain.FindSIMAttemptByProposalOrSHA(c.home, proposalID, sha); err == nil && att != nil {
		if isUnlockedFieldStatus(att.FieldStatus) {
			return att.PolicyModel, true
		}
		appliedModel = strings.TrimSpace(att.PolicyModel)
	}
	if act, ok := c.findDigestAction(proposalID, sha, origSignal); ok {
		if isUnlockedFieldStatus(act.Status) {
			return appliedModel, true
		}
		if appliedModel == "" {
			appliedModel = strings.TrimSpace(act.PolicyModel)
		}
	}
	return appliedModel, false
}

func (c *appliedDedupeContext) dailyDigest() *metrics.DailyDigest {
	if !c.digestLoaded {
		c.digestLoaded = true
		c.digest, _ = metrics.ReadLatestDailyDigest(c.home)
	}
	return c.digest
}

func (c *appliedDedupeContext) dominantModel7d() string {
	if !c.dominantLoaded {
		c.dominantLoaded = true
		c.dominantModel = readDominantModel7d(c.home, c.now)
	}
	return c.dominantModel
}

func (c *appliedDedupeContext) findDigestAction(proposalID, sha, origSignal string) (metrics.DigestAppliedAction, bool) {
	d := c.dailyDigest()
	if d == nil {
		return metrics.DigestAppliedAction{}, false
	}
	var matched metrics.DigestAppliedAction
	found := false
	for _, act := range d.Actions {
		if !digestActionMatches(act, proposalID, sha, origSignal) {
			continue
		}
		if isUnlockedFieldStatus(act.Status) {
			return act, true
		}
		if !found || (matched.PolicyModel == "" && act.PolicyModel != "") {
			matched = act
			found = true
		}
	}
	return matched, found
}

func digestActionMatches(act metrics.DigestAppliedAction, proposalID, sha, origSignal string) bool {
	if proposalID != "" && act.SignalID == proposalID {
		return true
	}
	actSHA := actionAppliedSHA(act)
	if sha != "" && actSHA != "" {
		return shaPrefixMatch(actSHA, sha)
	}
	return origSignal != "" && act.SignalID == origSignal
}

func actionAppliedSHA(act metrics.DigestAppliedAction) string {
	if s := strings.TrimSpace(act.AppliedSHA); s != "" {
		return s
	}
	if rest, ok := strings.CutPrefix(strings.TrimSpace(act.RevertCmd), "git revert "); ok {
		fields := strings.Fields(rest)
		if len(fields) > 0 && !strings.EqualFold(fields[0], "HEAD") {
			return fields[0]
		}
	}
	return ""
}

func isUnlockedFieldStatus(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	return s == metrics.StatusStillRegressing || s == "reverted"
}

func isHexSHA(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func shaPrefixMatch(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if !isHexSHA(a) || !isHexSHA(b) {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func policyGenerationKey(model string) string {
	s := strings.ToLower(strings.TrimSpace(model))
	s = strings.TrimPrefix(s, "models/")
	for {
		trimmed := false
		for _, suf := range policyEffortSuffixes {
			if strings.HasSuffix(s, suf) {
				s = strings.TrimSuffix(s, suf)
				trimmed = true
				break
			}
		}
		if !trimmed {
			break
		}
	}
	return strings.TrimSpace(s)
}

func policyGenerationShifted(appliedModel, dominantModel string) bool {
	a := policyGenerationKey(appliedModel)
	b := policyGenerationKey(dominantModel)
	return a != "" && b != "" && a != b
}

func readDominantModel7d(home string, now time.Time) string {
	if home == "" {
		return ""
	}
	path := metrics.Path(home)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	tmp, err := os.MkdirTemp(filepath.Dir(path), "dream-dedupe-metrics-")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(tmp)
	s, err := metrics.Snapshot(path, tmp)
	if err != nil {
		return ""
	}
	defer s.Close()
	model, err := metrics.DominantModelSince(s, now.Add(-appliedSkillCooldown))
	if err != nil {
		return ""
	}
	return model
}

func firstSignalInText(text string) string {
	for _, tok := range strings.FieldsFunc(text, isNotSignalRune) {
		if sig := cleanSignalID(tok); sig != "" {
			return sig
		}
	}
	return ""
}

func isNotSignalRune(r rune) bool {
	return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ':' || r == '_' || r == '-')
}
