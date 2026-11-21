package auto

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/redact"
)

type sectionItem struct {
	droppedLabel string
	rawEntry     string
}

func appendBoundedSection(b *strings.Builder, heading string, items []sectionItem, realHome, home string, maxEntry, limit int) []string {
	var dropped []string
	wroteHeader, capped := false, false
	for _, it := range items {
		if capped || b.Len() >= limit {
			dropped = append(dropped, it.droppedLabel)
			continue
		}
		entry := boundProposalEntry(scrubEntry(it.rawEntry, realHome, home), maxEntry)
		header := ""
		if !wroteHeader {
			header = sectionBreakPrefix(b.String()) + heading + "\n\n"
		}
		rem := limit - b.Len() - len(header)
		if nl := strings.IndexByte(entry, '\n'); nl >= 0 && rem <= nl+3 {
			rem = 0
		}
		if bounded := boundProposalEntry(entry, rem); bounded == "" {
			capped = true
			dropped = append(dropped, it.droppedLabel)
		} else {
			b.WriteString(header + bounded)
			wroteHeader = true
		}
	}
	return dropped
}

func droppedRecordLabel(kind, id, title string) string {
	if t := strings.TrimSpace(title); t != "" {
		return kind + ": " + id + " " + t
	}
	return kind + ": " + id
}

func appendMemorySection(b *strings.Builder, heading string, opts brain.Options, realHome string, limit int) []string {
	mems, err := brain.List(opts, brain.ListFilter{Kind: "memory"})
	if err != nil || len(mems) == 0 {
		return nil
	}
	items := make([]sectionItem, 0, len(mems))
	for _, m := range mems {
		if m.Status == "closed" {
			raw := fmt.Sprintf("### %s (provenance %s, kind %s, validity %.2f)\n%s\n\n", m.ID, m.Provenance, m.MemoryKind, m.Validity, m.Body)
			items = append(items, sectionItem{droppedLabel: droppedRecordLabel("memory", m.ID, m.Title), rawEntry: raw})
		}
	}
	return appendBoundedSection(b, heading, items, realHome, opts.Home, maxMemoryEntryBytes, limit)
}

func orderMatchingProposals(props []brain.Record, root, taskID string) []brain.Record {
	var first, rest []brain.Record
	for _, pr := range props {
		if pr.ID == taskID {
			first = append(first, pr)
		} else if pr.Project == root || isHarnessTargetProposal(root, pr) {
			rest = append(rest, pr)
		}
	}
	return append(first, rest...)
}

func (r *run) writeProposalSection(b *strings.Builder, props []brain.Record, limit int) []string {
	if len(props) == 0 {
		return nil
	}
	items := make([]sectionItem, len(props))
	for i, pr := range props {
		raw := fmt.Sprintf("### %s: %s\n%s\n\n", pr.ID, oneLine(pr.Title), pr.Body)
		items[i] = sectionItem{droppedLabel: droppedRecordLabel("proposal", pr.ID, pr.Title), rawEntry: raw}
	}
	return appendBoundedSection(b, "## Queued proposals", items, r.o.realHome(), r.o.Home, maxProposalEntryBytes, limit)
}

func appendTelemetrySections(b *strings.Builder, nums, diag, traj, realHome, home string, limit int) []string {
	return appendTelemetrySectionsWithDigest(b, nums, diag, "", traj, realHome, home, limit)
}

func appendTelemetrySectionsWithDigest(b *strings.Builder, nums, diag, digest, traj, realHome, home string, limit int) []string {
	nums, diag = scrubEntry(nums, realHome, home), scrubEntry(diag, realHome, home)
	digest, traj = scrubEntry(digest, realHome, home), scrubEntry(traj, realHome, home)
	items := []struct{ label, head, body string }{
		{"telemetry: metric numbers", "## Metric numbers\n", nums},
		{"telemetry: diagnostic signals", "## Diagnostic signals\n", diag},
		{"telemetry: daily digest", "## Daily digest\n", digest},
		{"telemetry: trajectory digest", "## Trajectory digest\n", traj},
	}
	var dropped []string
	for i, it := range items {
		reserve := 0
		for _, rem := range items[i+1:] {
			if rem.body != "" {
				reserve += telemetryMinShare
			}
		}
		if d := appendCappedSection(b, it.label, it.head, it.body, max(b.Len(), limit-reserve)); d != "" {
			dropped = append(dropped, d)
		}
	}
	return dropped
}

