package check

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	steProseWordLimit = 25
	steListWordLimit  = 20
	stePassTargetPct  = 80
	maxGlossaryBytes  = 64 << 10
)

var (
	listPrefixRe = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+(.+)$`)
	htmlTagRe    = regexp.MustCompile(`<[^>]+>`)
	mdImageRe    = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdLinkTextRe = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	bareURLRe    = regexp.MustCompile(`https?://\S+`)
	ruleLineRe   = regexp.MustCompile(`^\s*(?:[-*_]\s*){3,}$`)
)

type glossaryEntry struct{ forbidden, preferred string }

func steSentenceNote(where string, rawLines []string, fenced []bool) string {
	pass, total := scoreSTEFile(rawLines, fenced)
	if total == 0 || pass*100 >= stePassTargetPct*total {
		return ""
	}
	return fmt.Sprintf("docs-style-note: %s: STE sentence-length pass rate %d%% (%d/%d within limit, target >= %d%%)", where, (pass*100)/total, pass, total, stePassTargetPct)
}

func scoreSTEFile(rawLines []string, fenced []bool) (pass, total int) {
	inComment, inTable := false, false
	var para []string
	flush := func() {
		if len(para) > 0 {
			p, n := scoreSTESentences(strings.Join(para, " "), steProseWordLimit)
			pass, total, para = pass+p, total+n, para[:0]
		}
	}
	for i, l := range rawLines {
		if fenced[i] {
			flush()
			inTable = false
			continue
		}
		if skip, next := skipHTMLCommentLine(l, inComment); skip {
			inComment = next
			flush()
			continue
		}
		if isTableLine(rawLines, fenced, i, inTable) {
			inTable = true
			flush()
			continue
		}
		inTable = false
		if shouldSkipSTELine(l) {
			flush()
			continue
		}
		if m := listPrefixRe.FindStringSubmatch(l); m != nil {
			flush()
			p, n := scoreSTESentences(strings.TrimSpace(m[1]), steListWordLimit)
			pass, total = pass+p, total+n
			continue
		}
		para = append(para, strings.TrimSpace(l))
	}
	flush()
	return pass, total
}

func skipHTMLCommentLine(l string, inComment bool) (bool, bool) {
	if inComment {
		return true, !strings.Contains(l, "-->")
	}
	if strings.HasPrefix(strings.TrimSpace(l), "<!--") {
		return true, !strings.Contains(l, "-->")
	}
	return false, false
}

func isTableLine(lines []string, fenced []bool, i int, inTable bool) bool {
	if inTable {
		return isTableDataLine(lines[i])
	}
	return isTableSeparator(lines[i]) || (i+1 < len(lines) && !fenced[i+1] && tableColCount(lines[i]) > 0 && isTableSeparator(lines[i+1]))
}

func shouldSkipSTELine(l string) bool {
	t := strings.TrimSpace(l)
	if t == "" || strings.HasPrefix(t, ">") || atxHeadingRe.MatchString(l) || ruleLineRe.MatchString(t) {
		return true
	}
	if (strings.HasPrefix(t, "<") && strings.HasSuffix(t, ">")) || strings.TrimSpace(htmlTagRe.ReplaceAllString(t, "")) == "" {
		return true
	}
	s := bareURLRe.ReplaceAllString(mdLinkTextRe.ReplaceAllString(mdImageRe.ReplaceAllString(t, " "), " "), " ")
	return !hasAlnum(s)
}

func scoreSTESentences(block string, limit int) (pass, total int) {
	s := htmlTagRe.ReplaceAllString(mdLinkTextRe.ReplaceAllString(mdImageRe.ReplaceAllString(block, " "), "$1"), " ")
	for _, sent := range splitSTESentences(s) {
		w := 0
		for _, tok := range strings.Fields(sent) {
			if hasAlnum(tok) {
				w++
			}
		}
		if w == 0 {
			continue
		}
		total++
		if w <= limit {
			pass++
		}
	}
	return pass, total
}

func splitSTESentences(s string) []string {
	var out []string
	start, inCode := 0, false
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			inCode = !inCode
			continue
		}
		if inCode || (s[i] != '.' && s[i] != '!' && s[i] != '?') {
			continue
		}
		if i+1 < len(s) && s[i+1] != ' ' && s[i+1] != '\t' {
			continue
		}
		out = append(out, s[start:i+1])
		start = i + 1
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func hasAlnum(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func (k *checker) loadGlossary() ([]glossaryEntry, string) {
	data, present, skipReason := k.readGlossaryBytes()
	if !present {
		return nil, ""
	}
	if skipReason != "" {
		return nil, "docs-style-note: .agents/glossary.json skipped: " + skipReason
	}
	trimmed := bytes.TrimSpace(data)
	var raw map[string]string
	if !bytes.HasPrefix(trimmed, []byte("{")) || json.Unmarshal(trimmed, &raw) != nil {
		return nil, "docs-style-note: .agents/glossary.json skipped: malformed JSON"
	}
	entries := make([]glossaryEntry, 0, len(raw))
	for forbidden, preferred := range raw {
		if strings.TrimSpace(forbidden) != "" {
			entries = append(entries, glossaryEntry{forbidden, preferred})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].forbidden < entries[j].forbidden })
	return entries, ""
}

func (k *checker) readGlossaryBytes() ([]byte, bool, string) {
	dirInfo, err := k.root.Lstat(".agents")
	if err != nil {
		return nil, !errors.Is(err, fs.ErrNotExist), err.Error()
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 {
		return nil, true, ".agents symlink refused"
	}
	if !dirInfo.IsDir() {
		return nil, false, ""
	}
	rel := filepath.Join(".agents", "glossary.json")
	fi, err := k.root.Lstat(rel)
	if err != nil {
		return nil, !errors.Is(err, fs.ErrNotExist), err.Error()
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, true, "symlink refused"
	}
	if !fi.Mode().IsRegular() {
		return nil, true, "not a regular file"
	}
	if fi.Size() > maxGlossaryBytes {
		return nil, true, fmt.Sprintf("%d bytes exceeds %d-byte limit", fi.Size(), maxGlossaryBytes)
	}
	return k.openAndReadGlossary(rel)
}

func (k *checker) openAndReadGlossary(rel string) ([]byte, bool, string) {
	f, err := k.root.OpenFile(rel, os.O_RDONLY|nonBlock, 0)
	if err != nil {
		return nil, true, err.Error()
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, true, err.Error()
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxGlossaryBytes {
		return nil, true, fmt.Sprintf("not a regular file <= %d bytes", maxGlossaryBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxGlossaryBytes+1))
	if err != nil {
		return nil, true, err.Error()
	}
	if len(data) > maxGlossaryBytes {
		return nil, true, fmt.Sprintf("exceeds %d-byte limit", maxGlossaryBytes)
	}
	return data, true, ""
}

func glossaryNotes(where, text string, entries []glossaryEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	var out []string
	unfenced(text, func(n int, l string) bool {
		masked := maskProseLine(l)
		for _, e := range entries {
			if hasWordTerm(masked, e.forbidden) {
				out = append(out, fmt.Sprintf("docs-style-note: %s:%d: glossary prefers %q over %q", where, n, e.preferred, e.forbidden))
			}
		}
		return true
	})
	return out
}
