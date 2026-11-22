package relay

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/search"
)

const (
	GeminiFloorFive = 20 // minimum remaining 5-hour quota % for active Gemini pools
	GeminiFloorWeek = 15 // minimum remaining weekly quota % for active Gemini pools
)

// QuotaStatus reports the result of the pre-phase quota/budget floor check.
type QuotaStatus struct {
	OK    bool          `json:"ok"`
	Note  string        `json:"note"`
	Pools []models.Pool `json:"pools,omitempty"`
}

// QuotaCheckFunc checks quota headroom before launching a relay phase.
type QuotaCheckFunc func(ctx context.Context, home string) (QuotaStatus, error)

// CheckBudgetFloor reads cached models.json via internal/models and checks
// active Gemini pools against GeminiFloorFive (20%) and GeminiFloorWeek (15%).
func CheckBudgetFloor(home string, requireQuota bool) (QuotaStatus, error) {
	return CheckBudgetFloorAt(home, requireQuota, time.Now().UTC())
}

// CheckBudgetFloorAt checks cached models.json pool quotas at clock time now,
// validating DefaultPoolQuotaTTL freshness and skipping DisabledWindow (-1).
func CheckBudgetFloorAt(home string, requireQuota bool, now time.Time) (QuotaStatus, error) {
	cat, ok, err := models.LoadCatalog(home)
	if err != nil {
		return QuotaStatus{OK: false, Note: "models.json unreadable: " + err.Error()}, nil
	}
	if !ok {
		return QuotaStatus{OK: !requireQuota, Note: missingQuotaNote(requireQuota, "models.json")}, nil
	}
	status := EvaluatePoolsFloor(cat.Pools, requireQuota)
	if len(cat.Pools) > 0 && !models.HasFreshPoolQuotas(cat, now, models.DefaultPoolQuotaTTL) {
		status.Note = "stale quota cache: " + status.Note
		if requireQuota {
			status.OK = false
		}
	}
	return status, nil
}

func missingQuotaNote(required bool, what string) string {
	if required {
		return what + " missing (quota required)"
	}
	return what + " absent (quota check skipped)"
}

// EvaluatePoolsFloor checks active Gemini pools against the 20% 5h / 15% weekly floors,
// skipping disabled (-1) windows rather than treating them as below floor.
func EvaluatePoolsFloor(pools []models.Pool, requireQuota bool) QuotaStatus {
	if len(pools) == 0 {
		return QuotaStatus{OK: !requireQuota, Note: missingQuotaNote(requireQuota, "quota pools")}
	}
	var gemPools []models.Pool
	for _, p := range pools {
		low := strings.ToLower(p.Name)
		if strings.Contains(low, "gemini") || strings.Contains(low, "google") {
			gemPools = append(gemPools, p)
		}
	}
	if len(gemPools) == 0 {
		return QuotaStatus{OK: !requireQuota, Note: missingQuotaNote(requireQuota, "Gemini pool"), Pools: pools}
	}
	parts := make([]string, 0, len(gemPools))
	for _, p := range gemPools {
		fiveLow := p.Five != models.DisabledWindow && p.Five < GeminiFloorFive
		weekLow := p.Week != models.DisabledWindow && p.Week < GeminiFloorWeek
		if fiveLow || weekLow {
			note := fmt.Sprintf("%s below floor (5h=%s, week=%s; floor %d%%/%d%%)",
				p.Name, formatPct(p.Five), formatPct(p.Week), GeminiFloorFive, GeminiFloorWeek)
			return QuotaStatus{OK: false, Note: note, Pools: gemPools}
		}
		parts = append(parts, fmt.Sprintf("%s 5h=%s week=%s", p.Name, formatPct(p.Five), formatPct(p.Week)))
	}
	return QuotaStatus{OK: true, Note: strings.Join(parts, "; "), Pools: gemPools}
}

