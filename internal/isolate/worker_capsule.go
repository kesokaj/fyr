package isolate

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// KickoffCapsuleBudget is the maximum token budget (<= 1,500 tokens at
// chars / 4) for structural --slice capsules attached to worker kickoffs.
const KickoffCapsuleBudget = 1500

// CapsulePivot identifies a full-body symbol span rendered in a Kickoff's
// structural context capsule so read_ledger can pre-seed it.
type CapsulePivot struct {
	Path               string
	StartLine, EndLine int
}

var (
	capsuleHeaderRe = regexp.MustCompile(`^\[Hop [01]\] ([^:\s]+):(\d+)-(\d+) `)
	// DefaultSliceFunc and DefaultPreseedFunc are optional hooks registered by
	// callers that link internal/search so ParseKickoff attaches structural
	// capsules without internal/isolate importing modernc.org/sqlite (ADR-063).
	DefaultSliceFunc   func(dir, query string, budget int) (string, error)
	DefaultPreseedFunc func(dir string, pivots []CapsulePivot) error
)

func kickoffProjectRoot(text, path string) string {
	for _, line := range prefixed(text, "Project:") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) > 0 {
			cand := strings.TrimRight(fields[0], ".,;")
			if filepath.IsAbs(cand) {
				return cand
			}
		}
	}
	dir := filepath.Dir(path)
	for i := 0; i < 6 && dir != "" && dir != "/" && dir != "."; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".agents", "scratch", "search.db")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// sliceQuery returns the space-separated file/symbol query for building a
// structural --slice capsule from the Kickoff's Files: scope.
func (k Kickoff) sliceQuery() string {
	var parts []string
	for _, e := range k.Files {
		if recordID.MatchString(e) {
			continue
		}
		if clean := strings.TrimSuffix(strings.TrimSpace(e), "/"); clean != "" {
			parts = append(parts, clean)
		}
	}
	if len(parts) == 0 {
		return k.Name
	}
	return strings.Join(parts, " ")
}

func (k Kickoff) sliceQueryForRoot(root string) string {
	q := k.sliceQuery()
	for _, e := range k.Files {
		if !strings.HasSuffix(strings.ToLower(e), ".md") || recordID.MatchString(e) {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(root, e)); err == nil && len(b) > 0 {
			if len(b) > 8192 {
				b = b[:8192]
			}
			q += " " + string(b)
		}
	}
	return strings.TrimSpace(q)
}

// ParseKickoffCapsulePivots extracts full-body Hop-0 and Hop-1 file spans from
// a rendered --slice capsule for read_ledger pre-seeding, skipping snippet-only
// Hop-1 call-site entries.
func ParseKickoffCapsulePivots(capsule string) []CapsulePivot {
	var out []CapsulePivot
	for _, line := range strings.Split(capsule, "\n") {
		if strings.Contains(line, " snippet ") || strings.Contains(line, "[call site") {
			continue
		}
		m := capsuleHeaderRe.FindStringSubmatch(line)
		if len(m) != 4 {
			continue
		}
		start, err1 := strconv.Atoi(m[2])
		end, err2 := strconv.Atoi(m[3])
		if err1 == nil && err2 == nil && start > 0 && end >= start {
			out = append(out, CapsulePivot{Path: m[1], StartLine: start, EndLine: end})
		}
	}
	return out
}

// FormatKickoffCapsule appends a structural --slice capsule (<= 1,500 tokens)
// and pre-seeds read_ledger when .agents/scratch/search.db exists under root.
func FormatKickoffCapsule(
	k Kickoff,
	root string,
	sliceFn func(dir, query string, budget int) (string, error),
	preseedFn func(dir string, pivots []CapsulePivot) error,
) string {
	if root == "" || sliceFn == nil || strings.Contains(k.Text, "## Structural Context Capsule\n") {
		return k.Text
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", "scratch", "search.db")); err != nil {
		return k.Text
	}
	capsule, err := sliceFn(root, k.sliceQueryForRoot(root), KickoffCapsuleBudget)
	capsule = strings.TrimSpace(capsule)
	if err != nil || capsule == "" {
		return k.Text
	}
	maxChars := KickoffCapsuleBudget * 4
	truncated := len(capsule) > maxChars
	if truncated {
		capsule = strings.TrimSpace(truncateUTF8(capsule, maxChars))
	}
	if preseedFn != nil {
		pivots := ParseKickoffCapsulePivots(capsule)
		if truncated && len(pivots) > 0 {
			pivots = pivots[:len(pivots)-1]
		}
		if len(pivots) > 0 {
			_ = preseedFn(root, pivots)
		}
	}
	base := strings.TrimRight(k.Text, "\n")
	return base + "\n\n## Structural Context Capsule\n" + capsule + "\n"
}

func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}
