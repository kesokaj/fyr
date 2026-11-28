package ingest

import (
	"path"
	"regexp"
	"strings"
	"sync"
)

var (
	subcommandsOnce sync.Once
	subcommandsMap  map[string]map[string]bool
)

var subcommandSpecs = map[string]string{
	"git":       "add am apply archive bisect blame branch bundle cat-file check-ignore checkout cherry-pick clean clone commit config describe diff fetch for-each-ref format-patch fsck gc grep hash-object init log ls-files ls-remote ls-tree merge merge-base mv notes pull push range-diff rebase reflog remote reset restore rev-list rev-parse revert rm shortlog show sparse-checkout stash status submodule switch tag update-index worktree",
	"go":        "build clean doc env fix fmt generate get install list mod run test tool version vet work",
	"gh":        "api auth browse gist issue label pr release repo run search secret status workflow",
	"docker":    "build buildx compose container exec image images inspect login logout logs network ps pull push rm rmi run start stop system tag volume",
	"gcloud":    "alpha artifacts auth beta builds components compute config container iam logging projects run secrets services sql",
	"kubectl":   "annotate apply config create delete describe edit exec get label logs patch port-forward rollout scale top",
	"npm":       "audit ci exec init install ls pack publish run start test uninstall update version",
	"npx":       "",
	"terraform": "apply console destroy fmt graph import init output plan providers show state validate workspace",
	"fyr":       "adopt ast auto check close diff dream eval gate git-check graph help hook index install land list memory metrics models record relay render report resume search state status sync uninstall update verify version vet watch work",
	// The binary's name before the rename: transcripts and stored rows carry
	// `sh-agent search` and its kin (fyr D7, D20).
	"sh-agent": "adopt diff dream help index install list memory metrics render report search status sync uninstall update version",
}

func subcommandSet(prog string) (map[string]bool, bool) {
	subcommandsOnce.Do(initSubcommands)
	s, ok := subcommandsMap[prog]
	return s, ok
}

func initSubcommands() {
	m := make(map[string]map[string]bool, len(subcommandSpecs))
	for prog, words := range subcommandSpecs {
		set := map[string]bool{}
		for _, w := range strings.Fields(words) {
			set[w] = true
		}
		m[prog] = set
	}
	subcommandsMap = m
}

var gitGlobalArg = map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true, "--exec-path": true, "--config-env": true}

func skipGitGlobals(rest []string) []string {
	for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
		name, _, hasValue := strings.Cut(rest[0], "=")
		rest = rest[1:]
		if gitGlobalArg[name] && !hasValue && len(rest) > 0 {
			rest = rest[1:]
		}
	}
	return rest
}

var shAgentSub = map[string]bool{"ingest": true, "show": true, "report": true, "evals": true, "trajectory": true, "export": true, "import": true, "add": true}
var goStyle = map[string]bool{"go": true, "gofmt": true, "govulncheck": true, "find": true}
var dropped = map[string]bool{"sudo": true, "env": true, "command": true, "time": true, "nohup": true, "exec": true}
var wrapperArg = map[string]bool{"-u": true, "--unset": true, "-g": true, "-C": true, "--chdir": true}

var (
	keywordsLead = map[string]bool{"if": true, "then": true, "else": true, "elif": true, "do": true, "while": true, "until": true, "!": true}
	keywordsEnd  = map[string]bool{"done": true, "fi": true, "esac": true, "in": true}
	keywordsSkip = map[string]bool{"for": true, "case": true, "select": true, "function": true}
)

