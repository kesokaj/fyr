// Command fyr installs the agent harness (global context, skills,
// subagents, commands and rules) into the Antigravity CLI and keeps its
// records. Its help is help_text.go: `fyr` or `fyr help` prints the short
// page, `fyr help <verb>` or `fyr <verb> --help` the verb's page.
//
//	fyr install     deploy the harness into ~/.gemini/config (alias: sync)
//	fyr update      move this place to the newest harness
//	fyr status      what is installed, and whether it drifted
//	fyr diff        the diff of every drifted file
//	fyr uninstall   remove exactly what install wrote
//	fyr list        the skills and agents
//	fyr render      print the assembled context
//	fyr adopt       copy a skill from elsewhere into skills/
//	fyr record      add, get, edit, list or import trail records
//	fyr memory      save or show this project's memory
//	fyr state       export or import a portable state zip bundle
//	fyr resume      read-only seven-step session resume
//	fyr search      ranked search of records and files
//	fyr ast         AST slices, contracts, skeletons, paths, blast radius and map
//	fyr vet         9-mode intent, 6-anchor vetter and skill/record match
//	fyr hook        Antigravity CLI lifecycle hooks
//	fyr index       reconcile the search index; --register opts in
//	fyr graph       interactive AST and telemetry constellation
//	fyr dream       consolidate project memory and trail into proposals
//	fyr auto        idle-time runs: on, off, status, run, log, sim
//	fyr metrics     session shapes: ingest, show, report, evals, trajectory, export, record-run
//	fyr report      project or global usage report (HTML, --md, --json, --web)
//	fyr models      the live agy model catalog and role picks
//	fyr eval        run behavioural rows against the CLI
//	fyr verify      run gofmt, go vet, go test -race, check and write stamp
//	fyr gate        run lease, numstat, secret and trailer checks on a diff
//	fyr land        gate, squash-verify in worktree, ff main and delete branch
//	fyr close       write one handoff record embedding the fresh verify stamp
//	fyr check       read-only drift check of this checkout's docs
//	fyr git-check   read-only post-dispatch git status, log and tag check
//	fyr watch       inspect sessions, artifacts and processes for hangs
//	fyr version     print the build version
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	fyrisolate "github.com/kesokaj/fyr/internal/isolate"
)

// stderr is where a read-only verb names the state directory from before the
// rename, where a verb that does not read --repo says it ignored it, and
// where `record add` and `record edit` print the redactor
// warning: their stdout is data (render, list, metrics show --json, a new
// record's ID); a test replaces it.
var stderr io.Writer = os.Stderr

// version is set by a release build, -ldflags "-X main.version=vX.Y.Z"; empty
// otherwise, and buildVersion reads the build info instead.
var version string

func main() {
	fyrisolate.MaybeRunInit()
	if os.Getenv(harness.ChildEnv) != "1" {
		oldEnvNotices(os.Environ(), os.Stderr)
	}
	if err := run(os.Args[1:], os.Stdout); err != nil {
		var code exitCode
		if errors.As(err, &code) {
			os.Exit(int(code))
		}
		fmt.Fprintln(os.Stderr, "fyr:", err)
		os.Exit(1)
	}
}

// oldEnvNotices names each SH_AGENT_* variable still set: fyr reads FYR_*
// only, and an old SH_AGENT_REPO ignored in silence would change which tree
// an install takes (plan fyr D15). An empty one changes nothing and is not
// named; the rebuilt child of an install inherits the parent's environment
// and stays quiet, so each variable is named once.
func oldEnvNotices(environ []string, w io.Writer) {
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if name, ok := strings.CutPrefix(k, "SH_AGENT_"); ok && v != "" {
			fmt.Fprintf(w, "fyr: $%s is set but no longer read; rename it $FYR_%s\n", k, name)
		}
	}
}

// buildVersion describes the running process, for `fyr version`.
// `fyr status` reads the installed file instead (see status).
func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return versionFromInfo(info)
}

// releaseFromInfo renders an installed release binary's version: the module
// version when it is a release tag, which Go stamps for a build at a clean
// tag and `go install …@vX.Y.Z` records — a release build's -ldflags are not
// in the build info, since -trimpath leaves them out — and the VCS stamp
// otherwise.
func releaseFromInfo(info *debug.BuildInfo) string {
	if harness.IsReleaseVersion(info.Main.Version) {
		return info.Main.Version
	}
	return versionFromInfo(info)
}

