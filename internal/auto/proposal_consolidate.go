package auto

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/redact"
)

const stillRegressingProposalTTL = 14 * 24 * time.Hour

// consolidateProposals deduplicates open proposals sharing the same canonical
// key and TTL-retires stale derived still_regressing proposals during a real
// sweep. It edits records in place via brain.Edit and never deletes rows from
// brain.db (PRD R7).
func (r *run) consolidateProposals() {
	if r.dry || !brainDBExists(r.o.Home) {
		return
	}
	g := brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}
	open, err := brain.List(g, brain.ListFilter{Kind: "proposal", Open: true})
	if err != nil || len(open) == 0 {
		return
	}
	survivors, merged := r.consolidateDuplicateGroups(g, open)
	retired := r.retireStaleStillRegressing(g, survivors)
	if merged > 0 || retired > 0 {
		r.invalidateProposalCache()
		r.say(fmt.Sprintf("proposals: consolidated %d duplicate(s), retired %d stale still_regressing", merged, retired))
	}
}

func (r *run) consolidateDuplicateGroups(g brain.Options, open []brain.Record) (survivors []brain.Record, merged int) {
	groups := map[string][]brain.Record{}
	var order []string
	for _, rec := range open {
		key := proposalDedupeKey(rec)
		if key == "" {
			survivors = append(survivors, rec)
			continue
		}
		provKey := "operator|" + key
		if isDerivedProposal(rec) {
			provKey = "derived|" + key
		}
		if _, exists := groups[provKey]; !exists {
			order = append(order, provKey)
		}
		groups[provKey] = append(groups[provKey], rec)
	}
	for _, key := range order {
		grp := groups[key]
		if len(grp) == 1 {
			survivors = append(survivors, grp[0])
			continue
		}
		kept, count := r.mergeProposalGroup(g, key, grp)
		survivors = append(survivors, kept)
		merged += count
	}
	return survivors, merged
}

func (r *run) mergeProposalGroup(g brain.Options, key string, grp []brain.Record) (brain.Record, int) {
	slices.SortFunc(grp, compareCanonicalProposal)
	keep := grp[0]
	totalOcc := 0
	latest := time.Time{}
	var superseded []string
	for i, rec := range grp {
		totalOcc += parseProposalOccurrences(rec.Body)
		if ts := proposalLastSeen(rec); ts.After(latest) {
			latest = ts
		}
		if i > 0 {
			superseded = append(superseded, rec.ID)
		}
	}
	if latest.IsZero() {
		latest = r.o.now()
	}
	updatedKeep := formatConsolidatedSurvivorBody(keep, totalOcc, latest, superseded)
	if err := brain.Edit(g, brain.EditInput{ID: keep.ID, Body: strings.NewReader(redact.Redact(updatedKeep))}); err != nil {
		return keep, 0
	}
	keep.Body = updatedKeep
	merged := 0
	rejected := "rejected"
	for _, dup := range grp[1:] {
		note := fmt.Sprintf("## Auto-consolidated\n\nSuperseded by %s (duplicate key: `%s`; record preserved per R7).", keep.ID, oneLine(key))
		body := strings.TrimRight(dup.Body, "\n") + "\n\n" + note + "\n"
		if brain.Edit(g, brain.EditInput{ID: dup.ID, Status: &rejected, Body: strings.NewReader(redact.Redact(body))}) == nil {
			merged++
		}
	}
	return keep, merged
}

func compareCanonicalProposal(a, b brain.Record) int {
	aDer, bDer := isDerivedProposal(a), isDerivedProposal(b)
	if aDer != bDer {
		if !aDer {
			return -1
		}
		return 1
	}
	at, bt := proposalLastSeen(a), proposalLastSeen(b)
	if !at.Equal(bt) {
		if at.After(bt) {
			return -1
		}
		return 1
	}
	return strings.Compare(b.ID, a.ID)
}

func formatConsolidatedSurvivorBody(keep brain.Record, totalOcc int, latest time.Time, superseded []string) string {
	headers, rest := splitProposalHeaderAndBody(keep.Body)
	headers = upsertHeaderLine(headers, "Occurrences", strconv.Itoa(totalOcc))
	headers = upsertHeaderLine(headers, "LastSeen", latest.UTC().Format(time.RFC3339))
	shown := superseded
	extra := 0
	if len(shown) > 10 {
		extra = len(shown) - 10
		shown = shown[:10]
	}
	note := "Consolidated duplicates: " + strings.Join(shown, ", ")
	if extra > 0 {
		note += fmt.Sprintf(" (+%d more)", extra)
	}
	if isDerivedProposal(keep) {
		note = "    [" + note + "]"
	} else {
		note = "## Auto-consolidated\n\n" + note
	}
	return assembleProposalBody(headers, rest, note)
}

func (r *run) retireStaleStillRegressing(g brain.Options, open []brain.Record) int {
	dig, _ := metrics.ReadLatestDailyDigest(r.o.Home)
	recovered := recoveredSignalSet(dig)
	now := r.o.now()
	retired := 0
	rejected := "rejected"
	for _, rec := range open {
		if !isDerivedProposal(rec) || !isStillRegressingProposal(rec) {
			continue
		}
		reason := staleStillRegressingReason(rec, recovered, now)
		if reason == "" {
			continue
		}
		note := fmt.Sprintf("## Auto-consolidated\n\n%s (record preserved per R7).", reason)
		body := strings.TrimRight(rec.Body, "\n") + "\n\n" + note + "\n"
		if brain.Edit(g, brain.EditInput{ID: rec.ID, Status: &rejected, Body: strings.NewReader(redact.Redact(body))}) == nil {
			retired++
		}
	}
	return retired
}

func recoveredSignalSet(dig *metrics.DailyDigest) map[string]bool {
	if dig == nil {
		return nil
	}
	out := map[string]bool{}
	for _, a := range dig.Actions {
		if a.SignalID != "" && a.Status == metrics.StatusRecovered {
			out[a.SignalID] = true
		}
	}
	return out
}

func staleStillRegressingReason(rec brain.Record, recovered map[string]bool, now time.Time) string {
	sigID, _ := extractProposalAttribution(rec)
	if sigID == "" {
		sigID = firstSignalID(rec.Title)
	}
	if sigID != "" && recovered[sigID] {
		return fmt.Sprintf("Signal `%s` recovered in latest daily digest", sigID)
	}
	lastSeen := proposalLastSeen(rec)
	if !lastSeen.IsZero() && now.Sub(lastSeen) > stillRegressingProposalTTL {
		return fmt.Sprintf("TTL expired: no recurrence of still_regressing for >14d (last seen %s)", lastSeen.UTC().Format(time.RFC3339))
	}
	return ""
}
