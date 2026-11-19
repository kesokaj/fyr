package search

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

var bashCmdArgKeys = []string{"CommandLine", "commandLine", "command_line", "Command", "command", "cmd"}

func extractBashCmdArg(args map[string]any) (string, string) {
	for _, k := range bashCmdArgKeys {
		if s, ok := args[k].(string); ok && strings.TrimSpace(s) != "" {
			return k, s
		}
	}
	return "", ""
}

func checkInteractiveBashGuard(args map[string]any) (string, map[string]any) {
	key, cmd := extractBashCmdArg(args)
	if key == "" {
		return "", nil
	}
	reason, needPromptGuard := inspectBashCommandTree(cmd)
	if reason != "" {
		return reason, nil
	}
	if needPromptGuard {
		cloned := maps.Clone(args)
		cloned[key] = "export GIT_TERMINAL_PROMPT=0; " + cmd
		return "", cloned
	}
	return "", nil
}

func inspectBashCommandTree(cmd string) (string, bool) {
	needPromptGuard := false
	exportedPrompt := false
	for _, seg := range splitShellSegments(cmd) {
		toks := tokenizeShellSegment(seg)
		if isExportEnvAssign(toks, "GIT_TERMINAL_PROMPT") {
			exportedPrompt = true
		}
		cmdTok, rest := unwrapSegmentCommand(toks)
		if cmdTok == "" {
			continue
		}
		if reason := checkInteractiveSegment(cmdTok, rest); reason != "" {
			return reason, false
		}
		if cmdTok == "git" && needsGitPromptGuard(rest) && !exportedPrompt && !hasLeadingEnvAssign(toks, "GIT_TERMINAL_PROMPT") {
			needPromptGuard = true
		}
	}
	for _, sub := range extractSubshellCmds(cmd) {
		if reason, subGuard := inspectBashCommandTree(sub); reason != "" {
			return reason, false
		} else if subGuard && !exportedPrompt {
			needPromptGuard = true
		}
	}
	return "", needPromptGuard
}

func needsGitPromptGuard(rest []string) bool {
	sub, _ := skipGitGlobalFlags(rest)
	return slices.Contains([]string{"fetch", "pull", "push", "clone", "submodule", "ls-remote"}, sub)
}

