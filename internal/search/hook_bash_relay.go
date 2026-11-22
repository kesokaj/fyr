package search

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const maxBashInspectDepth = 6

var gitGlobalValFlags = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--exec-path", "--config-env"}

func inspectBashCommandTreeDepth(cmd string, depth int) (string, bool) {
	if depth > maxBashInspectDepth {
		if isRelaySession() {
			return "[RELAY_GUARD] nested command inspection depth limit exceeded during fyr relay session (fail-closed)", false
		}
		return "", false
	}
	needPromptGuard := false
	exportedPrompt := false
	for _, seg := range splitShellSegments(cmd) {
		toks := tokenizeShellSegment(seg)
		if isExportEnvAssign(toks, "GIT_TERMINAL_PROMPT") {
			exportedPrompt = true
		}
		reason, segPrompt := inspectShellTokens(toks, exportedPrompt, depth)
		if reason != "" {
			return reason, false
		}
		needPromptGuard = needPromptGuard || segPrompt
	}
	for _, sub := range extractSubshellCmds(cmd) {
		if reason, subGuard := inspectBashCommandTreeDepth(sub, depth+1); reason != "" {
			return reason, false
		} else if subGuard && !exportedPrompt {
			needPromptGuard = true
		}
	}
	return "", needPromptGuard
}

func inspectShellTokens(toks []string, exportedPrompt bool, depth int) (string, bool) {
	if reason := checkRelayGitConfigEnv(toks); reason != "" {
		return reason, false
	}
	cmdTok, rest := unwrapSegmentCommand(toks)
	if cmdTok == "" {
		return "", false
	}
	if nested, ok := extractNestedShellCommand(cmdTok, rest); ok {
		return inspectBashCommandTreeDepth(nested, depth+1)
	}
	if cmdTok == "xargs" {
		if subToks := unwrapXargsCommand(rest); len(subToks) > 0 {
			return inspectShellTokens(subToks, exportedPrompt, depth+1)
		}
	}
	if reason := checkRelayBashGuard(cmdTok, rest); reason != "" {
		return reason, false
	}
	if reason := checkInteractiveSegment(cmdTok, rest); reason != "" {
		return reason, false
	}
	if cmdTok == "sudo" {
		if subToks := unwrapSudoCommand(rest); len(subToks) > 0 {
			return inspectShellTokens(subToks, exportedPrompt, depth+1)
		}
	}
	if cmdTok == "git" && needsGitPromptGuard(rest) && !exportedPrompt && !hasLeadingEnvAssign(toks, "GIT_TERMINAL_PROMPT") {
		return "", true
	}
	return "", false
}

func checkRelayGitConfigEnv(toks []string) string {
	return checkRelayGitConfigEnvDepth(toks, 0)
}

func checkRelayGitConfigEnvDepth(toks []string, depth int) string {
	if !isRelaySession() {
		return ""
	}
	if depth > maxBashInspectDepth {
		return "[RELAY_GUARD] nested env -S depth limit exceeded during fyr relay session (fail-closed)"
	}
	for i := 0; i < len(toks); i++ {
		raw := toks[i]
		if splitArg, ok := extractEnvSplitStringArg(toks, &i); ok {
			if reason := checkRelayGitConfigEnvDepth(tokenizeShellSegment(splitArg), depth+1); reason != "" {
				return reason
			}
			continue
		}
		t := strings.TrimLeft(raw, "({")
		eq := strings.IndexByte(t, '=')
		if eq <= 0 {
			continue
		}
		key := t[:eq]
		if key == "GIT_CONFIG_PARAMETERS" || key == "GIT_CONFIG_COUNT" || key == "GIT_CONFIG_GLOBAL" ||
			key == "GIT_CONFIG_SYSTEM" || strings.HasPrefix(key, "GIT_CONFIG_KEY_") {
			return "[RELAY_GUARD] GIT_CONFIG_* env override is forbidden during fyr relay sessions"
		}
	}
	return ""
}

func extractEnvSplitStringArg(toks []string, idx *int) (string, bool) {
	a := toks[*idx]
	if (a == "-S" || a == "--split-string") && *idx+1 < len(toks) {
		*idx++
		return toks[*idx], true
	}
	if rest, ok := strings.CutPrefix(a, "--split-string="); ok {
		return rest, true
	}
	if strings.HasPrefix(a, "-S") && len(a) > 2 {
		return a[2:], true
	}
	return "", false
}

func unwrapPassThroughWrapper(kind string, toks []string) ([]string, bool) {
	switch kind {
	case "nohup", "setsid":
		return skipFlagTokens(toks, nil, []string{"-w", "--wait", "-c", "--ctty", "-f", "--fork"}), true
	case "nice":
		return skipFlagTokens(toks, []string{"-n", "--adjustment"}, nil), true
	case "stdbuf":
		return skipFlagTokens(toks, []string{"-i", "-o", "-e", "--input", "--output", "--error"}, nil), true
	case "ionice":
		return skipFlagTokens(toks, []string{"-c", "-n", "-p", "-P", "-u", "--class", "--classdata", "--pid", "--pgid", "--uid"}, []string{"-t", "--ignore"}), true
	case "timeout":
		rem := skipFlagTokens(toks, []string{"-k", "-s", "--kill-after", "--signal"}, []string{"-v", "--verbose", "--preserve-status", "--foreground"})
		if len(rem) > 1 {
			return rem[1:], true
		}
		return nil, true
	default:
		return nil, false
	}
}

