package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

func run(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(out, usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	v, ok := verbs[cmd]
	if !ok {
		// A usage error like every other: exit 2, said on the writer (IN16).
		fmt.Fprintf(out, "fyr: unknown command %q\n\n%s", cmd, usage)
		return exitCode(2)
	}
	// A zip place (R8, ADR-038): the operator named the archive, so no tree is
	// resolved and no other verb runs — `uninstall --from-zip` must not
	// uninstall. Refused before the FlagSet sees it, so the refusal names the
	// right verb rather than an undefined flag.
	if cmd != "update" && hasFlag(rest, "from-zip") {
		fmt.Fprintln(out, "fyr "+cmd+": --from-zip belongs to update: fyr update --from-zip <file>")
		return exitCode(2)
	}

	c := newCtx(cmd, v, out)
	if err := c.fs.Parse(reorder(c.fs, rest)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// Help is a successful ask: the FlagSet has printed the verb's
			// flags on the writer (IN15).
			return nil
		}
		// The flag package has named the flag on the writer; a usage error
		// exits 2 like every other usage error here.
		return exitCode(2)
	}
	if !v.positional && c.fs.NArg() > 0 {
		// A stray argument is never ignored: `list - --check` and `update --
		// --from-zip x` would otherwise run the verb as if nothing had been said.
		fmt.Fprintf(out, "%s takes no arguments: %q\n", cmd, c.fs.Args())
		return exitCode(2)
	}

	c.home = c.homeFlag
	if c.home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		c.home = h
	}
	// Absolute before anything runs: the checkout path rebuilds and runs the
	// build's install with cmd.Dir = repo, so a relative --home would land
	// inside the repository on a real install and under the invoking
	// directory on --dry-run (BUG-080).
	if abs, err := filepath.Abs(c.home); err == nil {
		c.home = abs
	}

	fromZipSet := false
	c.fs.Visit(func(f *flag.Flag) { fromZipSet = fromZipSet || f.Name == "from-zip" })
	// The parsed FlagSet decides, never a scan of the raw arguments: `--home --`
	// makes `--` a value, and a scan that stopped there would skip this branch
	// and run a normal update (security review 2026-10-01, round 2).
	if cmd == "update" && fromZipSet {
		// Every refusal comes before any command runs, and an empty value is
		// refused rather than read as no flag — that would fall through to a
		// normal update, which on a release binary downloads and replaces the
		// binary.
		switch {
		case c.fromZip == "" || strings.HasPrefix(c.fromZip, "-"):
			// A value that looks like a flag was a flag: `--from-zip --dry-run`
			// must not read --dry-run as the file and run a real update.
			fmt.Fprintln(out, "fyr update: --from-zip needs a file: fyr update --from-zip <file>")
			return exitCode(2)
		case c.check || c.repoFlag != "":
			fmt.Fprintln(out, "fyr update: --from-zip installs the named zip; --check/--repo do not apply to it")
			return exitCode(2)
		}
		if err := c.migrateStateDir(); err != nil {
			return err
		}
		return updateFromZip(c.fromZip, c.home, c.dryRun, out)
	}

	// Before the source is resolved: it falls back to the last install's
	// manifest, which a migration moves.
	if err := c.migrateStateDir(); err != nil {
		return err
	}
	if v.source {
		src, err := harness.ResolveSource(c.repoFlag, c.home, buildVersion())
		if err != nil {
			return err
		}
		c.src = src
	}
	return v.run(c)
}

// writesState reports whether this invocation may write under the state
// directory, and so moves the old one first (plan fyr D13). A dry run, a
// --check and a verb or subcommand that only reads answer false. The table is
// by verb, so it errs early on purpose: adopt (which writes into the repo), a
// project-local search or index (which writes .agents/scratch/search.db) and
// dream --install-timer or --remove-timer write nothing there, yet migrate
// too. Moving the old directory early is harmless — one rename, refused for
// a symlink, and both directories present are left alone — and a verb that
// starts writing there later needs no change here (IN18).
func (c *ctx) writesState() bool {
	switch c.cmd {
	case "install", "sync", "uninstall", "adopt", "index", "search", "dream":
		return !c.dryRun
	case "record":
		// Every record verb may write the brain store: a read loads a
		// trail.jsonl that changed under it (ADR-063, D21). Only import takes
		// --dry-run, which writes nothing (IN33 of the 2026-10-27 audit).
		return !c.dryRun
	case "update":
		return !c.dryRun && !c.check
	case "memory":
		return c.fs.Arg(0) == "add"
	case "metrics":
		return c.fs.Arg(0) == "ingest"
	case "eval":
		// Sandboxes, the metrics store and the testing record; --list reads.
		return !c.evalList
	}
	return false
}

// migrateStateDir moves ~/.config/sh-agent to ~/.config/fyr for a verb that
// writes there, and names it on stderr for one that only reads, which reads
// nothing from it.
func (c *ctx) migrateStateDir() error {
	if c.writesState() {
		return harness.MigrateStateDir(c.home, c.out)
	}
	if notice := harness.StateDirNotice(c.home); notice != "" {
		fmt.Fprintln(stderr, "fyr: "+notice)
	}
	return nil
}

// hasFlag reports whether args carry the named flag, as `-name`, `--name`,
// `-name=…` or `--name=…`, before `--`.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		t := strings.TrimLeft(a, "-")
		if len(a)-len(t) > 2 || len(a) == len(t) {
			continue
		}
		if t == name || strings.HasPrefix(t, name+"=") {
			return true
		}
	}
	return false
}

// reorder moves flags in front of positional arguments so that
// `fyr adopt my-skill --home /tmp/x` parses; the flag package stops at
// the first positional argument otherwise. Which flags take a value is read
// from the FlagSet itself, so a value flag can never be missing from a
// hand-kept list and swallow the next argument (BUG-002: the removed `report
// --feedback-repo /x --dry-run` once parsed --dry-run as the path).
func reorder(fs *flag.FlagSet, args []string) []string {
	valueFlags := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			return
		}
		valueFlags["-"+f.Name], valueFlags["--"+f.Name] = true, true
	})
	var flags, positional []string
	marker := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			// Everything after the marker is positional, flag-shaped or not; the
			// marker itself is re-emitted once, between the flags and every
			// positional, so the flag package stops exactly there.
			marker = true
			positional = append(positional, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-"):
			flags = append(flags, a)
			if valueFlags[a] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
		default:
			positional = append(positional, a)
		}
	}
	if marker {
		flags = append(flags, "--")
	}
	return append(flags, positional...)
}
