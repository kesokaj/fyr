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
	segs, sawSep, ok := splitSimpleShellChainsCore(cmd)
	if !ok || sawSep || len(segs) != 1 {
		return BashFileRead{}, false
	}
	return parseBashReadTokens(tokenizeValidatedSegment(segs[0]))
}

// ParseBashFileReads extracts file reads from &&, ;, and newline shell chains
// (including benign env prefixes and strictly read-only pipes), tracking cd prefixes.
func ParseBashFileReads(cmd string) []BashFileRead {
	return parseBashFileReadsExtended(cmd)
}

func parseBashReadTokens(tokens []string) (BashFileRead, bool) {
	if len(tokens) < 2 {
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

func parseBashReadTokensMulti(tokens []string) ([]BashFileRead, bool) {
	if len(tokens) < 2 {
		return nil, false
	}
	switch tool := filepath.Base(tokens[0]); tool {
	case "cat", "nl", "bat", "batcat":
		return parseBashSimpleOrBatMulti(tool, tokens[1:])
	case "head", "tail":
		return parseBashHeadOrTailMulti(tool, tokens[1:])
	case "sed":
		return parseBashSedMulti(tokens[1:])
	case "awk", "gawk", "mawk", "nawk":
		return parseBashAwkRead(tokens[1:])
	case "grep", "egrep", "fgrep", "rg":
		return parseBashMatchAllGrepRead(tool, tokens[1:])
	default:
		if isPythonToolName(tool) {
			return parseBashPythonRead(tokens[1:])
		}
	}
	return nil, false
}

func stepTrackedBashDir(cwd string, unknown bool, toks []string) (bool, string, bool) {
	hasEnv := false
	for len(toks) > 0 && isEnvAssignToken(toks[0]) {
		hasEnv = true
		toks = toks[1:]
	}
	if len(toks) == 0 {
		return false, cwd, unknown
	}
	if (toks[0] == "builtin" || toks[0] == "command") && len(toks) > 1 {
		toks = toks[1:]
		if toks[0] == "--" && len(toks) > 1 {
			toks = toks[1:]
		}
	}
	switch toks[0] {
	case "pushd", "popd":
		return true, "", true
	case "cd":
		if hasEnv || len(toks) != 2 || toks[1] == "" || strings.HasPrefix(toks[1], "-") || strings.HasPrefix(toks[1], "~") || strings.ContainsRune(toks[1], '$') {
			return true, "", true
		}
		if filepath.IsAbs(toks[1]) {
			return true, filepath.ToSlash(filepath.Clean(toks[1])), false
		}
		if unknown {
			return true, "", true
		}
		return true, filepath.ToSlash(filepath.Clean(filepath.Join(cwd, toks[1]))), false
	}
	return false, cwd, unknown
}

func splitSimpleShellChains(cmd string) ([]string, bool) {
	segs, _, ok := splitSimpleShellChainsCore(cmd)
	return segs, ok
}

func splitSimpleShellChainsCore(cmd string) ([]string, bool, bool) {
	var segs []string
	start, quote, sawSep := 0, byte(0), false
	for i := 0; i < len(cmd); i++ {
		nextIdx, nextQ, sepLen, ok := stepChainSplit(cmd, i, quote)
		if !ok {
			return nil, sawSep, false
		}
		if sepLen > 0 {
			sawSep = true
			if s := strings.TrimSpace(cmd[start:i]); s != "" {
				segs = append(segs, s)
			}
			start = i + sepLen
		}
		i, quote = nextIdx, nextQ
	}
	if s := strings.TrimSpace(cmd[start:]); quote == 0 && s != "" {
		segs = append(segs, s)
	}
	return segs, sawSep, quote == 0 && len(segs) > 0
}

func stepChainSplit(cmd string, i int, quote byte) (int, byte, int, bool) {
	if quote != 0 {
		nextIdx, nextQ, ok := stepQuotedShellByte(cmd, i, quote, nil)
		return nextIdx, nextQ, 0, ok
	}
	switch cmd[i] {
	case '\\':
		return i + 1, 0, 0, i+1 < len(cmd)
	case '\'', '"':
		return i, cmd[i], 0, true
	case ';':
		return i, 0, 1, true
	case '&':
		return i + 1, 0, 2, i+1 < len(cmd) && cmd[i+1] == '&' && (i+2 >= len(cmd) || cmd[i+2] != '&')
	}
	return i, 0, 0, strings.IndexByte("|<>()`$*?[\n\r", cmd[i]) < 0
}

func tokenizeValidatedSegment(seg string) []string {
	var toks []string
	var cur strings.Builder
	inTok, quote := false, byte(0)
	for i := 0; i < len(seg); i++ {
		switch ch := seg[i]; {
		case quote != 0:
			i, quote, _ = stepQuotedShellByte(seg, i, quote, &cur)
		case ch == '\'' || ch == '"':
			quote, inTok = ch, true
		case ch == '\\':
			i, inTok = i+1, true
			cur.WriteByte(seg[i])
		case ch == ' ' || ch == '\t':
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
	return toks
}

func stepQuotedShellByte(cmd string, i int, quote byte, cur *strings.Builder) (int, byte, bool) {
	if cmd[i] == quote {
		return i, 0, true
	}
	if quote == '"' && (cmd[i] == '`' || (cmd[i] == '$' && i+1 < len(cmd) && (cmd[i+1] == '(' || cmd[i+1] == '{'))) {
		return i, quote, false
	}
	if quote == '"' && cmd[i] == '\\' && i+1 < len(cmd) {
		i++
	}
	if cur != nil {
		cur.WriteByte(cmd[i])
	}
	return i, quote, true
}

func parseBashSimpleOrBat(tool string, args []string) (BashFileRead, bool) {
	reads, ok := parseBashSimpleOrBatMulti(tool, args)
	if !ok || len(reads) != 1 {
		return BashFileRead{}, false
	}
	return reads[0], true
}

func parseBashSimpleOrBatMulti(tool string, args []string) ([]BashFileRead, bool) {
	if tool == "batcat" {
		tool = "bat"
	}
	start, end := 1, 0
	var files []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			files, i = append(files, args[i+1:]...), len(args)
		case strings.HasPrefix(a, "-") && a != "-":
			var ok bool
			if i, start, end, ok = stepSimpleOrBatFlag(tool, args, i, start, end); !ok {
				return nil, false
			}
		default:
			files = append(files, a)
		}
	}
	if len(files) == 0 {
		return nil, false
	}
	out := make([]BashFileRead, 0, len(files))
	for _, f := range files {
		if f == "" || f == "-" {
			return nil, false
		}
		out = append(out, BashFileRead{Tool: tool, Path: f, StartLine: start, EndLine: end})
	}
	return out, true
}

func stepSimpleOrBatFlag(tool string, args []string, i, start, end int) (int, int, int, bool) {
	switch a := args[i]; {
	case tool == "cat":
		return i, start, end, strings.Contains("|--number|--number-nonblank|--squeeze-blank|--show-all|--show-ends|--show-tabs|--show-nonprinting|", "|"+a+"|") ||
			(!strings.HasPrefix(a, "--") && len(a) > 1 && strings.TrimLeft(a[1:], "nbsveEtTAu") == "")
	case tool == "nl":
		if strings.Contains("|-b|-n|-s|-w|-v|-i|-l|", "|"+a+"|") && i+1 < len(args) {
			i++
		}
	case a == "-r" || a == "--line-range":
		if i+1 >= len(args) {
			return i, 0, 0, false
		}
		s, e, ok := parseBatLineRange(args[i+1])
		return i + 1, s, e, ok
	case strings.HasPrefix(a, "--line-range=") || (strings.HasPrefix(a, "-r") && len(a) > 2):
		s, e, ok := parseBatLineRange(strings.TrimPrefix(strings.TrimPrefix(a, "--line-range="), "-r"))
		return i, s, e, ok
	case strings.Contains("|--style|--color|--paging|--decorations|--wrap|--theme|-l|--language|", "|"+a+"|") && i+1 < len(args):
		i++
	}
	return i, start, end, true
}

func parseBatLineRange(spec string) (int, int, bool) {
	sStr, eStr, ok := strings.Cut(spec, ":")
	if !ok {
		return 0, 0, false
	}
	start, ok1 := parseOptBatBound(sStr, 1, 1)
	end, ok2 := parseOptBatBound(eStr, start, 0)
	return start, end, ok1 && ok2
}

func parseOptBatBound(raw string, minVal, def int) (int, bool) {
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	return n, err == nil && n >= minVal
}

func parseBashHeadOrTail(tool string, args []string) (BashFileRead, bool) {
	reads, ok := parseBashHeadOrTailMulti(tool, args)
	if !ok || len(reads) != 1 {
		return BashFileRead{}, false
	}
	return reads[0], true
}

func parseBashHeadOrTailMulti(tool string, args []string) ([]BashFileRead, bool) {
	rawCount := "10"
	var files []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			files = append(files, args[i+1:]...)
			i = len(args)
		case a == "-n" || a == "--lines":
			if i+1 >= len(args) {
				return nil, false
			}
			i, rawCount = i+1, args[i+1]
		case strings.HasPrefix(a, "-n") || strings.HasPrefix(a, "--lines="):
			rawCount = strings.TrimPrefix(strings.TrimPrefix(a, "-n"), "--lines=")
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if _, err := strconv.Atoi(a[1:]); err == nil {
				rawCount = a[1:]
			} else if !strings.Contains("|-q|-v|--quiet|--silent|--verbose|", "|"+a+"|") {
				return nil, false
			}
		default:
			files = append(files, a)
		}
	}
	if len(files) == 0 {
		return nil, false
	}
	out := make([]BashFileRead, 0, len(files))
	for _, f := range files {
		if f == "" || f == "-" {
			return nil, false
		}
		r, ok := buildHeadOrTailRead(tool, f, rawCount)
		if !ok {
			return nil, false
		}
		out = append(out, r)
	}
	return out, true
}