func appendCappedSection(b *strings.Builder, label, heading, body string, limit int) string {
	if body == "" {
		return ""
	}
	prefix := sectionBreakPrefix(b.String())
	rem := limit - b.Len()
	if rem <= len(prefix)+len(heading) {
		return label
	}
	b.WriteString(truncateUTF8(prefix+heading+body+"\n\n", rem))
	return ""
}

func (r *run) appendConversationSections(b *strings.Builder, root string, p *Project, t Task, cutoff time.Time, limit int) []string {
	since := learningConvSince(p, t.Class)
	useDigest := t.Class == ClassRetroLite || t.Class == ClassMemoryCandidates || t.Class == ClassMemoryConsolidation
	realHome := r.o.realHome()
	var dropped []string
	capped := false
	for _, conv := range r.conversationsSinceCutoff(root, since, cutoff) {
		if capped || b.Len() >= limit {
			dropped = append(dropped, "transcript: "+conv.label)
			continue
		}
		rawConv := fmt.Sprintf("## Conversation %s\n%s\n\n", conv.label, conv.text)
		if useDigest {
			rawConv = r.sessionDigestFor(conv).Format()
		}
		scrubbed := scrubEntry(rawConv, realHome, r.o.Home)
		prefix := sectionBreakPrefix(b.String())
		rem := limit - b.Len() - len(prefix)
		fits := rem > len("## Conversation ")+len(conv.label)+2
		if useDigest {
			fits = len(scrubbed) <= rem
		}
		if !fits {
			capped = true
			dropped = append(dropped, "transcript: "+conv.label)
			continue
		}
		b.WriteString(prefix + truncateUTF8(scrubbed, rem))
	}
	return dropped
}

func learningConvSince(p *Project, class string) time.Time {
	if class == ClassMemoryCandidates {
		return p.LastMemoryCandidates
	} else if class == ClassMemoryConsolidation {
		return p.LastMemoryConsolidation
	}
	return p.LastRetroLite
}

func sectionBreakPrefix(s string) string {
	if s == "" || strings.HasSuffix(s, "\n\n") {
		return ""
	} else if strings.HasSuffix(s, "\n") {
		return "\n"
	}
	return "\n\n"
}

func boundProposalEntry(entry string, maxBytes int) string {
	const sep = "\n\n"
	if maxBytes <= 0 || (maxBytes <= len(sep) && len(entry) > maxBytes) {
		return ""
	}
	if len(entry) <= maxBytes {
		return entry
	}
	head := truncateUTF8(entry, maxBytes-len(sep))
	if nl := strings.LastIndexByte(head, '\n'); nl >= len(head)/2 {
		head = head[:nl]
	}
	if head = strings.TrimRight(head, "\r\n"); head == "" {
		return ""
	}
	return head + sep
}

func (r *run) redactedMetricsDiagnosticsAndTrajectory(root string) (string, string, string) {
	nums := ""
	if sum := r.projectSummary(root); sum != nil {
		nums = marshalRedactedJSON(func() (any, error) { return sum.Numbers(), nil })
	}
	diag := r.redactedDiagnosticSection(root)
	s, closeSnap := r.sweepMetricsSnapshot()
	if s == nil {
		return nums, diag, ""
	}
	defer closeSnap()
	return nums, diag, marshalRedactedJSON(func() (any, error) { return s.DreamTrajectoryDigest(root, r.o.now().AddDate(0, 0, -30)) })
}

func marshalRedactedJSON(fn func() (any, error)) string {
	v, err := fn()
	if err != nil {
		return ""
	}
	if b, err := json.MarshalIndent(v, "", "  "); err == nil {
		return redact.RedactJSONL(string(b))
	}
	return ""
}
