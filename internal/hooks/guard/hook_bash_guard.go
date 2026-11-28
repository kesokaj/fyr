package guard

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kesokaj/fyr/internal/readledger"
)

var bashCmdArgKeys = []string{"CommandLine", "commandLine", "command_line", "Command", "command", "cmd"}

// ExtractBashCmdArg returns the key and command string from a shell tool's args map.
func ExtractBashCmdArg(args map[string]any) (string, string) {
	for _, k := range bashCmdArgKeys {
		if s, ok := args[k].(string); ok && strings.TrimSpace(s) != "" {
			return k, s
		}
	}
	return "", ""
}

// CheckInteractiveBashGuard inspects a shell tool's args for interactive/relay hazards or git prompt guards.
func CheckInteractiveBashGuard(args map[string]any) (string, map[string]any) {
	key, cmd := ExtractBashCmdArg(args)
	if key == "" {
		return "", nil
	}
	reason, needPromptGuard := InspectBashCommandTree(cmd)
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

// InspectBashCommandTree analyzes a shell command tree for interactive prompts or relay violations.
func InspectBashCommandTree(cmd string) (string, bool) {
	return inspectBashCommandTreeDepth(cmd, 0)
}

func needsGitPromptGuard(rest []string) bool {
	sub, _ := SkipGitGlobalFlags(rest)
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
		if readledger.IsEnvAssignToken(t) || t == "export" || t == "command" || t == "exec" || t == "time" || t == "env" ||
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

// UnwrapSegmentCommand strips leading env assignments and transparent wrapper commands.
func UnwrapSegmentCommand(toks []string) (string, []string) {
	for len(toks) > 0 {
		t := strings.TrimLeft(toks[0], "({")
		if t == "" || readledger.IsEnvAssignToken(t) || t == "export" {
			toks = toks[1:]
			continue
		}
		base := filepath.Base(t)
		if base == "command" && len(toks) > 1 && (toks[1] == "-v" || toks[1] == "-V") {
			return base, toks[1:]
		}
		if base == "command" || base == "env" || base == "exec" || base == "time" {
			toks = UnwrapEnvOrExec(base, toks[1:])
			continue
		}
		if rem, ok := unwrapPassThroughWrapper(base, toks[1:]); ok {
			toks = rem
			continue
		}
		return base, toks[1:]
	}
	return "", nil
}

// UnwrapEnvOrExec strips flags from env, exec, command, or time wrappers.
func UnwrapEnvOrExec(kind string, toks []string) []string {
	envVal := []string{"-u", "--unset", "-C", "--chdir"}
	for len(toks) > 0 {
		a := toks[0]
		if a == "--" {
			return toks[1:]
		}
		if kind == "env" {
			if expanded, ok := expandEnvSplitArg(toks); ok {
				toks = expanded
				continue
			}
		}
		if len(toks) > 1 && ((kind == "env" && slices.Contains(envVal, a)) || (kind == "exec" && a == "-a")) {
			toks = toks[2:]
			continue
		}
		if isIgnoredEnvExecFlag(kind, a) {
			toks = toks[1:]
			continue
		}
		return toks
	}
	return nil
}

func expandEnvSplitArg(toks []string) ([]string, bool) {
	a := toks[0]
	switch {
	case (a == "-S" || a == "--split-string") && len(toks) > 1:
		return append(TokenizeShellSegment(toks[1]), toks[2:]...), true
	case strings.HasPrefix(a, "--split-string="):
		return append(TokenizeShellSegment(strings.TrimPrefix(a, "--split-string=")), toks[1:]...), true
	case strings.HasPrefix(a, "-S") && len(a) > 2:
		return append(TokenizeShellSegment(a[2:]), toks[1:]...), true
	default:
		return nil, false
	}
}

func isIgnoredEnvExecFlag(kind, a string) bool {
	if readledger.IsEnvAssignToken(a) {
		return true
	}
	switch kind {
	case "command", "time":
		return a == "-p"
	case "exec":
		return a == "-c" || a == "-l"
	case "env":
		envBool := []string{"-i", "-0", "--ignore-environment", "--null"}
		return slices.Contains(envBool, a) || strings.HasPrefix(a, "--unset=") || strings.HasPrefix(a, "--chdir=")
	default:
		return false
	}
}

func checkInteractiveSegment(cmd string, args []string) string {
	switch cmd {
	case "vim", "nvim", "vi", "nano", "less", "more":
		return "[INTERACTIVE_STDIN] interactive editor/pager " + cmd + " blocks on stdin; use non-interactive read/edit tools"
	case "sudo":
		if !hasNonInteractiveSudoFlag(args) {
			return "[INTERACTIVE_STDIN] sudo without -n/--non-interactive may prompt for input"
		}
	case "read":
		if hasReadPromptFlag(args) {
			return "[INTERACTIVE_STDIN] interactive read -p prompts on stdin; pass values non-interactively"
		}
	case "ssh":
		return checkInteractiveSSH(args)
	case "git":
		return checkInteractiveGitSegment(args)
	}
	return ""
}

func checkInteractiveGitSegment(args []string) string {
	switch sub, subArgs := SkipGitGlobalFlags(args); sub {
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
	return ""
}

// SkipGitGlobalFlags skips leading git global flags and returns the subcommand and remaining args.
func SkipGitGlobalFlags(args []string) (string, []string) {
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
		if slices.Contains(exact, a) || hasAnyPrefix(a, prefixes) || hasCommitShortFlag(a) {
			return true
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func hasCommitShortFlag(a string) bool {
	if len(a) <= 1 || a[0] != '-' || a[1] == '-' {
		return false
	}
	for _, r := range a[1:] {
		if strings.ContainsRune("mFCc", r) {
			return true
		}
		if strings.ContainsRune("Stgu", r) {
			return false
		}
	}
	return false
}

func hasGitFlagWithValues(args []string, shorts, valShorts, optShorts string, valLongs []string, longs ...string) bool {
	for i := 0; i < len(args); i++ {
		next, hit, stop := stepGitFlagArg(args, i, shorts, valShorts, optShorts, valLongs, longs)
		if hit {
			return true
		}
		if stop {
			return false
		}
		i = next
	}
	return false
}

func stepGitFlagArg(args []string, i int, shorts, valShorts, optShorts string, valLongs, longs []string) (int, bool, bool) {
	a := args[i]
	switch {
	case a == "--":
		return i, false, true
	case slices.Contains(longs, a):
		return i, true, false
	case strings.HasPrefix(a, "--"):
		if slices.Contains(valLongs, a) && i+1 < len(args) {
			return i + 1, false, false
		}
		return i, false, false
	case len(a) > 1 && a[0] == '-':
		hit, skipNext := scanGitShortCluster(a[1:], shorts, valShorts, optShorts)
		if skipNext && i+1 < len(args) {
			i++
		}
		return i, hit, false
	default:
		return i, false, false
	}
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
	valLong := []string{"--user", "--group", "--host", "--prompt", "--chdir", "--chroot", "--role", "--type"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--" || !strings.HasPrefix(a, "-") || a == "-":
			return false
		case a == "-n" || a == "--non-interactive":
			return true
		case slices.Contains(valLong, a):
			i++
		case strings.HasPrefix(a, "--"):
		default:
			hit, skipNext := scanGitShortCluster(a[1:], "n", "ughpCTDrtURac", "")
			if hit {
				return true
			}
			if skipNext {
				i++
			}
		}
	}
	return false
}

func isReadOnlyGitTag(args []string) bool {
	if len(args) == 0 {
		return true
	}
	hasList := false
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "-l" || a == "--list" || strings.HasPrefix(a, "--list=") {
			hasList = true
			continue
		}
		if isMutatingGitTagFlag(a) {
			return false
		}
	}
	return hasList
}

func isMutatingGitTagFlag(a string) bool {
	mutLong := []string{"--annotate", "--sign", "--local-user", "--force", "--delete", "--message", "--file", "--edit", "--cleanup", "--create-reflog"}
	for _, p := range mutLong {
		if a == p || strings.HasPrefix(a, p+"=") {
			return true
		}
	}
	return len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], "asufdmFe")
}

func hasReadPromptFlag(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || !strings.HasPrefix(a, "-") || a == "-" {
			return false
		}
		if strings.HasPrefix(a, "--") {
			continue
		}
		hit, skipNext := scanGitShortCluster(a[1:], "p", "dinNtua", "")
		if hit {
			return true
		}
		if skipNext {
			i++
		}
	}
	return false
}

