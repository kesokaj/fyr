package search

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	awkNRRangeCommaRe = regexp.MustCompile(`^\s*F?NR\s*==\s*(\d+)\s*,\s*F?NR\s*==\s*(\d+)\s*$`)
	awkNREqRe         = regexp.MustCompile(`^\s*F?NR\s*==\s*(\d+)\s*$`)
	awkNRCmpRe        = regexp.MustCompile(`^\s*(?:(F?NR)\s*(>=|>|<=|<)\s*(\d+)|(\d+)\s*(>=|>|<=|<)\s*(F?NR))\s*$`)
)

func isUnsafeAwkArg(a string) bool {
	low := strings.ToLower(a)
	return strings.ContainsAny(a, ">|") || awkSystemCallRe.MatchString(a) || strings.Contains(low, "getline")
}

func isReadOnlyAwkArgs(args []string) bool {
	hasProg := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if isUnsafeAwkArg(a) {
			return false
		}
		if a == "--" {
			return len(args)-i-1 == 1 && !hasProg && args[i+1] != "-" && !isUnsafeAwkArg(args[i+1])
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			nextIdx, ok := stepSafeAwkFlag(args, i)
			if !ok {
				return false
			}
			i = nextIdx
			continue
		}
		if hasProg || a == "-" {
			return false
		}
		hasProg = true
	}
	return hasProg
}

func stepSafeAwkFlag(args []string, i int) (int, bool) {
	a := args[i]
	if a == "-F" || a == "-v" || a == "--field-separator" || a == "--assign" {
		return i + 1, i+1 < len(args)
	}
	if strings.HasPrefix(a, "-F") || strings.HasPrefix(a, "-v") || strings.HasPrefix(a, "--field-separator=") || strings.HasPrefix(a, "--assign=") {
		return i, true
	}
	return i, false
}

func isReadOnlySortArgs(args []string) bool {
	valFlags := []string{"-k", "-t", "-T", "-S", "--key", "--field-separator", "--temporary-directory", "--buffer-size", "--parallel", "--sort", "--batch-size"}
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
		if isBlockedSortFlag(a) {
			return false
		}
		if slices.Contains(valFlags, a) {
			i++
		}
	}
	return true
}

func isBlockedSortFlag(a string) bool {
	if strings.HasPrefix(a, "--") {
		return a == "--output" || strings.HasPrefix(a, "--output=") ||
			a == "--compress-program" || strings.HasPrefix(a, "--compress-program=") ||
			a == "--files0-from" || strings.HasPrefix(a, "--files0-from=")
	}
	for j := 1; j < len(a); j++ {
		switch a[j] {
		case 'o':
			return true
		case 'k', 't', 'T', 'S':
			return false
		}
	}
	return false
}

func isReadOnlyFilterArgs(tool string, args []string) bool {
	switch tool {
	case "grep", "rg":
		return isStdinPatternFilter(tool, args)
	case "jq":
		return isStdinJqFilter(args)
	case "tr":
		return isStdinPositionalBound(args, 2, nil)
	case "cut":
		return isStdinPositionalBound(args, 0, []string{"-b", "-c", "-d", "-f", "--bytes", "--characters", "--delimiter", "--fields", "--output-delimiter"})
	case "wc":
		return isStdinNoFlagVal(args, []string{"--files0-from"})
	case "fold":
		return isStdinPositionalBound(args, 0, []string{"-w", "--width"})
	case "expand":
		return isStdinPositionalBound(args, 0, []string{"-t", "--tabs"})
	case "column":
		return isStdinPositionalBound(args, 0, []string{"-c", "-s", "-o", "-N", "-R", "-H", "-T", "-W", "--output-width", "--separator", "--output-separator"})
	case "rev":
		return isStdinPositionalBound(args, 0, nil)
	default:
		return false
	}
}

