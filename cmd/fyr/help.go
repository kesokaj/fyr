package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

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
	newCtx(verb, verbs[verb], io.Discard).fs.VisitAll(func(f *flag.Flag) {
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
	fs.String("home", "", "")
	fs.String("repo", "", "")
	fs.String("dir", "", "")
	fs.String("C", "", "")
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
	case verbs[name].run == nil:
		return usageErr(out, "help", fmt.Sprintf("fyr help: unknown command %q", name))
	default:
		fmt.Fprint(out, verbHelp(name))
	}
	return nil
}
