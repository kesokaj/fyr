package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

const recordUsage = "usage: fyr record add <kind> --title T [--status S] [--id ID] [--global [--project ROOT]] (body on stdin)" +
	" | fyr record get <id>... [--global]" +
	" | fyr record edit <id> [--status S] [--title T] [--body] [--global]" +
	" | fyr record list [--kind K] [--open] [--global [--project ROOT]] [--all]" +
	" | fyr record import [--dry-run] [<docs-root>] | fyr record import --global [--dry-run]"

// recordFlags is the flags each record subverb takes, besides --home and
// --repo: one it does not take is a usage error, never parsed and ignored
// (IN5's rule, per subverb).
var recordFlags = map[string]map[string]bool{
	"add":    {"title": true, "status": true, "id": true, "project": true, "global": true},
	"get":    {"global": true},
	"edit":   {"status": true, "title": true, "body": true, "global": true},
	"list":   {"kind": true, "open": true, "project": true, "all": true, "global": true},
	"import": {"dry-run": true, "global": true},
}

// runRecord serves `record add|get|edit|list` (spec
// .agents/docs/specs/fyr/record.md); it needs no harness tree. Exit 0 with
// or without output, 1 a refusal, 2 a usage error, the reason on the writer.
func runRecord(c *ctx) error {
	args := c.fs.Args()
	if len(args) == 0 {
		fmt.Fprintln(c.out, recordUsage)
		return exitCode(2)
	}
	sub, rest := args[0], args[1:]
	takes, ok := recordFlags[sub]
	if !ok {
		fmt.Fprintln(c.out, recordUsage)
		return exitCode(2)
	}
	set := map[string]bool{}
	var stray []string
	c.fs.Visit(func(f *flag.Flag) {
		set[f.Name] = true
		if f.Name != "home" && f.Name != "repo" && !takes[f.Name] {
			stray = append(stray, "--"+f.Name)
		}
	})
	if len(stray) > 0 {
		sort.Strings(stray)
		fmt.Fprintf(c.out, "fyr record %s does not take %s\n%s\n", sub, strings.Join(stray, ", "), recordUsage)
		return exitCode(2)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	o := brain.Options{Home: c.home, Dir: wd, Global: c.global}
	switch sub {
	case "add":
		if len(rest) != 1 {
			break
		}
		id, err := brain.Add(o, brain.AddInput{Kind: rest[0], Title: c.recTitle, Status: c.recStatus, ID: c.recID, Project: c.recProject, Body: stdin})
		if err == nil {
			fmt.Fprintln(c.out, id)
		}
		return recordErr(c, err)
	case "get":
		if len(rest) == 0 {
			break
		}
		recs, _, err := brain.Get(o, rest)
		for i, r := range recs {
			if i > 0 {
				fmt.Fprintln(c.out)
			}
			fmt.Fprint(c.out, brain.FormatGet(r))
		}
		return recordErr(c, err)
	case "edit":
		if len(rest) != 1 {
			break
		}
		in := brain.EditInput{ID: rest[0]}
		if set["status"] {
			in.Status = &c.recStatus
		}
		if set["title"] {
			in.Title = &c.recTitle
		}
		if c.recBody {
			in.Body = stdin
		}
		return recordErr(c, brain.Edit(o, in))
	case "list":
		if len(rest) != 0 {
			break
		}
		recs, err := brain.List(o, brain.ListFilter{Kind: c.kind, Open: c.openRecords, Project: c.recProject, All: c.recAll})
		for _, r := range recs {
			fmt.Fprintln(c.out, brain.FormatLine(r, c.recAll))
		}
		return recordErr(c, err)
	case "import":
		if len(rest) > 1 || (c.global && len(rest) != 0) {
			break
		}
		var res *brain.ImportResult
		if c.global {
			res, err = brain.ImportGlobal(o, c.dryRun)
		} else {
			docs := ""
			if len(rest) == 1 {
				docs = rest[0]
			}
			res, err = brain.Import(o, docs, c.dryRun)
		}
		if err == nil {
			printImport(c, res)
		}
		return recordErr(c, err)
	}
	fmt.Fprintln(c.out, recordUsage)
	return exitCode(2)
}

// printImport is what an import did, and every file the caller removes by
// name afterwards (D13): the import itself removes nothing. Each path is one
// shell word (brain.ShellQuote), and git is told to read them literally: a
// tracked name can hold a glob or start with a dash.
func printImport(c *ctx, res *brain.ImportResult) {
	p := func(format string, a ...any) { fmt.Fprintln(c.out, search.Printable(fmt.Sprintf(format, a...))) }
	head := "import"
	if c.dryRun {
		head = "import (dry run: nothing written)"
	}
	p("%s: %s; %d records, %d dropped (git history keeps them), %d already stored", head, brain.ImportSummary(res), len(res.Records), res.Dropped, res.Skipped)
	for _, r := range res.Records {
		st := r.Status
		if st == "" {
			st = "-"
		}
		p("record  %s  %s  %s  %s", r.ID, r.Kind, st, r.Path)
	}
	for _, s := range res.Problems {
		p("problem  %s", s)
	}
	if len(res.Remove) > 0 {
		p("remove with git --literal-pathspecs rm -- and these paths, after committing .agents/trail.jsonl (%d files):", len(res.Remove))
		for _, f := range res.Remove {
			p("%s", brain.ShellQuote(f))
		}
	}
	if len(res.Memory) > 0 {
		p("remove with rm by name, untracked memory store files (%d):", len(res.Memory))
		for _, f := range res.Memory {
			p("%s", brain.ShellQuote(f))
		}
	}
	if len(res.Sources) > 0 {
		p("read from ~/.config/fyr; remove by name once the records are checked (%d):", len(res.Sources))
		for _, f := range res.Sources {
			p("%s", brain.ShellQuote(f))
		}
	}
}

// recordErr prints a refusal (exit 1) or a usage error (exit 2) on the
// writer, through printable: the message can quote a record's own text.
func recordErr(c *ctx, err error) error {
	switch {
	case err == nil:
		return nil
	case brain.IsUsage(err):
		fmt.Fprintln(c.out, search.Printable("fyr record: "+err.Error()))
		return exitCode(2)
	case brain.IsRefusal(err):
		fmt.Fprintln(c.out, search.Printable("fyr record: "+err.Error()))
		return exitCode(1)
	}
	// Any other error can carry a checkout's path, which a hand-extracted
	// archive can fill with escape bytes: filtered too (security review
	// 2026-10-25, finding 8).
	return errors.New(search.Printable(err.Error()))
}

// recordHits prints the brain store's hits before the file hits (ADR-063):
// for a words search the ranked records, for --refs the records naming the
// ID or path, for --open the open records (D30). Nothing when there are
// none, and a one-line note when the records could not be searched (a
// trail.jsonl that does not parse), so the file search still answers.
func recordHits(c *ctx, o search.Options) {
	limit := o.Limit
	if limit == 0 {
		limit = 10
	}
	bo := brain.Options{Home: o.Home, Dir: o.Dir, Global: o.Global}
	var hits []brain.Hit
	var err error
	var head string
	switch {
	case o.Refs != "":
		hits, err = brain.Refs(bo, o.Refs, o.Kind, limit)
		head = fmt.Sprintf("records: %d naming %s", len(hits), o.Refs)
	case o.Open:
		hits, err = brain.OpenRecords(bo, o.Kind, limit)
		head = fmt.Sprintf("records: %d open", len(hits))
	default:
		hits, err = brain.Search(bo, o.Words, o.Kind, limit)
		head = fmt.Sprintf("records: %d hits for %q", len(hits), strings.Join(o.Words, " "))
	}
	if err != nil {
		fmt.Fprintln(c.out, search.Printable("records: not searched: "+err.Error()))
		return
	}
	if len(hits) == 0 {
		return
	}
	fmt.Fprintln(c.out, search.Printable(head))
	for _, h := range hits {
		fmt.Fprintln(c.out, brain.FormatHit(h))
		if h.Snippet != "" {
			fmt.Fprintf(c.out, "    %s\n", h.Snippet)
		}
	}
}