// versionFromInfo renders a binary's VCS stamp as the short revision with a
// "+dirty" suffix for a modified tree, or the module version when the build
// carried no stamp (`go run`, `go test`, a build outside git).
func versionFromInfo(info *debug.BuildInfo) string {
	rev, modified := "", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				modified = "+dirty"
			}
		}
	}
	if rev == "" {
		return info.Main.Version
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return rev + modified
}

// helpWidth is the widest line a page may print: an 80-column terminal never
// wraps it (plan ledger D19).
const helpWidth = 79

// pageOf looks up a verb's page in pages (help_text.go); sync is install's
// alias and shares it.
func pageOf(verb string) (verbPage, bool) {
	if verb == "sync" {
		verb = "install"
	}
	p, ok := pages[verb]
	return p, ok
}

// verbHelp is the page `fyr help <verb>` and `fyr <verb> -h` print: the
// synopsis, what the verb does, its own flags, and the global flags line.
func verbHelp(verb string) string {
	p, _ := pageOf(verb)
	var b strings.Builder
	b.WriteString(strings.Join(p.synopsis, "\n") + "\n\n")
	b.WriteString(strings.Join(p.about, "\n") + "\n")
	if fl := flagLines(verb); len(fl) > 0 {
		b.WriteString("\nFlags:\n" + strings.Join(fl, "\n") + "\n")
	}
	b.WriteString("\n" + helpLast + "\n")
	return b.String()
}

// flagLines lists the verb's own flags from the FlagSet run parses, in name
// order: `  --name` for a bool, `  --name ARG` otherwise, where ARG is the
// backquoted word of the usage string, and the usage wrapped beneath.
func flagLines(verb string) []string {
	var lines []string
	newCtx(verb, allVerbs()[verb], io.Discard).fs.VisitAll(func(f *flag.Flag) {
		if isGlobalFlag(f.Name) {
			return
		}
		arg, use := flag.UnquoteUsage(f)
		head := "  --" + f.Name
		if arg != "" {
			head += " " + arg
		}
		lines = append(lines, head)
		lines = append(lines, wrapWords(use, "      ")...)
	})
	return lines
}

// wrapWords breaks text into lines of at most helpWidth runes, each
// beginning with indent.
func wrapWords(text, indent string) []string {
	var lines []string
	line := indent
	for _, w := range strings.Fields(text) {
		if line != indent && len([]rune(line))+1+len([]rune(w)) > helpWidth {
			lines = append(lines, line)
			line = indent
		}
		if line != indent {
			line += " "
		}
		line += w
	}
	return append(lines, line)
}

// usageErr is every usage error D21 (2) names, said on the writer, exit 2:
// the reason when there is one, then the verb's synopsis and a hint. An
// unknown verb ("") gets the hint to the short page, never the page itself;
// help's own errors end at its synopsis.
func usageErr(out io.Writer, verb, reason string) error {
	if reason != "" {
		fmt.Fprintln(out, reason)
	}
	switch verb {
	case "":
		fmt.Fprintln(out, "run 'fyr help' for the commands")
	case "help":
		fmt.Fprintln(out, helpSynopsis)
	default:
		p, _ := pageOf(verb)
		fmt.Fprintln(out, strings.Join(p.synopsis, "\n"))
		fmt.Fprintf(out, "run 'fyr help %s' for its flags\n", verb)
	}
	return exitCode(2)
}

// runHelp serves `fyr help [<verb>]`, and `fyr -h|-help|--help [<verb>]`. --home
// and --repo are accepted and unused, since help writes nothing; a -h among
// the arguments asks for the same page.
func runHelp(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("help", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() {}
	registerGlobalFlags(fs, nil)
	var kept []string
	for i, a := range args {
		if a == "--" {
			kept = append(kept, args[i:]...)
			break
		}
		if a != "-h" && a != "-help" && a != "--help" {
			kept = append(kept, a)
		}
	}
	if err := fs.Parse(reorder(fs, kept)); err != nil {
		return usageErr(out, "help", "")
	}
	switch name := fs.Arg(0); {
	case fs.NArg() > 1:
		return usageErr(out, "help", fmt.Sprintf("fyr help takes one command: %q", fs.Args()))
	case fs.NArg() == 0 || name == "help":
		fmt.Fprint(out, shortPage)
	case allVerbs()[name].run == nil:
		return usageErr(out, "help", fmt.Sprintf("fyr help: unknown command %q", name))
	default:
		fmt.Fprint(out, verbHelp(name))
	}
	return nil
}