func checkInteractiveSSH(args []string) string {
	const sshValFlags = "BbcDEeFIiJLlmOoPpQRSWw"
	batchMode, batchDisabled, forceTTY, nonConn := false, false, false, false
	posIdx := len(args)
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			posIdx = i + 1
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			posIdx = i
			break
		}
		if strings.HasPrefix(a, "--") {
			continue
		}
		i, batchMode, batchDisabled, forceTTY, nonConn = stepSSHShortFlags(args, i, sshValFlags, batchMode, batchDisabled, forceTTY, nonConn)
	}
	if nonConn {
		return ""
	}
	if forceTTY || len(args)-posIdx < 2 || !batchMode || batchDisabled {
		return "[INTERACTIVE_STDIN] interactive ssh may block on stdin/tty; pass -o BatchMode=yes with a remote command (without -t)"
	}
	return ""
}

func stepSSHShortFlags(args []string, i int, valFlags string, batchMode, batchDisabled, forceTTY, nonConn bool) (int, bool, bool, bool, bool) {
	cl := args[i][1:]
	for j := 0; j < len(cl); j++ {
		c := cl[j]
		switch c {
		case 't':
			forceTTY = true
		case 'V', 'G', 'Q', 'O':
			nonConn = true
		}
		if strings.IndexByte(valFlags, c) >= 0 {
			i, batchMode, batchDisabled = applySSHOptFlag(args, i, c, cl[j+1:], batchMode, batchDisabled)
			return i, batchMode, batchDisabled, forceTTY, nonConn
		}
	}
	return i, batchMode, batchDisabled, forceTTY, nonConn
}

func applySSHOptFlag(args []string, i int, c byte, tail string, batchMode, batchDisabled bool) (int, bool, bool) {
	val := tail
	if val == "" && i+1 < len(args) {
		i++
		val = args[i]
	}
	if c != 'o' {
		return i, batchMode, batchDisabled
	}
	if isSSHBatchModeValue(val, true) {
		return i, true, batchDisabled
	}
	if isSSHBatchModeValue(val, false) {
		return i, batchMode, true
	}
	return i, batchMode, batchDisabled
}

func isSSHBatchModeValue(raw string, wantYes bool) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	var key, val string
	if k, v, ok := strings.Cut(s, "="); ok {
		key, val = strings.TrimSpace(k), strings.TrimSpace(v)
	} else if f := strings.Fields(s); len(f) == 2 {
		key, val = f[0], f[1]
	} else {
		return false
	}
	if key != "batchmode" {
		return false
	}
	if wantYes {
		return val == "yes" || val == "true"
	}
	return val == "no" || val == "false"
}
