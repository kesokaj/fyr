package search

import (
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	awkSystemCallRe = regexp.MustCompile(`(?i)\bsystem\s*\(`)
	pyOpenCallRe    = regexp.MustCompile(`\bopen\s*\(\s*(?:'([^'\n\r]+)'|"([^"\n\r]+)")\s*(?:,\s*([^)]*))?\)`)
	pyPathReadRe    = regexp.MustCompile(`\bPath\s*\(\s*(?:'([^'\n\r]+)'|"([^"\n\r]+)")\s*\)\s*\.\s*(?:read_text|read_bytes|open)\s*\(([^)]*)\)`)
	pyModeLitRe     = regexp.MustCompile(`(?:^\s*|\bmode\s*=\s*)(?:'([^']+)'|"([^"]+)")`)
	pyLineSliceRe   = regexp.MustCompile(`(?:readlines|splitlines)\s*\(\s*\)\s*\[\s*(\d*)\s*:\s*(\d*)\s*\]`)
)

func parseBashReadPipelineMulti(stages [][]string) ([]BashFileRead, bool) {
	first := stripLeadingEnvAssigns(stages[0])
	reads, ok := parseBashReadTokensMulti(first)
	if !ok || len(reads) == 0 {
		return nil, false
	}
	lineMapped := len(reads) == 1 && isSourceLineMapped(first)
	for _, stage := range stages[1:] {
		toks := stripLeadingEnvAssigns(stage)
		if len(toks) == 0 {
			return nil, false
		}
		if len(reads) == 1 {
			var stageOK bool
			reads[0], lineMapped, stageOK = applyReadOnlyPipeStage(reads[0], lineMapped, toks)
			if !stageOK {
				return nil, false
			}
			continue
		}
		if _, _, stageOK := applyReadOnlyPipeStage(reads[0], false, toks); !stageOK {
			return nil, false
		}
	}
	return reads, true
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
	base := filepath.Base(toks[0])
	if isPythonToolName(base) {
		return false
	}
	if base == "cat" {
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
		return cur, false, isReadOnlyFilterArgs(tool, args)
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

func isReadOnlyUniqArgs(args []string) bool {
	pos := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos += len(args) - i - 1
			break
		}
		if a == "-" {
			continue
		}
		if !strings.HasPrefix(a, "-") {
			pos++
			continue
		}
		if slices.Contains([]string{"-f", "-s", "-w", "--skip-fields", "--skip-chars", "--check-chars"}, a) && i+1 < len(args) {
			i++
		}
	}
	return pos == 0
}

func isPythonToolName(tool string) bool {
	if tool == "python" || tool == "python3" {
		return true
	}
	if !strings.HasPrefix(tool, "python3.") || len(tool) <= len("python3.") {
		return false
	}
	_, err := strconv.Atoi(tool[len("python3."):])
	return err == nil
}

func parseBashPythonRead(args []string) ([]BashFileRead, bool) {
	code, ok := extractPythonCodeArg(args)
	if !ok || isUnsafePythonReadCode(code) {
		return nil, false
	}
	paths, ok := extractPythonReadPaths(code)
	if !ok || len(paths) == 0 {
		return nil, false
	}
	start, end := 1, 0
	if len(paths) == 1 {
		start, end = parsePythonLineSlice(code)
	}
	out := make([]BashFileRead, 0, len(paths))
	for _, p := range paths {
		out = append(out, BashFileRead{Tool: "python", Path: p, StartLine: start, EndLine: end})
	}
	return out, true
}

func extractPythonCodeArg(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-c":
			if i+1 < len(args) && strings.TrimSpace(args[i+1]) != "" {
				return args[i+1], true
			}
			return "", false
		case strings.HasPrefix(a, "-c") && len(a) > 2:
			return a[2:], true
		case a == "-W" || a == "-X":
			i++
		case slices.Contains([]string{"-u", "-B", "-S", "-E", "-I", "-s", "-O", "-OO", "-q"}, a):
		default:
			return "", false
		}
	}
	return "", false
}

func isUnsafePythonReadCode(code string) bool {
	blocked := []string{
		"subprocess", "os.system", "os.popen", "os.remove", "os.unlink",
		"os.rename", "os.replace", "shutil", "write_text", "write_bytes",
		".write(", ".writelines(", ".truncate(", "exec(", "eval(",
		"chmod(", "rmdir(", "makedirs(", "mkdir(",
	}
	for _, b := range blocked {
		if strings.Contains(code, b) {
			return true
		}
	}
	return false
}

func extractPythonReadPaths(code string) ([]string, bool) {
	var paths []string
	seen := map[string]bool{}
	addMatch := func(p, modeArgs string) bool {
		if m := pyModeLitRe.FindStringSubmatch(modeArgs); len(m) == 3 {
			mode := m[1]
			if mode == "" {
				mode = m[2]
			}
			if strings.ContainsAny(mode, "wax+") {
				return false
			}
		}
		if p == "" || p == "-" || strings.ContainsAny(p, "$*?[\x00") {
			return false
		}
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
		return true
	}
	for _, m := range pyOpenCallRe.FindAllStringSubmatch(code, -1) {
		p := m[1]
		if p == "" {
			p = m[2]
		}
		if !addMatch(p, m[3]) {
			return nil, false
		}
	}
	for _, m := range pyPathReadRe.FindAllStringSubmatch(code, -1) {
		p := m[1]
		if p == "" {
			p = m[2]
		}
		if !addMatch(p, m[3]) {
			return nil, false
		}
	}
	return paths, len(paths) > 0
}

