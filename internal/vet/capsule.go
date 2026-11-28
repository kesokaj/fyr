package vet

import "strings"

func isProjectThisCloneLine(line string) bool {
	const prefix = "project: this clone"
	if !strings.HasPrefix(line, prefix) {
		return false
	}
	if len(line) == len(prefix) {
		return true
	}
	switch line[len(prefix)] {
	case ' ', '\t', ',', '.', ';', ':':
		return true
	default:
		return false
	}
}

// IsKickoffHeaderMetaLine reports whether line starts with a Kickoff metadata header key.
func IsKickoffHeaderMetaLine(line string) bool {
	for _, p := range []string{"project:", "session:", "plan:", "model:", "class:", "level:", "never:", "done when:"} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// HasWorkerTopHeader reports whether lines begin with a worker brief or Kickoff header.
func HasWorkerTopHeader(lines []string) bool {
	limit := min(len(lines), 8)
	for i := 0; i < limit; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			return false
		}
		switch {
		case strings.HasPrefix(line, "files:"):
			return true
		case isProjectThisCloneLine(line):
			return true
		case strings.HasPrefix(line, "standing approvals:"),
			strings.HasPrefix(line, "standing approvals (closed set):"):
			return true
		case i == 0 && line == "this is a kickoff written by fyr auto.":
			return true
		case IsKickoffHeaderMetaLine(line):
			continue
		default:
			return false
		}
	}
	return false
}

// HasWorkerCapsuleSection reports whether lines contain an unfenced valid worker capsule block.
func HasWorkerCapsuleSection(lines []string) bool {
	mdClose := ""
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if nextClose, toggled := stepMarkdownFence(trimmed, mdClose); toggled {
			mdClose = nextClose
			continue
		}
		if mdClose != "" {
			continue
		}
		if isWorkerCapsuleHeader(lines, i, trimmed) {
			return true
		}
	}
	return false
}

func isWorkerCapsuleHeader(lines []string, i int, trimmed string) bool {
	return trimmed == "[fyr_worker_capsule]" &&
		i+1 < len(lines) &&
		strings.HasPrefix(strings.TrimSpace(lines[i+1]), "trust: ") &&
		validWorkerCapsuleBlock(lines[i+2:])
}

func stepMarkdownFence(trimmed, curClose string) (string, bool) {
	if curClose != "" {
		if isMarkdownFenceClose(trimmed, curClose) {
			return "", true
		}
		return curClose, false
	}
	return parseMarkdownFenceOpen(trimmed)
}

func isMarkdownFenceClose(trimmed, curClose string) bool {
	return strings.HasPrefix(trimmed, curClose) && strings.TrimLeft(trimmed, string(curClose[0])) == ""
}

func parseMarkdownFenceOpen(trimmed string) (string, bool) {
	if len(trimmed) < 3 {
		return "", false
	}
	ch := trimmed[0]
	if ch != '`' && ch != '~' {
		return "", false
	}
	n := countLeadingByte(trimmed, ch)
	if n < 3 {
		return "", false
	}
	if ch == '`' && strings.ContainsRune(trimmed[n:], '`') {
		return "", false
	}
	return strings.Repeat(string(ch), n), true
}

func countLeadingByte(s string, ch byte) int {
	n := 0
	for n < len(s) && s[n] == ch {
		n++
	}
	return n
}

func validWorkerCapsuleBlock(tail []string) bool {
	closedTag := false
	dataFenceTag := ""
	for _, line := range tail {
		if !stepWorkerCapsuleLine(strings.TrimSpace(line), &closedTag, &dataFenceTag) {
			return false
		}
	}
	return dataFenceTag == ""
}

func stepWorkerCapsuleLine(trimmed string, closedTag *bool, dataFenceTag *string) bool {
	if *dataFenceTag != "" {
		if isCapsuleDataFenceEnd(trimmed, *dataFenceTag) {
			*dataFenceTag = ""
		}
		return true
	}
	if trimmed == "" {
		return true
	}
	if tag, ok := parseCapsuleDataFenceOpen(trimmed); ok {
		*dataFenceTag = tag
		return true
	}
	if *closedTag {
		return false
	}
	if trimmed == "[/fyr_worker_capsule]" {
		*closedTag = true
		return true
	}
	return strings.HasPrefix(trimmed, "inferred_files: ")
}

func parseCapsuleDataFenceOpen(trimmed string) (string, bool) {
	tag, rest, ok := strings.Cut(trimmed, " begin data ")
	if !ok || len(tag) <= 10 || !strings.HasPrefix(tag, "=====") || !strings.HasSuffix(tag, "=====") {
		return "", false
	}
	if strings.ContainsAny(tag, " \t") || !strings.HasSuffix(rest, " =====") {
		return "", false
	}
	return tag, true
}

func isCapsuleDataFenceEnd(trimmed, tag string) bool {
	return strings.HasPrefix(trimmed, tag+" end data ") && strings.HasSuffix(trimmed, " =====")
}

// IsWorkerBriefPrompt reports whether prompt (original or lowercased) is an
// isolated worker brief or autonomous Kickoff so hooks never route workers into
// COORDINATE_SUBTASKS, fyr-swarm, or [COORDINATOR_GUARD] (D25, SPEC-fyr-pre-hook-coordinator).
func IsWorkerBriefPrompt(prompt string) bool {
	s := StripAnySandboxPreamble(prompt)
	if s == "" {
		return false
	}
	low := strings.ToLower(strings.ReplaceAll(s, "\r\n", "\n"))
	lines := strings.Split(low, "\n")
	return HasWorkerTopHeader(lines) || HasWorkerCapsuleSection(lines)
}

func extractCapsuleSymbolFiles(capsule string) map[string][]string {
	out := map[string][]string{}
	for _, line := range strings.Split(capsule, "\n") {
		if !strings.HasPrefix(line, "[Hop 0] ") && !strings.HasPrefix(line, "[Hop 1] ") {
			continue
		}
		if path, sym := parseCapsuleLinePathSym(line); path != "" && sym != "" {
			out[sym] = append(out[sym], path)
		}
	}
	return out
}

func parseCapsuleLinePathSym(line string) (string, string) {
	rest := line[strings.IndexByte(line, ']')+2:]
	spec, after, ok := strings.Cut(rest, " (")
	if !ok {
		return "", ""
	}
	colon := strings.LastIndexByte(spec, ':')
	sym, _, okSym := strings.Cut(after, ")")
	if colon <= 0 || !okSym {
		return "", ""
	}
	return NormalizeSlashPath(spec[:colon]), strings.TrimSpace(sym)
}

func hop0DirRanks(capsule string) map[string]int {
	ranks, idx := map[string]int{}, 0
	for _, line := range strings.Split(capsule, "\n") {
		if !strings.HasPrefix(line, "[Hop 0] ") {
			continue
		}
		if path, _ := parseCapsuleLinePathSym(line); path != "" {
			dir := PackageDirKey(path)
			if _, exists := ranks[dir]; !exists {
				ranks[dir] = idx
				idx++
			}
		}
	}
	return ranks
}
