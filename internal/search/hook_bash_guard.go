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
	needPromptGuard := false
	for _, seg := range splitShellSegments(cmd) {
		cmdTok, rest := unwrapSegmentCommand(tokenizeShellSegment(seg))
		if cmdTok == "" {
			continue
		}
		if reason := checkInteractiveSegment(cmdTok, rest); reason != "" {
			return reason, nil
		}
		if !needPromptGuard && cmdTok == "git" {
			sub, _ := skipGitGlobalFlags(rest)
			needPromptGuard = slices.Contains([]string{"fetch", "pull", "push", "clone", "submodule", "ls-remote"}, sub)
		}
	}
	if needPromptGuard && !strings.Contains(cmd, "GIT_TERMINAL_PROMPT=") {
		cloned := maps.Clone(args)
		cloned[key] = "export GIT_TERMINAL_PROMPT=0; " + cmd
		return "", cloned
	}
	return "", nil
}

func splitShellSegments(cmd string) []string {
	var out, heredocs []string
	var cur strings.Builder
	var q byte
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if q != 0 {
			i, q = stepQuotedRune(cmd, i, q, &cur)
			continue
		}
		if ch == '\\' && i+1 < len(cmd) {
			cur.WriteString(cmd[i : i+2])
			i++
			continue
		}
		if ch == '\'' || ch == '"' {
			q = ch
			cur.WriteByte(ch)
		} else if ch == '#' && (i == 0 || strings.ContainsRune(" \t\n\r;|&", rune(cmd[i-1]))) {
			for i+1 < len(cmd) && cmd[i+1] != '\n' {
				i++
			}
		} else if ch == '<' && i+1 < len(cmd) && cmd[i+1] == '<' && (i+2 >= len(cmd) || cmd[i+2] != '<') {
			delim, next := parseHeredocDelim(cmd, i+2)
			if delim != "" {
				heredocs = append(heredocs, delim)
			}
			cur.WriteString(cmd[i:next])
			i = next - 1
		} else if next, sep := matchShellSeparator(cmd, i); sep {
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
			if ch == '\n' && len(heredocs) > 0 {
				next, heredocs = skipHeredocBodies(cmd, next, heredocs), nil
			}
			i = next - 1
		} else {
			cur.WriteByte(ch)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func stepQuotedRune(cmd string, i int, q byte, cur *strings.Builder) (int, byte) {
	cur.WriteByte(cmd[i])
	if q == '"' && cmd[i] == '\\' && i+1 < len(cmd) {
		cur.WriteByte(cmd[i+1])
		return i + 1, q
	}
	if cmd[i] == q {
		return i, 0
	}
	return i, q
}

func matchShellSeparator(cmd string, i int) (int, bool) {
	c := cmd[i]
	if c == '\n' || c == ';' {
		return i + 1, true
	}
	if c != '|' && (c != '&' || (i > 0 && (cmd[i-1] == '>' || cmd[i-1] == '<'))) {
		return i, false
	}
	if i+1 < len(cmd) && cmd[i+1] == c {
		return i + 2, true
	}
	return i + 1, true
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
		case q == '"' && ch == '\\' && i+1 < len(seg):
			i++
			cur.WriteByte(seg[i])
		case q != 0 && ch == q:
			q = 0
		case q != 0:
			cur.WriteByte(ch)
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
			if hasGitShortOrLongFlag(subArgs, "i", "--interactive") {
				return "[INTERACTIVE_STDIN] interactive git rebase (-i/--interactive) blocks on editor input"
			}
		case "add":
			if hasGitShortOrLongFlag(subArgs, "ip", "--interactive", "--patch") {
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
	for _, a := range args {
		if a == "--" {
			return false
		}
		if slices.Contains(longs, a) || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a[1:], shorts)) {
			return true
		}
	}
	return false
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