func isStdinPatternFilter(tool string, args []string) bool {
	hasExpr := false
	pos := 0
	valFlags := []string{"-e", "--regexp", "-m", "--max-count", "-A", "-B", "-C", "--after-context", "--before-context", "--context", "-g", "--glob", "-t", "--type"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos += len(args) - i - 1
			break
		}
		if a == "-" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			if isBlockedPatternFlag(tool, a) {
				return false
			}
			if a == "-e" || a == "--regexp" || strings.HasPrefix(a, "--regexp=") || (len(a) > 2 && !strings.HasPrefix(a, "--") && strings.HasPrefix(a, "-e")) {
				hasExpr = true
			}
			if slices.Contains(valFlags, a) {
				if i+1 >= len(args) {
					return false
				}
				i++
			}
			continue
		}
		pos++
	}
	if hasExpr {
		return pos == 0
	}
	return pos <= 1
}

func isBlockedPatternFlag(tool, a string) bool {
	if a == "-f" || a == "--file" || strings.HasPrefix(a, "--file=") || (len(a) > 2 && !strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], 'f')) {
		return true
	}
	if tool == "rg" && (a == "--pre" || strings.HasPrefix(a, "--pre=") || a == "--pre-glob" || strings.HasPrefix(a, "--pre-glob=")) {
		return true
	}
	return false
}

func isStdinJqFilter(args []string) bool {
	pos := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos += len(args) - i - 1
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if isBlockedJqFlag(a) {
				return false
			}
			if a == "--arg" || a == "--argjson" {
				i += 2
				continue
			}
			if a == "--indent" {
				i++
			}
			continue
		}
		pos++
	}
	return pos <= 1
}

func isBlockedJqFlag(a string) bool {
	for _, p := range []string{"-f", "-L", "--from-file", "--slurpfile", "--rawfile", "--argfile", "--library-path"} {
		if a == p || strings.HasPrefix(a, p+"=") {
			return true
		}
	}
	return !strings.HasPrefix(a, "--") && strings.ContainsAny(a[1:], "fL")
}

func isStdinPositionalBound(args []string, maxPos int, valFlags []string) bool {
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
		if strings.HasPrefix(a, "-") {
			if slices.Contains(valFlags, a) {
				i++
			}
			continue
		}
		pos++
	}
	return pos <= maxPos
}

func isStdinNoFlagVal(args, blockedLongs []string) bool {
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
		for _, b := range blockedLongs {
			if a == b || strings.HasPrefix(a, b+"=") {
				return false
			}
		}
	}
	return true
}

func parseBashAwkRead(args []string) ([]BashFileRead, bool) {
	var prog string
	var files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest := args[i+1:]
			if prog == "" && len(rest) > 0 {
				prog, rest = rest[0], rest[1:]
			}
			files = append(files, rest...)
			break
		}
		if prog == "" && strings.HasPrefix(a, "-") && len(a) > 1 {
			nextIdx, ok := stepSafeAwkFlag(args, i)
			if !ok {
				return nil, false
			}
			i = nextIdx
			continue
		}
		if prog == "" {
			prog = a
		} else {
			files = append(files, a)
		}
	}
	if prog == "" || len(files) == 0 || !isSafeAwkReadProgram(prog) {
		return nil, false
	}
	start, end, ok := parseAwkLineRange(prog)
	if !ok {
		return nil, false
	}
	out := make([]BashFileRead, 0, len(files))
	for _, f := range files {
		if f == "" || f == "-" {
			return nil, false
		}
		out = append(out, BashFileRead{Tool: "awk", Path: f, StartLine: start, EndLine: end})
	}
	return out, true
}

func isSafeAwkReadProgram(prog string) bool {
	low := strings.ToLower(prog)
	if strings.Contains(prog, "|") || strings.Contains(prog, ">>") ||
		awkSystemCallRe.MatchString(prog) || strings.Contains(low, "getline") ||
		strings.Contains(low, "close(") || strings.Contains(low, "fflush(") {
		return false
	}
	brace := strings.IndexByte(prog, '{')
	for i := 0; i < len(prog); i++ {
		if prog[i] != '>' || (i+1 < len(prog) && prog[i+1] == '=') {
			continue
		}
		if brace >= 0 && i >= brace {
			return false
		}
	}
	return true
}

