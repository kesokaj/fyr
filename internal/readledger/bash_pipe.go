package readledger

import (
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	awkSystemCallRe   = regexp.MustCompile(`(?i)\bsystem\s*\(`)
	pyOpenCallRe      = regexp.MustCompile(`\bopen\s*\(\s*(?:'([^'\n\r]+)'|"([^"\n\r]+)")\s*(?:,\s*([^)]*))?\)`)
	pyPathReadRe      = regexp.MustCompile(`\bPath\s*\(\s*(?:'([^'\n\r]+)'|"([^"\n\r]+)")\s*\)\s*\.\s*(?:read_text|read_bytes|open)\s*\(([^)]*)\)`)
	pyModeLitRe       = regexp.MustCompile(`(?:^\s*|\bmode\s*=\s*)(?:'([^']+)'|"([^"]+)")`)
	pyLineSliceRe     = regexp.MustCompile(`(?:readlines|splitlines)\s*\(\s*\)\s*\[\s*(\d*)\s*:\s*(\d*)\s*\]`)
	pyOutputSinkRe    = regexp.MustCompile(`\b(?:print|sys\.stdout\.write)\s*\(`)
	pyVerbatimReadRe  = regexp.MustCompile(`\.(?:read_text\s*\([^)]*\)|(?:read|read_bytes|readlines|splitlines)\s*\(\s*\))`)
	pyReadCharSliceRe = regexp.MustCompile(`\.(?:read|read_text|read_bytes)\s*\([^)]*\)\s*\[`)
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
	for len(toks) > 0 && IsEnvAssignToken(toks[0]) {
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
		a := args[i]
		if a == "--" {
			return rawCount, i == len(args)-1 || (i+1 == len(args)-1 && args[i+1] == "-")
		}
		if a == "-" {
			continue
		}
		if !strings.HasPrefix(a, "-") || len(a) <= 1 {
			return "", false
		}
		var ok bool
		if i, rawCount, ok = stepHeadOrTailCountFlag(args, i, rawCount, isTail); !ok {
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
	if !ok || isUnsafePythonReadCode(code) || !isVerbatimPythonDump(code) {
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
	scrubbed := strings.ReplaceAll(code, "sys.stdout.write(", "")
	blocked := []string{
		"subprocess", "os.system", "os.popen", "os.remove", "os.unlink",
		"os.rename", "os.replace", "shutil", "write_text", "write_bytes",
		".write(", ".writelines(", ".truncate(", "exec(", "eval(",
		"chmod(", "rmdir(", "makedirs(", "mkdir(",
	}
	for _, b := range blocked {
		if strings.Contains(scrubbed, b) {
			return true
		}
	}
	return false
}

func isVerbatimPythonDump(code string) bool {
	if !pyOutputSinkRe.MatchString(code) || !pyVerbatimReadRe.MatchString(code) || pyReadCharSliceRe.MatchString(code) {
		return false
	}
	summaryTokens := []string{
		"len(", "sum(", "min(", "max(", "sorted(", "set(", "dict(", "any(", "all(",
		"json.", "yaml.", "toml", "csv.", "xml.", "ast.", "re.", "regex",
		"hashlib", "base64", "pickle", "sqlite3",
		".count(", ".find(", ".index(", ".startswith(", ".endswith(", ".replace(",
		".split(", ".strip(", ".lower(", ".upper(", ".keys(", ".values(", ".items(", ".get(",
		" if ", "\nif ", ";if ", " for ", "\nfor ", ";for ", "def ", "class ", "lambda ",
	}
	for _, tok := range summaryTokens {
		if strings.Contains(code, tok) {
			return false
		}
	}
	return true
}

func extractPythonReadPaths(code string) ([]string, bool) {
	seen := map[string]bool{}
	paths, ok := appendPythonMatches(pyOpenCallRe, code, seen, nil)
	if !ok {
		return nil, false
	}
	paths, ok = appendPythonMatches(pyPathReadRe, code, seen, paths)
	if !ok {
		return nil, false
	}
	return paths, len(paths) > 0
}

func appendPythonMatches(re *regexp.Regexp, code string, seen map[string]bool, paths []string) ([]string, bool) {
	for _, m := range re.FindAllStringSubmatch(code, -1) {
		p := m[1]
		if p == "" {
			p = m[2]
		}
		if !validatePythonReadMatch(p, m[3]) {
			return nil, false
		}
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths, true
}

func validatePythonReadMatch(p, modeArgs string) bool {
	if m := pyModeLitRe.FindStringSubmatch(modeArgs); len(m) == 3 {
		mode := m[1]
		if mode == "" {
			mode = m[2]
		}
		if strings.ContainsAny(mode, "wax+") {
			return false
		}
	}
	return p != "" && p != "-" && !strings.ContainsAny(p, "$*?[\x00")
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
	if !hasMatchAllGrepPattern(patterns, fixed) {
		return nil, false
	}
	normTool := "grep"
	if tool == "rg" {
		normTool = "rg"
	}
	return buildUniformBashReads(normTool, pos, 1, maxCount)
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
			nextIdx, pat, ok := consumeShortClusterArg(args, i, cl[j+1:])
			return nextIdx, fixed, maxCount, append(patterns, pat), ok
		case 'm':
			nextIdx, raw, ok := consumeShortClusterArg(args, i, cl[j+1:])
			if !ok {
				return i, fixed, maxCount, patterns, false
			}
			n, err := strconv.Atoi(raw)
			return nextIdx, fixed, n, patterns, err == nil && n > 0
		default:
			return i, fixed, maxCount, patterns, false
		}
	}
	return i, fixed, maxCount, patterns, true
}

func consumeShortClusterArg(args []string, i int, tail string) (int, string, bool) {
	if tail != "" {
		return i, tail, true
	}
	if i+1 >= len(args) {
		return i, "", false
	}
	return i + 1, args[i+1], true
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
