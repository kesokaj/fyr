package search

import "strings"

func stripAnySandboxPreamble(prompt string) string {
	s := strings.TrimSpace(StripSandboxPreamble(strings.TrimSpace(prompt)))
	low := strings.ToLower(s)
	if strings.HasPrefix(low, strings.ToLower(sandboxPreamblePrefix)) {
		if idx := strings.Index(low, strings.ToLower(sandboxPreambleEnd)); idx >= 0 {
			return strings.TrimSpace(s[idx+len(sandboxPreambleEnd):])
		}
	}
	return s
}

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

func isKickoffHeaderMetaLine(line string) bool {
	for _, p := range []string{"project:", "session:", "plan:", "model:", "class:", "level:", "never:", "done when:"} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

func hasWorkerTopHeader(lines []string) bool {
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
		case isKickoffHeaderMetaLine(line):
			continue
		default:
			return false
		}
	}
	return false
}

func hasWorkerCapsuleSection(lines []string) bool {
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
		if trimmed == "[fyr_worker_capsule]" && i+1 < len(lines) &&
			strings.HasPrefix(strings.TrimSpace(lines[i+1]), "trust: ") &&
			validWorkerCapsuleBlock(lines[i+2:]) {
			return true
		}
	}
	return false
}

func stepMarkdownFence(trimmed, curClose string) (string, bool) {
	if curClose != "" {
		if strings.HasPrefix(trimmed, curClose) && strings.TrimLeft(trimmed, string(curClose[0])) == "" {
			return "", true
		}
		return curClose, false
	}
	for _, ch := range []byte{'`', '~'} {
		if len(trimmed) >= 3 && trimmed[0] == ch && trimmed[1] == ch && trimmed[2] == ch {
			n := 0
			for n < len(trimmed) && trimmed[n] == ch {
				n++
			}
			if ch == '`' && strings.ContainsRune(trimmed[n:], '`') {
				return "", false
			}
			return strings.Repeat(string(ch), n), true
		}
	}
	return "", false
}

func validWorkerCapsuleBlock(tail []string) bool {
	closedTag := false
	dataFenceTag := ""
	for _, line := range tail {
		trimmed := strings.TrimSpace(line)
		if dataFenceTag != "" {
			if isCapsuleDataFenceEnd(trimmed, dataFenceTag) {
				dataFenceTag = ""
			}
			continue
		}
		if trimmed == "" {
			continue
		}
		if tag, ok := parseCapsuleDataFenceOpen(trimmed); ok {
			dataFenceTag = tag
			continue
		}
		if !closedTag {
			if trimmed == "[/fyr_worker_capsule]" {
				closedTag = true
				continue
			}
			if strings.HasPrefix(trimmed, "inferred_files: ") {
				continue
			}
		}
		return false
	}
	return dataFenceTag == ""
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

// isWorkerBriefPrompt reports whether prompt (original or lowercased) is an
// isolated worker brief or autonomous Kickoff so hooks never route workers into
// COORDINATE_SUBTASKS, fyr-swarm, or [COORDINATOR_GUARD] (D25, SPEC-fyr-pre-hook-coordinator).
func isWorkerBriefPrompt(prompt string) bool {
	s := stripAnySandboxPreamble(prompt)
	if s == "" {
		return false
	}
	low := strings.ToLower(strings.ReplaceAll(s, "\r\n", "\n"))
	lines := strings.Split(low, "\n")
	return hasWorkerTopHeader(lines) || hasWorkerCapsuleSection(lines)
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
	return normalizeSlashPath(spec[:colon]), strings.TrimSpace(sym)
}

func hop0DirRanks(capsule string) map[string]int {
	ranks, idx := map[string]int{}, 0
	for _, line := range strings.Split(capsule, "\n") {
		if !strings.HasPrefix(line, "[Hop 0] ") {
			continue
		}
		if path, _ := parseCapsuleLinePathSym(line); path != "" {
			dir := packageDirKey(path)
			if _, exists := ranks[dir]; !exists {
				ranks[dir] = idx
				idx++
			}
		}
	}
	return ranks
}
