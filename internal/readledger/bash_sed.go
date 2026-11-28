package readledger

import (
	"strconv"
	"strings"
)

func parseBashSedMulti(args []string) ([]BashFileRead, bool) {
	quiet, scripts, pos, ok := parseSedArgs(args, -1)
	if !ok || !quiet || len(scripts) == 0 || len(pos) == 0 {
		return nil, false
	}
	ranges, ok := parseSedMultiRanges(scripts)
	if !ok {
		return nil, false
	}
	out := make([]BashFileRead, 0, len(pos)*len(ranges))
	for _, f := range pos {
		if f == "" || f == "-" {
			return nil, false
		}
		for _, rg := range ranges {
			out = append(out, BashFileRead{Tool: "sed", Path: f, StartLine: rg[0], EndLine: rg[1]})
		}
	}
	return out, true
}

func isSedClauseSep(r rune) bool {
	return r == ';' || r == '\n'
}

func parseSedMultiRanges(scripts []string) ([][2]int, bool) {
	var ranges [][2]int
	for _, sc := range scripts {
		for _, raw := range strings.FieldsFunc(sc, isSedClauseSep) {
			rng, isRange, ok := parseSedRangeClause(strings.TrimSpace(raw))
			if !ok {
				return nil, false
			}
			if isRange {
				ranges = append(ranges, rng)
			}
		}
	}
	return ranges, len(ranges) > 0
}

func parseSedRangeClause(clause string) ([2]int, bool, bool) {
	if clause == "" {
		return [2]int{}, false, true
	}
	if strings.HasSuffix(clause, "q") {
		pref := strings.TrimSpace(strings.TrimSuffix(clause, "q"))
		if pref == "" {
			return [2]int{}, false, true
		}
		n, err := strconv.Atoi(pref)
		return [2]int{}, false, err == nil && n > 0
	}
	s, e, ok := parseSedRangeScript(clause)
	return [2]int{s, e}, ok, ok
}

func applyStdinSedStage(cur BashFileRead, lineMapped bool, args []string) (BashFileRead, bool, bool) {
	quiet, scripts, ok := parseStdinSedArgs(args)
	if !ok || !quiet || len(scripts) == 0 {
		return BashFileRead{}, false, false
	}
	if len(scripts) == 1 {
		if s, e, ok := parseSedRangeScript(scripts[0]); ok {
			if lineMapped {
				return refineStreamRange(cur, s, e), true, true
			}
			return cur, false, true
		}
	}
	return cur, false, allSafeReadOnlySedScripts(scripts)
}

func parseStdinSedArgs(args []string) (bool, []string, bool) {
	quiet, scripts, files, ok := parseSedArgs(args, 0)
	return quiet, scripts, ok && len(files) == 0
}

func parseSedArgs(args []string, maxFiles int) (bool, []string, []string, bool) {
	quiet := false
	var scripts, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			var ok bool
			if i, quiet, scripts, ok = stepSedFlag(args, i, quiet, scripts); !ok {
				return false, nil, nil, false
			}
			continue
		}
		pos = append(pos, a)
	}
	return finalizeSedArgs(quiet, scripts, pos, maxFiles)
}

func finalizeSedArgs(quiet bool, scripts, pos []string, maxFiles int) (bool, []string, []string, bool) {
	if len(scripts) == 0 && len(pos) > 0 && pos[0] != "-" {
		if (maxFiles < 0 && len(pos) >= 2) || (maxFiles >= 0 && len(pos) == maxFiles+1) {
			scripts, pos = []string{pos[0]}, pos[1:]
		}
	}
	if maxFiles < 0 {
		return quiet, scripts, pos, len(pos) >= 1
	}
	return quiet, scripts, pos, len(pos) <= maxFiles
}

func stepSedFlag(args []string, i int, quiet bool, scripts []string) (int, bool, []string, bool) {
	switch a := args[i]; {
	case a == "-n" || a == "--quiet" || a == "--silent":
		return i, true, scripts, true
	case a == "-e" || a == "--expression":
		if i+1 >= len(args) {
			return i, false, nil, false
		}
		return i + 1, quiet, append(scripts, args[i+1]), true
	case strings.HasPrefix(a, "--expression="):
		return i, quiet, append(scripts, strings.TrimPrefix(a, "--expression=")), true
	case a == "-E" || a == "-r" || a == "-u" || a == "--regexp-extended" || a == "--unbuffered" || a == "--posix" || a == "--sandbox":
		return i, quiet, scripts, true
	case strings.HasPrefix(a, "--"):
		return i, false, nil, false
	default:
		nextIdx, nextQuiet, sc, hasScript, ok := parseShortSedFlags(args, i, quiet)
		if !ok {
			return i, false, nil, false
		}
		if hasScript {
			scripts = append(scripts, sc)
		}
		return nextIdx, nextQuiet, scripts, true
	}
}