func isExportEnvAssign(toks []string, name string) bool {
	if len(toks) < 2 || toks[0] != "export" {
		return false
	}
	prefix := name + "="
	for _, t := range toks[1:] {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

func hasLeadingEnvAssign(toks []string, name string) bool {
	prefix := name + "="
	for i := 0; i < len(toks); i++ {
		t := strings.TrimLeft(toks[i], "({")
		if strings.HasPrefix(t, prefix) {
			return true
		}
		if isEnvAssignToken(t) || t == "export" || t == "command" || t == "exec" || t == "time" || t == "env" ||
			t == "-i" || t == "-0" || t == "-p" || t == "--ignore-environment" || t == "--null" ||
			strings.HasPrefix(t, "--unset=") || strings.HasPrefix(t, "--chdir=") {
			continue
		}
		if (t == "-u" || t == "--unset" || t == "-C" || t == "--chdir" || t == "-a") && i+1 < len(toks) {
			i++
			continue
		}
		return false
	}
	return false
}

func splitShellSegments(cmd string) []string {
	var out, heredocs []string
	var cur strings.Builder
	var q byte
	for i := 0; i < len(cmd); i++ {
		if q != 0 {
			i, q = stepQuotedAtom(cmd, i, q, &cur)
			continue
		}
		var flush bool
		i, q, heredocs, flush = stepUnquotedSplitChar(cmd, i, heredocs, &cur)
		if flush {
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func stepUnquotedSplitChar(cmd string, i int, heredocs []string, cur *strings.Builder) (int, byte, []string, bool) {
	ch := cmd[i]
	if ch == '\\' && i+1 < len(cmd) {
		cur.WriteString(cmd[i : i+2])
		return i + 1, 0, heredocs, false
	}
	if next, handled := writeOpaqueAtomOrComment(cmd, i, cur); handled {
		if next == i {
			return skipLineComment(cmd, i), 0, heredocs, false
		}
		return next - 1, 0, heredocs, false
	}
	if ch == '$' && i+1 < len(cmd) && cmd[i+1] == '\'' {
		cur.WriteString("$'")
		return i + 1, '$', heredocs, false
	}
	if ch == '\'' || ch == '"' {
		cur.WriteByte(ch)
		return i, ch, heredocs, false
	}
	if ch == '<' && i+1 < len(cmd) && cmd[i+1] == '<' && (i+2 >= len(cmd) || cmd[i+2] != '<') {
		delim, next := parseHeredocDelim(cmd, i+2)
		if delim != "" {
			heredocs = append(heredocs, delim)
		}
		cur.WriteString(cmd[i:next])
		return next - 1, 0, heredocs, false
	}
	if next, sep := matchShellSeparator(cmd, i); sep {
		if ch == '\n' && len(heredocs) > 0 {
			next, heredocs = skipHeredocBodies(cmd, next, heredocs), nil
		}
		return next - 1, 0, heredocs, true
	}
	cur.WriteByte(ch)
	return i, 0, heredocs, false
}

func stepQuotedAtom(cmd string, i int, q byte, cur *strings.Builder) (int, byte) {
	if q == '"' && cmd[i] == '$' && i+1 < len(cmd) && cmd[i+1] == '(' {
		end := scanBalancedParenAtom(cmd, i+2)
		cur.WriteString(cmd[i:end])
		return end - 1, q
	}
	if q == '"' && cmd[i] == '`' {
		end := scanBacktickAtom(cmd, i+1)
		cur.WriteString(cmd[i:end])
		return end - 1, q
	}
	cur.WriteByte(cmd[i])
	if (q == '"' || q == '$') && cmd[i] == '\\' && i+1 < len(cmd) {
		cur.WriteByte(cmd[i+1])
		return i + 1, q
	}
	if (q == '$' && cmd[i] == '\'') || cmd[i] == q {
		return i, 0
	}
	return i, q
}

func writeOpaqueAtomOrComment(cmd string, i int, cur *strings.Builder) (int, bool) {
	ch := cmd[i]
	if ch == '#' && (i == 0 || strings.ContainsRune(" \t\n\r;|&(", rune(cmd[i-1]))) {
		return i, true
	}
	if (ch == '$' || ch == '<' || ch == '>') && i+1 < len(cmd) && cmd[i+1] == '(' {
		end := scanBalancedParenAtom(cmd, i+2)
		cur.WriteString(cmd[i:end])
		return end, true
	}
	if ch == '`' {
		end := scanBacktickAtom(cmd, i+1)
		cur.WriteString(cmd[i:end])
		return end, true
	}
	return i, false
}

func skipLineComment(cmd string, i int) int {
	for i+1 < len(cmd) && cmd[i+1] != '\n' {
		i++
	}
	return i
}

func matchShellSeparator(cmd string, i int) (int, bool) {
	c := cmd[i]
	if c == '\n' || c == ';' {
		return i + 1, true
	}
	if c == '|' && i+1 < len(cmd) && cmd[i+1] == '&' {
		return i + 2, true
	}
	if c == '&' && i+1 < len(cmd) && cmd[i+1] == '>' {
		return i, false
	}
	if c != '|' && (c != '&' || (i > 0 && (cmd[i-1] == '>' || cmd[i-1] == '<'))) {
		return i, false
	}
	if i+1 < len(cmd) && cmd[i+1] == c {
		return i + 2, true
	}
	return i + 1, true
}

func scanBalancedParenAtom(cmd string, pos int) int {
	depth, q := 1, byte(0)
	for i := pos; i < len(cmd); i++ {
		c := cmd[i]
		if q != 0 {
			i, q = advanceShellQuote(cmd, i, q)
			continue
		}
		if c == '\\' && i+1 < len(cmd) {
			i++
			continue
		}
		if c == '$' && i+1 < len(cmd) && cmd[i+1] == '\'' {
			q = '$'
			i++
			continue
		}
		if c == '\'' || c == '"' {
			q = c
		} else if c == '#' && (i == 0 || strings.ContainsRune(" \t\n\r;|&(", rune(cmd[i-1]))) {
			i = skipLineComment(cmd, i)
		} else if c == '(' {
			depth++
		} else if c == ')' {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(cmd)
}

func scanBacktickAtom(cmd string, pos int) int {
	for i := pos; i < len(cmd); i++ {
		if cmd[i] == '\\' && i+1 < len(cmd) {
			i++
			continue
		}
		if cmd[i] == '`' {
			return i + 1
		}
	}
	return len(cmd)
}

func advanceShellQuote(cmd string, i int, q byte) (int, byte) {
	if (q == '"' || q == '$') && cmd[i] == '\\' && i+1 < len(cmd) {
		return i + 1, q
	}
	if (q == '$' && cmd[i] == '\'') || cmd[i] == q {
		return i, 0
	}
	return i, q
}

func hasShellExpansionOutsideSingleQuotes(cmd string) bool {
	hasExp, _ := scanShellExpansions(cmd)
	return hasExp
}

func extractSubshellCmds(cmd string) []string {
	_, subs := scanShellExpansions(cmd)
	return subs
}

func scanShellExpansions(cmd string) (bool, []string) {
	hasExp := false
	var subs []string
	for i, q := 0, byte(0); i < len(cmd); i++ {
		ch := cmd[i]
		if q == '\'' || q == '$' {
			i, q = advanceShellQuote(cmd, i, q)
			continue
		}
		if q == '"' {
			if next, ok := captureDoubleQuotedSub(cmd, i, &subs); ok {
				hasExp, i = true, next-1
				continue
			}
			i, q = advanceShellQuote(cmd, i, q)
			continue
		}
		if ch == '\\' && i+1 < len(cmd) {
			i++
			continue
		}
		if ch == '$' && i+1 < len(cmd) && cmd[i+1] == '\'' {
			q = '$'
			i++
			continue
		}
		if ch == '\'' || ch == '"' {
			q = ch
			continue
		}
		if ch == '#' && (i == 0 || strings.ContainsRune(" \t\n\r;|&(", rune(cmd[i-1]))) {
			i = skipLineComment(cmd, i)
			continue
		}
		if next, exp, ok := captureUnquotedSub(cmd, i, &subs); ok {
			hasExp = hasExp || exp
			i = next - 1
		}
	}
	return hasExp, subs
}

func captureDoubleQuotedSub(cmd string, i int, subs *[]string) (int, bool) {
	if cmd[i] == '$' && i+1 < len(cmd) && cmd[i+1] == '(' {
		end := scanBalancedParenAtom(cmd, i+2)
		appendInnerBody(cmd, i+2, end, ')', subs)
		return end, true
	}
	if cmd[i] == '`' {
		end := scanBacktickAtom(cmd, i+1)
		appendInnerBody(cmd, i+1, end, '`', subs)
		return end, true
	}
	return i, false
}

func captureUnquotedSub(cmd string, i int, subs *[]string) (int, bool, bool) {
	ch := cmd[i]
	if (ch == '$' || ch == '<' || ch == '>') && i+1 < len(cmd) && cmd[i+1] == '(' {
		end := scanBalancedParenAtom(cmd, i+2)
		appendInnerBody(cmd, i+2, end, ')', subs)
		return end, true, true
	}
	if ch == '`' {
		end := scanBacktickAtom(cmd, i+1)
		appendInnerBody(cmd, i+1, end, '`', subs)
		return end, true, true
	}
	if ch == '(' && (i == 0 || cmd[i-1] != '=') {
		end := scanBalancedParenAtom(cmd, i+1)
		appendInnerBody(cmd, i+1, end, ')', subs)
		return end, false, true
	}
	return i, false, false
}

func appendInnerBody(cmd string, start, end int, closeCh byte, subs *[]string) {
	bodyEnd := end
	if bodyEnd > start && cmd[bodyEnd-1] == closeCh {
		bodyEnd--
	}
	if bodyEnd > start {
		*subs = append(*subs, cmd[start:bodyEnd])
	}
}

func parseHeredocDelim(cmd string, i int) (string, int) {
	if i < len(cmd) && cmd[i] == '-' {
		i++
	}
	for i < len(cmd) && (cmd[i] == ' ' || cmd[i] == '\t') {
		i++
	}
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

func skipHeredocBodies(cmd string, pos int, delims []string) int {
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

func tokenizeShellSegment(seg string) []string {
	var toks []string
	var cur strings.Builder
	inTok := false
	for i, q := 0, byte(0); i < len(seg); i++ {
		switch ch := seg[i]; {
		case (q == '"' || q == '$') && ch == '\\' && i+1 < len(seg):
			i++
			cur.WriteByte(seg[i])
		case q == '$' && ch == '\'':
			q = 0
		case q != 0 && ch == q:
			q = 0
		case q != 0:
			cur.WriteByte(ch)
		case ch == '$' && i+1 < len(seg) && seg[i+1] == '\'':
			q, inTok = '$', true
			i++
		case ch == '\'' || ch == '"':
			q, inTok = ch, true
		case ch == '\\' && i+1 < len(seg):
			i++
			cur.WriteByte(seg[i])
			inTok = true
		case ch == ' ' || ch == '\t' || ch == '\r':
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

func unwrapSegmentCommand(toks []string) (string, []string) {
	for len(toks) > 0 {
		t := strings.TrimLeft(toks[0], "({")
		if t == "" || isEnvAssignToken(t) || t == "export" {
			toks = toks[1:]
			continue
		}
		base := filepath.Base(t)
		if base == "command" && len(toks) > 1 && (toks[1] == "-v" || toks[1] == "-V") {
			return base, toks[1:]
		}
		if base == "command" || base == "env" || base == "exec" || base == "time" {
			toks = unwrapEnvOrExec(base, toks[1:])
			continue
		}
		return base, toks[1:]
	}
	return "", nil
}

func unwrapEnvOrExec(kind string, toks []string) []string {
	envVal := []string{"-u", "--unset", "-C", "--chdir"}
	envBool := []string{"-i", "-0", "--ignore-environment", "--null"}
	for len(toks) > 0 {
		switch a := toks[0]; {
		case a == "--":
			return toks[1:]
		case len(toks) > 1 && ((kind == "env" && slices.Contains(envVal, a)) || (kind == "exec" && a == "-a")):
			toks = toks[2:]
		case isEnvAssignToken(a) || ((kind == "command" || kind == "time") && a == "-p") ||
			(kind == "env" && (slices.Contains(envBool, a) || strings.HasPrefix(a, "--unset=") || strings.HasPrefix(a, "--chdir="))) ||
			(kind == "exec" && (a == "-c" || a == "-l")):
			toks = toks[1:]
		default:
			return toks
		}
	}
	return nil
}

func isEnvAssignToken(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	for i := 0; eq > 0 && i < eq; i++ {
		if c := tok[i]; !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (i > 0 && c >= '0' && c <= '9')) {
			return false
		}
	}
	return eq > 0
}

func checkInteractiveSegment(cmd string, args []string) string {
	switch cmd {
	case "vim", "nvim", "vi", "nano", "less", "more":
		return "[INTERACTIVE_STDIN] interactive editor/pager " + cmd + " blocks on stdin; use non-interactive read/edit tools"
	case "sudo":
		if !hasNonInteractiveSudoFlag(args) {
			return "[INTERACTIVE_STDIN] sudo without -n/--non-interactive may prompt for input"
		}
	case "git":
		switch sub, subArgs := skipGitGlobalFlags(args); sub {
		case "commit":
			if !hasNonInteractiveCommitFlag(subArgs) {
				return "[INTERACTIVE_STDIN] git commit without -m/-F/--file/--message/--reuse-message/-C/--reedit-message/-c/--no-edit opens an editor"
			}
		case "rebase":
			if hasGitFlagWithValues(subArgs, "i", "XsxC", "S", []string{"--onto", "--strategy", "--strategy-option", "--exec", "--whitespace", "--empty"}, "--interactive") {
				return "[INTERACTIVE_STDIN] interactive git rebase (-i/--interactive) blocks on editor input"
			}
		case "add":
			if hasGitFlagWithValues(subArgs, "ip", "", "", []string{"--chmod", "--pathspec-from-file"}, "--interactive", "--patch") {
				return "[INTERACTIVE_STDIN] interactive git add (-i/--interactive/-p/--patch) blocks on stdin"
			}
		}
	}
	return ""
}

func skipGitGlobalFlags(args []string) (string, []string) {
	valFlags := []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--exec-path", "--config-env"}
	for i := 0; i < len(args); i++ {
		if a := args[i]; !strings.HasPrefix(a, "-") || a == "-" {
			return a, args[i+1:]
		} else if a == "--" && i+1 < len(args) {
			return args[i+1], args[i+2:]
		} else if slices.Contains(valFlags, a) && i+1 < len(args) {
			i++
		}
	}
	return "", nil
}

func hasNonInteractiveCommitFlag(args []string) bool {
	exact := []string{"-m", "-F", "-C", "-c", "--file", "--message", "--reuse-message", "--reedit-message", "--no-edit"}
	prefixes := []string{"--file=", "--message=", "--reuse-message=", "--reedit-message="}
	for _, a := range args {
		if a == "--" {
			return false
		}
		if slices.Contains(exact, a) || slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(a, p) }) {
			return true
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' {
			for _, r := range a[1:] {
				if strings.ContainsRune("mFCc", r) {
					return true
				}
				if strings.ContainsRune("Stgu", r) {
					break
				}
			}
		}
	}
	return false
}

func hasGitShortOrLongFlag(args []string, shorts string, longs ...string) bool {
	return hasGitFlagWithValues(args, shorts, "XsxC", "S", []string{"--onto", "--strategy", "--strategy-option", "--exec", "--whitespace", "--empty", "--chmod", "--pathspec-from-file"}, longs...)
}

func hasGitFlagWithValues(args []string, shorts, valShorts, optShorts string, valLongs []string, longs ...string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return false
		}
		if slices.Contains(longs, a) {
			return true
		}
		if strings.HasPrefix(a, "--") {
			if slices.Contains(valLongs, a) && i+1 < len(args) {
				i++
			}
			continue
		}
		if len(a) > 1 && a[0] == '-' {
			hit, skipNext := scanGitShortCluster(a[1:], shorts, valShorts, optShorts)
			if hit {
				return true
			}
			if skipNext && i+1 < len(args) {
				i++
			}
		}
	}
	return false
}

func scanGitShortCluster(cluster, shorts, valShorts, optShorts string) (bool, bool) {
	for j := 0; j < len(cluster); j++ {
		r := rune(cluster[j])
		if strings.ContainsRune(shorts, r) {
			return true, false
		}
		if strings.ContainsRune(valShorts, r) {
			return false, j == len(cluster)-1
		}
		if strings.ContainsRune(optShorts, r) {
			return false, false
		}
	}
	return false, false
}

func hasNonInteractiveSudoFlag(args []string) bool {
	valLong := []string{"--user", "--group", "--host", "--prompt", "--chdir", "--role", "--type"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || !strings.HasPrefix(a, "-") || a == "-" {
			return false
		}
		if a == "-n" || a == "--non-interactive" || (!strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], 'n')) {
			return true
		}
		if slices.Contains(valLong, a) || (!strings.HasPrefix(a, "--") && strings.ContainsRune("ughpCTDrtU", rune(a[len(a)-1]))) {
			i++
		}
	}
	return false
}
