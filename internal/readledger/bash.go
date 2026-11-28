package readledger

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// BashFileRead describes a single-file read extracted from a shell command string.
type BashFileRead struct {
	Tool, Path                    string
	StartLine, EndLine, TailLines int
}

// AdvanceShellQuote steps one byte inside a single-, double-, or ANSI-C ($'...') quoted shell span.
func AdvanceShellQuote(cmd string, i int, q byte) (int, byte) {
	if (q == '"' || q == '$') && cmd[i] == '\\' && i+1 < len(cmd) {
		return i + 1, q
	}
	if (q == '$' && cmd[i] == '\'') || cmd[i] == q {
		return i, 0
	}
	return i, q
}

// IsEnvAssignToken reports whether tok is a valid POSIX shell VAR=value prefix assignment.
func IsEnvAssignToken(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	for i := 0; eq > 0 && i < eq; i++ {
		if c := tok[i]; (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '_' && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return eq > 0
}

// SkipLineComment advances i to the last byte before the next newline (or end of cmd).
func SkipLineComment(cmd string, i int) int {
	for i+1 < len(cmd) && cmd[i+1] != '\n' {
		i++
	}
	return i
}

// ParseHeredocDelim reads a heredoc delimiter starting immediately after "<<".
func ParseHeredocDelim(cmd string, i int) (string, int) {
	if i < len(cmd) && cmd[i] == '-' {
		i++
	}
	i = skipHorizontalSpace(cmd, i)
	var d strings.Builder
	for q := byte(0); i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case q != 0 && c == q:
			q = 0
		case q != 0:
			d.WriteByte(c)
		case c == '\'' || c == '"':
			q = c
		case c == '\\' && i+1 < len(cmd):
			i++
			d.WriteByte(cmd[i])
		case strings.ContainsRune(" \t\r\n;|&<>()", rune(c)):
			return d.String(), i
		default:
			d.WriteByte(c)
		}
	}
	return d.String(), i
}

// SkipHeredocBodies advances pos past the terminating lines of all pending heredoc delimiters.
func SkipHeredocBodies(cmd string, pos int, delims []string) int {
	for _, delim := range delims {
		for pos < len(cmd) {
			line := cmd[pos:]
			if nl := strings.IndexByte(line, '\n'); nl < 0 {
				pos = len(cmd)
			} else {
				line, pos = line[:nl], pos+nl+1
			}
			if strings.TrimLeft(strings.TrimSuffix(line, "\r"), "\t") == delim {
				break
			}
		}
	}
	return pos
}

// TokenizeShellSegment splits one shell command segment into unquoted word tokens.
func TokenizeShellSegment(seg string) []string {
	var toks []string
	var cur strings.Builder
	inTok := false
	for i, q := 0, byte(0); i < len(seg); i++ {
		if q != 0 {
			i, q = stepShellSegmentQuoted(seg, i, q, &cur)
			continue
		}
		var flush bool
		i, q, inTok, flush = stepShellSegmentUnquoted(seg, i, inTok, &cur)
		if flush {
			toks, inTok = append(toks, cur.String()), false
			cur.Reset()
		}
	}
	if inTok {
		toks = append(toks, cur.String())
	}
	return toks
}

func stepShellSegmentQuoted(seg string, i int, q byte, cur *strings.Builder) (int, byte) {
	ch := seg[i]
	if (q == '"' || q == '$') && ch == '\\' && i+1 < len(seg) {
		cur.WriteByte(seg[i+1])
		return i + 1, q
	}
	if (q == '$' && ch == '\'') || ch == q {
		return i, 0
	}
	cur.WriteByte(ch)
	return i, q
}

func stepShellSegmentUnquoted(seg string, i int, inTok bool, cur *strings.Builder) (int, byte, bool, bool) {
	switch ch := seg[i]; {
	case ch == '$' && i+1 < len(seg) && seg[i+1] == '\'':
		return i + 1, '$', true, false
	case ch == '\'' || ch == '"':
		return i, ch, true, false
	case ch == '\\' && i+1 < len(seg):
		cur.WriteByte(seg[i+1])
		return i + 1, 0, true, false
	case ch == ' ' || ch == '\t' || ch == '\r':
		return i, 0, inTok, inTok
	default:
		cur.WriteByte(ch)
		return i, 0, true, false
	}
}

// ParseBashFileReads extracts file reads from &&, ;, and newline shell chains
// (including benign env prefixes and strictly read-only pipes), tracking cd prefixes.
func ParseBashFileReads(cmd string) []BashFileRead {
	return parseBashFileReadsExtended(cmd)
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
	trimmed := stripLeadingEnvAssigns(toks)
	hasEnv := len(trimmed) < len(toks)
	toks = unwrapBuiltinTokens(trimmed)
	if len(toks) == 0 {
		return false, cwd, unknown
	}
	switch toks[0] {
	case "pushd", "popd":
		return true, "", true
	case "cd":
		nextDir, nextUnknown := resolveTrackedCdTarget(cwd, unknown, hasEnv, toks)
		return true, nextDir, nextUnknown
	}
	return false, cwd, unknown
}

func unwrapBuiltinTokens(toks []string) []string {
	if len(toks) > 1 && (toks[0] == "builtin" || toks[0] == "command") {
		toks = toks[1:]
		if len(toks) > 1 && toks[0] == "--" {
			toks = toks[1:]
		}
	}
	return toks
}

func resolveTrackedCdTarget(cwd string, unknown, hasEnv bool, toks []string) (string, bool) {
	if hasEnv || len(toks) != 2 || toks[1] == "" || strings.HasPrefix(toks[1], "-") || strings.HasPrefix(toks[1], "~") || strings.ContainsRune(toks[1], '$') {
		return "", true
	}
	if filepath.IsAbs(toks[1]) {
		return filepath.ToSlash(filepath.Clean(toks[1])), false
	}
	if unknown {
		return "", true
	}
	return filepath.ToSlash(filepath.Clean(filepath.Join(cwd, toks[1]))), false
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
	return buildUniformBashReads(tool, files, start, end)
}

func buildUniformBashReads(tool string, files []string, start, end int) ([]BashFileRead, bool) {
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

func parseBashHeadOrTailMulti(tool string, args []string) ([]BashFileRead, bool) {
	rawCount := "10"
	var files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			files = append(files, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			var ok bool
			if i, rawCount, ok = stepHeadOrTailCountFlag(args, i, rawCount, tool == "tail"); !ok {
				return nil, false
			}
			continue
		}
		files = append(files, a)
	}
	return buildHeadOrTailReads(tool, files, rawCount)
}

func stepHeadOrTailCountFlag(args []string, i int, rawCount string, isTail bool) (int, string, bool) {
	a := args[i]
	switch {
	case a == "-n" || a == "--lines":
		if i+1 >= len(args) {
			return i, "", false
		}
		return i + 1, args[i+1], true
	case strings.HasPrefix(a, "-n") || strings.HasPrefix(a, "--lines="):
		return i, strings.TrimPrefix(strings.TrimPrefix(a, "-n"), "--lines="), true
	case isTail && (a == "-f" || a == "-F" || strings.HasPrefix(a, "--follow")):
		return i, "", false
	}
	if _, err := strconv.Atoi(a[1:]); err == nil {
		return i, a[1:], true
	}
	if slices.Contains([]string{"-q", "-v", "--quiet", "--silent", "--verbose"}, a) {
		return i, rawCount, true
	}
	return i, "", false
}

func buildHeadOrTailReads(tool string, files []string, rawCount string) ([]BashFileRead, bool) {
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