func parseShortSedFlags(args []string, i int, quiet bool) (int, bool, string, bool, bool) {
	a := args[i]
	for j := 1; j < len(a); j++ {
		switch a[j] {
		case 'n':
			quiet = true
		case 'E', 'r', 'u':
		case 'e':
			nextIdx, sc, ok := consumeShortClusterArg(args, i, a[j+1:])
			return nextIdx, quiet, sc, ok, ok
		default:
			return i, quiet, "", false, false
		}
	}
	return i, quiet, "", false, true
}

func allSafeReadOnlySedScripts(scripts []string) bool {
	if len(scripts) == 0 {
		return false
	}
	for _, sc := range scripts {
		if !isSafeReadOnlySedScript(sc) {
			return false
		}
	}
	return true
}

func isSafeReadOnlySedScript(sc string) bool {
	depth, sawCmd := 0, false
	for i := 0; i < len(sc); {
		i = skipSedSpace(sc, i)
		if i >= len(sc) {
			break
		}
		var cmd, ok bool
		i, depth, cmd, ok = stepSafeSedToken(sc, i, depth)
		if !ok {
			return false
		}
		sawCmd = sawCmd || cmd
	}
	return depth == 0 && sawCmd
}

func stepSafeSedToken(sc string, i, depth int) (int, int, bool, bool) {
	if sc[i] == ';' || sc[i] == '\n' {
		return i + 1, depth, false, true
	}
	if sc[i] == '}' {
		return i + 1, depth - 1, false, depth > 0
	}
	next, hasAddr, ok := skipSedAddrRange(sc, i)
	if !ok {
		return 0, 0, false, false
	}
	i = skipSedSpace(sc, next)
	if hasAddr && i < len(sc) && sc[i] == '!' {
		i = skipSedSpace(sc, i+1)
	}
	if i >= len(sc) {
		return 0, 0, false, false
	}
	if sc[i] == '{' {
		return i + 1, depth + 1, false, true
	}
	after, ok := stepSafeSedCmd(sc, i)
	return after, depth, ok, ok
}

func stepSafeSedCmd(sc string, i int) (int, bool) {
	switch sc[i] {
	case 'p', 'P', '=', 'd', 'D', 'n', 'N', 'h', 'H', 'g', 'G', 'x':
		i++
	case 'q', 'Q', 'l':
		i = skipSedDigits(sc, skipSedSpace(sc, i+1))
	default:
		return 0, false
	}
	i = skipSedSpace(sc, i)
	if i < len(sc) && sc[i] != ';' && sc[i] != '\n' && sc[i] != '}' {
		return 0, false
	}
	return i, true
}

func skipSedSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

func skipSedDigits(s string, i int) int {
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}

func skipSedAddrRange(s string, i int) (int, bool, bool) {
	next, has1, ok := skipSedAddr(s, i, false)
	if !ok || !has1 {
		return i, false, ok
	}
	after := skipSedSpace(s, next)
	if after >= len(s) || s[after] != ',' {
		return next, true, true
	}
	after = skipSedSpace(s, after+1)
	end, has2, ok := skipSedAddr(s, after, true)
	if !ok || !has2 {
		return 0, false, false
	}
	return end, true, true
}

func skipSedAddr(s string, i int, allowRel bool) (int, bool, bool) {
	if i >= len(s) {
		return i, false, true
	}
	if s[i] == '$' {
		return i + 1, true, true
	}
	if s[i] == '/' {
		end, ok := skipSedRegex(s, i)
		return end, ok, ok
	}
	if allowRel && (s[i] == '+' || s[i] == '~') {
		end := skipSedDigits(s, i+1)
		return end, end > i+1, end > i+1
	}
	if s[i] >= '0' && s[i] <= '9' {
		end := skipSedDigits(s, i)
		if end < len(s) && s[end] == '~' {
			stepEnd := skipSedDigits(s, end+1)
			return stepEnd, stepEnd > end+1, stepEnd > end+1
		}
		return end, true, true
	}
	return i, false, true
}

func skipSedRegex(s string, i int) (int, bool) {
	inClass, classStart := false, false
	for i++; i < len(s); i++ {
		ch := s[i]
		if inClass {
			var ok bool
			if inClass, classStart, ok = stepSedRegexClass(ch, classStart); !ok {
				return 0, false
			}
			continue
		}
		switch ch {
		case '\\':
			if i+1 >= len(s) || s[i+1] == '\n' {
				return 0, false
			}
			i++
		case '[':
			inClass, classStart = true, true
		case '/':
			return skipSedRegexFlags(s, i+1), true
		case '\n':
			return 0, false
		}
	}
	return 0, false
}

func stepSedRegexClass(ch byte, classStart bool) (bool, bool, bool) {
	if ch == '/' || ch == '\n' || ch == '[' {
		return false, false, false
	}
	if ch == ']' && !classStart {
		return false, false, true
	}
	if classStart && ch == '^' {
		return true, true, true
	}
	return true, false, true
}

func skipSedRegexFlags(s string, i int) int {
	for i < len(s) && (s[i] == 'I' || s[i] == 'M') {
		i++
	}
	return i
}
