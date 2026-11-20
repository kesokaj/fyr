package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

func safeBacktickFenceWithLang(text, lang string) string {
	body := strings.TrimRight(text, "\n")
	fence := safeBacktickFence(body)
	return fence + lang + "\n" + body + "\n" + fence
}

func marshalPrettyJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "{}"
	}
	return strings.TrimRight(buf.String(), "\n")
}

func turnBannerMetaFromExtras(extraSections, extraBanners []string) TurnBannerMeta {
	meta := TurnBannerMeta{ExtraSections: extraSections}
	var b PreInvocationBanner
	for _, extra := range extraBanners {
		applyExtraBanner(&b, extra)
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

func cleanSubtaskSlices(items []SubtaskSlice) []SubtaskSlice {
	if len(items) == 0 {
		return nil
	}
	out := make([]SubtaskSlice, 0, len(items))
	for _, it := range items {
		out = append(out, SubtaskSlice{
			ID:     collapseOneLine(printable(it.ID), 40),
			Role:   collapseOneLine(printable(it.Role), 40),
			Goal:   collapseOneLine(printable(it.Goal), 200),
			Files:  cleanPrintableSlice(it.Files, 200),
			Skills: cleanPrintableSlice(it.Skills, 80),
			Verify: collapseOneLine(printable(it.Verify), 160),
		})
	}
	return out
}

func truncateBytesUTF8(s string, maxB int) string {
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

func compactRecordNote(r MatchedRecord, title string) string {
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
	note := collapseOneLine(printable(strings.Join(kept, " ")), 220)
	if note == "" || strings.EqualFold(note, title) {
		return ""
	}
	return note
}

func cleanPrintableSlice(items []string, maxLen int) []string {
	var out []string
	for _, item := range items {
		clean := collapseOneLine(printable(item), maxLen)
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
		lines[i] = printable(strings.TrimRight(line, "\r"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func applyExtraBanner(b *PreInvocationBanner, raw string) {
	clean := strings.TrimSpace(printable(raw))
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
	clean := strings.TrimSpace(printable(s))
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
