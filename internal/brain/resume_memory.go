package brain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/search"
)

func writeResumeMemory(b *strings.Builder, top string, recs []Record, now time.Time) {
	b.WriteString("\n## Memory\n")
	var feedbacks []Record
	for _, r := range recs {
		if r.Kind == "feedback" {
			feedbacks = append(feedbacks, r)
		}
	}
	var mems []scoredMemory
	for _, r := range recs {
		if r.Kind != "memory" || r.Status != "closed" || resolveMemoryKind(r) != "semantic" {
			continue
		}
		staleTag := MemoryStalenessTag(top, r)
		val := computeMemoryValidityWithStale(r, staleTag != "", CountMemoryContradictions(r, feedbacks), now)
		if val >= MinActiveValidity {
			mems = append(mems, scoredMemory{rec: r, val: val, staleTag: staleTag})
		}
	}
	if len(mems) == 0 {
		b.WriteString("(none)\n")
		return
	}
	sortScoredMemories(mems)
	base := b.Len()
	for i, item := range mems {
		l := formatMemoryIndexLineWithStale(item.rec, item.val, item.staleTag)
		if i == memoryMaxLines || (b.Len()-base)+len(l)+1 > memoryMaxBytes {
			fmt.Fprintf(b, "(… %d more: fyr record list --kind memory, then fyr record get MEMORY-<name>)\n", len(mems)-i)
			return
		}
		b.WriteString(l + "\n")
	}
}

func formatMemoryIndexLineWithStale(r Record, val float64, stale string) string {
	stale = search.Printable(stale)
	var scopeTag string
	if r.Scope == "" {
		scopeTag = " [global]"
	}
	l := search.Printable(fmt.Sprintf("- %s (%s, %.2f)%s — %s",
		strings.TrimPrefix(r.ID, "MEMORY-"), resolveMemoryKind(r), val, scopeTag, r.Title))
	limit := memoryMaxLine
	if stale != "" && limit > len(stale)+1 {
		limit -= len(stale) + 1
	}
	for len(l) > limit {
		_, size := utf8.DecodeLastRuneInString(l)
		l = l[:len(l)-size]
	}
	if stale != "" {
		l += " " + stale
	}
	return l
}

var sha256PinRe = regexp.MustCompile(`(?i)\bsha256[:=\s]+([0-9a-f]{7,64})\b`)

type repoFileRef struct {
	rel    string
	pinHex string
}

// MemoryStalenessTag returns "[STALE: <path> modified since <ID>]" when a memory
// record references a repository file whose content SHA-256 prefix no longer
// matches a hash pin in the record (unchanged mtime touches incur 0 penalty).
func MemoryStalenessTag(top string, r Record) string {
	if top == "" || (r.Kind != "" && r.Kind != "memory") {
		return ""
	}
	text := r.Title + "\n" + r.Body
	for _, ref := range extractRepoFileRefs(top, text) {
		if ref.pinHex == "" {
			continue
		}
		abs := filepath.Join(top, filepath.FromSlash(ref.rel))
		fi, err := os.Lstat(abs)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		data, err := readRegular(abs, maxBody)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		if !strings.HasPrefix(hex.EncodeToString(sum[:]), ref.pinHex) {
			return fmt.Sprintf("[STALE: %s modified since %s]", ref.rel, r.ID)
		}
	}
	return ""
}

func appendRepoFilePins(top, title, body string) string {
	return appendRepoFilePinsBounded(top, title, body, maxBody)
}

func appendRepoFilePinsBounded(top, title, body string, limit int) string {
	if top == "" {
		return body
	}
	refs := extractRepoFileRefs(top, title+"\n"+body)
	var lines []string
	for _, ref := range refs {
		if ref.pinHex != "" {
			continue
		}
		abs := filepath.Join(top, filepath.FromSlash(ref.rel))
		data, err := readRegular(abs, maxBody)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		lines = append(lines, fmt.Sprintf("<!-- pin: %s sha256:%s -->", ref.rel, hex.EncodeToString(sum[:6])))
	}
	if len(lines) == 0 {
		return body
	}
	block := strings.Join(lines, "\n")
	sep := "\n\n"
	if body == "" {
		sep = ""
	}
	if len(body)+len(sep)+len(block) > limit {
		return body
	}
	return body + sep + block
}

func parseRecordTimestamp(r Record) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, strings.TrimSpace(r.Updated)); err == nil {
			return t
		}
	}
	if t, err := time.Parse("2006-01-02", strings.TrimSpace(r.Date)); err == nil {
		return t.Add(24*time.Hour - time.Nanosecond)
	}
	return time.Time{}
}

func extractRepoFileRefs(top, text string) []repoFileRef {
	idxByPath := map[string]int{}
	var out []repoFileRef
	for _, line := range strings.Split(text, "\n") {
		linePin := ""
		if m := sha256PinRe.FindStringSubmatch(line); len(m) == 2 {
			linePin = strings.ToLower(m[1])
		}
		var linePaths []string
		for _, raw := range strings.Fields(line) {
			cand := cleanCandidatePath(raw)
			if cand == "" {
				continue
			}
			abs := filepath.Join(top, filepath.FromSlash(cand))
			if fi, err := os.Lstat(abs); err == nil && fi.Mode().IsRegular() {
				linePaths = append(linePaths, cand)
				if _, seen := idxByPath[cand]; !seen {
					idxByPath[cand] = len(out)
					out = append(out, repoFileRef{rel: cand})
				}
			}
		}
		if linePin != "" && len(linePaths) > 0 {
			last := linePaths[len(linePaths)-1]
			if out[idxByPath[last]].pinHex == "" {
				out[idxByPath[last]].pinHex = linePin
			}
		}
	}
	if len(out) == 1 && out[0].pinHex == "" {
		if m := sha256PinRe.FindStringSubmatch(text); len(m) == 2 {
			out[0].pinHex = strings.ToLower(m[1])
		}
	}
	return out
}

func cleanCandidatePath(raw string) string {
	s := strings.Trim(raw, "\"'`()[]{}<>,;!")
	s = strings.TrimPrefix(s, "./")
	if idx := strings.IndexAny(s, ":#"); idx > 0 {
		s = s[:idx]
	}
	s = strings.TrimRight(s, ".:")
	if s == "" || strings.Contains(s, "://") || strings.Contains(s, "..") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "-") {
		return ""
	}
	if !strings.Contains(s, "/") && filepath.Ext(s) == "" {
		return ""
	}
	clean := filepath.ToSlash(filepath.Clean(s))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "..") {
		return ""
	}
	return clean
}
