package search

import (
	"slices"
	"strings"
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
