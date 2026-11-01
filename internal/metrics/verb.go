package metrics

import (
	"path"
	"regexp"
	"strings"
)

// subcommands are, per tool, the subcommands kept after the program: an
// allowlist, because a word in that position can be a short secret
// (`gh hunter2pass`); an unlisted word is dropped and the verb is the program
// alone (operator, 2026-10-09). npx is listed with none: its package is never
// kept.
var subcommands = map[string]map[string]bool{}

func init() {
	for prog, words := range map[string]string{
		"git":       "add am apply archive bisect blame branch bundle cat-file check-ignore checkout cherry-pick clean clone commit config describe diff fetch for-each-ref format-patch fsck gc grep hash-object init log ls-files ls-remote ls-tree merge merge-base mv notes pull push range-diff rebase reflog remote reset restore rev-list rev-parse revert rm shortlog show sparse-checkout stash status submodule switch tag update-index worktree",
		"go":        "build clean doc env fix fmt generate get install list mod run test tool version vet work",
		"gh":        "api auth browse gist issue label pr release repo run search secret status workflow",
		"docker":    "build buildx compose container exec image images inspect login logout logs network ps pull push rm rmi run start stop system tag volume",
		"gcloud":    "alpha artifacts auth beta builds components compute config container iam logging projects run secrets services sql",
		"kubectl":   "annotate apply config create delete describe edit exec get label logs patch port-forward rollout scale top",
		"npm":       "audit ci exec init install ls pack publish run start test uninstall update version",
		"npx":       "",
		"terraform": "apply console destroy fmt graph import init output plan providers show state validate workspace",
		"fyr":       "adopt auto check diff dream eval help index install list memory metrics record render search serve status sync uninstall update version work",
		// The binary's name before the rename: transcripts and stored rows
		// from before it carry `sh-agent search` and its kin (fyr D7, D20).
		"sh-agent": "adopt diff dream help index install list memory metrics render report search status sync uninstall update version",
	} {
		set := map[string]bool{}
		for _, w := range strings.Fields(words) {
			set[w] = true
		}
		subcommands[prog] = set
	}
}

// gitGlobalArg are git's global options that take the next word as their
// value (`git -C dir`, `git -c k=v`); the others are single words.
var gitGlobalArg = map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true, "--exec-path": true, "--config-env": true}

// skipGitGlobals drops git's global options and their values from the front
// of its arguments, so the subcommand behind them is found (`git -C dir add
// -A` is `git add -A`).
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

// shAgentSub are fyr's second-level verbs, and sh-agent's before the rename, kept after the subcommand.
var shAgentSub = map[string]bool{"ingest": true, "show": true, "report": true, "evals": true, "export": true, "add": true}

// goStyle programs take long flags behind a single dash (`go test -race
// -count=1`) and never glue a value to a flag, so the whole name is kept.
var goStyle = map[string]bool{"go": true, "gofmt": true, "govulncheck": true, "find": true}

// dropped are wrapper words skipped before the program.
var dropped = map[string]bool{"sudo": true, "env": true, "command": true, "time": true, "nohup": true, "exec": true}

// wrapperArg are wrapper flags that take a value (`env -u NAME`,
// `sudo -u USER`, `env -C DIR`): the value is skipped with the flag.
var wrapperArg = map[string]bool{"-u": true, "--unset": true, "-g": true, "-C": true, "--chdir": true}

// keywordsLead open a compound command and are followed by one (`do git
// add`); keywordsEnd close one and carry none; keywordsSkip open a segment
// that is not a command (`for f in a b`).
var (
	keywordsLead = map[string]bool{"if": true, "then": true, "else": true, "elif": true, "do": true, "while": true, "until": true, "!": true}
	keywordsEnd  = map[string]bool{"done": true, "fi": true, "esac": true, "in": true}
	keywordsSkip = map[string]bool{"for": true, "case": true, "select": true, "function": true}
)

