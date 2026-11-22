package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
)

var autoSIMBoolFlags = map[string]bool{
	"json":    true,
	"json-in": true,
}

func flagTokenName(a string) (string, bool) {
	if !strings.HasPrefix(a, "-") || a == "-" || a == "--" {
		return "", false
	}
	t := strings.TrimPrefix(a, "-")
	t = strings.TrimPrefix(t, "-")
	if t == "" || strings.HasPrefix(t, "-") {
		return "", false
	}
	name, _, _ := strings.Cut(t, "=")
	return name, true
}

func isAutoSIMValueFlag(name string) bool {
	return autoFlags["sim"][name] && !autoSIMBoolFlags[name]
}

// hasAutoSIMSubcommand reports whether rest names "sim" as its first
// non-global-flag positional argument.
func hasAutoSIMSubcommand(rest []string) bool {
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if a == "--" {
			return false
		}
		if name, ok := flagTokenName(a); ok {
			if isGlobalFlag(name) {
				if !strings.Contains(a, "=") && i+1 < len(rest) {
					i++
				}
				continue
			}
			return false
		}
		return a == "sim"
	}
	return false
}

// splitAutoSIMGlobals pulls global flags (--home, --repo, --dir, -C) from
// anywhere in rest while honoring sim value flags and `--`.
func splitAutoSIMGlobals(rest []string) (globals, simArgs []string) {
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if a == "--" {
			simArgs = append(simArgs, rest[i:]...)
			break
		}
		if name, ok := flagTokenName(a); ok {
			if isGlobalFlag(name) {
				globals = append(globals, a)
				if !strings.Contains(a, "=") && i+1 < len(rest) {
					globals = append(globals, rest[i+1])
					i++
				}
				continue
			}
			simArgs = append(simArgs, a)
			if isAutoSIMValueFlag(name) && !strings.Contains(a, "=") && i+1 < len(rest) {
				simArgs = append(simArgs, rest[i+1])
				i++
			}
			continue
		}
		simArgs = append(simArgs, a)
	}
	return globals, simArgs
}

func hasAutoSIMHelp(simArgs []string) bool {
	for i := 1; i < len(simArgs); i++ {
		a := simArgs[i]
		if a == "--" {
			return false
		}
		if name, ok := flagTokenName(a); ok {
			if name == "h" || name == "help" {
				return true
			}
			if isAutoSIMValueFlag(name) && !strings.Contains(a, "=") && i+1 < len(simArgs) {
				i++
			}
		}
	}
	return false
}

// parseAutoSIMArgs parses global flags for `fyr auto sim ...`, answers `-h`
// before the real-home guard, and leaves the sim arguments in c.args.
func (c *ctx) parseAutoSIMArgs(rest []string) (bool, error) {
	globals, simArgs := splitAutoSIMGlobals(rest)
	gfs := flag.NewFlagSet("auto", flag.ContinueOnError)
	gfs.SetOutput(c.out)
	gfs.Usage = func() {}
	registerGlobalFlags(gfs, c)
	if err := gfs.Parse(globals); err != nil {
		return true, usageErr(c.out, "auto", "")
	}
	if hasAutoSIMHelp(simArgs) {
		fmt.Fprint(c.out, autoSIMHelp())
		return true, nil
	}
	c.args = simArgs
	return false, nil
}

func newSIMFlagSet(name string, c *ctx) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.out)
	fs.Usage = func() {}
	return fs
}

func parseSIMFlagSet(fs *flag.FlagSet, rest []string, c *ctx) (bool, error) {
	if err := fs.Parse(reorder(fs, rest)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(c.out, autoSIMHelp())
			return true, nil
		}
		return true, autoSIMUsageErr(c.out, "")
	}
	return false, nil
}

func runAutoSIM(c *ctx, args []string) error {
	if len(args) == 0 {
		return autoSIMUsageErr(c.out, "")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return runAutoSIMList(c, rest)
	case "show":
		return runAutoSIMShow(c, rest)
	case "record":
		return runAutoSIMRecord(c, rest)
	case "merge":
		return runAutoSIMMerge(c, rest)
	default:
		return autoSIMUsageErr(c.out, fmt.Sprintf("fyr auto sim: unknown subcommand %q (list, show, record, merge)", sub))
	}
}

func runAutoSIMList(c *ctx, rest []string) error {
	fs := newSIMFlagSet("auto sim list", c)
	var sig, pathPrefix, status string
	var limit int
	var asJSON bool
	fs.StringVar(&sig, "signal", "", "")
	fs.StringVar(&pathPrefix, "path", "", "")
	fs.StringVar(&status, "status", "active", "")
	fs.IntVar(&limit, "limit", 20, "")
	fs.BoolVar(&asJSON, "json", false, "")
	if stop, err := parseSIMFlagSet(fs, rest, c); stop {
		return err
	}
	if fs.NArg() > 0 {
		return autoSIMUsageErr(c.out, fmt.Sprintf("fyr auto sim list takes no arguments: %q", fs.Args()))
	}
	if limit < 1 {
		return autoSIMUsageErr(c.out, "fyr auto sim list: --limit must be >= 1")
	}
	if !validSIMListStatus(status) {
		return autoSIMUsageErr(c.out, fmt.Sprintf("fyr auto sim list: invalid --status %q (active, resolved, merged, obsolete, all)", status))
	}
	items, err := brain.ListSIMProblems(c.home, brain.SIMListFilter{
		SignalID: sig, PathPrefix: pathPrefix, Status: status, Limit: limit,
	})
	if err != nil {
		return autoSIMErr(c, err)
	}
	return renderAutoSIMList(c.out, items, asJSON)
}