func buildHeadOrTailRead(tool, filePath, rawCount string) (BashFileRead, bool) {
	if tool == "head" {
		n, err := strconv.Atoi(strings.TrimPrefix(rawCount, "+"))
		return BashFileRead{Tool: "head", Path: filePath, StartLine: 1, EndLine: n}, !strings.HasPrefix(rawCount, "-") && err == nil && n > 0
	}
	if strings.HasPrefix(rawCount, "+") {
		n, err := strconv.Atoi(rawCount[1:])
		return BashFileRead{Tool: "tail", Path: filePath, StartLine: n}, err == nil && n > 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(rawCount, "-"))
	return BashFileRead{Tool: "tail", Path: filePath, StartLine: -n, TailLines: n}, err == nil && n > 0
}

func parseBashSed(args []string) (BashFileRead, bool) {
	quiet, scripts, pos, ok := parseSedArgs(args, 1)
	if !ok || !quiet || len(scripts) != 1 || len(pos) != 1 || pos[0] == "-" {
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
	startStr, endStr, hasRange := strings.Cut(strings.TrimSuffix(script, "p"), ",")
	start, err := strconv.Atoi(strings.TrimSpace(startStr))
	if err != nil || start <= 0 {
		return 0, 0, false
	}
	if !hasRange {
		return start, start, true
	}
	if strings.TrimSpace(endStr) == "$" {
		return start, 0, true
	}
	end, err := strconv.Atoi(strings.TrimSpace(endStr))
	return start, end, err == nil && end >= start
}
