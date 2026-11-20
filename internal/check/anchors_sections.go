package check

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	spanSecRe          = regexp.MustCompile(`^([^\s]+\.md)\s*§\s*(.*)$`)
	proseSkillBeforeRe = regexp.MustCompile(`([a-z0-9-]+)\s+skill(?:'s)?\s+$`)
	proseContWords     = map[string]bool{
		"is": true, "are": true, "was": true, "were": true,
		"has": true, "have": true, "lists": true, "names": true,
		"holds": true, "describes": true, "defines": true, "states": true,
		"verbatim": true, "rules": true, "rule": true, "item": true,
		"items": true, "says": true, "gives": true, "shows": true,
		"for": true, "of": true, "in": true, "on": true,
		"to": true, "and": true, "or": true,
	}
	skillAgentDocs = []string{"AGENTS.md", "context/AGENTS.md", "skills/fyr-init-project/assets/AGENTS.md"}
)

const proseStopChars = ",;:.)]|`—–?!"

func (k *checker) sectionRefFindings(where string, line int, rawLine string, sp []inlineSpan) []Finding {
	var out []Finding
	for _, s := range sp {
		if m := spanSecRe.FindStringSubmatch(s.text); m != nil {
			target, sec := m[1], strings.TrimSpace(m[2])
			if !skipSectionRef(target, sec) {
				if f, ok := k.checkSectionRef(where, rawLine[:s.start], line, target, sec, false); ok {
					out = append(out, f)
				}
			}
			continue
		}
		if !isMDToken(s.text) {
			continue
		}
		after := strings.TrimLeft(rawLine[s.end:], " \t")
		if rest, ok := strings.CutPrefix(after, "§"); ok {
			sec := captureProseSection(rest)
			if !skipSectionRef(s.text, sec) {
				if f, ok := k.checkSectionRef(where, rawLine[:s.start], line, s.text, sec, true); ok {
					out = append(out, f)
				}
			}
		}
	}
	return out
}

func (k *checker) checkSectionRef(where, beforeLine string, line int, target, sec string, prose bool) (Finding, bool) {
	targets := k.resolveSectionTargets(where, beforeLine, target)
	if len(targets) == 0 {
		return Finding{where, line, "anchor", fmt.Sprintf("`%s` § %s: target %s does not exist", target, sec, target)}, true
	}
	for _, p := range targets {
		if hs, ok := k.fileHeadings(p); ok && matchesSection(sec, hs, prose) {
			return Finding{}, false
		}
	}
	return Finding{where, line, "anchor", fmt.Sprintf("`%s` § %s: section %q not found in %s", target, sec, sec, targets[0])}, true
}

func (k *checker) resolveSectionTargets(where, beforeLine, target string) []string {
	if (strings.HasPrefix(where, "skills/") || strings.HasPrefix(where, "agents/")) && target == "AGENTS.md" {
		var out []string
		for _, p := range skillAgentDocs {
			if _, ok := k.fileHeadings(p); ok {
				out = append(out, p)
			}
		}
		return out
	}
	if p, _, ok := k.resolveMDTarget(where, target); ok {
		return []string{p}
	}
	if strings.HasPrefix(where, "skills/") {
		return k.resolveSkillSectionTargets(where, beforeLine, target)
	}
	return nil
}

func (k *checker) resolveSkillSectionTargets(where, beforeLine, target string) []string {
	cands := []string{path.Clean(path.Join("skills", target))}
	rest := strings.TrimPrefix(where, "skills/")
	if skill, _, ok := strings.Cut(rest, "/"); ok && skill != "" {
		cands = append(cands, path.Clean(path.Join("skills", skill, target)))
	}
	if !strings.Contains(target, "/") {
		if m := proseSkillBeforeRe.FindStringSubmatch(strings.ToLower(beforeLine)); m != nil {
			cands = append(cands,
				path.Clean(path.Join("skills", m[1], target)),
				path.Clean(path.Join("skills", m[1], "references", target)),
			)
		}
	}
	for _, p := range cands {
		if _, ok := k.fileHeadings(p); ok {
			return []string{p}
		}
	}
	return nil
}

func matchesSection(sec string, headings []string, prose bool) bool {
	s := strings.ToLower(strings.TrimSpace(sec))
	sWords := strings.Fields(s)
	for _, rawH := range headings {
		h := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(rawH, "`", "")))
		if h == s || ((len(s) >= 2 || isAllDigits(s)) && hasWordPrefix(h, s)) || hasWordPrefix(s, h) {
			return true
		}
		if prose && len(sWords) >= 2 && proseContWords[trimEdgePunct(sWords[1])] {
			hWords := strings.Fields(h)
			if len(hWords) > 0 && trimEdgePunct(hWords[0]) == trimEdgePunct(sWords[0]) {
				return true
			}
		}
	}
	return false
}

func hasWordPrefix(str, pre string) bool {
	if !strings.HasPrefix(str, pre) {
		return false
	}
	if len(str) == len(pre) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(str[len(pre):])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
}

func captureProseSection(rest string) string {
	rest = strings.TrimLeft(rest, " \t")
	for i, r := range rest {
		if strings.ContainsRune(proseStopChars, r) {
			return strings.TrimSpace(rest[:i])
		}
	}
	return strings.TrimSpace(rest)
}

func isMDToken(s string) bool {
	return s != "" && strings.HasSuffix(strings.ToLower(s), ".md") && !strings.ContainsAny(s, " \t<>*{}$…")
}

func skipSectionRef(target, sec string) bool {
	return strings.ContainsAny(target, "<>*{}$…") || sec == "" || strings.ContainsAny(sec, "<>") || !hasWordChar(sec)
}

func hasWordChar(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func trimEdgePunct(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
