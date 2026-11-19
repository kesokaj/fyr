package search

import (
	"path/filepath"
	"strconv"
	"strings"
)

// BashFileRead describes a single-file read extracted from a shell command string.
type BashFileRead struct {
	Tool, Path                    string
	StartLine, EndLine, TailLines int
}

// ParseBashFileRead detects single-file cat, head, tail, sed -n, nl, and bat commands without pipes or redirects.
func ParseBashFileRead(cmd string) (BashFileRead, bool) {
	tokens, ok := tokenizeSimpleShell(cmd)
	if !ok || len(tokens) < 2 {
		return BashFileRead{}, false
	}
	switch tool := filepath.Base(tokens[0]); tool {
	case "cat", "nl", "bat", "batcat":
		return parseBashSimpleOrBat(tool, tokens[1:])
	case "head", "tail":
		return parseBashHeadOrTail(tool, tokens[1:])
	case "sed":
		return parseBashSed(tokens[1:])
	}
	return BashFileRead{}, false
}

func tokenizeSimpleShell(cmd string) ([]string, bool) {
	var toks []string
	var cur strings.Builder
	inTok, quote := false, byte(0)
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if quote != 0 {
			nextIdx, ok := stepQuotedShellByte(cmd, i, quote, &cur)
			if !ok {
				return nil, false
			}
			if nextIdx < 0 {
				quote = 0
			} else {
				i = nextIdx
			}
			continue
		}
		if strings.IndexByte("|&;<>()`$*?[\n\r", ch) >= 0 || (ch == '\\' && i+1 >= len(cmd)) {
			return nil, false
		}
		switch ch {
		case '\'', '"':
			quote, inTok = ch, true
		case '\\':
			i, inTok = i+1, true
			cur.WriteByte(cmd[i])
		case ' ', '\t':
			if inTok {
				toks, inTok = append(toks, cur.String()), false
				cur.Reset()
			}
		default:
			inTok = true
			cur.WriteByte(ch)
		}
	}
	if inTok {
		toks = append(toks, cur.String())
	}
	return toks, quote == 0 && len(toks) > 0
}

func stepQuotedShellByte(cmd string, i int, quote byte, cur *strings.Builder) (int, bool) {
	if cmd[i] == quote {
		return -1, true
	}
	if quote == '"' && (cmd[i] == '`' || (cmd[i] == '$' && i+1 < len(cmd) && (cmd[i+1] == '(' || cmd[i+1] == '{'))) {
		return i, false
	}
	if quote == '"' && cmd[i] == '\\' && i+1 < len(cmd) {
		cur.WriteByte(cmd[i+1])
		return i + 1, true
	}
	cur.WriteByte(cmd[i])
	return i, true
}

func parseBashSimpleOrBat(rawTool string, args []string) (BashFileRead, bool) {
	tool := rawTool
	if tool == "batcat" {
		tool = "bat"
	}
	start, end := 1, 0
	var files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			files = append(files, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			nextIdx, s, e, ok := stepSimpleOrBatFlag(tool, args, i, start, end)
			if !ok {
				return BashFileRead{}, false
			}
			i, start, end = nextIdx, s, e
			continue
		}
		files = append(files, a)
	}
	if len(files) != 1 || files[0] == "-" {
		return BashFileRead{}, false
	}
	return BashFileRead{Tool: tool, Path: files[0], StartLine: start, EndLine: end}, true
}

func stepSimpleOrBatFlag(tool string, args []string, i, start, end int) (int, int, int, bool) {
	a := args[i]
	if tool == "cat" {
		ok := strings.Contains("|--number|--number-nonblank|--squeeze-blank|--show-all|--show-ends|--show-tabs|--show-nonprinting|", "|"+a+"|") ||
			(!strings.HasPrefix(a, "--") && len(a) > 1 && strings.TrimLeft(a[1:], "nbsveEtTAu") == "")
		return i, start, end, ok
	}
	if tool == "nl" {
		if strings.Contains("|-b|-n|-s|-w|-v|-i|-l|", "|"+a+"|") && i+1 < len(args) {
			return i + 1, start, end, true
		}
		return i, start, end, true
	}
	if (a == "-r" || a == "--line-range") && i+1 < len(args) {
		s, e, ok := parseBatLineRange(args[i+1])
		return i + 1, s, e, ok
	}
	if strings.HasPrefix(a, "--line-range=") || (strings.HasPrefix(a, "-r") && len(a) > 2) {
		s, e, ok := parseBatLineRange(strings.TrimPrefix(strings.TrimPrefix(a, "--line-range="), "-r"))
		return i, s, e, ok
	}
	if a == "-r" || a == "--line-range" {
		return i, 0, 0, false
	}
	if strings.Contains("|--style|--color|--paging|--decorations|--wrap|--theme|-l|--language|", "|"+a+"|") && i+1 < len(args) {
		return i + 1, start, end, true
	}
	return i, start, end, true
}

