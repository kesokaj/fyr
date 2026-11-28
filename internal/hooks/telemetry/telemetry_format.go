package telemetry

import (
	"fmt"
	"github.com/kesokaj/fyr/internal/vet"
	"strconv"
	"strings"
	"unicode"
)

func SafeBacktickTextFence(text string) string {
	body := strings.TrimRight(text, "\n")
	fence := safeBacktickFence(body)
	return fence + "text\n" + body + "\n" + fence
}

func TurnBannerMetaFromExtras(extraSections, extraBanners []string) TurnBannerMeta {
	meta := TurnBannerMeta{ExtraSections: extraSections}
	var b PreInvocationBanner
	for _, extra := range extraBanners {
		ApplyExtraBanner(&b, extra)
	}
	meta.CompactedGeneration = b.CompactedGeneration
	meta.DirtyFastPath = b.DirtyFastPath
	meta.PivotCount = b.PivotCount
	meta.ContextKneeWarning = b.TokenKneeWarning
	meta.ModelTip = b.ModelTip
	meta.StoreError = b.StoreError
	meta.Notices = b.Notices
	return meta
}

func cleanSubtaskSlices(items []vet.SubtaskSlice) []vet.SubtaskSlice {
	if len(items) == 0 {
		return nil
	}
	out := make([]vet.SubtaskSlice, 0, len(items))
	for _, it := range items {
		out = append(out, vet.SubtaskSlice{
			ID:     vet.CollapseOneLine(Printable(it.ID), 40),
			Role:   vet.CollapseOneLine(Printable(it.Role), 40),
			Goal:   vet.CollapseOneLine(Printable(it.Goal), 200),
			Files:  CleanPrintableSlice(it.Files, 200),
			Skills: CleanPrintableSlice(it.Skills, 80),
			Verify: vet.CollapseOneLine(Printable(it.Verify), 160),
		})
	}
	return out
}

func TruncateBytesUTF8(s string, maxB int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxB {
		return s
	}
	i := maxB
	for i > 0 && (s[i]&0xc0) == 0x80 {
		i--
	}
	return strings.TrimSpace(s[:i])
}

func CompactRecordNote(r vet.MatchedRecord, title string) string {
	lines := strings.Split(strings.ReplaceAll(r.Body, "\r\n", "\n"), "\n")
	var kept []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		return ""
	}
	note := vet.CollapseOneLine(Printable(strings.Join(kept, " ")), 220)
	if note == "" || strings.EqualFold(note, title) {
		return ""
	}
	return note
}

func CleanPrintableSlice(items []string, maxLen int) []string {
	var out []string
	for _, item := range items {
		clean := vet.CollapseOneLine(Printable(item), maxLen)
		if clean != "" {
			out = append(out, clean)
		}
	}
	return out
}

func printableMultiline(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\t", "  ")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = Printable(strings.TrimRight(line, "\r"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func ApplyExtraBanner(b *PreInvocationBanner, raw string) {
	clean := strings.TrimSpace(Printable(raw))
	if clean == "" {
		return
	}
	var gen int
	switch {
	case strings.HasPrefix(clean, "compacted (gen "):
		if _, err := fmt.Sscanf(clean, "compacted (gen %d):", &gen); err == nil && gen > 0 {
			b.CompactedGeneration = gen
			return
		}
	case strings.Contains(clean, ">15 dirty files: serving cached AST snapshot"):
		b.DirtyFastPath = true
		return
	case strings.HasSuffix(clean, " pivots"):
		trimmed := strings.TrimSpace(strings.TrimSuffix(clean, " pivots"))
		if n, err := strconv.Atoi(trimmed); err == nil && n > 0 {
			b.PivotCount = n
			return
		}
	case strings.Contains(clean, "tokens (>= ") && strings.Contains(clean, "knee)"):
		b.TokenKneeWarning = clean
		return
	case strings.HasPrefix(clean, "model tip: "):
		b.ModelTip = strings.TrimSpace(strings.TrimPrefix(clean, "model tip: "))
		return
	case strings.HasPrefix(clean, "store unavailable ("):
		b.StoreError = clean
		return
	}
	b.Notices = append(b.Notices, clean)
}

func normalizeUpperSnakeEnum(s string) string {
	clean := strings.TrimSpace(Printable(s))
	if clean == "" {
		return ""
	}
	var b strings.Builder
	prevUnderscore := true
	for _, r := range clean {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
			prevUnderscore = false
		case !prevUnderscore:
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	return strings.TrimRight(b.String(), "_")
}

// Printable replaces control, format, line/paragraph separator, and default-ignorable runes with '?'.
func Printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return '?'
		}
		if unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || unicode.Is(unicode.Cf, r) {
			return '?'
		}
		if unicode.Is(unicode.Variation_Selector, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) {
			return '?'
		}
		return r
	}, s)
}

// OneLineErr renders err as a single bounded whitespace-normalized line.
func OneLineErr(err error) string {
	if err == nil {
		return "unknown error"
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
