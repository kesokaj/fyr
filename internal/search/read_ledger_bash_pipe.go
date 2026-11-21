package search

import (
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var awkSystemCallRe = regexp.MustCompile(`(?i)\bsystem\s*\(`)

func parseBashReadPipeline(stages [][]string) (BashFileRead, bool) {
	first := stripLeadingEnvAssigns(stages[0])
	r, ok := parseBashReadTokens(first)
	if !ok {
		return BashFileRead{}, false
	}
	lineMapped := isSourceLineMapped(first)
	for _, stage := range stages[1:] {
		toks := stripLeadingEnvAssigns(stage)
		if len(toks) == 0 {
			return BashFileRead{}, false
		}
		var stageOK bool
		r, lineMapped, stageOK = applyReadOnlyPipeStage(r, lineMapped, toks)
		if !stageOK {
			return BashFileRead{}, false
		}
	}
	return r, true
}

func stripLeadingEnvAssigns(toks []string) []string {
	for len(toks) > 0 && isEnvAssignToken(toks[0]) {
		toks = toks[1:]
	}
	return toks
}

func isSourceLineMapped(toks []string) bool {
	if len(toks) == 0 {
		return false
	}
	if filepath.Base(toks[0]) == "cat" {
		for _, a := range toks[1:] {
			if a == "--squeeze-blank" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], 's')) {
				return false
			}
		}
	}
	return true
}

func applyReadOnlyPipeStage(cur BashFileRead, lineMapped bool, toks []string) (BashFileRead, bool, bool) {
	tool := filepath.Base(toks[0])
	args := toks[1:]
	switch tool {
	case "nl":
		return cur, lineMapped, isStdinOnlyNL(args)
	case "cat":
		return applyStdinCatStage(cur, lineMapped, args)
	case "sed":
		return applyStdinSedStage(cur, lineMapped, args)
	case "head":
		return applyStdinHeadStage(cur, lineMapped, args)
	case "tail":
		return applyStdinTailStage(cur, lineMapped, args)
	case "grep", "rg", "cut", "wc", "tr", "jq", "column", "expand", "fold", "rev":
		return cur, false, true
	case "awk":
		return cur, false, isReadOnlyAwkArgs(args)
	case "sort":
		return cur, false, isReadOnlySortArgs(args)
	case "uniq":
		return cur, false, isReadOnlyUniqArgs(args)
	default:
		return BashFileRead{}, false, false
	}
}

func isStdinOnlyNL(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return i == len(args)-1 || (i+1 == len(args)-1 && args[i+1] == "-")
		}
		if a == "-" {
			continue
		}
		if !strings.HasPrefix(a, "-") {
			return false
		}
		if strings.Contains("|-b|-n|-s|-w|-v|-i|-l|", "|"+a+"|") && i+1 < len(args) {
			i++
		}
	}
	return true
}

func applyStdinCatStage(cur BashFileRead, lineMapped bool, args []string) (BashFileRead, bool, bool) {
	for _, a := range args {
		if a == "-" {
			continue
		}
		if !strings.HasPrefix(a, "-") {
			return BashFileRead{}, false, false
		}
		if a == "--squeeze-blank" || (!strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], 's')) {
			lineMapped = false
		}
	}
	return cur, lineMapped, true
}

func applyStdinHeadStage(cur BashFileRead, lineMapped bool, args []string) (BashFileRead, bool, bool) {
	rawCount, ok := parseStdinLineCount(args, false)
	if !ok {
		return BashFileRead{}, false, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(rawCount, "+"))
	if strings.HasPrefix(rawCount, "-") || err != nil || n <= 0 {
		return BashFileRead{}, false, false
	}
	if !lineMapped || cur.TailLines > 0 {
		return cur, false, true
	}
	return refineStreamRange(cur, 1, n), true, true
}

