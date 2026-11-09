package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/check"
	"github.com/kesokaj/fyr/internal/search"
)

// check is registered here, not in the verbs literal: runCheck reads verbs
// to build the vocabulary, and the literal would then refer to itself (an
// initialization cycle).
func init() {
	verbs["check"] = verb{run: runCheck}
}

// runResume serves `fyr resume` (ADR-508): read-only seven-step session
// resume over the working directory's checkout. Exit 0 ok, 2 on a refusal.
func runResume(c *ctx) error {
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	out, err := brain.Resume(brain.Options{Home: c.home, Dir: wd})
	if brain.IsRefusal(err) {
		fmt.Fprintln(c.out, search.Printable("fyr resume: "+err.Error()))
		return exitCode(2)
	}
	if err != nil {
		return err
	}
	fmt.Fprint(c.out, out)
	return nil
}

// runGitCheck serves `fyr git-check`: read-only post-dispatch git status, log
// and tag check over the working directory's checkout. Exit 0 ok, 2 on a
// refusal.
func runGitCheck(c *ctx) error {
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	out, err := check.GitCheck(wd)
	if errors.Is(err, check.ErrRefused) {
		fmt.Fprintln(c.out, search.Printable("fyr git-check: "+err.Error()))
		return exitCode(2)
	}
	if err != nil {
		return err
	}
	fmt.Fprint(c.out, out)
	return nil
}

// runCheck serves `fyr check` (spec SPEC-fyr-check, ADR-064): read-only over
// the working directory's checkout. Exit 0 clean, 1 with the summary line on
// findings, 1 with no summary and the reason on stderr for a refusal; notes
// never count.
func runCheck(c *ctx) error {
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	rep, err := check.Run(wd, vocabulary(), c.home)
	if errors.Is(err, check.ErrRefused) {
		fmt.Fprintln(stderr, search.Printable("check: "+err.Error()))
		return exitCode(1)
	}
	if err != nil {
		return err
	}
	printCheck(c.out, rep)
	if len(rep.Findings) > 0 {
		return exitCode(1)
	}
	return nil
}

func printCheck(w io.Writer, rep check.Report) {
	for _, f := range rep.Findings {
		where := f.Where
		if f.Line > 0 {
			where += ":" + strconv.Itoa(f.Line)
		}
		fmt.Fprintln(w, search.Printable(where+"  "+f.Rule+"  "+f.Text))
	}
	for _, n := range rep.Notes {
		fmt.Fprintln(w, search.Printable("note: "+n))
	}
	d, s, r, t := check.Counts(rep.Findings)
	fmt.Fprintln(w, search.Printable(fmt.Sprintf("check: %d findings (docs %d, specs %d, requirements %d, traces %d); fyr %s", len(rep.Findings), d, s, r, t, buildVersion())))
}

// vocabulary is the running binary's verbs, subverbs and flags, read from
// the same FlagSets run parses (plan fyr-check D14): record's subverbs take
// recordFlags' flags; metrics' and memory's take the verb's. Every verb and
// subverb also takes the four global flags (--home, --repo, --dir, -C) and
// --help, which the flag package answers.
func vocabulary() check.Vocabulary {
	v := check.Vocabulary{"help": {Flags: map[string]bool{"home": true, "repo": true, "dir": true, "C": true, "help": true}}}
	for name, vb := range verbs {
		c := newCtx(name, vb, io.Discard)
		flags := map[string]bool{"help": true}
		c.fs.VisitAll(func(f *flag.Flag) { flags[f.Name] = true })
		spec := check.VerbSpec{Flags: flags}
		switch name {
		case "record":
			spec.Subverbs = map[string]map[string]bool{}
			for sub, takes := range recordFlags {
				own := map[string]bool{"home": true, "repo": true, "dir": true, "C": true, "help": true}
				for f := range takes {
					own[f] = true
				}
				spec.Subverbs[sub] = own
			}
		case "auto":
			spec.Subverbs = map[string]map[string]bool{}
			for _, sub := range autoSubverbs {
				own := map[string]bool{"home": true, "repo": true, "dir": true, "C": true, "help": true}
				for f := range autoFlags[sub] {
					own[f] = true
				}
				spec.Subverbs[sub] = own
			}
		case "metrics":
			spec.Subverbs = subverbsTaking(metricsSubverbs, flags)
		case "memory":
			spec.Subverbs = subverbsTaking(memorySubverbs, flags)
		}
		v[name] = spec
	}
	return v
}

func subverbsTaking(subs []string, flags map[string]bool) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, s := range subs {
		out[s] = flags
	}
	return out
}