var awkVerbatimPrintBodyRe = regexp.MustCompile(`^print(?:\s+\$0|\s+NR(?:\s*,\s*|\s+|\s*"[^"]*"\s*|\s*,\s*"[^"]*"\s*,\s*)\$0)?$`)

func isVerbatimAwkPrintBody(body string) bool {
	if !strings.HasPrefix(body, "{") || !strings.HasSuffix(body, "}") {
		return false
	}
	inner := strings.TrimSpace(body[1 : len(body)-1])
	inner = strings.TrimSpace(strings.TrimSuffix(inner, ";"))
	if inner == "" || strings.ContainsAny(inner, "{};") {
		return false
	}
	return awkVerbatimPrintBodyRe.MatchString(inner)
}

func parseAwkLineRange(prog string) (int, int, bool) {
	cond, body := strings.TrimSpace(prog), ""
	if brace := strings.IndexByte(cond, '{'); brace >= 0 {
		body = strings.TrimSpace(cond[brace:])
		cond = strings.TrimSpace(cond[:brace])
		if !isVerbatimAwkPrintBody(body) {
			return 0, 0, false
		}
	}
	if cond == "" {
		if body == "" {
			return 0, 0, false
		}
		return 1, 0, true
	}
	for strings.HasPrefix(cond, "(") && strings.HasSuffix(cond, ")") {
		cond = strings.TrimSpace(cond[1 : len(cond)-1])
	}
	if cond == "1" {
		return 1, 0, true
	}
	if m := awkNRRangeCommaRe.FindStringSubmatch(cond); len(m) == 3 {
		s, err1 := strconv.Atoi(m[1])
		e, err2 := strconv.Atoi(m[2])
		return s, e, err1 == nil && err2 == nil && s >= 1 && e >= s
	}
	if m := awkNREqRe.FindStringSubmatch(cond); len(m) == 2 {
		n, err := strconv.Atoi(m[1])
		return n, n, err == nil && n >= 1
	}
	return parseAwkNRCompound(cond)
}

func parseAwkNRCompound(cond string) (int, int, bool) {
	clauses := strings.Split(cond, "&&")
	if len(clauses) == 0 || len(clauses) > 2 {
		return 0, 0, false
	}
	lower, upper, hasBound := 1, 0, false
	for _, cl := range clauses {
		isLower, val, ok := parseAwkNRBound(cl)
		if !ok {
			return 0, 0, false
		}
		hasBound = true
		if isLower {
			lower = max(lower, val)
		} else if upper == 0 || val < upper {
			upper = val
		}
	}
	return lower, upper, hasBound && lower >= 1 && (upper == 0 || upper >= lower)
}

func parseAwkNRBound(clause string) (bool, int, bool) {
	m := awkNRCmpRe.FindStringSubmatch(strings.TrimSpace(clause))
	if len(m) != 7 {
		return false, 0, false
	}
	var op string
	var n int
	var err error
	if m[1] != "" {
		op, n, err = m[2], 0, nil
		n, err = strconv.Atoi(m[3])
	} else {
		n, err = strconv.Atoi(m[4])
		switch m[5] {
		case "<=":
			op = ">="
		case "<":
			op = ">"
		case ">=":
			op = "<="
		case ">":
			op = "<"
		}
	}
	if err != nil {
		return false, 0, false
	}
	switch op {
	case ">=":
		return true, max(1, n), n >= 0
	case ">":
		return true, max(1, n+1), n >= 0
	case "<=":
		return false, n, n >= 1
	case "<":
		return false, n - 1, n >= 2
	}
	return false, 0, false
}
