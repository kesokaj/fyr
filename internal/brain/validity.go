package brain

import (
	"math"
	"strings"
	"time"
	"unicode"
)

// MinActiveValidity is the minimum computed validity score required for a
// memory card to appear in default `fyr memory show`, `fyr resume`, and
// `PreInvocation` injection.
const MinActiveValidity = 0.40

var memoryMatchStopwords = map[string]bool{
	"about": true, "after": true, "again": true, "also": true, "always": true,
	"another": true, "before": true, "being": true, "below": true, "better": true,
	"between": true, "both": true, "could": true, "does": true, "doing": true,
	"done": true, "each": true, "else": true, "enough": true, "even": true,
	"every": true, "example": true, "explain": true, "find": true, "first": true,
	"from": true, "further": true, "give": true, "good": true, "great": true,
	"have": true, "having": true, "help": true, "here": true, "however": true,
	"into": true, "just": true, "keep": true, "know": true, "like": true,
	"list": true, "look": true, "make": true, "many": true, "more": true,
	"most": true, "much": true, "must": true, "need": true, "never": true,
	"next": true, "note": true, "notes": true, "often": true, "only": true,
	"other": true, "over": true, "part": true, "please": true, "same": true,
	"several": true, "should": true, "show": true, "since": true, "some": true,
	"something": true, "still": true, "such": true, "sure": true, "take": true,
	"than": true, "that": true, "their": true, "them": true, "then": true,
	"there": true, "these": true, "they": true, "thing": true, "think": true,
	"this": true, "those": true, "through": true, "under": true, "until": true,
	"upon": true, "used": true, "using": true, "very": true, "want": true,
	"well": true, "were": true, "what": true, "when": true, "where": true,
	"whether": true, "which": true, "while": true, "with": true, "without": true,
	"work": true, "works": true, "would": true, "your": true,
}

// CountMemoryContradictions counts FEEDBACK-* records strictly newer than
// max(r.VerifiedAt, r.Updated) that match r's memory slug or >= 2 non-stopword
// title tokens (len >= 4).
func CountMemoryContradictions(r Record, feedbacks []Record) int {
	slug := strings.ToLower(strings.TrimPrefix(r.ID, "MEMORY-"))
	tokens := titleMatchTokens(r.Title)
	refTime := memoryRefTime(r)
	count := 0
	for _, fb := range feedbacks {
		if fb.Kind != "feedback" && !strings.HasPrefix(fb.ID, "FEEDBACK-") {
			continue
		}
		fbTime := parseRecordTimestamp(fb)
		if !refTime.IsZero() && !fbTime.After(refTime) {
			continue
		}
		text := strings.ToLower(fb.Title + "\n" + fb.Body)
		if (slug != "" && strings.Contains(text, slug)) || countTokenMatches(text, tokens) >= 2 {
			count++
		}
	}
	return count
}

func titleMatchTokens(title string) []string {
	parts := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := map[string]bool{}
	var out []string
	for _, p := range parts {
		if len(p) < 4 || memoryMatchStopwords[p] || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func countTokenMatches(text string, tokens []string) int {
	hits := 0
	for _, tok := range tokens {
		if strings.Contains(text, tok) {
			hits++
		}
	}
	return hits
}

// ComputeMemoryValidity computes the deterministic validity score [0.00, 1.00]
// of a memory record at now, considering provenance, evidence count, file/hash
// staleness in top, contradiction count, and part-specific age decay.
func ComputeMemoryValidity(top string, r Record, contraCount int, now time.Time) float64 {
	score := memoryBaseValidity(r.Provenance, r.EvidenceCount)
	if top != "" && MemoryStalenessTag(top, r) != "" {
		score -= 0.45
	}
	if contraCount > 0 {
		score -= 0.25 * float64(min(contraCount, 2))
	}
	score *= memoryAgeDecay(r, now)
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return math.Round(score*100) / 100
}

func memoryBaseValidity(prov string, evidence int) float64 {
	bonus := 0.05 * float64(min(max(evidence, 0), 3))
	switch prov {
	case "operator":
		return 1.00
	case "derived":
		return math.Min(0.55+bonus, 0.70)
	default:
		return math.Min(0.70+bonus, 0.85)
	}
}

func memoryAgeDecay(r Record, now time.Time) float64 {
	refTime := memoryRefTime(r)
	if refTime.IsZero() {
		return 1.0
	}
	ageDays := now.Sub(refTime).Hours() / 24.0
	if ageDays <= 0 {
		return 1.0
	}
	halfLife := 180.0
	switch resolveMemoryKind(r) {
	case "procedural":
		halfLife = 90.0
	case "episodic":
		halfLife = 30.0
	}
	return math.Max(0.40, math.Pow(0.5, ageDays/halfLife))
}

func memoryRefTime(r Record) time.Time {
	upd := parseRecordTimestamp(r)
	ver := parseTimeString(r.VerifiedAt)
	if ver.After(upd) {
		return ver
	}
	return upd
}

func parseTimeString(raw string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			return t
		}
	}
	return time.Time{}
}

func resolveMemoryKind(r Record) string {
	if r.MemoryKind != "" {
		if norm, err := NormalizeMemoryType(r.MemoryKind); err == nil {
			return norm
		}
	}
	if m := memoryType.FindStringSubmatch(r.Body); m != nil {
		if norm, err := NormalizeMemoryType(m[1]); err == nil {
			return norm
		}
	}
	if strings.HasPrefix(r.Body, "type: ") {
		line, _, _ := strings.Cut(r.Body, "\n")
		if norm, err := NormalizeMemoryType(strings.TrimSpace(strings.TrimPrefix(line, "type: "))); err == nil {
			return norm
		}
	}
	return "semantic"
}
