package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

// recordFlags is the flags each record subverb takes, besides --home and
// --repo: one it does not take is a usage error, never parsed and ignored
// (IN5's rule, per subverb).
var recordFlags = map[string]map[string]bool{
	"add":    {"title": true, "title-file": true, "body-file": true, "status": true, "id": true, "project": true, "global": true, "json-in": true},
	"get":    {"global": true, "out": true, "json": true},
	"edit":   {"status": true, "title": true, "body": true, "body-file": true, "global": true, "json-in": true},
	"list":   {"kind": true, "open": true, "project": true, "all": true, "global": true, "limit": true, "json": true},
	"export": {},
	"sync":   {},
	"import": {"dry-run": true, "global": true},
}

// runRecord serves `record add|get|edit|list|export|sync|import` (spec
// the record SPEC-fyr-record); it needs no harness tree. Exit 0 with
// or without output, 1 a refusal, 2 a usage error, the reason on the writer.
func runRecord(c *ctx) error {
	var sub string
	rest := c.fs.Args()
	if len(rest) > 0 {
		sub, rest = rest[0], rest[1:]
	}
	takes, ok := recordFlags[sub] // no subcommand is not one of them
	if !ok {
		return usageErr(c.out, "record", unknownRecordSub(sub))
	}
	set, err := recordFlagsSet(c, sub, takes)
	if err != nil {
		return err
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	return dispatchRecord(c, brain.Options{Home: c.home, Dir: wd, Repo: c.repoFlag, Global: c.global}, sub, rest, set)
}

func dispatchRecord(c *ctx, o brain.Options, sub string, rest []string, set map[string]bool) error {
	switch sub {
	case "add":
		if len(rest) == 1 {
			return recordAdd(c, o, rest[0], set)
		}
	case "get":
		if len(rest) > 0 {
			return recordGet(c, o, rest, set)
		}
	case "edit":
		if len(rest) == 1 {
			return recordEdit(c, o, rest[0], set)
		}
	case "list":
		if len(rest) == 0 {
			return recordList(c, o, set)
		}
	case "export":
		if len(rest) == 0 {
			return recordExport(c, o)
		}
	case "sync":
		if len(rest) == 0 {
			return recordSync(c, o)
		}
	case "import":
		if len(rest) <= 1 && (!c.global || len(rest) == 0) {
			return recordImport(c, o, rest)
		}
	}
	return usageErr(c.out, "record", "")
}

// unknownRecordSub is the reason line for a subverb record does not have; a
// bare `fyr record` has none (the operator's reading, plan ledger D24).
func unknownRecordSub(sub string) string {
	if sub == "" {
		return ""
	}
	return fmt.Sprintf("fyr record: unknown subcommand %q", sub)
}

// recordFlagsSet returns the flags set on the command line; a flag sub does
// not take is a usage error, said on the writer.
func recordFlagsSet(c *ctx, sub string, takes map[string]bool) (map[string]bool, error) {
	set := map[string]bool{}
	var stray []string
	c.fs.Visit(func(f *flag.Flag) {
		set[f.Name] = true
		if !isGlobalFlag(f.Name) && !takes[f.Name] {
			stray = append(stray, "--"+f.Name)
		}
	})
	if len(stray) > 0 {
		sort.Strings(stray)
		return nil, usageErr(c.out, "record", fmt.Sprintf("fyr record %s does not take %s", sub, strings.Join(stray, ", ")))
	}
	return set, nil
}

// recordList serves `record list`: one line per record, or JSON with --json.
func recordList(c *ctx, o brain.Options, set map[string]bool) error {
	if set["limit"] && c.limit < 1 {
		return usageErr(c.out, "record", "fyr record list: --limit must be at least 1")
	}
	recs, err := brain.List(o, brain.ListFilter{Kind: c.kind, Open: c.openRecords, Project: c.recProject, All: c.recAll})
	if err != nil {
		return recordErr(c, err)
	}
	sortRecordList(recs)
	if c.limit > 0 && len(recs) > c.limit {
		recs = recs[:c.limit]
	}
	if c.jsonOut {
		js, jerr := brain.FormatRecordsJSON(recs, false)
		if jerr != nil {
			return recordErrTo(stderr, jerr)
		}
		fmt.Fprint(c.out, js)
		return nil
	}
	for _, r := range recs {
		fmt.Fprintln(c.out, formatRecordListLine(r, c.recAll))
	}
	return nil
}

func recordUpdatedDay(r brain.Record) string {
	if r.Updated == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339Nano, r.Updated); err == nil {
		return t.Format("2006-01-02")
	}
	if len(r.Updated) >= 10 {
		return r.Updated[:10]
	}
	return ""
}

