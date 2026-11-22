package search

import "strings"

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
		switch a := args[i]; {
		case a == "--":
			pos, i = append(pos, args[i+1:]...), len(args)
		case a == "-n" || a == "--quiet" || a == "--silent":
			quiet = true
		case a == "-e" || a == "--expression":
			if i+1 >= len(args) {
				return false, nil, nil, false
			}
			i, scripts = i+1, append(scripts, args[i+1])
		case strings.HasPrefix(a, "--expression="):
			scripts = append(scripts, strings.TrimPrefix(a, "--expression="))
		case a == "-E" || a == "-r" || a == "-u" || a == "--regexp-extended" || a == "--unbuffered" || a == "--posix" || a == "--sandbox":
		case strings.HasPrefix(a, "--"):
			return false, nil, nil, false
		case strings.HasPrefix(a, "-") && len(a) > 1:
			var sc string
			var hasScript, ok bool
			i, quiet, sc, hasScript, ok = parseShortSedFlags(args, i, quiet)
			if !ok {
				return false, nil, nil, false
			}
			if hasScript {
				scripts = append(scripts, sc)
			}
		default:
			pos = append(pos, a)
		}
	}
	if len(scripts) == 0 && len(pos) == maxFiles+1 && pos[0] != "-" {
		scripts, pos = []string{pos[0]}, pos[1:]
	}
	return quiet, scripts, pos, len(pos) <= maxFiles
}

func parseShortSedFlags(args []string, i int, quiet bool) (int, bool, string, bool, bool) {
	a := args[i]
	for j := 1; j < len(a); j++ {
		switch a[j] {
		case 'n':
			quiet = true
		case 'E', 'r', 'u':
		case 'e':
			if j+1 < len(a) {
				return i, quiet, a[j+1:], true, true
			}
			if i+1 >= len(args) {
				return i, quiet, "", false, false
			}
			return i + 1, quiet, args[i+1], true, true
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
		if sc[i] == ';' || sc[i] == '\n' {
			i++
			continue
		}
		if sc[i] == '}' {
			if depth == 0 {
				return false
			}
			depth, i = depth-1, i+1
			continue
		}
		next, hasAddr, ok := skipSedAddrRange(sc, i)
		if !ok {
			return false
		}
		i = skipSedSpace(sc, next)
		if hasAddr && i < len(sc) && sc[i] == '!' {
			i = skipSedSpace(sc, i+1)
		}
		if i >= len(sc) {
			return false
		}
		if sc[i] == '{' {
			depth, i = depth+1, i+1
			continue
		}
		after, ok := stepSafeSedCmd(sc, i)
		if !ok {
			return false
		}
		sawCmd, i = true, after
	}
	return depth == 0 && sawCmd
}

func stepSafeSedCmd(sc string, i int) (int, bool) {
	switch sc[i] {
	case 'p', 'P', '=', 'd', 'D', 'n', 'N', 'h', 'H', 'g', 'G', 'x':
		i++
	case 'q', 'Q', 'l':
		i = skipSedSpace(sc, i+1)
		for i < len(sc) && sc[i] >= '0' && sc[i] <= '9' {
			i++
		}
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
		start := i + 1
		end := start
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		return end, end > start, end > start
	}
	if s[i] >= '0' && s[i] <= '9' {
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i < len(s) && s[i] == '~' {
			i++
			start := i
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
			return i, i > start, i > start
		}
		return i, true, true
	}
	return i, false, true
}

func skipSedRegex(s string, i int) (int, bool) {
	inClass, classStart := false, false
	for i++; i < len(s); i++ {
		ch := s[i]
		if inClass {
			if ch == '/' || ch == '\n' || ch == '[' {
				return 0, false
			}
			if ch == ']' && !classStart {
				inClass = false
				continue
			}
			if classStart && ch == '^' {
				continue
			}
			classStart = false
			continue
		}
		if ch == '\\' {
			if i+1 >= len(s) || s[i+1] == '\n' {
				return 0, false
			}
			i++
			continue
		}
		if ch == '[' {
			inClass, classStart = true, true
			continue
		}
		if ch == '/' {
			i++
			for i < len(s) && (s[i] == 'I' || s[i] == 'M') {
				i++
			}
			return i, true
		}
		if ch == '\n' {
			return 0, false
		}
	}
	return 0, false
}
