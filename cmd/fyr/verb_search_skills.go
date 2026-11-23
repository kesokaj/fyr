package main

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/search"
)

func makeSkillCatalog() func(home string) []search.SkillMeta {
	return LoadSkillCatalog
}

// LoadSkillCatalog reads installed SKILL.md metadata under
// <home>/.gemini/config/skills/<name>/SKILL.md. Returns nil when home is empty
// or no skills are installed (zero phantom fallback).
func LoadSkillCatalog(home string) []search.SkillMeta {
	if home == "" {
		return nil
	}
	skillsDir := filepath.Join(home, ".gemini", "config", "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}
	var out []search.SkillMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(skillsDir, e.Name(), "SKILL.md")
		if meta, ok := parseSkillMetaFile(e.Name(), p); ok {
			out = append(out, meta)
		}
	}
	return out
}

func parseSkillMetaFile(name, path string) (search.SkillMeta, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return search.SkillMeta{}, false
	}
	raw := string(data)
	desc := extractSkillDescription(raw)
	digest := extractActionableSkillDigest(raw)
	if digest == "" {
		digest = desc
	}
	if desc == "" && digest == "" {
		return search.SkillMeta{}, false
	}
	desc = truncateUTF8(desc, 600)
	digest = truncateUTF8(digest, 600)
	return search.SkillMeta{
		Name:        name,
		Description: desc,
		Path:        "~/.gemini/config/skills/" + name + "/SKILL.md",
		Digest:      digest,
	}, true
}

func isYAMLBlockHeader(s string) bool {
	if s == "" || (s[0] != '|' && s[0] != '>') {
		return false
	}
	for _, r := range s[1:] {
		if r != '+' && r != '-' && (r < '1' || r > '9') {
			return false
		}
	}
	return true
}

func unquoteYAMLValue(s string) string {
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

func extractSkillDescription(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	var descLines []string
	inDesc, isBlock := false, false
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.HasPrefix(line, "description:") {
			inDesc = true
			rest := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			isBlock = isYAMLBlockHeader(rest)
			if !isBlock && rest != "" {
				descLines = append(descLines, rest)
			}
			continue
		}
		if !inDesc {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			descLines = append(descLines, strings.TrimSpace(line))
			continue
		}
		break
	}
	joined := strings.Join(descLines, " ")
	if !isBlock {
		joined = unquoteYAMLValue(joined)
	}
	return joined
}

func extractActionableSkillDigest(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	start := 0
	if len(lines) >= 3 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				start = i + 1
				break
			}
		}
	}
	var parts []string
	inFence := false
	total := 0
	for i := start; i < len(lines) && total < 580; i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence || t == "" || strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "<!--") {
			continue
		}
		if len(t) > 180 {
			t = truncateUTF8(t, 180) + "..."
		}
		parts = append(parts, t)
		total += len(t) + 3
		if len(parts) >= 6 {
			break
		}
	}
	return truncateUTF8(strings.Join(parts, " | "), 600)
}

func truncateUTF8(s string, maxBytes int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxBytes {
		return s
	}
	i := maxBytes
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return strings.TrimSpace(s[:i])
}