func parsePythonLineSlice(code string) (int, int) {
	m := pyLineSliceRe.FindStringSubmatch(code)
	if len(m) != 3 || (m[1] == "" && m[2] == "") {
		return 1, 0
	}
	start, end := 1, 0
	if m[1] != "" {
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 0 {
			return 1, 0
		}
		start = n + 1
	}
	if m[2] != "" {
		n, err := strconv.Atoi(m[2])
		if err != nil || n < start {
			return 1, 0
		}
		end = n
	}
	return start, end
}

func parseBashMatchAllGrepRead(tool string, args []string) ([]BashFileRead, bool) {
	fixed, maxCount := tool == "fgrep", 0
	var patterns, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			var ok bool
			if i, fixed, maxCount, patterns, ok = stepGrepReadFlag(args, i, fixed, maxCount, patterns); !ok {
				return nil, false
			}
			continue
		}
		pos = append(pos, a)
	}
	if len(patterns) == 0 && len(pos) > 0 {
		patterns, pos = []string{pos[0]}, pos[1:]
	}
	if len(pos) == 0 || !hasMatchAllGrepPattern(patterns, fixed) {
		return nil, false
	}
	normTool := "grep"
	if tool == "rg" {
		normTool = "rg"
	}
	out := make([]BashFileRead, 0, len(pos))
	for _, f := range pos {
		if f == "" || f == "-" {
			return nil, false
		}
		out = append(out, BashFileRead{Tool: normTool, Path: f, StartLine: 1, EndLine: maxCount})
	}
	return out, true
}

func stepGrepReadFlag(args []string, i int, fixed bool, maxCount int, patterns []string) (int, bool, int, []string, bool) {
	a := args[i]
	switch {
	case a == "-e" || a == "--regexp":
		if i+1 >= len(args) {
			return i, fixed, maxCount, patterns, false
		}
		return i + 1, fixed, maxCount, append(patterns, args[i+1]), true
	case strings.HasPrefix(a, "--regexp="):
		return i, fixed, maxCount, append(patterns, strings.TrimPrefix(a, "--regexp=")), true
	case a == "-m" || a == "--max-count":
		if i+1 >= len(args) {
			return i, fixed, maxCount, patterns, false
		}
		n, err := strconv.Atoi(args[i+1])
		return i + 1, fixed, n, patterns, err == nil && n > 0
	case strings.HasPrefix(a, "--max-count="):
		n, err := strconv.Atoi(strings.TrimPrefix(a, "--max-count="))
		return i, fixed, n, patterns, err == nil && n > 0
	case a == "--fixed-strings":
		return i, true, maxCount, patterns, true
	case strings.HasPrefix(a, "--"):
		ok := slices.Contains([]string{
			"--line-number", "--with-filename", "--no-filename", "--extended-regexp",
			"--basic-regexp", "--perl-regexp", "--ignore-case", "--no-messages",
			"--text", "--color", "--no-heading", "--no-line-number",
		}, a) || strings.HasPrefix(a, "--color=")
		return i, fixed, maxCount, patterns, ok
	default:
		return stepGrepShortCluster(args, i, fixed, maxCount, patterns)
	}
}

func stepGrepShortCluster(args []string, i int, fixed bool, maxCount int, patterns []string) (int, bool, int, []string, bool) {
	cl := args[i][1:]
	for j := 0; j < len(cl); j++ {
		switch cl[j] {
		case 'n', 'H', 'h', 'E', 'G', 'P', 'i', 's', 'a', 'I', 'N':
		case 'F':
			fixed = true
		case 'e':
			if j+1 < len(cl) {
				return i, fixed, maxCount, append(patterns, cl[j+1:]), true
			}
			if i+1 >= len(args) {
				return i, fixed, maxCount, patterns, false
			}
			return i + 1, fixed, maxCount, append(patterns, args[i+1]), true
		case 'm':
			raw := cl[j+1:]
			if raw == "" {
				if i+1 >= len(args) {
					return i, fixed, maxCount, patterns, false
				}
				i, raw = i+1, args[i+1]
			}
			n, err := strconv.Atoi(raw)
			return i, fixed, n, patterns, err == nil && n > 0
		default:
			return i, fixed, maxCount, patterns, false
		}
	}
	return i, fixed, maxCount, patterns, true
}

func hasMatchAllGrepPattern(patterns []string, fixed bool) bool {
	for _, p := range patterns {
		if p == "" {
			return true
		}
		if !fixed && (p == "^" || p == "$" || p == ".*" || p == "^.*$") {
			return true
		}
	}
	return false
}