func applyStdinTailStage(cur BashFileRead, lineMapped bool, args []string) (BashFileRead, bool, bool) {
	rawCount, ok := parseStdinLineCount(args, true)
	if !ok {
		return BashFileRead{}, false, false
	}
	if strings.HasPrefix(rawCount, "+") {
		n, err := strconv.Atoi(rawCount[1:])
		if err != nil || n <= 0 {
			return BashFileRead{}, false, false
		}
		if !lineMapped || cur.TailLines > 0 {
			return cur, false, true
		}
		return refineStreamRange(cur, n, 0), true, true
	}
	n, err := strconv.Atoi(strings.TrimPrefix(rawCount, "-"))
	if err != nil || n <= 0 {
		return BashFileRead{}, false, false
	}
	if !lineMapped {
		return cur, false, true
	}
	return refineStreamTail(cur, n), true, true
}

func parseStdinLineCount(args []string, isTail bool) (string, bool) {
	rawCount := "10"
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			return rawCount, i == len(args)-1 || (i+1 == len(args)-1 && args[i+1] == "-")
		case a == "-":
		case a == "-n" || a == "--lines":
			if i+1 >= len(args) {
				return "", false
			}
			i, rawCount = i+1, args[i+1]
		case strings.HasPrefix(a, "-n") || strings.HasPrefix(a, "--lines="):
			rawCount = strings.TrimPrefix(strings.TrimPrefix(a, "-n"), "--lines=")
		case isTail && (a == "-f" || a == "-F" || strings.HasPrefix(a, "--follow")):
			return "", false
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if _, err := strconv.Atoi(a[1:]); err == nil {
				rawCount = a[1:]
			} else if !slices.Contains([]string{"-q", "-v", "--quiet", "--silent", "--verbose"}, a) {
				return "", false
			}
		default:
			return "", false
		}
	}
	return rawCount, true
}

func refineStreamRange(cur BashFileRead, s, e int) BashFileRead {
	base := max(1, cur.StartLine)
	nextStart := base + s - 1
	nextEnd := cur.EndLine
	if e > 0 {
		candEnd := base + e - 1
		if nextEnd == 0 || candEnd < nextEnd {
			nextEnd = candEnd
		}
	}
	if nextEnd > 0 && nextStart > nextEnd {
		nextStart = nextEnd
	}
	cur.StartLine, cur.EndLine, cur.TailLines = nextStart, nextEnd, 0
	return cur
}

func refineStreamTail(cur BashFileRead, n int) BashFileRead {
	if cur.TailLines > 0 {
		keep := min(cur.TailLines, n)
		cur.StartLine, cur.EndLine, cur.TailLines = -keep, 0, keep
		return cur
	}
	if cur.EndLine > 0 {
		base := max(1, cur.StartLine)
		cur.StartLine = max(base, cur.EndLine-n+1)
		cur.TailLines = 0
		return cur
	}
	cur.StartLine, cur.EndLine, cur.TailLines = -n, 0, n
	return cur
}

func isReadOnlyAwkArgs(args []string) bool {
	for _, a := range args {
		low := strings.ToLower(a)
		if a == "-f" || a == "-i" || a == "-l" || strings.HasPrefix(a, "--file") || strings.HasPrefix(a, "--include") || strings.HasPrefix(a, "--load") {
			return false
		}
		if strings.ContainsAny(a, ">|") || awkSystemCallRe.MatchString(a) || strings.Contains(low, "getline") {
			return false
		}
	}
	return true
}

func isReadOnlySortArgs(args []string) bool {
	for _, a := range args {
		if a == "-o" || a == "--output" || strings.HasPrefix(a, "-o") || strings.HasPrefix(a, "--output=") {
			return false
		}
	}
	return true
}

func isReadOnlyUniqArgs(args []string) bool {
	pos := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos += len(args) - i - 1
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			pos++
			continue
		}
		if slices.Contains([]string{"-f", "-s", "-w", "--skip-fields", "--skip-chars", "--check-chars"}, a) && i+1 < len(args) {
			i++
		}
	}
	return pos <= 1
}