func recordListOrderKey(r brain.Record) (day, ts string) {
	day = r.Date
	if r.Status == "current" {
		if upd := recordUpdatedDay(r); upd > day {
			day = upd
		}
		ts = r.Updated
	}
	return day, ts
}

func sortRecordList(recs []brain.Record) {
	sort.SliceStable(recs, func(i, j int) bool {
		di, ti := recordListOrderKey(recs[i])
		dj, tj := recordListOrderKey(recs[j])
		if di != dj {
			return di > dj
		}
		if ti != tj && recs[i].Status == "current" && recs[j].Status == "current" {
			return ti > tj
		}
		return false
	})
}

func formatRecordListLine(r brain.Record, showScope bool) string {
	line := brain.FormatLine(r, showScope)
	if r.Status != "current" {
		return line
	}
	upd := recordUpdatedDay(r)
	if upd == "" || upd == r.Date {
		return line
	}
	if r.Date != "" && strings.Contains(line, "  "+r.Date+"  ") {
		return strings.Replace(line, "  "+r.Date+"  ", "  "+r.Date+" (updated "+upd+")  ", 1)
	}
	return line + " (updated " + upd + ")"
}

// recordGet serves `record get`: each record found, a blank line between,
// then the refusal naming any missing; with --out writes one record's
// body under .agents/scratch/; or with --json prints a JSON array.
func recordGet(c *ctx, o brain.Options, ids []string, set map[string]bool) error {
	if set["out"] && c.jsonOut {
		return usageErr(c.out, "record", "fyr record get takes --out or --json, not both")
	}
	if set["out"] {
		if len(ids) != 1 {
			return usageErr(c.out, "record", "fyr record get --out takes one id")
		}
		if _, _, err := brain.CheckScratchOut(o.Dir, c.recOut); err != nil {
			return recordErr(c, err)
		}
	}
	recs, _, err := brain.Get(o, ids)
	if set["out"] {
		if err != nil {
			return recordErr(c, err)
		}
		return recordErr(c, brain.WriteScratch(o.Dir, c.recOut, recs[0].Body))
	}
	if c.jsonOut {
		if len(recs) > 0 || err == nil {
			js, jerr := brain.FormatRecordsJSON(recs, true)
			if jerr != nil {
				return recordErrTo(stderr, jerr)
			}
			fmt.Fprint(c.out, js)
		}
		return recordErrTo(stderr, err)
	}
	for i, r := range recs {
		if i > 0 {
			fmt.Fprintln(c.out)
		}
		fmt.Fprint(c.out, brain.FormatGet(r))
	}
	return recordErr(c, err)
}

// recordErr prints a refusal (exit 1) or a usage error (exit 2) on the
// writer, through printable: the message can quote a record's own text.
func recordErr(c *ctx, err error) error {
	return recordErrTo(c.out, err)
}

func recordErrTo(w io.Writer, err error) error {
	switch {
	case err == nil:
		return nil
	case brain.IsUsage(err):
		fmt.Fprintln(w, search.Printable("fyr record: "+err.Error()))
		return exitCode(2)
	case brain.IsRefusal(err):
		fmt.Fprintln(w, search.Printable("fyr record: "+err.Error()))
		return exitCode(1)
	}
	// Any other error can carry a checkout's path, which a hand-extracted
	// archive can fill with escape bytes: filtered too (security review
	// 2026-10-25, finding 8).
	return errors.New(search.Printable(err.Error()))
}