func formatPct(v int) string {
	if v == models.DisabledWindow || v < 0 {
		return "disabled"
	}
	return strconv.Itoa(v) + "%"
}

var (
	normTimestampRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[tT ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:[zZ]|[+-]\d{2}:?\d{2})?`)
	normDurationRe  = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h)\b`)
	normLineColRe   = regexp.MustCompile(`:\d+(?::\d+)?\b`)
	normHexSHARe    = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
)

// NormalizeDiagnostic strips volatile timestamps, durations, line numbers, and SHAs.
func NormalizeDiagnostic(diag string) string {
	s := strings.ToLower(search.Printable(strings.TrimSpace(diag)))
	s = normTimestampRe.ReplaceAllString(s, "<ts>")
	s = normLineColRe.ReplaceAllString(s, ":<n>")
	s = normDurationRe.ReplaceAllString(s, "<dur>")
	s = normHexSHARe.ReplaceAllString(s, "<sha>")
	return strings.Join(strings.Fields(s), " ")
}

// HashDiagnostic returns the SHA-256 hex digest of NormalizeDiagnostic(diag).
func HashDiagnostic(diag string) string {
	sum := sha256.Sum256([]byte(NormalizeDiagnostic(diag)))
	return hex.EncodeToString(sum[:])
}

// ErrorTracker detects repeating normalized failure diagnostics across attempts/runs.
type ErrorTracker struct{ seen map[string]int }

// NewErrorTracker constructs an ErrorTracker optionally seeded with prior hashes.
func NewErrorTracker(seed ...string) *ErrorTracker {
	t := &ErrorTracker{seen: map[string]int{}}
	t.Seed(seed)
	return t
}

// Seed registers already-observed failure hashes (e.g. from prior halted handoffs).
func (t *ErrorTracker) Seed(hashes []string) {
	for _, h := range hashes {
		if clean := strings.ToLower(strings.TrimSpace(h)); clean != "" {
			t.seen[clean]++
		}
	}
}

// Record hashes diag and reports whether the same normalized hash was already seen.
func (t *ErrorTracker) Record(diag string) (hash string, repeated bool) {
	hash = HashDiagnostic(diag)
	t.seen[hash]++
	return hash, t.seen[hash] >= 2
}

// LoadPriorErrorHashes reads brain.db handoffs explicitly sorted newest-first
// for planID until hitting a passing checkpoint, returning prior Relay-Error-Hash digests.
func LoadPriorErrorHashes(home, dir, planID string) []string {
	recs, err := brain.List(brain.Options{Home: home, Dir: dir}, brain.ListFilter{Kind: "handoff"})
	if err != nil {
		return nil
	}
	sortRecordsNewestFirst(recs)
	var hashes []string
	planLine := "Plan: " + strings.TrimSpace(planID)
	for _, r := range recs {
		if !hasBodyLine(r.Body, planLine) {
			continue
		}
		if hasBodyLine(r.Body, "Relay-Outcome: PASS") {
			break
		}
		if h := headerVal(r.Body, "Relay-Error-Hash:"); h != "" {
			hashes = append(hashes, h)
		}
	}
	return hashes
}

func sortRecordsNewestFirst(recs []brain.Record) {
	slices.SortStableFunc(recs, func(a, b brain.Record) int {
		ta, oka := parseRecordTime(a.Updated)
		tb, okb := parseRecordTime(b.Updated)
		if oka && okb && !ta.Equal(tb) {
			if ta.After(tb) {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(b.Updated, a.Updated), cmp.Compare(b.Date, a.Date), cmp.Compare(b.ID, a.ID))
	})
}

func parseRecordTime(raw string) (time.Time, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

func hasBodyLine(body, want string) bool {
	for _, l := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "## ") {
			return false
		}
		if trimmed == want {
			return true
		}
	}
	return false
}

func headerVal(body, prefix string) string {
	for _, l := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "## ") {
			return ""
		}
		if rest, ok := strings.CutPrefix(trimmed, prefix); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}