var (
	programName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.+-]{0,31}$`)
	longFlag        = regexp.MustCompile(`^--[a-z0-9][a-z0-9-]{0,23}$`)
	goFlag          = regexp.MustCompile(`^[a-z][a-z0-9]{0,23}$`)
	letters         = regexp.MustCompile(`^[A-Za-z]+$`)
	redirection     = regexp.MustCompile(`^(?:[0-9]*|&)[<>]`)
	redirectionOnly = regexp.MustCompile(`^(?:[0-9]*|&)(?:>>?|<<?|>&|<&)$`)
)

// Verb reduces a shell command line to its shape: one string per command in
// it, "program [subcommand] [flag names…]". No positional word, no flag value
// and no comment is kept, except a bare "." after `git add` (D22).
func Verb(cmd string) []string {
	var out []string
	for _, seg := range segments(StripHeredocs(cmd)) {
		if v := segmentVerb(seg); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func segmentVerb(words []string) string {
	if i := programAt(words); i >= 0 {
		return verbFrom(words[i:])
	}
	return ""
}

func programAt(words []string) int {
	i := 0
	for i < len(words) {
		w := words[i]
		switch {
		case keywordsLead[w], dropped[w], isAssignment(w):
			i++
		case redirection.MatchString(w):
			i++
			if redirectionOnly.MatchString(w) && i < len(words) {
				i++
			}
		case strings.HasPrefix(w, "-") && i > 0 && wrapper(words[:i]):
			i++
			if wrapperArg[w] && i < len(words) {
				i++
			}
		default:
			return i
		}
	}
	return -1
}

func verbFrom(words []string) string {
	if keywordsEnd[words[0]] || keywordsSkip[words[0]] {
		return ""
	}
	prog, rest := path.Base(words[0]), words[1:]
	if p, r, direct, ok := resolveGoRun(prog, rest); ok {
		if direct != "" {
			return direct
		}
		prog, rest = p, r
	}
	if !programName.MatchString(prog) {
		return ""
	}
	if prog == "git" {
		rest = skipGitGlobals(rest)
	}
	parts, rest := takeSubcommands(prog, rest)
	parts = appendVerbFlags(parts, prog, rest)
	return strings.Join(parts, " ")
}

func resolveGoRun(prog string, rest []string) (nextProg string, nextRest []string, direct string, handled bool) {
	if prog != "go" || len(rest) < 2 || rest[0] != "run" {
		return prog, rest, "", false
	}
	target := path.Base(strings.TrimSuffix(rest[1], "/"))
	if _, ok := subcommandSet(target); ok {
		return target, rest[2:], "", true
	}
	parts := appendVerbFlags([]string{"go", "run"}, "go", rest[2:])
	return "", nil, strings.Join(parts, " "), true
}

func takeSubcommands(prog string, rest []string) ([]string, []string) {
	parts := []string{prog}
	if subs, ok := subcommandSet(prog); ok && len(rest) > 0 && subs[rest[0]] {
		parts = append(parts, rest[0])
		rest = rest[1:]
	}
	if (prog == "fyr" || prog == "sh-agent") && len(parts) == 2 && len(rest) > 0 && shAgentSub[rest[0]] {
		parts = append(parts, rest[0])
		rest = rest[1:]
	}
	return parts, rest
}

func appendVerbFlags(parts []string, prog string, rest []string) []string {
	gitAdd := prog == "git" && len(parts) == 2 && parts[1] == "add"
	for _, w := range rest {
		if f := flagName(prog, w); f != "" {
			parts = append(parts, f)
		} else if gitAdd && w == "." {
			parts = append(parts, ".")
		}
	}
	return parts
}

func flagName(prog, w string) string {
	if strings.HasPrefix(w, "--") {
		name, _, _ := strings.Cut(w, "=")
		if longFlag.MatchString(name) {
			return name
		}
		return ""
	}
	if len(w) < 2 || w[0] != '-' {
		return ""
	}
	name, _, hasValue := strings.Cut(w[1:], "=")
	switch {
	case goStyle[prog]:
		if goFlag.MatchString(name) {
			return "-" + name
		}
		return ""
	case !letters.MatchString(w[1:2]):
		return ""
	case !hasValue && len(w) <= 4 && letters.MatchString(w[1:]):
		return w
	}
	return w[:2]
}

func wrapper(before []string) bool {
	for _, w := range before {
		if !dropped[w] && !strings.HasPrefix(w, "-") && !isAssignment(w) {
			return false
		}
	}
	return true
}

func isAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}