func parseBatLineRange(spec string) (int, int, bool) {
	sStr, eStr, ok := strings.Cut(spec, ":")
	if !ok {
		return 0, 0, false
	}
	start, end := 1, 0
	if sStr != "" {
		if n, err := strconv.Atoi(sStr); err != nil || n <= 0 {
			return 0, 0, false
		} else {
			start = n
		}
	}
	if eStr != "" {
		if n, err := strconv.Atoi(eStr); err != nil || n < start {
			return 0, 0, false
		} else {
			end = n
		}
	}
	return start, end, true
}

func parseBashHeadOrTail(tool string, args []string) (BashFileRead, bool) {
	rawCount := "10"
	var files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			files = append(files, args[i+1:]...)
			i = len(args)
		case a == "-n" || a == "--lines":
			if i+1 >= len(args) {
				return BashFileRead{}, false
			}
			i, rawCount = i+1, args[i+1]
		case strings.HasPrefix(a, "-n") || strings.HasPrefix(a, "--lines="):
			rawCount = strings.TrimPrefix(strings.TrimPrefix(a, "-n"), "--lines=")
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if _, err := strconv.Atoi(a[1:]); err == nil {
				rawCount = a[1:]
			} else if !strings.Contains("|-q|-v|--quiet|--silent|--verbose|", "|"+a+"|") {
				return BashFileRead{}, false
			}
		default:
			files = append(files, a)
		}
	}
	if len(files) != 1 || files[0] == "-" {
		return BashFileRead{}, false
	}
	return buildHeadOrTailRead(tool, files[0], rawCount)
}

func buildHeadOrTailRead(tool, filePath, rawCount string) (BashFileRead, bool) {
	if tool == "head" {
		if strings.HasPrefix(rawCount, "-") {
			return BashFileRead{}, false
		}
		n, err := strconv.Atoi(strings.TrimPrefix(rawCount, "+"))
		return BashFileRead{Tool: "head", Path: filePath, StartLine: 1, EndLine: n}, err == nil && n > 0
	}
	if strings.HasPrefix(rawCount, "+") {
		n, err := strconv.Atoi(rawCount[1:])
		return BashFileRead{Tool: "tail", Path: filePath, StartLine: n, EndLine: 0}, err == nil && n > 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(rawCount, "-"))
	return BashFileRead{Tool: "tail", Path: filePath, StartLine: -n, EndLine: 0, TailLines: n}, err == nil && n > 0
}

func parseBashSed(args []string) (BashFileRead, bool) {
	quiet := false
	var scripts, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			pos, i = append(pos, args[i+1:]...), len(args)
		case a == "-n" || a == "--quiet" || a == "--silent":
			quiet = true
		case a == "-e":
			if i+1 >= len(args) {
				return BashFileRead{}, false
			}
			i, scripts = i+1, append(scripts, args[i+1])
		case strings.HasPrefix(a, "-"):
			if strings.ContainsAny(a, "if") {
				return BashFileRead{}, false
			}
			quiet = quiet || strings.ContainsRune(a, 'n')
			if strings.HasSuffix(a, "e") && i+1 < len(args) {
				i, scripts = i+1, append(scripts, args[i+1])
			}
		default:
			pos = append(pos, a)
		}
	}
	if len(scripts) == 0 && len(pos) == 2 {
		scripts, pos = []string{pos[0]}, pos[1:]
	}
	if !quiet || len(scripts) != 1 || len(pos) != 1 || pos[0] == "-" {
		return BashFileRead{}, false
	}
	start, end, ok := parseSedRangeScript(scripts[0])
	return BashFileRead{Tool: "sed", Path: pos[0], StartLine: start, EndLine: end}, ok
}

func parseSedRangeScript(script string) (int, int, bool) {
	script = strings.TrimSpace(script)
	if semi := strings.IndexByte(script, ';'); semi > 0 {
		tail := strings.TrimSpace(script[semi+1:])
		if _, err := strconv.Atoi(strings.TrimSuffix(tail, "q")); !strings.HasSuffix(tail, "q") || err != nil {
			return 0, 0, false
		}
		script = strings.TrimSpace(script[:semi])
	}
	if !strings.HasSuffix(script, "p") {
		return 0, 0, false
	}
	addr := strings.TrimSuffix(script, "p")
	if startStr, endStr, ok := strings.Cut(addr, ","); ok {
		start, err := strconv.Atoi(strings.TrimSpace(startStr))
		if err != nil || start <= 0 {
			return 0, 0, false
		}
		if strings.TrimSpace(endStr) == "$" {
			return start, 0, true
		}
		end, err := strconv.Atoi(strings.TrimSpace(endStr))
		return start, end, err == nil && end >= start
	}
	line, err := strconv.Atoi(strings.TrimSpace(addr))
	return line, line, err == nil && line > 0
}