func runAutoSIMShow(c *ctx, rest []string) error {
	fs := newSIMFlagSet("auto sim show", c)
	var asJSON bool
	fs.BoolVar(&asJSON, "json", false, "")
	if stop, err := parseSIMFlagSet(fs, rest, c); stop {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		return autoSIMUsageErr(c.out, "fyr auto sim show requires <SIM-ID>")
	}
	detail, err := brain.GetSIMProblem(c.home, fs.Arg(0))
	if err != nil {
		return autoSIMErr(c, err)
	}
	return renderAutoSIMShow(c.out, detail, asJSON)
}

func runAutoSIMRecord(c *ctx, rest []string) error {
	in, asJSON, stop, err := parseAutoSIMRecordFlags(c, rest)
	if stop || err != nil {
		return err
	}
	res, err := brain.RecordSIMAttempt(c.home, in)
	if err != nil {
		return autoSIMErr(c, err)
	}
	if asJSON {
		return writePrettyJSON(c.out, res)
	}
	fmt.Fprintf(c.out, "%s attempt #%d\n", res.ProblemID, res.AttemptID)
	return nil
}

func parseAutoSIMRecordFlags(c *ctx, rest []string) (brain.SIMRecordInput, bool, bool, error) {
	fs := newSIMFlagSet("auto sim record", c)
	var in brain.SIMRecordInput
	var filesCSV, predRowsCSV, predSigsCSV, fixedCSV, missedCSV, regCSV string
	var jsonIn, asJSON bool
	registerSIMRecordFlags(fs, &in, &filesCSV, &predRowsCSV, &predSigsCSV, &fixedCSV, &missedCSV, &regCSV, &jsonIn, &asJSON)
	if stop, err := parseSIMFlagSet(fs, rest, c); stop {
		return brain.SIMRecordInput{}, false, true, err
	}
	if fs.NArg() > 0 {
		return brain.SIMRecordInput{}, false, true, autoSIMUsageErr(c.out, fmt.Sprintf("fyr auto sim record takes no arguments: %q", fs.Args()))
	}
	set := visitedFlags(fs)
	applySIMRecordCSVs(&in, set, filesCSV, predRowsCSV, predSigsCSV, fixedCSV, missedCSV, regCSV)
	if jsonIn {
		doc, err := decodeAutoSIMRecordJSON(stdin)
		if err != nil {
			return brain.SIMRecordInput{}, false, true, autoSIMUsageErr(c.out, "fyr auto sim record: "+err.Error())
		}
		applySIMRecordJSON(&in, doc, set)
	}
	if err := applySIMRecordDefaults(&in, set); err != nil {
		return brain.SIMRecordInput{}, false, true, autoSIMUsageErr(c.out, "fyr auto sim record: "+err.Error())
	}
	in.Now = time.Now().UTC()
	return in, asJSON, false, nil
}

func registerSIMRecordFlags(fs *flag.FlagSet, in *brain.SIMRecordInput, files, predRows, predSigs, fixed, missed, reg *string, jsonIn, asJSON *bool) {
	fs.StringVar(&in.ProblemID, "problem", "", "")
	fs.StringVar(&in.RootCause, "root-cause", "", "")
	fs.StringVar(&in.MechanismClass, "mechanism", "", "")
	fs.StringVar(&in.ProblemStatus, "status", "", "")
	fs.StringVar(&in.SignalID, "signal", "", "")
	fs.StringVar(&in.What, "what", "", "")
	fs.StringVar(files, "files", "", "")
	fs.StringVar(&in.ProposalID, "proposal", "", "")
	fs.StringVar(&in.CommitSHA, "commit", "", "")
	fs.StringVar(&in.ParentSHA, "parent", "", "")
	fs.StringVar(&in.HarnessVersion, "harness", "", "")
	fs.StringVar(&in.ProposerModel, "proposer-model", "", "")
	fs.StringVar(&in.PolicyModel, "policy-model", "", "")
	fs.StringVar(&in.SourceKind, "source", "", "")
	fs.StringVar(&in.EvalVerdict, "verdict", "", "")
	fs.StringVar(&in.FieldStatus, "field-status", "", "")
	fs.StringVar(predRows, "predicted-rows", "", "")
	fs.StringVar(predSigs, "predicted-signals", "", "")
	fs.StringVar(fixed, "fixed-rows", "", "")
	fs.StringVar(missed, "missed-rows", "", "")
	fs.StringVar(reg, "regressed-rows", "", "")
	fs.StringVar(&in.Provenance, "provenance", "", "")
	fs.BoolVar(jsonIn, "json-in", false, "")
	fs.BoolVar(asJSON, "json", false, "")
}

func runAutoSIMMerge(c *ctx, rest []string) error {
	fs := newSIMFlagSet("auto sim merge", c)
	var prov string
	var asJSON bool
	fs.StringVar(&prov, "provenance", "operator", "")
	fs.BoolVar(&asJSON, "json", false, "")
	if stop, err := parseSIMFlagSet(fs, rest, c); stop {
		return err
	}
	if fs.NArg() != 2 {
		return autoSIMUsageErr(c.out, "fyr auto sim merge requires <OLD-ID> <TARGET-ID>")
	}
	if prov != "operator" && prov != "derived" {
		return autoSIMUsageErr(c.out, fmt.Sprintf("fyr auto sim merge: invalid --provenance %q (operator, derived)", prov))
	}
	res, err := brain.MergeSIMProblem(c.home, fs.Arg(0), fs.Arg(1), prov, time.Now().UTC())
	if err != nil {
		return autoSIMErr(c, err)
	}
	if asJSON {
		return writePrettyJSON(c.out, res)
	}
	fmt.Fprintf(c.out, "%s -> %s\n", res.OldID, res.TargetID)
	return nil
}