// programName is what a command name looks like. Anything else in command
// position (a glob, an expansion, a quoted path) is content and yields no
// verb.
var (
	// Names and flags are capped well below a token's length: a word that
	// long is content, not a command's name (security review of v2.6.0).
	programName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.+-]{0,31}$`)
	// A long flag is lowercase letters, digits and dashes, at most 24
	// characters after its dashes: `--sk_live_…` or `--Authorization` is
	// content (operator, 2026-10-09).
	longFlag = regexp.MustCompile(`^--[a-z0-9][a-z0-9-]{0,23}$`)
	goFlag   = regexp.MustCompile(`^[a-z][a-z0-9]{0,23}$`)
	letters  = regexp.MustCompile(`^[A-Za-z]+$`)
	// redirection is a word that opens with one (`>f`, `2>>log`, `<in`,
	// `&>f`); redirectionOnly is one whose target is the next word.
	redirection     = regexp.MustCompile(`^(?:[0-9]*|&)[<>]`)
	redirectionOnly = regexp.MustCompile(`^(?:[0-9]*|&)(?:>>?|<<?|>&|<&)$`)
)

// Verb reduces a shell command line to its shape: one string per command in
// it, "program [subcommand] [flag names…]". No positional word, no flag value
// and no comment is kept, except a bare "." after `git add` (D22). Content in
// the shell constructs the tokenizer models — quotes, here-documents, ${…},
// $((…)), case patterns, comments — never reaches a verb; a word in command
// position must look like a command name. One residue: an unquoted value
// shaped like a flag (`--title --x`) reads as a flag, since telling them
// apart would take a flag table per program.
func Verb(cmd string) []string {
	var out []string
	for _, seg := range segments(stripHeredocs(cmd)) {
		if v := segmentVerb(seg); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// tokenizer splits a command line into segments of words.
type tokenizer struct {
	segs        [][]string
	words       []string
	cur         strings.Builder
	inWord      bool
	quoted      bool // the current word holds a quoted or escaped rune; see markQuoted
	glued       bool // the word is a short flag and its quoted value; see markQuoted
	parenDepth  int  // open ( and $( subshells
	caseDepth   int  // open case … esac
	wantPattern bool // inside a case, before the next pattern's )
}

func (t *tokenizer) flushWord() {
	if !t.inWord {
		return
	}
	w := t.cur.String()
	t.cur.Reset()
	t.inWord = false
	switch {
	case t.glued:
		w = w[:2]
	case t.quoted && strings.HasPrefix(w, "-"):
		w = quotedMark + w
	}
	t.quoted, t.glued = false, false
	if len(t.words) == 0 {
		switch w {
		case "case":
			t.caseDepth++
		case "esac":
			if t.caseDepth > 0 {
				t.caseDepth--
			}
			t.wantPattern = false
		}
	}
	t.words = append(t.words, w)
	if w == "in" && len(t.words) >= 3 && t.words[0] == "case" {
		t.words = nil // the case header carries no command
		t.wantPattern = true
	}
}

func (t *tokenizer) flushSeg() {
	t.flushWord()
	if len(t.words) > 0 && !t.wantPattern {
		t.segs = append(t.segs, t.words)
	}
	t.words = nil
}

// segments splits cmd at ; && || | & newlines and subshell parentheses,
// outside quotes and expansions, and returns each segment's words.
func segments(cmd string) [][]string {
	t := &tokenizer{}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		// The three scans keep the order of one switch: quotes and escapes
		// first, then expansions, then everything else.
		switch r := rs[i]; {
		case r == '\'' || r == '"' || r == '\\' && i+1 < len(rs):
			i = t.scanQuoted(rs, i)
		case r == '$' && (runeAt(rs, i+1, '{') || runeAt(rs, i+1, '(')):
			i = t.scanDollar(rs, i)
		default:
			i = t.scanOperator(rs, i)
		}
	}
	t.flushSeg()
	return t.segs
}

// swallow appends the balanced open…close run starting at rs[i] to the
// current word, unparsed, and returns the index of its last rune.
func (t *tokenizer) swallow(rs []rune, i int, open, close rune) int {
	t.inWord = true
	t.cur.WriteRune('$')
	depth := 0
	for ; i < len(rs); i++ {
		t.cur.WriteRune(rs[i])
		switch rs[i] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return i
}

func segmentVerb(words []string) string {
	if i := programAt(words); i >= 0 {
		return verbFrom(words[i:])
	}
	return ""
}

// programAt is the index of the segment's program, past keywords, wrappers
// and their flags, assignments and leading redirections; -1 when none is left.
func programAt(words []string) int {
	i := 0
	for i < len(words) {
		w := words[i]
		switch {
		case keywordsLead[w], dropped[w], isAssignment(w):
			i++
		case redirection.MatchString(w):
			i++ // a leading redirection: >file, 2>err, or > and its target
			if redirectionOnly.MatchString(w) && i < len(words) {
				i++
			}
		case strings.HasPrefix(w, "-") && i > 0 && wrapper(words[:i]):
			i++ // a wrapper's own flag (env -u X, sudo -n)
			if wrapperArg[w] && i < len(words) {
				i++ // and the value it takes
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
	prog := path.Base(words[0])
	rest := words[1:]
	if prog == "go" && len(rest) >= 2 && rest[0] == "run" {
		// The target counts as the program only when it is a tool the
		// allowlist knows (`go run ./cmd/fyr`); any other target is
		// content, and the verb is `go run` (security review of v2.6.0).
		if target := path.Base(strings.TrimSuffix(rest[1], "/")); subcommands[target] != nil {
			prog, rest = target, rest[2:]
		} else {
			parts := []string{"go", "run"}
			for _, w := range rest[2:] {
				if f := flagName("go", w); f != "" {
					parts = append(parts, f)
				}
			}
			return strings.Join(parts, " ")
		}
	}
	if !programName.MatchString(prog) {
		return ""
	}
	parts := []string{prog}
	if prog == "git" {
		rest = skipGitGlobals(rest)
	}
	if subs, ok := subcommands[prog]; ok && len(rest) > 0 && subs[rest[0]] {
		parts = append(parts, rest[0])
		rest = rest[1:]
	}
	// fyr's own second-level verbs (`metrics show`, `memory add`), under
	// either name, are
	// identifiers from a fixed list; any other second word stays a positional.
	if (prog == "fyr" || prog == "sh-agent") && len(parts) == 2 && len(rest) > 0 && shAgentSub[rest[0]] {
		parts = append(parts, rest[0])
		rest = rest[1:]
	}
	gitAdd := prog == "git" && len(parts) == 2 && parts[1] == "add"
	for _, w := range rest {
		if f := flagName(prog, w); f != "" {
			parts = append(parts, f)
		} else if gitAdd && w == "." {
			parts = append(parts, ".")
		}
	}
	return strings.Join(parts, " ")
}

// flagName is the name of the flag w, or "" when w is no flag or its name
// cannot be told from a value glued to it. `--name=v` keeps `--name`; a
// go-style `-name=v` keeps `-name`; a short-flag group of at most three
// letters (`-la`, `-rnw`) is kept whole; anything longer or with a digit
// keeps its first letter only (`-c1-200`, `-psecret`, `-xzvf` → `-c`, `-p`,
// `-x`), since a short option can carry its value glued on.
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
		return "" // -20, -1: a value, not a flag name
	case !hasValue && len(w) <= 4 && letters.MatchString(w[1:]):
		return w
	}
	return w[:2]
}

// wrapper reports whether the words before a flag are a wrapper and its own
// flags, so the flag belongs to the wrapper and not to the program.
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
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