func skipFlagTokens(toks, valFlags, boolFlags []string) []string {
	for i := 0; i < len(toks); i++ {
		a := toks[i]
		if a == "--" {
			return toks[i+1:]
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			return toks[i:]
		}
		if slices.Contains(valFlags, a) {
			i++
			continue
		}
		if slices.Contains(boolFlags, a) || strings.Contains(a, "=") || hasShortValPrefix(a, valFlags) {
			continue
		}
	}
	return nil
}

func hasShortValPrefix(tok string, valFlags []string) bool {
	for _, f := range valFlags {
		if len(f) == 2 && f[0] == '-' && strings.HasPrefix(tok, f) && len(tok) > 2 {
			return true
		}
	}
	return false
}

func unwrapSudoCommand(args []string) []string {
	valLong := []string{"--user", "--group", "--host", "--prompt", "--chdir", "--chroot", "--role", "--type"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return args[i+1:]
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			return args[i:]
		}
		if strings.HasPrefix(a, "--") {
			if slices.Contains(valLong, a) {
				i++
			}
			continue
		}
		if _, skipNext := scanGitShortCluster(a[1:], "", "ughpCTDrtURac", ""); skipNext {
			i++
		}
	}
	return nil
}

func extractNestedShellCommand(cmd string, rest []string) (string, bool) {
	if cmd == "eval" {
		if len(rest) == 0 {
			return "", false
		}
		if rest[0] == "--" {
			rest = rest[1:]
		}
		return strings.Join(rest, " "), len(rest) > 0
	}
	if !slices.Contains([]string{"sh", "bash", "zsh", "dash", "ksh"}, cmd) {
		return "", false
	}
	seenC := false
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if a == "--" || a == "-" {
			if seenC && i+1 < len(rest) {
				return rest[i+1], rest[i+1] != ""
			}
			return "", false
		}
		if a == "" || a == "+" || (a[0] != '-' && a[0] != '+') {
			if seenC {
				return a, a != ""
			}
			return "", false
		}
		if a == "-o" || a == "+o" || a == "--rcfile" || a == "--init-file" {
			i++
			continue
		}
		if a[0] == '-' && !strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], 'c') {
			seenC = true
		}
	}
	return "", false
}

func unwrapXargsCommand(rest []string) []string {
	valFlags := []string{"-I", "-L", "-n", "-P", "-d", "-E", "-s", "-a", "-R", "--replace", "--max-lines", "--max-args", "--max-procs", "--delimiter", "--eof", "--max-chars", "--arg-file"}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if a == "--" {
			return rest[i+1:]
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			return rest[i:]
		}
		if slices.Contains(valFlags, a) {
			i++
		}
	}
	return nil
}

func checkRelayBashGuard(cmd string, args []string) string {
	if os.Getenv("FYR_RELAY") != "1" {
		return ""
	}
	if cmd == "git-send-pack" {
		return "[RELAY_GUARD] git-send-pack is forbidden during fyr relay sessions (keep commits local)"
	}
	if cmd != "git" {
		return ""
	}
	if reason := checkRelayGitConfigAlias(args); reason != "" {
		return reason
	}
	switch sub, subArgs := skipGitGlobalFlags(args); sub {
	case "push":
		return "[RELAY_GUARD] git push is forbidden during fyr relay sessions (keep commits local)"
	case "send-pack":
		return "[RELAY_GUARD] git send-pack is forbidden during fyr relay sessions (keep commits local)"
	case "tag":
		if !isReadOnlyGitTag(subArgs) {
			return "[RELAY_GUARD] git tag mutation is forbidden during fyr relay sessions (only read-only git tag --list/-l is allowed)"
		}
	case "update-ref":
		if targetsGitTagOrStdin(subArgs) {
			return "[RELAY_GUARD] git update-ref on refs/tags/ or --stdin is forbidden during fyr relay sessions"
		}
	}
	return ""
}

func checkRelayGitConfigAlias(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || !strings.HasPrefix(a, "-") || a == "-" {
			return ""
		}
		if a == "-c" && i+1 < len(args) {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(args[i+1])), "alias.") {
				return "[RELAY_GUARD] git -c alias.* is forbidden during fyr relay sessions"
			}
			i++
			continue
		}
		if strings.HasPrefix(a, "-c") && len(a) > 2 && strings.HasPrefix(strings.ToLower(strings.TrimSpace(a[2:])), "alias.") {
			return "[RELAY_GUARD] git -c alias.* is forbidden during fyr relay sessions"
		}
		if a == "--config-env" && i+1 < len(args) {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(args[i+1])), "alias.") {
				return "[RELAY_GUARD] git --config-env alias.* is forbidden during fyr relay sessions"
			}
			i++
			continue
		}
		if strings.HasPrefix(strings.ToLower(a), "--config-env=alias.") {
			return "[RELAY_GUARD] git --config-env alias.* is forbidden during fyr relay sessions"
		}
		if slices.Contains(gitGlobalValFlags, a) && i+1 < len(args) {
			i++
		}
	}
	return ""
}

func targetsGitTagOrStdin(args []string) bool {
	for _, a := range args {
		t := strings.TrimSpace(a)
		if t == "--stdin" || strings.HasPrefix(filepath.ToSlash(t), "refs/tags/") {
			return true
		}
	}
	return false
}
